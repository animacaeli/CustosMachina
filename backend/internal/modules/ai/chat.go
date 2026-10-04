// chat.go P6-M1 AI 对话：会话持久化 + 双模式（通用/平台上下文挂载）+ SSE 流式。
// 治理设计见 docs/design-chat-sse.md；AI 五铁律落点：角色过滤（BuildContextPack
// Sensitive 块）+ DLP（ApplyDLP）+ 不可信输入围栏（Render）+ 会话归属本人。
package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/logger"
)

// 对话模式。
const (
	ChatModeGeneral  = "general"  // 通用对话：不接平台数据（兼作中转配置调试）
	ChatModePlatform = "platform" // 平台上下文：会话挂载 Context Pack
)

// 消息状态（assistant 消息的终态）。
const (
	MsgDone    = "done"
	MsgAborted = "aborted" // 客户端停止/断连，已生成部分落库
	MsgError   = "error"
)

// Conversation 对话会话（归属用户；admin 可跨用户查看/管理——对话内容合规审阅所需）。
type Conversation struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	UserID    uint           `gorm:"index;not null" json:"userId"`
	Title     string         `gorm:"size:128" json:"title"` // 首条用户消息截断
	Mode      string         `gorm:"size:16;not null;default:general" json:"mode"`
	MountJSON string         `gorm:"type:text" json:"-"` // 挂载快照（platform 模式）
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Conversation) TableName() string { return "ai_conversations" }

// Mount 挂载快照（platform 模式）：重开会话才刷新，会话中途不重取。
type Mount struct {
	ProjectIDs []uint `json:"projectIds"`
	ServerIDs  []uint `json:"serverIds"`
	Hours      int    `json:"hours"` // 上下文时间窗（默认 24）
}

// ChatAttachment 附件（多模态）：图片走 image_url data URI；文本类解码后并入文本。
type ChatAttachment struct {
	Name string `json:"name"`
	Mime string `json:"mime"`
	Data string `json:"data"` // base64（前端编码；后端限制单文件 4MB、每条 ≤3 个）
}

// ChatMessage 对话消息。
type ChatMessage struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	ConversationID uint      `gorm:"index;not null" json:"conversationId"`
	Role           string    `gorm:"size:16;not null" json:"role"` // user | assistant
	Content        string    `gorm:"type:text" json:"content"`
	Attachments    string    `gorm:"type:text" json:"attachments,omitempty"` // JSON []ChatAttachment（user 消息可带）
	Status         string    `gorm:"size:16;not null;default:done" json:"status"`
	PackRedactions int       `gorm:"not null;default:0" json:"packRedactions"` // 本轮 pack 拦截数（留痕）
	CreatedAt      time.Time `json:"createdAt"`
}

func (ChatMessage) TableName() string { return "ai_messages" }

// ChatContextSource 对话挂载上下文投影（app 层注入实现，避免 ai 直连业务表）。
// Sensitive 标记与角色过滤语义见 BuildContextPack。
type ChatContextSource interface {
	MountContext(ctx context.Context, m Mount, viewerRoles []string) []ContextBlock
}

// ---- ChatService ----

// chatSemCap 单实例同时活跃流上限（单实例轻量约束，见设计文档 §5）。
const chatSemCap = 8

// 历史窗口：最近 N 轮进 prompt（防超模型窗口），更早历史仅 UI 可见。
const chatHistoryRounds = 12

type ChatService struct {
	db    *gorm.DB
	relay *Service
	// MountSource 挂载上下文源（app 层注入；nil = platform 模式无上下文可用）
	MountSource ChatContextSource
	// 会话级互斥（同一会话同时只允许一个进行中的流）；
	// HTTP 请求并发到达，map 读写必须持锁
	mu          sync.Mutex
	activeConvs map[uint]struct{}
	sem         chan struct{}
}

func NewChatService(db *gorm.DB, relay *Service) *ChatService {
	return &ChatService{
		db:          db,
		relay:       relay,
		activeConvs: map[uint]struct{}{},
		sem:         make(chan struct{}, chatSemCap),
	}
}

