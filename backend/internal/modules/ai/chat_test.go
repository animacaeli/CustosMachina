package ai

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// fakeRelaySSE 假 OpenAI 兼容流式端点：按词发 delta 后 [DONE]。
func fakeRelaySSE(t *testing.T, words []string, delay time.Duration) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, wd := range words {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", wd)
			if fl != nil {
				fl.Flush()
			}
			time.Sleep(delay)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		if fl != nil {
			fl.Flush()
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func chatTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&Usage{}, &Conversation{}, &ChatMessage{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS platform_settings (skey TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
		t.Fatalf("建 settings 表失败: %v", err)
	}
	return db
}

func relayFor(t *testing.T, db *gorm.DB, endpoint string) *Service {
	t.Helper()
	cipher, err := cryptopkg.NewCipher("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("构造 cipher 失败: %v", err)
	}
	s := NewService(db, cipher)
	if err := s.SaveSettings(context.Background(), endpoint, "test-model", "sk-x"); err != nil {
		t.Fatalf("配置中转层失败: %v", err)
	}
	return s
}

func TestCompleteStreamAggregates(t *testing.T) {
	db := chatTestDB(t)
	srv := fakeRelaySSE(t, []string{"你", "好", "，", "平台"}, 5*time.Millisecond)
	s := relayFor(t, db, srv.URL+"/v1")

	var calls atomic.Int32
	out, _, err := s.CompleteStream(t.Context(), "chat:test", []Message{
		{Role: "user", Content: "打个招呼"},
	}, 0, nil, func(string) { calls.Add(1) })
	if err != nil {
		t.Fatalf("流式补全失败: %v", err)
	}
	if out != "你好，平台" {
		t.Fatalf("聚合内容 = %q", out)
	}
	if calls.Load() != 4 {
		t.Fatalf("delta 回调数 = %d, want 4", calls.Load())
	}
	var n int64
	db.Model(&Usage{}).Where("caller = ?", "chat:test").Count(&n)
	if n != 1 {
		t.Fatalf("用量留痕 = %d, want 1", n)
	}
}

// 客户端取消：聚合的部分内容仍随 err 返回（调用方落库 aborted）。
func TestCompleteStreamCanceledKeepsPartial(t *testing.T) {
	db := chatTestDB(t)
	srv := fakeRelaySSE(t, []string{"a", "b", "c", "d", "e"}, 60*time.Millisecond)
	s := relayFor(t, db, srv.URL+"/v1")

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(120 * time.Millisecond) // 收到 a、b 后取消
		cancel()
	}()
	out, _, err := s.CompleteStream(ctx, "chat:cancel", []Message{{Role: "user", Content: "hi"}}, 0, nil, nil)
	if err == nil {
		t.Fatal("取消应返回错误")
	}
	if out != "a" && out != "ab" {
		t.Fatalf("部分内容异常: %q", out)
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("错误语义异常: %v", err)
	}
}

func TestChatStreamEndToEnd(t *testing.T) {
	db := chatTestDB(t)
	srv := fakeRelaySSE(t, []string{"回答", "内容"}, 5*time.Millisecond)
	s := relayFor(t, db, srv.URL+"/v1")
	svc := NewChatService(db, s)

	conv, err := svc.CreateConversation(t.Context(), 7, ChatModeGeneral, nil)
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	var deltas atomic.Int32
	msg, err := svc.ChatStream(t.Context(), 7, conv.ID, "你好", "", "", nil, []string{"admin"}, func(string) { deltas.Add(1) }, nil, nil)
	if err != nil {
		t.Fatalf("对话失败: %v", err)
	}
	if msg.Status != MsgDone || msg.Content != "回答内容" {
		t.Fatalf("回复落库异常: %+v", msg)
	}
	if deltas.Load() != 2 {
		t.Fatalf("delta 数 = %d", deltas.Load())
	}
	// 两条消息（user + assistant）
	var cnt int64
	db.Model(&ChatMessage{}).Where("conversation_id = ?", conv.ID).Count(&cnt)
	if cnt != 2 {
		t.Fatalf("消息数 = %d, want 2", cnt)
	}
	// 归属校验：他人访问拒绝
	if _, err := svc.Messages(t.Context(), 8, conv.ID, false); err == nil {
		t.Fatal("非归属用户应被拒绝")
	}
	// admin 跨用户可查看
	if _, err := svc.Messages(t.Context(), 8, conv.ID, true); err != nil {
		t.Fatalf("admin 跨用户查看应放行: %v", err)
	}
	// 会话互斥：进行中重复发消息被拒——串行场景下第二次正常（上轮已结束），
	// 互斥行为由 handler 层并发触发，此处验证会话可继续
	if _, err := svc.ChatStream(t.Context(), 7, conv.ID, "再来一轮", "", "", nil, []string{"dev"}, nil, nil, nil); err != nil {
		t.Fatalf("第二轮对话失败: %v", err)
	}
}

// dev 角色的 platform 会话：Sensitive 块被剔除（ai 铁律单测）。
func TestChatPlatformPackRoleFilter(t *testing.T) {
	// MountSource 提供 Sensitive + 非 Sensitive 各一块
	blocks := []ContextBlock{
		{Source: "inventory", Text: "server-1 10.0.0.1"},
		{Source: "events", Text: "server-1 exec uptime", Sensitive: true},
	}
	db := chatTestDB(t)
	var gotRoles []string
	svc := NewChatService(db, nil)
	svc.MountSource = mountSourceFunc(func(ctx context.Context, m Mount, roles []string) []ContextBlock {
		gotRoles = roles
		return blocks
	})
	// 不实际调中转（未配置会在 ChatStream 前报错）——直接验证 buildPrompt 过滤
	conv := &Conversation{ID: 1, UserID: 7, Mode: ChatModePlatform,
		MountJSON: `{"hours":24}`}
	db.Create(conv)
	db.Create(&ChatMessage{ConversationID: 1, Role: "user", Content: "看下状态"})

	msgs, _, err := svc.buildPrompt(t.Context(), conv, []string{"dev"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	sys, _ := msgs[0].Content.(string)
	if !strings.Contains(sys, "server-1 10.0.0.1") {
		t.Fatal("非敏感块应保留")
	}
	if strings.Contains(sys, "exec uptime") {
		t.Fatal("dev 视角不应包含 Sensitive 块")
	}
	// admin 视角两块都在
	msgs, _, _ = svc.buildPrompt(t.Context(), conv, []string{"admin"}, nil, "")
	sysAdmin, _ := msgs[0].Content.(string)
	if !strings.Contains(sysAdmin, "exec uptime") {
		t.Fatal("admin 视角应包含 Sensitive 块")
	}
	_ = gotRoles
}

// mountSourceFunc 测试用 MountSource 适配器。
type mountSourceFunc func(ctx context.Context, m Mount, viewerRoles []string) []ContextBlock

func (f mountSourceFunc) MountContext(ctx context.Context, m Mount, roles []string) []ContextBlock {
	return f(ctx, m, roles)
}

// DLP 在对话挂载链路同样生效。
func TestChatPackDLP(t *testing.T) {
	svc := &ChatService{db: nil, relay: nil}
	_ = svc
	blocks := []ContextBlock{
		{Source: "events", Text: "token=sk-abc123456 password=hunter2secret"},
	}
	pack := BuildContextPack([]string{"admin"}, blocks)
	rendered := pack.Render()
	if strings.Contains(rendered, "sk-abc123456") || strings.Contains(rendered, "hunter2secret") {
		t.Fatalf("DLP 未拦截: %s", rendered)
	}
	if !strings.Contains(rendered, "[REDACTED") {
		t.Fatal("应出现 REDACTED 标记")
	}
}

// 多模态组装：图片附件 → image_url 数组；纯文本附件 → 并入文本仍为 string。
func TestMessageContentMultimodal(t *testing.T) {
	img := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	h := ChatMessage{Role: "user", Content: "看这张图",
		Attachments: fmt.Sprintf(`[{"name":"a.png","mime":"image/png","data":"%s"},{"name":"b.png","mime":"image/png","data":"%s"}]`, img, img)}
	got, ok := messageContent(h).([]map[string]any)
	if !ok || len(got) != 3 {
		t.Fatalf("图片附件应组装 text+2 image_url: %T %v", messageContent(h), got)
	}
	if got[0]["type"] != "text" || got[1]["type"] != "image_url" {
		t.Fatalf("part 类型异常: %v", got)
	}
	iu := got[1]["image_url"].(map[string]string)
	if !strings.HasPrefix(iu["url"], "data:image/png;base64,") {
		t.Fatalf("data URI 异常: %v", iu["url"])
	}

	txt := base64.StdEncoding.EncodeToString([]byte("hello attachment"))
	h2 := ChatMessage{Role: "user", Content: "带文本附件",
		Attachments: fmt.Sprintf(`[{"name":"n.txt","mime":"text/plain","data":"%s"}]`, txt)}
	s, ok := h2.Content, false
	_ = s
	got2, isStr := messageContent(h2).(string)
	if !isStr || !strings.Contains(got2, "[附件 n.txt]") || !strings.Contains(got2, "hello attachment") {
		t.Fatalf("文本附件应并入文本: %v", messageContent(h2))
	}
	// 无附件：原样 string
	if got3 := messageContent(ChatMessage{Content: "plain"}); got3 != "plain" {
		t.Fatalf("无附件应原样: %v", got3)
	}
}

// /命令技能：触发注入、{{q}} 替换、角色 allowlist、越权同不存在语义。
func TestSkillChat(t *testing.T) {
	db := chatTestDB(t)
	if err := db.AutoMigrate(&Skill{}); err != nil {
		t.Fatal(err)
	}
	skills := NewSkillService(db)
	svc := NewChatService(db, nil)
	svc.Skills = skills

	in := SaveSkillInput{Name: "troubleshoot", Title: "故障排查", Prompt: "排查：{{q}}，按 runbook 走"}
	enabled := true
	in.Enabled = &enabled
	sk, err := skills.Save(t.Context(), 0, in)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "troubleshoot" {
		t.Fatalf("技能名异常: %s", sk.Name)
	}
	// 大写触发名归一
	lookup, err := skills.LookupFor(t.Context(), "TroubleShoot", []string{"dev"})
	if err != nil || lookup.ID != sk.ID {
		t.Fatalf("触发名归一失败: %v", err)
	}
	// 渲染：{{q}} 替换
	if got := RenderSkill(lookup, "demo 挂了"); !contains(got, "排查：demo 挂了") {
		t.Fatalf("{{q}} 未替换: %s", got)
	}
	// buildPrompt 注入（经 buildPrompt 间接断言 system 含技能块）
	conv := &Conversation{ID: 9, UserID: 7, Mode: ChatModeGeneral}
	db.Create(conv)
	msgs, _, err := svc.buildPrompt(t.Context(), conv, []string{"dev"}, lookup, "demo 挂了")
	if err != nil {
		t.Fatal(err)
	}
	sys, _ := msgs[0].Content.(string)
	if !contains(sys, "/troubleshoot 显式触发") || !contains(sys, "排查：demo 挂了") {
		t.Fatalf("技能块未注入 system: %.200s", sys)
	}

	// 角色 allowlist：限定 ops 后 dev 不可见
	sk.Roles = "ops"
	db.Save(sk)
	if _, err := skills.LookupFor(t.Context(), "troubleshoot", []string{"dev"}); err == nil {
		t.Fatal("dev 越权触发应拒绝")
	}
	if _, err := skills.LookupFor(t.Context(), "troubleshoot", []string{"ops"}); err != nil {
		t.Fatalf("ops 应可用: %v", err)
	}
	// 非法名拒绝
	if _, err := skills.Save(t.Context(), 0, SaveSkillInput{Name: "Bad Name", Title: "x", Prompt: "p"}); err == nil {
		t.Fatal("非法技能名应拒绝")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && strings.Contains(s, sub)
}

// 软删除语义：本人删除后不可见不可访问；admin 可按用户查看、可含已删（deleted
// 标记）、仍能查看消息（合规留档）；普通用户的过滤参数被忽略。
func TestChatSoftDelete(t *testing.T) {
	db := chatTestDB(t)
	svc := NewChatService(db, nil)

	conv, err := svc.CreateConversation(t.Context(), 7, ChatModeGeneral, nil)
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	if err := db.Create(&ChatMessage{ConversationID: conv.ID, Role: "user", Content: "hi", Status: MsgDone}).Error; err != nil {
		t.Fatalf("落消息失败: %v", err)
	}

	// 本人软删
	if err := svc.DeleteConversation(t.Context(), 7, conv.ID, false); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	// 消息保留（留档）
	var cnt int64
	db.Model(&ChatMessage{}).Where("conversation_id = ?", conv.ID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("软删后消息应保留, got %d", cnt)
	}

	// 本人列表不含（filterUserID/includeDeleted 对普通用户无效）
	if list, _ := svc.ListConversations(t.Context(), 7, false, 0, true); len(list) != 0 {
		t.Fatalf("本人列表应不含已删会话, got %d", len(list))
	}
	// 普通用户带 filterUserID 也只能看自己
	if list, _ := svc.ListConversations(t.Context(), 9, false, 7, true); len(list) != 0 {
		t.Fatalf("普通用户 filterUserID 应被忽略, got %d", len(list))
	}
	if _, err := svc.Messages(t.Context(), 7, conv.ID, false); err == nil {
		t.Fatal("本人访问已删会话应被拒")
	}
	if err := svc.DeleteConversation(t.Context(), 9, conv.ID, false); err == nil {
		t.Fatal("他人删除应被拒")
	}

	// admin 按用户查看：不含已删 → 空；含已删 → 1 条带 deleted 标记
	if fl, _ := svc.ListConversations(t.Context(), 8, true, 7, false); len(fl) != 0 {
		t.Fatalf("admin 查用户 7（不含已删）应为空, got %d", len(fl))
	}
	fl, err := svc.ListConversations(t.Context(), 8, true, 7, true)
	if err != nil || len(fl) != 1 {
		t.Fatalf("admin 查用户 7（含已删）应 1 条: %v %d", err, len(fl))
	}
	if !fl[0].Deleted || fl[0].ID != conv.ID {
		t.Fatalf("deleted 标记异常: %+v", fl[0])
	}
	// admin 查不存在用户 → 空
	if fl, _ := svc.ListConversations(t.Context(), 8, true, 999, true); len(fl) != 0 {
		t.Fatalf("查不存在用户应为空, got %d", len(fl))
	}
	// admin 仍可查看消息（留档）
	if _, err := svc.Messages(t.Context(), 8, conv.ID, true); err != nil {
		t.Fatalf("admin 查看已删会话消息应放行: %v", err)
	}

	// admin 再删（对已删会话幂等）不报错
	if err := svc.DeleteConversation(t.Context(), 8, conv.ID, true); err != nil {
		t.Fatalf("admin 删除应放行: %v", err)
	}
}

// 空会话不进列表：新建未开口的对话直接丢弃（本人视图与 admin 留档视图一致）。
func TestChatListSkipsEmpty(t *testing.T) {
	db := chatTestDB(t)
	svc := NewChatService(db, nil)

	empty, err := svc.CreateConversation(t.Context(), 7, ChatModePlatform, nil)
	if err != nil {
		t.Fatalf("建空会话失败: %v", err)
	}
	filled, err := svc.CreateConversation(t.Context(), 7, ChatModeGeneral, nil)
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	if err := db.Create(&ChatMessage{ConversationID: filled.ID, Role: "user", Content: "hi", Status: MsgDone}).Error; err != nil {
		t.Fatalf("落消息失败: %v", err)
	}

	// 本人视图：仅 filled
	list, err := svc.ListConversations(t.Context(), 7, false, 0, false)
	if err != nil || len(list) != 1 || list[0].ID != filled.ID {
		t.Fatalf("本人视图应仅含有消息的会话: %v %+v", err, list)
	}
	// 空会话即便被删除，admin 含已删视图也不出现（无留档价值）
	if err := svc.DeleteConversation(t.Context(), 7, empty.ID, false); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	alist, err := svc.ListConversations(t.Context(), 8, true, 7, true)
	if err != nil || len(alist) != 1 || alist[0].ID != filled.ID {
		t.Fatalf("admin 留档视图也应过滤空会话: %v %+v", err, alist)
	}
}

// fakeRelayTools 模拟支持 function calling 的上游：
// 第一轮（无 tool 消息）返回 tool_calls 流；之后轮返回文本流。
// 返回的 tool 名固定 list_builds，参数 {"project_id":2,"limit":3}。
func fakeRelayTools(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		hasToolMsg := strings.Contains(string(body), `"role":"tool"`)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		if !hasToolMsg {
			// tool_calls 增量分三块：id+name / arguments 片段 / finish_reason
			chunks := []string{
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"list_bu","arguments":""}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"ilds","arguments":"{\"project_id\":2,"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"limit\":3}"}}]}}]}`,
				`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
			}
			for _, c := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", c)
				fl.Flush()
			}
		} else {
			for _, wd := range []string{"构建", "正常"} {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", wd)
				fl.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type fakeToolSource struct{}

func (fakeToolSource) ChatTools(context.Context, []string) []ChatTool {
	return []ChatTool{{
		Name:        "list_builds",
		Description: "近期 CI 构建记录",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"project_id": map[string]any{"type": "integer"},
			"limit":      map[string]any{"type": "integer"},
		}},
		Fn: func(_ context.Context, args map[string]any) (string, error) {
			if int(args["project_id"].(float64)) != 2 {
				return "", fmt.Errorf("project_id 解析异常: %v", args["project_id"])
			}
			return "build v1 ok", nil
		},
	}}
}

