// action.go P6-M4 NL→操作确认层：AI 只生成意图，人确认才执行。
// 白名单三件套（低危）：容器重启 / cron 手动触发 / 配置文件下发；
// 真实执行由 app 层桥接既有 service，casbin 权限与审计与 REST 同链路。
// 验收语义：不确认不执行（TTL 过期即作废）；解析/校验失败给明确错误而非猜测执行。
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// 白名单操作类型（意图工具名 = 操作类型，模型 function calling 直接触发）。
const (
	ActRestartContainer = "restart_container"
	ActTriggerCron      = "trigger_cron"
	ActDeployConfig     = "deploy_config"
)

// PendingAction 状态机。
const (
	ActPending   = "pending"
	ActDone      = "done"
	ActFailed    = "failed"
	ActCancelled = "cancelled"
	ActExpired   = "expired"
)

// PendingAction AI 生成的待确认操作：创建后仅发起人可在 TTL 内确认；
// 确认才执行（AI 永远不直接触发写操作）。
type PendingAction struct {
	ID             uint      `gorm:"primarykey" json:"id"`
	UserID         uint      `gorm:"index;not null" json:"userId"` // 仅本人可确认/取消
	ConversationID uint      `gorm:"index" json:"conversationId"`
	Type           string    `gorm:"size:32;not null" json:"type"`
	Params         string    `gorm:"type:text" json:"params"` // JSON（确认卡展示 + 执行入参）
	Summary        string    `gorm:"size:256" json:"summary"` // 人读摘要（确认卡标题）
	Roles          string    `gorm:"size:128" json:"-"`       // 创建时角色快照（执行时 casbin 判定）
	Status         string    `gorm:"size:16;not null;default:pending" json:"status"`
	Result         string    `gorm:"size:512" json:"result,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

func (PendingAction) TableName() string { return "ai_pending_actions" }

// ChatActionDef NL→操作白名单项：Type 即意图工具名；Parameters 为 JSON Schema；
// Summary 渲染人读摘要（确认卡展示——用户看的就是这句，必须准确无歧义）。
type ChatActionDef struct {
	Type        string
	Description string
	Parameters  map[string]any
	Summary     func(params map[string]any) string
}

// ChatActionExecutor app 层注入（与 ChatToolSource 同一桥接纪律）：
// Validate 在创建意图卡时做参数校验（拒绝幻觉 ID——容器/任务/配置必须真实存在）；
// Execute 在用户确认后走既有 service + casbin（AI 只生成意图，执行权在人）。
type ChatActionExecutor interface {
	ChatActions() []ChatActionDef
	ValidateChatAction(ctx context.Context, typ string, params map[string]any) (string, error)
	ExecuteChatAction(ctx context.Context, userID uint, operator, rolesCSV, typ string, params map[string]any) (string, error)
}

// intentTTL 意图卡有效期：不确认不执行，过时作废（关页即丢弃的等价语义）。
const intentTTL = 5 * time.Minute

// CreatePendingAction 模型发起写意图：校验参数真实存在 → 落待确认卡。
// 调用点是 ChatStream 工具循环（NeedsConfirm 工具）。
func (s *ChatService) CreatePendingAction(ctx context.Context, userID, convID uint, rolesCSV, typ string, params map[string]any) (*PendingAction, error) {
	if s.ActionExecutor == nil {
		return nil, fmt.Errorf("操作执行器未注入（平台未启用 NL 操作）")
	}
	summary, err := s.ActionExecutor.ValidateChatAction(ctx, typ, params)
	if err != nil {
		return nil, err
	}
	pb, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	a := PendingAction{
		UserID: userID, ConversationID: convID, Type: typ,
		Params: string(pb), Summary: summary, Roles: rolesCSV, Status: ActPending,
		ExpiresAt: time.Now().Add(intentTTL),
	}
	if err := s.db.WithContext(ctx).Create(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ErrActionHandled 意图卡不在可确认状态（已处理/过期）——错误信息面向用户可直接展示。
var ErrActionHandled = fmt.Errorf("该操作卡已处理或已过期，请重新发起")

// ConfirmAction 用户点击确认：仅发起人本人、pending 且未过期 → 执行 → 落结果。
func (s *ChatService) ConfirmAction(ctx context.Context, userID uint, operator string, id uint) (*PendingAction, error) {
	if s.ActionExecutor == nil {
		return nil, fmt.Errorf("操作执行器未注入")
	}
	var a PendingAction
	if err := s.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, ErrNotOwner
	}
	if a.UserID != userID {
		return nil, fmt.Errorf("仅发起人本人可确认该操作")
	}
	if a.Status != ActPending {
		return nil, ErrActionHandled
	}
	if time.Now().After(a.ExpiresAt) {
		a.Status = ActExpired
		s.db.WithContext(ctx).Model(&a).Update("status", ActExpired)
		return nil, ErrActionHandled
	}
	var params map[string]any
	_ = json.Unmarshal([]byte(a.Params), &params)

	out, err := s.ActionExecutor.ExecuteChatAction(ctx, userID, operator, a.Roles, a.Type, params)
	if err != nil {
		a.Status, a.Result = ActFailed, truncStr(err.Error(), 500)
	} else {
		a.Status, a.Result = ActDone, truncStr(out, 500)
	}
	s.db.WithContext(ctx).Model(&PendingAction{}).Where("id = ?", a.ID).
		Updates(map[string]any{"status": a.Status, "result": a.Result})
	return &a, nil
}

// CancelAction 发起人取消意图卡。
func (s *ChatService) CancelAction(ctx context.Context, userID, id uint) (*PendingAction, error) {
	var a PendingAction
	if err := s.db.WithContext(ctx).First(&a, id).Error; err != nil {
		return nil, ErrNotOwner
	}
	if a.UserID != userID {
		return nil, fmt.Errorf("仅发起人本人可取消该操作")
	}
	if a.Status != ActPending {
		return &a, nil // 已终态，幂等返回
	}
	a.Status = ActCancelled
	s.db.WithContext(ctx).Model(&a).Update("status", ActCancelled)
	return &a, nil
}

// ListActions 批量查意图卡状态（历史消息渲染确认卡用）：本人可见自己的，
// admin 可见全部（跨用户审阅与对话留档视图一致）。
func (s *ChatService) ListActions(ctx context.Context, userID uint, isAdmin bool, ids []uint) ([]PendingAction, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	q := s.db.WithContext(ctx).Where("id IN ?", ids)
	if !isAdmin {
		q = q.Where("user_id = ?", userID)
	}
	var as []PendingAction
	if err := q.Find(&as).Error; err != nil {
		return nil, err
	}
	return as, nil
}

// ActionTrace 消息留痕项（ChatMessage.Actions）。
type ActionTrace struct {
	ID      uint   `json:"id"`
	Type    string `json:"type"`
	Summary string `json:"summary"`
}
