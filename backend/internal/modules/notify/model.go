package notify

import (
	"time"

	"gorm.io/gorm"
)

// 群用途前缀（与名称约定一一对应）：【P】= 生产类（正式/灰度可选），【dev】= 测试类。
const (
	ScopeProd = "prod"
	ScopeDev  = "dev"

	SettingOpsGroup = "notify.ops_group_id" // 平台级运维告警群（服务器不可达等）
)

// 统一通知路由的事件源（P5 M1）。预留源（o2_alert/cert_expiring/backup_failed/
// ai_digest）允许提前配置规则，对应里程碑接入时即生效。
const (
	SourceCronFailed   = "cron_failed"   // 定时任务失败（含重试通知）
	SourceObservFailed = "observ_failed" // 观测组件部署/操作失败
	SourcePlatformOps  = "platform_ops"  // 平台级运维事件（服务器不可达/恢复等）
	SourceO2Alert      = "o2_alert"      // O2 告警事件（M3 接入）
	SourceCertExpiring = "cert_expiring" // 证书即将到期/续期失败（M5 接入）
	SourceBackupFailed = "backup_failed" // 备份任务失败（M2 接入）
	SourceAIDigest     = "ai_digest"     // AI 摘要投递（M6 接入）
	SourceBusiness     = "business"      // 业务服务告警（P7-M5 轻量入口）
)

// ValidSources 可配置的事件源集合。
var ValidSources = []string{
	SourceCronFailed, SourceObservFailed, SourcePlatformOps, SourceO2Alert,
	SourceCertExpiring, SourceBackupFailed, SourceAIDigest, SourceBusiness,
}

// 事件级别（rank 递增：info < warn < critical）。
const (
	LevelInfo     = "info"
	LevelWarn     = "warn"
	LevelCritical = "critical"
)

var levelRank = map[string]int{LevelInfo: 0, LevelWarn: 1, LevelCritical: 2}

// ValidLevels 合法级别集合。
var ValidLevels = []string{LevelInfo, LevelWarn, LevelCritical}

// Group 通知群（多渠道，管理员手工登记）：webhook（企微/钉钉/飞书群机器人）、
// telegram（Bot API，chat id 在 Target）、smtp（邮箱收件人在 Target，逗号分隔）。
// 渠道凭据（bot token / SMTP 账号）是平台级设置，不随群存。
type Group struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	Name      string         `gorm:"size:64;uniqueIndex;not null" json:"name"`
	Scope     string         `gorm:"size:8;not null" json:"scope"`                    // prod | dev
	Channel   string         `gorm:"size:16;not null;default:webhook" json:"channel"` // webhook | telegram | smtp
	Webhook   string         `gorm:"type:text" json:"-"`                              // webhook 渠道：加密 url；其他渠道闲置
	Target    string         `gorm:"size:512" json:"target"`                          // telegram: chat id；smtp: 收件人（逗号分隔）
	Remark    string         `gorm:"size:255" json:"remark"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Group) TableName() string { return "notify_groups" }

// Rule 通知路由规则：事件按 source + 最低级别匹配，投递到目标群；
// 可选静默时段（critical 永不静默）与同类聚合窗口。
type Rule struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	Name         string    `gorm:"size:64;not null" json:"name"`
	Source       string    `gorm:"size:32;not null;index" json:"source"`         // 事件源，见 ValidSources
	MinLevel     string    `gorm:"size:8;not null;default:warn" json:"minLevel"` // 事件级别 >= 此值才匹配
	GroupID      uint      `gorm:"not null" json:"groupId"`                      // 目标通知群
	SilentStart  string    `gorm:"size:5" json:"silentStart"`                    // 静默开始 HH:MM，空=无静默
	SilentEnd    string    `gorm:"size:5" json:"silentEnd"`                      // 静默结束 HH:MM（支持跨零点）
	AggregateSec int       `gorm:"not null;default:0" json:"aggregateSec"`       // 聚合窗口秒，0=立即投递
	Enabled      bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func (Rule) TableName() string { return "notify_rules" }

// SendRecord 通知发送留痕（失败也落，便于排查企微侧问题）。
type SendRecord struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	GroupID   uint      `gorm:"index" json:"groupId"`
	Title     string    `gorm:"size:128" json:"title"`
	Content   string    `gorm:"type:text" json:"content"`
	Status    string    `gorm:"size:16" json:"status"` // ok | failed
	Error     string    `gorm:"size:512" json:"error"`
	CreatedAt time.Time `json:"createdAt"`
}

func (SendRecord) TableName() string { return "notify_records" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any {
	return []any{&Group{}, &SendRecord{}, &Rule{}, &BusinessToken{}}
}