// 对话内工具调用端到端：模型发起 tool_calls（流式增量聚合）→ 平台执行 →
// tool 消息回填 → 最终回答流式返回；工具留痕落 ChatMessage.Tools。
func TestChatFunctionCalling(t *testing.T) {
	db := chatTestDB(t)
	srv := fakeRelayTools(t)
	s := relayFor(t, db, srv.URL+"/v1")
	svc := NewChatService(db, s)
	svc.ToolSource = fakeToolSource{}

	conv, err := svc.CreateConversation(t.Context(), 7, ChatModePlatform, nil)
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	var toolEvents []string
	msg, err := svc.ChatStream(t.Context(), 7, conv.ID, "看下构建", "", "", nil, []string{"admin"}, nil, func(name, args string) {
		toolEvents = append(toolEvents, name+" "+args)
	}, nil)
	if err != nil {
		t.Fatalf("对话失败: %v", err)
	}
	if msg.Content != "构建正常" {
		t.Fatalf("最终回复 = %q", msg.Content)
	}
	if len(toolEvents) != 1 || toolEvents[0] != `list_builds {"project_id":2,"limit":3}` {
		t.Fatalf("onTool 事件异常: %v", toolEvents)
	}
	if !contains(msg.Tools, "list_builds") || !contains(msg.Tools, "project_id") {
		t.Fatalf("工具留痕异常: %q", msg.Tools)
	}
}