// ErrChatBusy / ErrConvActive 对话并发限制（设计文档 §5）。
var (
	ErrChatBusy    = errors.New("对话并发已达上限，请稍后再试")
	ErrConvActive  = errors.New("该会话有进行中的回复")
	ErrNotOwner    = errors.New("会话不存在或无权访问")
	ErrNotConfigur = errors.New("AI 中转层未配置，请先在管理后台完成配置")
)

// ConversationsOut 列表视图（不含消息；Owner 仅 admin 全量视图填充）。
type ConversationsOut struct {
	Conversation
	Mount *Mount `json:"mount"`
	Owner string `json:"owner,omitempty"`
}

// CreateConversation 新建会话。
func (s *ChatService) CreateConversation(ctx context.Context, userID uint, mode string, mount *Mount) (*ConversationsOut, error) {
	if mode == "" {
		mode = ChatModeGeneral
	}
	if mode != ChatModeGeneral && mode != ChatModePlatform {
		return nil, fmt.Errorf("mode 取值须为 general|platform")
	}
	c := Conversation{UserID: userID, Mode: mode}
	if mode == ChatModePlatform && mount != nil {
		b, err := json.Marshal(mount)
		if err != nil {
			return nil, err
		}
		c.MountJSON = string(b)
	}
	if err := s.db.WithContext(ctx).Create(&c).Error; err != nil {
		return nil, err
	}
	return s.toOut(&c), nil
}

// UpdateMount 更新挂载（platform 模式；重开会话刷新语义由前端触发）。
func (s *ChatService) UpdateMount(ctx context.Context, userID, id uint, mount *Mount) error {
	c, err := s.accessible(ctx, userID, id, false)
	if err != nil {
		return err
	}
	b, err := json.Marshal(mount)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(c).Update("mount_json", string(b)).Error
}

// ListConversations 会话列表（最近在前）。admin 且 all=true 时返回全平台会话
// （含归属人），否则仅本人——各用户对话历史相对独立，管理员可查看全部。
func (s *ChatService) ListConversations(ctx context.Context, userID uint, isAdmin, all bool) ([]ConversationsOut, error) {
	q := s.db.WithContext(ctx).Model(&Conversation{}).Order("updated_at DESC").Limit(200)
	if !(isAdmin && all) {
		q = q.Where("user_id = ?", userID)
	}
	var cs []Conversation
	if err := q.Find(&cs).Error; err != nil {
		return nil, err
	}
	out := make([]ConversationsOut, len(cs))
	for i := range cs {
		out[i] = *s.toOut(&cs[i])
	}
	if isAdmin && all {
		s.fillOwners(ctx, out)
	}
	return out, nil
}

// fillOwners 批量填充归属人显示名（admin 全量视图用）。
func (s *ChatService) fillOwners(ctx context.Context, out []ConversationsOut) {
	uniq := map[uint]bool{}
	for _, o := range out {
		uniq[o.UserID] = true
	}
	idList := make([]uint, 0, len(uniq))
	for id := range uniq {
		idList = append(idList, id)
	}
	var rows []struct {
		ID          uint
		DisplayName string
		Username    string
	}
	if err := s.db.WithContext(ctx).Table("users").
		Select("id, display_name, username").Where("id IN ?", idList).Scan(&rows).Error; err != nil {
		return
	}
	names := map[uint]string{}
	for _, r := range rows {
		if r.DisplayName != "" {
			names[r.ID] = r.DisplayName
		} else {
			names[r.ID] = r.Username
		}
	}
	for i := range out {
		out[i].Owner = names[out[i].UserID]
	}
}

// DeleteConversation 删除会话及消息（硬删：软删行占索引且历史无留档价值）。
// 本人或 admin（管理权）。
func (s *ChatService) DeleteConversation(ctx context.Context, userID, id uint, isAdmin bool) error {
	c, err := s.accessible(ctx, userID, id, isAdmin)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Unscoped().Delete(&ChatMessage{}, "conversation_id = ?", c.ID).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Unscoped().Delete(c).Error
}

