package ai

import (
	"context"
	"fmt"
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
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS platform_settings (key TEXT PRIMARY KEY, value TEXT)`).Error; err != nil {
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
	out, err := s.CompleteStream(t.Context(), "chat:test", []Message{
		{Role: "user", Content: "打个招呼"},
	}, 0, func(string) { calls.Add(1) })
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
	out, err := s.CompleteStream(ctx, "chat:cancel", []Message{{Role: "user", Content: "hi"}}, 0, nil)
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
	msg, err := svc.ChatStream(t.Context(), 7, conv.ID, "你好", []string{"admin"}, func(string) { deltas.Add(1) })
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
	if _, err := svc.Messages(t.Context(), 8, conv.ID); err == nil {
		t.Fatal("非归属用户应被拒绝")
	}
	// 会话互斥：进行中重复发消息被拒——串行场景下第二次正常（上轮已结束），
	// 互斥行为由 handler 层并发触发，此处验证会话可继续
	if _, err := svc.ChatStream(t.Context(), 7, conv.ID, "再来一轮", []string{"dev"}, nil); err != nil {
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

	msgs, err := svc.buildPrompt(t.Context(), conv, []string{"dev"})
	if err != nil {
		t.Fatal(err)
	}
	sys := msgs[0].Content
	if !strings.Contains(sys, "server-1 10.0.0.1") {
		t.Fatal("非敏感块应保留")
	}
	if strings.Contains(sys, "exec uptime") {
		t.Fatal("dev 视角不应包含 Sensitive 块")
	}
	// admin 视角两块都在
	msgs, _ = svc.buildPrompt(t.Context(), conv, []string{"admin"})
	if !strings.Contains(msgs[0].Content, "exec uptime") {
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
