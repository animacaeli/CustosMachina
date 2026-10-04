// Package ci CI 集成（第三阶段 M2 起步为 gitea 单场景，P6-M8 起 GitProvider/CIProvider 多生态）。
// 交互面收窄为"观测 + 触发"：构建由项目仓库里的流水线驱动（监听标签推送），
// 平台接收 webhook 落构建记录、按 provider 轮询状态、失败/成功推通知。
// 已接组合：gitea+gitea Actions、gitee+Jenkins；适配器见 provider.go。
package ci

import (
	"time"

	"gorm.io/gorm"
)

// 构建状态。
const (
	BuildPending = "pending"
	BuildRunning = "running"
	BuildSuccess = "success"
	BuildFailed  = "failed"
)

// 构建来源。
const (
	SourceTag = "tag" // 标签推送自动触发（正式/灰度）
)

// Build 构建记录（三环境统一承接；测试环境槽位构建 M5 复用）。
type Build struct {
	ID           uint           `gorm:"primarykey" json:"id"`
	ProjectID    uint           `gorm:"index:idx_proj_env;not null" json:"projectId"`
	EnvType      string         `gorm:"index:idx_proj_env;size:16;not null" json:"envType"` // prod | canary | test
	Tag          string         `gorm:"size:128;not null" json:"tag"`
	SHA          string         `gorm:"size:64" json:"sha"`                             // 标签指向的提交（轮询 commit status 用）
	Provider     string         `gorm:"size:16;not null;default:gitea" json:"provider"` // gitea | gitee（CI 引擎由映射决定，历史构建不随项目配置漂移）
	Builder      string         `gorm:"size:64" json:"builder"`
	Source       string         `gorm:"size:16;not null" json:"source"`
	Status       string         `gorm:"size:16;not null;default:pending" json:"status"`
	DurationSecs int            `json:"durationSecs"`                    // 终态时计算；running 期由前端用 started_at 差值显示
	FailCount    int            `gorm:"not null;default:0" json:"-"`     // 防抖：连续 N 次非 success 才标 failed（commit status 在 job 切换间隙可能短暂回落）
	LogURL       string         `gorm:"size:512" json:"logUrl"`          // CI Web UI 页面（外链兜底）
	Notified     bool           `gorm:"not null;default:false" json:"-"` // 终态是否已通知
	StartedAt    time.Time      `json:"startedAt"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Build) TableName() string { return "builds" }

// Registry 镜像仓库（阿里云 ACR / 腾讯云 TCR / gitea 内置）。
type Registry struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	Name       string         `gorm:"size:64;uniqueIndex;not null" json:"name"`
	Type       string         `gorm:"size:16;not null" json:"type"` // aliyun | tencent | gitea | harbor
	Address    string         `gorm:"size:255;not null" json:"address"`
	Credential string         `gorm:"type:text" json:"-"` // 加密后的用户名:密码，绝不外发
	Remark     string         `gorm:"size:255" json:"remark"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Registry) TableName() string { return "registries" }

// GlobalConfig 平台全局 CI 配置（单行表 id=1）。
// P6-M8 扩展：gitee（git 托管）与 Jenkins（CI 引擎）独立凭证——各生态账号体系不同，不共用。
type GlobalConfig struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	GiteaBaseURL  string    `gorm:"size:255" json:"giteaBaseUrl"`
	GiteaToken    string    `gorm:"type:text" json:"-"` // 加密后的全局 token
	WebhookSecret string    `gorm:"size:128" json:"-"`  // gitea webhook HMAC 密钥（明文存取，仅服务端校验用）
	GiteeBaseURL  string    `gorm:"size:255" json:"giteeBaseUrl"`
	GiteeToken    string    `gorm:"type:text" json:"-"`          // 加密后的 gitee 全局 token
	GiteeWebhook  string    `gorm:"size:128" json:"-"`           // gitee webhook 密码（X-Gitee-Token 明文比对）
	JenkinsURL    string    `gorm:"size:255" json:"jenkinsUrl"`  // 如 https://jenkins.example.com
	JenkinsUser   string    `gorm:"size:128" json:"jenkinsUser"` // API token 所属账号
	JenkinsToken  string    `gorm:"type:text" json:"-"`          // 加密后的 Jenkins API token
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (GlobalConfig) TableName() string { return "ci_global_config" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any {
	return []any{&Build{}, &Registry{}, &GlobalConfig{}}
}
