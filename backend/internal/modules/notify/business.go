// business.go P7-M5 业务告警 API：业务服务（如 app-attribution）不需要 O2
// 也能接入平台通知——POST /api/notify/business 一个 HTTP 调用即可。
// 鉴权沿用 P6 配置拉取 API 的应用级凭证模式（明文只出一次、库存 sha256、
// Enabled 即时吊销）+ IP 限速；契约对齐平台枚举：source 固定 business
// （ValidSources 新增项，规则页按 source=business 配置路由），业务名走 app
// 字段渲染进标题；投递复用统一通知路由（无匹配规则时回退运维群保底）。
package notify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// BusinessToken 业务告警凭证：签发时绑定 app，上报 app 必须与之一致
// （防 token 借用串名）。app 不要求是平台项目——业务服务独立于平台项目体系。
type BusinessToken struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	Name       string    `gorm:"size:64;not null" json:"name"`
	App        string    `gorm:"size:64;index;not null" json:"app"`
	TokenHash  string    `gorm:"size:64;uniqueIndex;not null" json:"-"`
	Enabled    bool      `gorm:"not null;default:true" json:"enabled"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (BusinessToken) TableName() string { return "business_alert_tokens" }

// BusinessTokenOut 签发结果（Plaintext 仅此一次）。
type BusinessTokenOut struct {
	BusinessToken
	Plaintext string `json:"plaintext,omitempty"`
}

// IssueBusinessToken 签发业务告警凭证（admin）。
func (s *Service) IssueBusinessToken(ctx context.Context, name, app string) (*BusinessTokenOut, error) {
	app = strings.TrimSpace(app)
	if app == "" {
		return nil, fmt.Errorf("app 必填（业务服务标识，如 app-attribution）")
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	plaintext := "biz_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))
	t := BusinessToken{Name: name, App: app, TokenHash: hex.EncodeToString(sum[:]), Enabled: true}
	if err := s.db.WithContext(ctx).Create(&t).Error; err != nil {
		return nil, err
	}
	return &BusinessTokenOut{BusinessToken: t, Plaintext: plaintext}, nil
}

func (s *Service) ListBusinessTokens(ctx context.Context) ([]BusinessToken, error) {
	var ts []BusinessToken
	if err := s.db.WithContext(ctx).Order("id DESC").Find(&ts).Error; err != nil {
		return nil, err
	}
	return ts, nil
}

func (s *Service) SetBusinessTokenEnabled(ctx context.Context, id uint, enabled bool) error {
	res := s.db.WithContext(ctx).Model(&BusinessToken{}).Where("id = ?", id).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("业务告警凭证不存在")
	}
	return nil
}

// VerifyBusinessToken 公开接口鉴权：hash 查 token → 启用 → app 绑定校验。
// LastUsedAt best-effort 更新（不阻塞请求）。
func (s *Service) VerifyBusinessToken(ctx context.Context, plaintext, app string) (*BusinessToken, error) {
	sum := sha256.Sum256([]byte(plaintext))
	var t BusinessToken
	if err := s.db.WithContext(ctx).
		Where("token_hash = ? AND enabled = ?", hex.EncodeToString(sum[:]), true).
		First(&t).Error; err != nil {
		return nil, fmt.Errorf("凭证无效或已吊销")
	}
	if t.App != app {
		return nil, fmt.Errorf("凭证与业务 %q 不匹配（签发绑定为 %q）", app, t.App)
	}
	s.db.WithContext(ctx).Model(&t).Update("last_used_at", time.Now())
	return &t, nil
}

// BusinessAlertInput 业务告警载荷（契约见 plan-phase7 M5：level 用平台枚举）。
type BusinessAlertInput struct {
	App      string            `json:"app" binding:"required,max=64"`
	Level    string            `json:"level" binding:"required,oneof=info warn critical"`
	Title    string            `json:"title" binding:"required,max=128"`
	Detail   string            `json:"detail" binding:"max=2048"`
	Metadata map[string]string `json:"metadata" binding:"max=20,dive,max=128"`
}

// RenderBusinessAlert 渲染投递文本（标题带业务名前缀；metadata 追加 key: value 行）。
func RenderBusinessAlert(in BusinessAlertInput) (title, detail, dedupKey string) {
	title = "[" + in.App + "] " + in.Title
	dedupKey = "business/" + in.App + "/" + in.Title
	var sb strings.Builder
	sb.WriteString(in.Detail)
	for k, v := range in.Metadata {
		sb.WriteString("\n")
		sb.WriteString(k)
		sb.WriteString(": ")
		sb.WriteString(v)
	}
	return title, sb.String(), dedupKey
}