// Messages 会话消息（升序）。本人或 admin。
func (s *ChatService) Messages(ctx context.Context, userID, id uint, isAdmin bool) ([]ChatMessage, error) {
	c, err := s.accessible(ctx, userID, id, isAdmin)
	if err != nil {
		return nil, err
	}
	var ms []ChatMessage
	if err := s.db.WithContext(ctx).Where("conversation_id = ?", c.ID).
		Order("id").Limit(500).Find(&ms).Error; err != nil {
		return nil, err
	}
	return ms, nil
}

// accessible 归属校验：admin 跨用户放行（查看/管理），普通用户仅本人。
func (s *ChatService) accessible(ctx context.Context, userID, id uint, isAdmin bool) (*Conversation, error) {
	var c Conversation
	if err := s.db.WithContext(ctx).First(&c, id).Error; err != nil {
		return nil, ErrNotOwner
	}
	if c.UserID != userID && !isAdmin {
		return nil, ErrNotOwner
	}
	return &c, nil
}

func (s *ChatService) toOut(c *Conversation) *ConversationsOut {
	out := &ConversationsOut{Conversation: *c}
	if c.MountJSON != "" {
		var m Mount
		if json.Unmarshal([]byte(c.MountJSON), &m) == nil {
			out.Mount = &m
		}
	}
	return out
}

// acquire 会话互斥 + 全局并发闸（两把都拿到才算进入）。
func (s *ChatService) acquire(convID uint) (release func(), err error) {
	s.mu.Lock()
	if _, dup := s.activeConvs[convID]; dup {
		s.mu.Unlock()
		return nil, ErrConvActive
	}
	select {
	case s.sem <- struct{}{}:
	default:
		s.mu.Unlock()
		return nil, ErrChatBusy
	}
	s.activeConvs[convID] = struct{}{}
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		delete(s.activeConvs, convID)
		s.mu.Unlock()
		<-s.sem
	}, nil
}

// ChatStream 一轮流式对话：落用户消息（可带附件）→ 组 prompt（挂载 pack + 历史）→ 流式回调。
// 返回值：assistant 消息落库结果（断连时 err=ErrConvAborted 语义由调用方判定）。
// onDelta 在 relay 读到增量时同步调用（调用方负责转发 SSE——写慢会自然背压到上游）。
func (s *ChatService) ChatStream(ctx context.Context, userID uint, convID uint, content string, attachments []ChatAttachment, viewerRoles []string, onDelta func(string)) (*ChatMessage, error) {
	if !s.relay.Configured(ctx) {
		return nil, ErrNotConfigur
	}
	c, err := s.accessible(ctx, userID, convID, false)
	if err != nil {
		return nil, err
	}
	release, err := s.acquire(convID)
	if err != nil {
		return nil, err
	}
	defer release()

	if c.Title == "" && content != "" {
		title := []rune(content)
		if len(title) > 32 {
			title = title[:32]
		}
		s.db.WithContext(ctx).Model(c).Update("title", string(title))
	}
	userMsg := ChatMessage{ConversationID: c.ID, Role: "user", Content: content, Status: MsgDone}
	if len(attachments) > 0 {
		if b, err := json.Marshal(attachments); err == nil {
			userMsg.Attachments = string(b)
		}
	}
	if err := s.db.WithContext(ctx).Create(&userMsg).Error; err != nil {
		return nil, err
	}

	msgs, redactions, err := s.buildPrompt(ctx, c, viewerRoles)
	if err != nil {
		return nil, err
	}

	// 整体 deadline 5min（设计文档 §2）；落库用 WithoutCancel
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	var assistant ChatMessage
	out, rerr := s.relay.CompleteStream(ctx, "chat:"+fmt.Sprint(c.ID), msgs, 0, onDelta)

	status := MsgDone
	switch {
	case rerr == nil:
	case errors.Is(rerr, context.Canceled):
		status = MsgAborted
	case errors.Is(rerr, context.DeadlineExceeded):
		status = MsgError
	default:
		status = MsgError
	}
	if out == "" && rerr != nil {
		// 无任何产出：不落空 assistant 消息，返回错误
		return nil, rerr
	}
	assistant = ChatMessage{ConversationID: c.ID, Role: "assistant", Content: out, Status: status, PackRedactions: redactions}
	sctx := context.WithoutCancel(ctx)
	if err := s.db.WithContext(sctx).Create(&assistant).Error; err != nil {
		logger.Warnf("[ai-chat] 回复落库失败 conv=%d: %v", c.ID, err)
	}
	if rerr != nil && status == MsgAborted {
		return &assistant, rerr // 断连：已落部分内容，调用方据此收尾
	}
	if rerr != nil {
		return &assistant, rerr
	}
	return &assistant, nil
}

