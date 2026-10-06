# 安全策略

CustosMachina 是持有纳管主机 SSH 凭据与多套组件凭据的运维平台，安全报告对我们非常重要。

## 漏洞披露

- **请勿在公开 issue 中报告安全漏洞**（issue 会被爬虫和订阅者即时看到）
- 报告渠道：使用 GitHub 私密安全公告（Security → Report a vulnerability），或联系维护者邮箱
- 报告内容请包含：影响版本、复现步骤 / PoC、影响评估、（可选）修复建议
- 我们会在 **3 个工作日内**确认收到的报告，修复视严重度按 hotfix 或常规版本发布

## 支持的版本

| 版本 | 支持状态 |
|---|---|
| 最新 tag（v0.12.x） | ✅ 安全修复 |
| 更早版本 | ❌ 请升级 |

## 安全设计要点（供评审参考）

- SSH 全仓单一实现（`resources` 模块，TOFU 主机密钥固定）；凭据 AES-256-GCM 加密（AAD 字段级绑定——**范围与约束**：①绑定到字段而非行，同字段跨行互换仍可解开（行级绑定在路线图上）；②保护仅对 v0.12.2+ 写入的密文生效，存量密文经无 AAD 回退读取、下次保存时自动升级；③**不可回滚**：新版二进制写入的密文旧版二进制（<v0.12.2）解不开——回滚版本会导致 webhook 通知、registry 凭据、kubeconfig、证书续期等静默失效，回滚前先备份并咨询）
- 终端路径拒绝 casbin 通配：仅内置 admin 角色 + 逐主机精确 ACL（双 gate）
- 用户输入拼远端 shell 命令处一律 `pkg/shellx.Quote` + 入参白名单
- JWT HMAC 家族严格断言、登录限速/锁定、refresh token 一次性轮换（GETDEL）
- 公开端点（webhook / 配置拉取 / 业务告警）全部带 token 校验 + IP 限速

## 部署加固清单

1. `CUSTOS_AUTH_JWT_SECRET` 与 `CUSTOS_SECRETS_MASTER_KEY` 必须显式配置强随机值
2. `CUSTOS_CORS_ORIGINS` 收敛为前端实际域名
3. 平台自身流量经可信反代时正确传递 `X-Forwarded-For`（限速按真实 IP 聚合）
4. 平台管理端口不对公网开放；Web 终端按主机 ACL 最小授权
