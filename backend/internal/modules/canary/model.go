// Package canary 灰度策略（第三阶段 M4）：策略模型只存语义（header 匹配 / 流量比例），
// 承载层适配器化——compose 期渲染 nginx 配置片段写目标机，k3s 期翻译成原生流量资源（v1.x）。
// 发布语义：策略增删改只落库（未发布），聚合发布 = 版本化整体替换生效配置。
package canary

import (
	"time"

	"gorm.io/gorm"
)

// 策略类型。
const (
	TypeHeader  = "header"  // 指定请求头命中 → 对应灰度实例
	TypeTraffic = "traffic" // 未命中请求头 → 按比例加权随机进灰度
)

type Policy struct {
	ID               uint           `gorm:"primarykey" json:"id"`
	ProjectID        uint           `gorm:"index;not null" json:"projectId"`
	Type             string         `gorm:"size:8;not null" json:"type"` // header | traffic
	HeaderKey        string         `gorm:"size:64" json:"headerKey"`
	HeaderValue      string         `gorm:"size:128" json:"headerValue"`
	TrafficPercent   int            `json:"trafficPercent"`
	BoundTag         string         `gorm:"size:128" json:"boundTag"` // 绑定的 canary 标签部署
	Enabled          bool           `gorm:"not null;default:true" json:"enabled"`
	PublishedVersion int            `gorm:"not null;default:0" json:"publishedVersion"` // 0=未发布
	CreatedAt        time.Time      `json:"createdAt"`
	UpdatedAt        time.Time      `json:"updatedAt"`
	DeletedAt        gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Policy) TableName() string { return "canary_policies" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any { return []any{&Policy{}} }
