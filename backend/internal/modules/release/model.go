// Package release 发布管理（第三阶段 M3）：把已通过 CI 的标签发布到环境部署目标。
// 发布 = 按标签从 gitea 取部署描述（compose 文件）→ resources.DeployComposeTo 部署；
// 回滚 = 重新发布历史 release 指向的标签（新记录 rollback_of 指向旧记录）。
package release

import (
	"time"

	"gorm.io/gorm"
)

const (
	ReleaseRunning = "running" // 蓝绿异步执行中（前端轮询进度）
	ReleaseSuccess = "success"
	ReleaseFailed  = "failed"
)

type Release struct {
	ID           uint           `gorm:"primarykey" json:"id"`
	ProjectID    uint           `gorm:"index:idx_rel_proj_env;not null" json:"projectId"`
	EnvType      string         `gorm:"index:idx_rel_proj_env;size:16;not null" json:"envType"` // prod | canary
	Tag          string         `gorm:"size:128;not null" json:"tag"`
	ServerID     uint           `json:"serverId"`
	Runtime      string         `gorm:"size:16" json:"runtime"`
	ReleaseBy    string         `gorm:"size:64" json:"releaseBy"`
	DurationSecs int            `json:"durationSecs"`
	Status       string         `gorm:"size:16;not null" json:"status"`
	Color        string         `gorm:"size:8" json:"color"`     // 蓝绿发布落点颜色（blue|green；非 prod 为空）
	Output       string         `gorm:"type:text" json:"output"` // 部署输出（失败原因）
	RollbackOf   *uint          `json:"rollbackOf"`              // 回滚指向的原 release
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Release) TableName() string { return "releases" }

// BGState 蓝绿状态：项目当前的活跃颜色（一项目一行）。无记录 = 未启用蓝绿。
type BGState struct {
	ProjectID   uint   `gorm:"primarykey" json:"projectId"`
	ActiveColor string `gorm:"size:8;not null" json:"activeColor"` // blue | green
	// 活跃域实际入口容器名（部署形态决定：占位符 compose=<域>-1；存量=<域>-server-1；
	// conf 渲染直接引用，避免两代命名规则在渲染层推断）
	ActiveContainer string `gorm:"size:128" json:"activeContainer"`
	// 转正后的稳态容器名（空=未转正；转正= rename 到 <proj>-prod-1 + conf 恒定名）
	PromotedContainer string    `gorm:"size:128" json:"promotedContainer"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

func (BGState) TableName() string { return "blue_green_states" }

// CanaryState 灰度域状态（2026-11-08 命名统一：canary 域版本化）：
// 记录当前活跃 canary 域与容器名，新灰度部署后据此销毁旧域。
type CanaryState struct {
	ProjectID uint      `gorm:"primarykey" json:"projectId"`
	Domain    string    `gorm:"size:128;not null" json:"domain"`    // <proj>-canary-<yyyymmdd-缩写>
	Container string    `gorm:"size:128;not null" json:"container"` // 入口容器名（含端口前缀名）
	UpdatedAt time.Time `json:"updatedAt"`
}

func (CanaryState) TableName() string { return "canary_states" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any { return []any{&Release{}, &BGState{}, &CanaryState{}} }
