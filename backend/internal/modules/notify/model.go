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

// Group 通知群（企微群机器人 webhook，管理员手工登记）。
type Group struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	Name      string         `gorm:"size:64;uniqueIndex;not null" json:"name"` // 必须以【P】或【dev】开头
	Scope     string         `gorm:"size:8;not null" json:"scope"`             // prod | dev（由名称前缀推导并强校验）
	Webhook   string         `gorm:"type:text" json:"-"`                       // 加密后的 webhook，绝不外发
	Remark    string         `gorm:"size:255" json:"remark"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Group) TableName() string { return "notify_groups" }

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
	return []any{&Group{}, &SendRecord{}}
}
