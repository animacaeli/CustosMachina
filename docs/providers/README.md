# Provider 扩展契约（独立审核第 3 批 P4）

> 面向贡献者：如何为 CustosMachina 编写新的 CI 引擎 / Git 托管 / 通知渠道 / DNS / 观测后端适配器。

## 能力矩阵

| 扩展点 | 接口 | 内置实现 | 维护级别 |
|---|---|---|---|
| CI 引擎 | `ci.CIProvider` | gitea Actions、Jenkins | 核心 |
| Git 托管 | `ci.GitProvider` | gitea、gitee | 核心 |
| 通知渠道 | `notify` channel const | 企微/钉钉/飞书 webhook、SMTP | 核心 |
| DNS provider | `certs.DNSProvider`（lego） | alidns、cloudflare、dnspod、tencentcloud、huaweicloud、gandi、godaddy | 核心（随 lego） |
| 观测后端 | `observ.ObservBackend` | OpenObserve（v2 API） | 核心 |
| 配置中心 | `configs` AgileConfig 通道 | AgileConfig | 核心 |

## CI 引擎（CIProvider）

```go
// backend/internal/modules/ci/provider.go
type CIProvider interface {
    Name() string
    Verify(ctx context.Context) error                          // 凭证可用性（管理后台「测试」按钮）
    TriggerBuild(ctx context.Context, repoPath, branch, tag string) (*BuildRef, error)
    PollStatus(ctx context.Context, ref *BuildRef) (*BuildStatus, error)
    Log(ctx context.Context, ref *BuildRef) (string, error)   // 全量控制台日志
}
```

**最小示例**：参考 `ci/jenkins.go`（HTTP + API token）或 `ci/gitea.go`（gitea Actions API）。
关键行为：
- `TriggerBuild` 返回 `*BuildRef{ExternalID string}`，Poller 用它轮询
- `Log` 在终态时由 Poller 调用一次，截取尾部 200 行存 `log_tail`
- 凭证经 `pkg/crypto` AES-GCM 加密落库（勿明文）

## Git 托管（GitProvider）

```go
type GitProvider interface {
    Name() string  // "gitea" | "gitee" | 你的名字
    VerifyWebhook(body []byte, sigOrToken string) error
    ParsePush(body []byte) (*PushEvent, error)
    Branches(ctx context.Context, repoPath string) ([]string, error)
    RawFile(ctx context.Context, repoPath, path, ref string) ([]byte, error)
    ActionsURL(repoPath string) string
}
```

**最小示例**：`ci/gitea.go`（HMAC-SHA256 webhook）或 `ci/gitee.go`（token 回传比对）。
webhook 路由自动注册为 `POST /api/ci/webhook/{name}`。

## 通知渠道

通知渠道当前为**枚举常量**（`notify.Channel`），非插件式。添加新渠道需改：
1. `notify/model.go` 加 `Channel` 常量
2. `notify/sender.go` `detectProvider` 加 URL Host 判定
3. `notify/sender.go` `Send` 加消息格式分支
4. 前端 `admin/tabs/notify-groups.vue` 群表单渠道下拉

## 向前兼容规则

- API 与 DB 枚举遇到未知值时**跳过而非报错**（新渠道在旧版上静默忽略）
- `CIProvider`/`GitProvider` 接口新增方法时提供默认实现（Go 接口嵌入）
- webhook URL 保持 `{base}/api/ci/webhook/{provider-name}` 格式不变

## 注册方式

编译期注册（无插件运行时/动态加载）。在模块 `init()` 或构造函数中：
```go
func init() {
    ci.RegisterProvider("my-engine", NewMyProvider)
}
```

## 不做的事

- 不引入 OSGi/动态 .so 加载
- 不做 provider marketplace
- 不为未经真实使用的适配器维护兼容层（rule of two）