// buildPrompt 组 prompt：system（平台身份 + 挂载 pack 围栏）+ 最近 N 轮 + 本轮用户消息。
// 第二返回值 = pack 的 DLP 拦截数（消息留痕用）。
func (s *ChatService) buildPrompt(ctx context.Context, c *Conversation, viewerRoles []string) ([]Message, int, error) {
	var history []ChatMessage
	if err := s.db.WithContext(ctx).Where("conversation_id = ?", c.ID).
		Order("id DESC").Limit(chatHistoryRounds * 2).Find(&history).Error; err != nil {
		return nil, 0, err
	}
	// 反转为升序
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}

	sys := "你是 CustosMachina 运维平台的对话助手。回答保持简洁、面向运维场景。"
	sys += "当前为通用对话模式：你不掌握平台的实时数据（项目/主机/告警/任务等）；"
	sys += "若用户询问这些，明确说明需要新建「平台上下文」会话后再问。"

	redactions := 0
	if c.Mode == ChatModePlatform {
		var m Mount
		if c.MountJSON != "" {
			_ = json.Unmarshal([]byte(c.MountJSON), &m)
		}
		if m.Hours <= 0 {
			m.Hours = 24
		}
		sys += "\n\n当前为平台上下文模式：下方数据块即平台实时数据（全平台范围：项目/主机/构建/发布/事件/任务）。"
		sys += "平台数据仅以下方数据块为准，数据块之外不要臆造平台状态，也不要声称自己是通用模式或没有数据。"
		if s.MountSource != nil {
			if blocks := s.MountSource.MountContext(ctx, m, viewerRoles); len(blocks) > 0 {
				if pack := BuildContextPack(viewerRoles, blocks); len(pack.Blocks) > 0 {
					sys += "\n\n" + pack.Render()
					redactions = pack.Redactions
				}
			}
		}
	}

	msgs := []Message{{Role: "system", Content: sys}}
	for _, h := range history {
		if h.Content == "" && h.Attachments == "" {
			continue
		}
		if h.Role == "user" || h.Role == "assistant" {
			msgs = append(msgs, Message{Role: h.Role, Content: messageContent(h)})
		}
	}
	return msgs, redactions, nil
}

// messageContent 消息 → OpenAI content：无附件为 string；有附件组装多模态数组
// （图片 image_url data URI、文本类解码并入 text）。仅对上游请求生效，
// 落库仍存原始 Content/Attachments（展示与审计需要）。
func messageContent(h ChatMessage) any {
	if h.Attachments == "" {
		return h.Content
	}
	var atts []ChatAttachment
	if err := json.Unmarshal([]byte(h.Attachments), &atts); err != nil || len(atts) == 0 {
		return h.Content
	}
	text := h.Content
	parts := []map[string]any{}
	appendText := true
	for _, a := range atts {
		switch {
		case strings.HasPrefix(a.Mime, "image/"):
			parts = append(parts, map[string]any{
				"type":      "image_url",
				"image_url": map[string]string{"url": "data:" + a.Mime + ";base64," + a.Data},
			})
		default:
			// 文本类附件：解码后并入 text part（避免上游不支持 file part）
			if raw, err := base64.StdEncoding.DecodeString(a.Data); err == nil {
				text += fmt.Sprintf("\n\n[附件 %s]\n%s", a.Name, string(raw))
			}
		}
	}
	if len(parts) == 0 {
		return text // 只有文本附件：仍是纯文本消息
	}
	_ = appendText
	out := []map[string]any{{"type": "text", "text": text}}
	return append(out, parts...)
}

// ChatModels chat 模块自动迁移模型。
func ChatModels() []any { return []any{&Conversation{}, &ChatMessage{}} }