// 模型不支持 function calling：上游对 tools 请求回 400，chat 降级去工具重试成功。
func TestChatToolDegradation(t *testing.T) {
	db := chatTestDB(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), `"tools"`) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"tools is not supported"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"降级回答\"}}]}\n\ndata: [DONE]\n\n")
		fl.Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s := relayFor(t, db, srv.URL+"/v1")
	svc := NewChatService(db, s)
	svc.ToolSource = fakeToolSource{}

	conv, err := svc.CreateConversation(t.Context(), 7, ChatModePlatform, nil)
	if err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	msg, err := svc.ChatStream(t.Context(), 7, conv.ID, "看下构建", "", "", nil, []string{"admin"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("降级对话失败: %v", err)
	}
	if msg.Content != "降级回答" || msg.Tools != "" {
		t.Fatalf("降级回复异常: %q tools=%q", msg.Content, msg.Tools)
	}
}

// fakeActionSource 建议卡测试替身：校验拒绝幻觉 ID。
type fakeActionSource struct{}

func (fakeActionSource) ChatActions() []ChatActionDef {
	return []ChatActionDef{{
		Type:        ActTriggerCron,
		Description: "手动触发定时任务（建议）",
		Parameters: map[string]any{"type": "object", "properties": map[string]any{
			"job_id": map[string]any{"type": "integer"},
		}},
		Summary: func(p map[string]any) string { return fmt.Sprintf("建议触发任务 #%v", p["job_id"]) },
		Route:   func(_ map[string]any) string { return "/cron" },
	}}
}

