// provider.go P6-M8 生态适配：git 托管与 CI 引擎两层适配器接口。
// rule of two 落地：gitea+gitee（GitProvider）、gitea Actions+Jenkins（CIProvider）
// 实现确定要来，接口正当。适配器实例按全局配置即时构造（配置可运行期修改），
// 不做启动期注册表。CI 引擎与 git 托管的组合映射固定：gitea→gitea Actions、
// gitee→Jenkins（用户存量主力组合）；新组合出现时在 ciEngineFor 扩展。
package ci

import (
	"context"
	"time"
)

// Provider 常量（projects.provider 与 builds.provider 取值）。
const (
	ProviderGitea = "gitea"
	ProviderGitee = "gitee"
)

// ciEngineFor git 托管 → CI 引擎。平台只读 CI 状态，构建由仓库 webhook 驱动。
func ciEngineFor(gitProvider string) string {
	switch gitProvider {
	case ProviderGitee:
		return "jenkins"
	default:
		return "gitea-actions"
	}
}

// PushEvent 归一后的推送事件（各家 webhook payload 的公共语义）。
type PushEvent struct {
	Provider string // gitea | gitee（由回调端点决定，不信任 payload）
	Ref      string // refs/tags/v1.0.0 / refs/heads/<branch>
	SHA      string
	RepoPath string // owner/repo
	Pusher   string
}

// BuildRef CI 查询定位：gitea Actions 按 SHA；Jenkins 按 job + tag 参数。
type BuildRef struct {
	RepoPath string
	SHA      string
	Tag      string
	Job      string // Jenkins job 名（项目级配置，gitea 路径忽略）
}

// GitProvider git 托管适配器。
type GitProvider interface {
	Name() string
	// VerifyWebhook 校验回调鉴权头（gitea=HMAC-SHA256、gitee=X-Gitee-Token 明文常量时间比较）。
	VerifyWebhook(body []byte, sigOrToken string) error
	ParsePush(body []byte) (*PushEvent, error)
	Branches(ctx context.Context, repoPath string) ([]string, error)
	// RawFile 按标签/分支取仓库文件（release 取部署描述用）。
	RawFile(ctx context.Context, repoPath, path, ref string) ([]byte, error)
	// ActionsURL CI 日志的 Web UI 外链兜底。
	ActionsURL(repoPath string) string
}

// CIProvider CI 引擎适配器（状态/日志；只读）。
type CIProvider interface {
	Name() string
	// Status 返回 BuildPending/BuildRunning/BuildSuccess/BuildFailed。
	// 不可得（引擎不可达等瞬时错误）返回 error，调用方保留 pending 下轮再试。
	Status(ctx context.Context, ref BuildRef) (string, error)
	Log(ctx context.Context, ref BuildRef) (string, error)
}

// httpClient 共享的超时（各家 API 均为轻量 JSON/console 请求）。
const apiTimeout = 15 * time.Second
