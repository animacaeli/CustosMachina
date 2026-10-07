# Provider 扩展契约（独立审核第 3 批 P4；v0.12.15 按真实接口重写）

> 面向贡献者：如何为 CustosMachina 编写新的 CI 引擎 / Git 托管 / 通知渠道 / DNS / 观测后端适配器。
> 本文与 `backend/internal/modules/ci/` 的真实接口一一对应（编译期 switch，无插件运行时）。

## 能力矩阵

| 扩展点 | 接口 | 内置实现 | 维护级别 |
|---|---|---|---|
| CI 引擎 | `ci.CIProvider` | gitea Actions、Jenkins | 核心 |
| Git 托管 | `ci.GitProvider` | gitea、gitee | 核心 |
| 通知渠道 | `notify` channel const | 企微/钉钉/飞书 webhook、SMTP | 核心 |
| DNS provider | `certs.buildDNSProvider`（返回 lego `challenge.Provider`） | alidns、cloudflare、dnspod、tencentcloud、huaweicloud、gandi、godaddy | 核心（随 lego） |
| 观测后端 | `observ` 模块（Handler/Alert，OpenObserve v2 API 直连） | OpenObserve | 核心 |
| 配置中心 | `configs` AgileConfig 通道 | AgileConfig | 核心 |

## CI 引擎（CIProvider）——只读

平台**只读** CI 状态：构建由仓库 webhook 驱动，平台不触发构建。

```go
// backend/internal/modules/ci/provider.go
type CIProvider interface {
    Name() string
    // Status 返回 BuildPending/BuildRunning/BuildSuccess/BuildFailed。
    // 不可得（引擎不可达等瞬时错误）返回 error，调用方保留 pending 下轮再试。
    Status(ctx context.Context, ref BuildRef) (string, error)
    // Log 返回全量控制台日志（终态时由 Poller 调用一次，截尾部 200 行存 log_tail）。
    Log(ctx context.Context, ref BuildRef) (string, error)
}
```

`BuildRef` 定位一次构建：gitea Actions 按 SHA；Jenkins 按 job + tag 参数
（`RepoPath/SHA/Tag/Job` 四字段，见 provider.go）。

## Git 托管（GitProvider）

```go
type GitProvider interface {
    Name() string  // "gitea" | "gitee" | 你的名字
    VerifyWebhook(body []byte, sigOrToken string) error  // gitea=HMAC-SHA256、gitee=token 常量时间比较
    ParsePush(body []byte) (*PushEvent, error)
    Branches(ctx context.Context, repoPath string) ([]string, error)
    RawFile(ctx context.Context, repoPath, path, ref string) ([]byte, error)  // release 取部署描述
    ActionsURL(repoPath string) string  // CI 日志 Web UI 外链兜底
    Commits(ctx context.Context, repoPath, ref string, since time.Time, limit int) (string, error)
    // ^ P7-M2 告警 AI 分析用：ref 空=默认分支；返回归一化文本（sha 短/作者/时间/首行消息）
}
```

## 注册方式——编译期 switch，无注册表

**没有 `RegisterProvider` 之类的动态注册**。适配器实例按全局配置即时构造
（配置可运行期修改），git 托管 → CI 引擎的组合映射固定：

```go
// provider.go —— 新组合在这里扩展
func ciEngineFor(gitProvider string) string {
    switch gitProvider {
    case ProviderGitee:
        return "jenkins"
    default:
        return "gitea-actions"
    }
}
```

webhook 路由同样是编译期固定（`ci/handler.go`）：

```go
r.Public.POST("/ci/webhook/gitea", h.webhookGitea)   // 实际路径 /api/ci/webhook/gitea
r.Public.POST("/ci/webhook/gitee", h.webhookGitee)   // 实际路径 /api/ci/webhook/gitee
```

### 新增一个 Git 托管/CI 引擎的真实修改点

1. `ci/` 新增适配器文件，实现 `GitProvider` / `CIProvider`（参考 `gitea.go`、`gitee.go`、`jenkins.go`）；
2. `ci/provider.go` `ciEngineFor` 加组合映射；
3. `ci/handler.go` 加 webhook 路由与鉴权头解析；
4. 全局配置模型（`ci_global_config`）加凭证字段 + 前端 provider 枚举下拉；
5. 测试（见下节）；
6. 凭证经 `pkg/crypto` AES-GCM 加密落库（勿明文）。

## 契约测试

- `provider_contract_test.go` 的 `RunCIProviderContract(t, p)` 断言 **Name 契约**
  （非空、不含空格/路径分隔符——Name 用作 DB 枚举值与 webhook 路径段）；
- `Status/Log` 的行为契约（状态映射、日志获取、不可达降级）**由各引擎自己的
  fake-server 测试驱动**（需要真实 `BuildRef`，无法在通用套件里构造）——参考
  `jenkins_test.go` 的 `TestJenkinsStatusAndLog`、`gitea_test.go`/`gitee_test.go`。
  新引擎适配器必须提供同等的 fake-server 行为测试。

## 通知渠道

通知渠道当前为**枚举常量**（`notify.Channel`），非插件式。添加新渠道需改：
1. `notify/channels.go` 加 `Channel` 常量（当前仅 webhook/smtp）
2. `notify/sender.go` `detectProvider` 加 URL Host 判定
3. `notify/sender.go` `Send` 加消息格式分支
4. 前端 `admin/tabs/notify-groups.vue` 群表单渠道下拉

## 向前兼容规则

- API 与 DB 枚举遇到未知值时**跳过而非报错**（新渠道在旧版上静默忽略）
- `CIProvider`/`GitProvider` 接口新增方法时提供默认实现（Go 接口嵌入）
- webhook URL 保持 `{base}/api/ci/webhook/{provider-name}` 格式不变

## 不做的事

- 不引入 OSGi/动态 .so 加载/插件运行时
- 不做 provider marketplace
- 不为未经真实使用的适配器维护兼容层（rule of two：两个真实实现才抽象）