func (fakeActionSource) ValidateChatAction(_ context.Context, typ string, params map[string]any) (string, error) {
	if typ != ActTriggerCron {
		return "", fmt.Errorf("未知类型")
	}
	if _, ok := params["job_id"]; !ok {
		return "", fmt.Errorf("job_id 必填")
	}
	return fmt.Sprintf("建议触发任务 #%v", params["job_id"]), nil
}

// 建议卡全流程（用户定调：AI 只建议不执行）：模型调意图工具 → 生成建议卡
// （type/summary/route）→ onAction 回调 → 消息留痕；工具回文本引导用户自行操作。
func TestChatSuggestFlow(t *testing.T) {
	db := chatTestDB(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		if strings.Contains(string(body), `"role":"tool"`) {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"请到定时任务页操作\"}}]}\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"trigger_cron","arguments":"{\"job_id\":5}"}}]}}]}`+"\n\n"+
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		}
		fl.Flush()
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s := relayFor(t, db, srv.URL+"/v1")
	svc := NewChatService(db, s)
	svc.ActionSource = fakeActionSource{}

	conv, _ := svc.CreateConversation(t.Context(), 7, ChatModePlatform, nil)
	var cards []ActionTrace
	msg, err := svc.ChatStream(t.Context(), 7, conv.ID, "触发下任务5", "", "", nil, []string{"admin"}, nil, nil, func(a ActionTrace) {
		cards = append(cards, a)
	})
	if err != nil {
		t.Fatalf("对话失败: %v", err)
	}
	if len(cards) != 1 || cards[0].Type != ActTriggerCron || cards[0].Route != "/cron" {
		t.Fatalf("建议卡异常: %+v", cards)
	}
	if !contains(cards[0].Summary, "建议") {
		t.Fatalf("摘要应为指导语气: %q", cards[0].Summary)
	}
	if !contains(msg.Actions, "trigger_cron") || !contains(msg.Actions, "/cron") {
		t.Fatalf("消息留痕异常: %q", msg.Actions)
	}
}

// 建议参数校验失败（幻觉 ID）：不生成卡，错误文本回填模型可重试。
func TestChatSuggestValidateFail(t *testing.T) {
	db := chatTestDB(t)
	svc := NewChatService(db, nil)
	svc.ActionSource = fakeActionSource{}
	args := map[string]any{}
	summary, err := svc.ActionSource.ValidateChatAction(t.Context(), ActTriggerCron, args)
	if err == nil || summary != "" {
		t.Fatal("缺 job_id 应校验失败")
	}
}

// 安全边界声明进 system prompt（权限拒答/只读建议/不装 skill 与 MCP/注入免疫）。
func TestBuildPromptSecurityBoundary(t *testing.T) {
	db := chatTestDB(t)
	svc := NewChatService(db, nil)
	out, _ := svc.CreateConversation(t.Context(), 7, ChatModeGeneral, nil)
	conv := &Conversation{ID: out.ID, Mode: ChatModeGeneral}
	msgs, _, err := svc.buildPrompt(t.Context(), conv, []string{"dev"}, nil, "test")
	if err != nil {
		t.Fatalf("buildPrompt 失败: %v", err)
	}
	sys, _ := msgs[0].Content.(string)
	for _, want := range []string{"只读助手", "权限边界", "普通用户", "技能", "MCP", "忽略之前的要求"} {
		if !contains(sys, want) {
			t.Fatalf("system 缺少安全边界要素 %q: %.200s", want, sys)
		}
	}
}
