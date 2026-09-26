// Package projects 项目管理（第三阶段 M1）：项目 = git 仓库 + CI 配置 + 环境部署目标 + 通知配置。
// 正式 / 灰度 / 测试三环境是项目的部署目标（project_env_targets），不是独立实体。
package projects

import (
	"time"

	"gorm.io/gorm"
)

// 环境类型。
const (
	EnvProd   = "prod"
	EnvCanary = "canary"
	EnvTest   = "test"
)

// 部署目标运行时（compose 与 k3s 按项目共存，见 plan-phase3 运行时路线）。
const (
	RuntimeCompose = "compose"
	RuntimeK3s     = "k3s" // v1.x 才有实现，枚举先行
)

// Project 项目。
type Project struct {
	ID          uint   `gorm:"primarykey" json:"id"`
	Name        string `gorm:"size:64;uniqueIndex;not null" json:"name"`
	RepoURL     string `gorm:"size:255;not null" json:"repoUrl"`  // gitea 仓库（如 https://gitea.internal/org/repo）
	RepoPath    string `gorm:"size:255;not null" json:"repoPath"` // org/repo（调 gitea API 用）
	CIToken     string `gorm:"type:text" json:"-"`                // 加密后的项目级 token（空 = 用全局）
	ComposePath string `gorm:"size:255" json:"composePath"`       // 部署描述文件在仓库中的路径

	DefaultBranch string `gorm:"size:128;default:main" json:"defaultBranch"`

	// 通知（M1）：按环境绑定 notify_groups；留空 = 该环境不发通知
	NotifyProdGroupID   *uint `json:"notifyProdGroupId"`
	NotifyCanaryGroupID *uint `json:"notifyCanaryGroupId"`
	NotifyTestGroupID   *uint `json:"notifyTestGroupId"`
	NotifyOnSuccess     bool  `json:"notifyOnSuccess"` // 构建成功是否通知（默认只报失败）

	// 槽位（M5 使用，配置先行）：个数仅管理员可改
	TestSlotCount int `gorm:"not null;default:3" json:"testSlotCount"`

	// 灰度（M4 使用）：流量策略比例总和上限
	TrafficCap    int `gorm:"not null;default:50" json:"trafficCap"`
	SlotGraceDays int `gorm:"not null;default:3" json:"slotGraceDays"` // 槽位过期宽限天数

	DeployPrefix string `gorm:"-" json:"deployPrefix"` // <norm>-<env> 的项目段（容器视图过滤用，后端统一下发防前后端漂移）

	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Project) TableName() string { return "projects" }

// EnvTarget 环境部署目标：项目的某环境部署到哪台服务器、用什么运行时。
type EnvTarget struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	ProjectID uint      `gorm:"uniqueIndex:uniq_proj_env;not null" json:"projectId"`
	EnvType   string    `gorm:"uniqueIndex:uniq_proj_env;size:16;not null" json:"envType"` // prod | canary | test
	ServerID  uint      `gorm:"not null" json:"serverId"`                                  // FK servers.id（资源管理）
	Runtime   string    `gorm:"size:16;not null;default:compose" json:"runtime"`           // compose | k3s
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (EnvTarget) TableName() string { return "project_env_targets" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any {
	return []any{&Project{}, &EnvTarget{}}
}

// ValidEnvTypes 合法环境枚举。
func ValidEnvTypes() []string { return []string{EnvProd, EnvCanary, EnvTest} }
