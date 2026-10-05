# Changelog

## v0.10.0 (2026-10-05)

P6 AI 主线 + 配置双形态 + 生态适配：AI 成为主线功能。

### AI 对话（M1）

- SSE 流式对话（通用/平台上下文双模式），全平台实时数据自动注入（项目/主机/构建/发布/事件/cron/配置元信息）
- 多模态附件（图片 image_url、文本类并入正文，≤3 个/条，单文件 ≤4MB）
- 会话管理：惰性创建（空对话不落库）、软删除留档、管理员按用户查看、切换页面对话不中断
- AI 中转层：OpenAI 兼容协议、端点/密钥/模型可配、DeepSeek 真机验证
- 安全边界（全角色一致）：只读助手绝不执行变更（管理员也不行）、权限边界声明+越权拒答、杜绝经 AI 安装 skill/MCP、注入免疫

### MCP Server（M2）

- 官方 Go SDK（modelcontextprotocol/go-sdk v1.8.0），Streamable HTTP 单端点
- 8 个只读工具：list_servers/projects/builds/releases/cron_runs/cron_jobs/configs/containers
- 应用级 token 签发（admin/dev 角色）、调用审计、限速

### Skill 声明式技能（M3 前半）

- ai_skills 表：Prompt 模板 `{{q}}` 占位符 + Runbook，纯 prompt 注入无代码执行
- `/` 命令面板（Claude Code 风格）：↑↓ 选择、Tab/Enter 确认、Esc 关闭
- 管理后台「AI 技能」tab：技能 CRUD + 角色白名单
- 内置种子技能：troubleshoot（结构化排查）、release-check（发布体检）

### 对话内工具调用（M3 后半）

- Function Calling：模型可自动调用平台只读工具（与 MCP tools 同源），结果回填续答
- 流式 tool_calls 增量聚合（按 index 分片聚合 id/name/arguments）
- 上游不支持 function calling 时自动降级（HTTP 400 → 去工具重试）
- 工具调用留痕（ChatMessage.Tools JSON 审计）+ 前端 🔧 标签实时渲染

### NL→操作建议卡（M4）

- AI 只生成意图（容器重启/cron 触发/配置下发），操作建议卡引导用户到平台页面自行操作
- 不执行任何变更（不论用户身份，管理员也不行）——产品红线

### 配置拉取 API（M5）

- `GET /api/config/{app}/{env}`：按应用×环境深合并（yaml/json/toml 结构合并，env/ini key 平铺，冲突报错）
- 应用级 token（pull_ 前缀，sha256 落库，限 app×env 范围）
- 聚合指纹 X-Config-Version/ETag + If-None-Match 304 + 60s 短缓存（鉴权在缓存前）
- 管理后台「配置中心」tab：凭证签发 + AgileConfig 连接 + 手动同步/对账

### AgileConfig 共存（M7）

- 一套 UI：配置文件是唯一真相源与编辑入口，AgileConfig 是纯后端通道
- env/ini 文件「下发」时自动同步到 AgileConfig（已配置才触发，异步不阻塞）
- 对账三态漂移报告（missing/extra/drifted），告警不自动覆盖
- 合并视图：项目×环境聚合最终生效配置（JSON/YAML 只读预览）
- SDK 热更真机验证：WebSocket 推送 40s 内 OnChange 回调触发

### gitee + Jenkins 生态适配（M8）

- GitProvider/CIProvider 双接口抽象：gitea（既有）/ gitee（API v5）git 适配 + Jenkins CI 状态查询
- 存量 gitea 项目零扰动（回归验证通过）
- gitee tag push → 平台 Build 记录 → Jenkins 构建 → 状态回读 → tag 发布

### 通知渠道泛化（M9）

- SMTP 邮件通知：多收件人群发、465 端口隐式 TLS 支持、markdown 降级纯文本
- 通知规则路由：一个事件可路由到多个通知群（webhook + SMTP 混合）
- Telegram 已移除（国内不用，用户定调）

### 全站 AI 悬浮助手（M10）

- 悬浮球（自绘 SVG 机器人：渐变+天线+呼吸/回答动画）→ 悬浮面板（无遮罩、拖拽调宽高并记忆）
- 挂 App 根：路由切换对话不中断（流式中切页输出继续）
- 页面上下文感知：发送携带当前路由，AI 主动调工具取当前页数据
- 快捷提问卡片 + 对话历史子视图（admin 筛选/软删留档）
- 全功能与独立页面同源（ChatPanel 组件化共用）

### 堡垒机补齐（M6）

- 终端权限按主机细粒度：casbin 用户级策略（登录名级授权，admin 角色通配不受影响）
- 终端会话审计回放：asciinema v2 cast 录制（带时序帧）、旧 .log 即时转单帧兼容、网页回放
- 版本化 migration：schema_migrations + embed 顺序迁移文件，新库 AutoMigrate 快速起步、存量库走版本化增量

### 基础设施

- 版本化 migration（开源化前置）
- usePanel composable 沉淀
- rbac 种子 v19（终端审计/授权/配置中心/AI 技能等资源点）
