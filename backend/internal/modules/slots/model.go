// Package slots 测试环境槽位（第三阶段 M5，按 D14 落地）：
// 占用（人+分支+时长）→ 分支推送自动重建 → 到期/手动释放销毁容器（配置保留）。
// 槽位行按需创建；"全部槽位"视图由项目的 TestSlotCount 生成名字集合。
package slots

import (
	"time"

	"gorm.io/gorm"
)

const (
	StatusOccupied = "occupied"
	StatusExpired  = "expired" // 到期未释放（宽限期内仍占用）
)

type Slot struct {
	ID            uint           `gorm:"primarykey" json:"id"`
	ProjectID     uint           `gorm:"index;not null" json:"projectId"`
	SlotName      string         `gorm:"size:32;not null" json:"slotName"` // dev1 / dev2 ...
	Branch        string         `gorm:"size:128;not null" json:"branch"`
	OccupiedBy    string         `gorm:"size:64;not null" json:"occupiedBy"` // 展示名
	OccupiedByUID uint           `json:"occupiedByUid"`
	OccupiedAt    time.Time      `json:"occupiedAt"`
	Duration      time.Duration  `json:"duration"`
	ExpireAt      time.Time      `gorm:"index" json:"expireAt"`
	Status        string         `gorm:"size:16;not null" json:"status"`
	LastBuildID   *uint          `json:"lastBuildId"` // 最近一次自动构建
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	DeletedAt     gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Slot) TableName() string { return "env_slots" }

// Override 槽位差异化配置（key/value，释放保留；部署时环境变量注入）。
type Override struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	ProjectID uint      `gorm:"index:uniq_slot_key;not null" json:"projectId"`
	SlotName  string    `gorm:"index:uniq_slot_key;size:32;not null" json:"slotName"`
	Key       string    `gorm:"index:uniq_slot_key;size:64;not null" json:"key"`
	Value     string    `gorm:"size:512" json:"value"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (Override) TableName() string { return "slot_overrides" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any { return []any{&Slot{}, &Override{}} }
