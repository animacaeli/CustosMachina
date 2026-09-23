// Package release 发布管理（第三阶段 M3）：把已通过 CI 的标签发布到环境部署目标。
// 发布 = 按标签从 gitea 取部署描述（compose 文件）→ resources.DeployComposeTo 部署；
// 回滚 = 重新发布历史 release 指向的标签（新记录 rollback_of 指向旧记录）。
package release

import (
	"time"

	"gorm.io/gorm"
)

const (
	ReleaseSuccess = "success"
	ReleaseFailed  = "failed"
)

type Release struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	ProjectID  uint           `gorm:"index:idx_rel_proj_env;not null" json:"projectId"`
	EnvType    string         `gorm:"index:idx_rel_proj_env;size:16;not null" json:"envType"` // prod | canary
	Tag        string         `gorm:"size:128;not null" json:"tag"`
	ServerID   uint           `json:"serverId"`
	Runtime    string         `gorm:"size:16" json:"runtime"`
	ReleaseBy  string         `gorm:"size:64" json:"releaseBy"`
	Status     string         `gorm:"size:16;not null" json:"status"`
	Output     string         `gorm:"type:text" json:"output"` // 部署输出（失败原因）
	RollbackOf *uint          `json:"rollbackOf"`              // 回滚指向的原 release
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Release) TableName() string { return "releases" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any { return []any{&Release{}} }
