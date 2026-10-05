# 第六阶段计划：AI 主线（对话 UI / MCP Server / NL→操作）+ 配置双形态与生态适配 + 欠账收口

> 状态：**已定稿并开工**（v1.5，2026-10-04 定稿收官：D1~D8 全部拍板，D8 取选项 A 判据触发+收官重估；开工时序 M8 → SSE 设计文档 → M1，M8 已启动）
> 范围决策记录（承 roadmap §四/§五/§六与 P5 收官留档，含定稿决策）：**对话 UI 为 AI 主线首项**（旗舰体验，独立一级菜单）；**MCP Server 化与对话 UI 同期**（对话 UI 本身就是"内置 MCP 客户端 + 界面"）；**NL→操作带人工确认层**（首批白名单低危三件套，高危 P7 观察后再议）；**配置拉取 API（方案 B）+ AgileConfig 共存接入**（一个模型/两种底层/三种消费形态；平台单向推送+漂移告警；首批仅 K/V）；**gitee+Jenkins 组合适配前置**（存量项目主力组合，自用刚需优先于 AI 主线）；**开源生态适配批次全做**（用户 2026-10-04 追加：GitHub/GitLab GitProvider + Telegram/SMTP 通知 + GitHub OAuth 登录，不留 backlog，见 M9）；**IM 机器人双向顺延 P7**；堡垒机语义/审计回放/composable 抽取/版本化 migration 四笔顺延欠账**本阶段必须清**（均已顺延一次）。
> 阶段定位补充（v1.4 修正）：P6 是**功能开发的收官立项阶段**（运维功能面 + AI 主线 + 生态适配在此全部交付），收官后功能线转维护期（§十）；但**核心基建演进线独立于此**——compose MVP → k3s 的演进按判据/时间表触发另立 P7，见 §十一。
> 上游依据：docs/roadmap.md §四（P6）、§五（AI 规划）、§六（生态抽象纪律）、§三.3（AgileConfig 决策记录）；docs/plan-phase5-services.md 收官状态。
> 本阶段要回答的三个问题：
> 1. **对话入口如何不做只读超权**——会话挂载 Context Pack 后，角色过滤与 casbin 既有资源点如何对齐（对话不得成为绕过权限模型的旁路）？
> 2. **配置的两种消费底层如何共存**——文件真相源与 AgileConfig K/V 在同一应用×环境下并行，UI/模型/审计如何归一而不互相渗透（roadmap 解耦硬性要求的落地）？
> 3. **AI 主线与生态刚需/欠账的取舍**——时间不够时先保对话闭环还是先保 gitee+Jenkins 存量项目接入（自用刚需）？

## 一、总判断：P6 是"AI 成为主线功能"的阶段

P1~P5 把运维功能做成了 AI 主线的**地基**（数据、通道、审计、上下文），P5 末已铺中转层（OpenAI 兼容、非流式 `Complete`）与 Context Pack 只读版（角色过滤 + DLP 六类 + 不可信输入围栏）。P6 把这些地基变成用户可感知的主线功能：

- **对话**：从"告警通知附一段 AI 摘要"到"独立对话 UI（通用模式 + 平台上下文挂载）"；
- **工具协议**：从"平台私有 pack 组装"到"标准 MCP tools 暴露"（Claude Desktop / IDE / 任意 MCP 客户端可直连，开源放大器）；
- **操作**：从"AI 只读建议"到"NL→待执行操作→人确认→走既有 casbin 与审计执行"；
- **配置消费**：从"SFTP 下发文件"补齐"服务启动 API 拉取"（方案 B，文件真相源一址两用）+ AgileConfig K/V 共存（SDK 热更形态，平台模型归一）；
- **生态适配**：gitee+Jenkins 组合接入（GitProvider + CIProvider 两层抽象，存量项目主力组合回归平台）；
- **欠账**：堡垒机语义 + 终端审计回放（#20/#38）+ composable 抽取（#33）+ 版本化 migration（#9 延伸，开源化前置）。

**现状锚点（起草时实测）**：ai 模块 `Complete` 为非流式单发；平台 SSE 仅容器日志一处（`resources/containers.go`），cron 实时日志为 2s 轮询、终端为 WebSocket；notify 为 webhook 单向出站（企微/钉钉/飞书群机器人）。——即：**流式对话链路基本全新**，且"日志 follow 死锁"有前科（审核留档 P1 项），SSE 治理必须设计先行。

**依赖图与顺序理由**：

```
M8 gitee+Jenkins 生态适配（前置阶段，执行顺序第一——存量项目接入是当下痛点）
     │
[设计文档] SSE 治理一页 + 对话组件选型落地验证（M1 动手前评审）
     │
M1 对话 UI（SSE 流式 + 双模式 + pack 挂载）
     └──> M2 MCP Server 化（tools 层 = pack 供给与角色过滤的协议化）
              └──> M3 Skill 声明式技能包 + 对话内工具调用（对话与 tools 会师）
                       └──> M4 NL→操作（确认层闭环）
M5 配置拉取 API 方案 B（独立，仅复用 configs 模块，可与 M2~M4 并行）
M7 AgileConfig 共存接入（依赖 M5 的应用级 token 基建与 configs 现状；可与 M3/M4 并行）
M9 开源生态适配批次（GitProvider/CIProvider 扩展依赖 M8 接口；通知/OAuth 两块独立，可全程并行）
M6 欠账批次（独立，阶段收尾段，但本阶段不清则再顺延一次）
```

**执行顺序（定稿）**：M8（3~4 周）→ SSE 设计文档 → M1 → M2 → M3 → M4 主线推进；M5/M7/M9 与主线并行插入（M9 的通知/OAuth 块无前置依赖可最早开始，GitProvider 扩展块排在 M8 之后）；M6 收尾段清账。M4 对 M3 为虚线依赖（NL→操作可不经"对话内工具调用"独立实现：意图解析→确认卡→执行）。总周期约 12~17 周。

## 二、前置事项（开工前完成，不占里程碑）

1. **对话 UI 技术设计一页文档**（roadmap §五硬性要求）：SSE 穿中转层的超时/背压/goroutine 生命周期治理、断连清理、单实例并发会话上限；评审通过才动 M1。同批完成 `ant-design-x-vue` 落地验证（demo 工程跑通 Bubble 流式/Sender 停止，确认与 vben+antd 无样式冲突）。
2. **MCP Go SDK 选型调研**（M2 前 1 天）：官方 `modelcontextprotocol` SDK vs `mark3labs/mcp-go`（维护活跃度/传输支持/工具定义人体工学），结论落本计划附录。
3. **生产尾巴收口**（不阻塞开发，随 dogfood 批次）：①通知群/O2 连接/备份任务三处生产界面配置；②证书正式 CA 切换 + 容器化 nginx reload（staging 已验证）。

## 三、里程碑

### M1 AI 对话 UI（首项，旗舰体验，约 1.5~2 周）

**模型**：

- `ai_conversations` / `ai_messages`：会话（标题/模式/挂载快照）、消息（role/content/用量/pack 版本留痕）；
- 双模式：**通用对话**（不接平台数据，纯模型，兼作中转配置调试）与**平台上下文对话**（会话挂载 Context Pack：项目/主机/时间窗选择器）；
- 中转层补**流式透传**：`CompleteStream`（OpenAI 兼容 stream 语义）→ 平台 SSE 下发；用量在流结束时落库（与现有 `ai_usages` 同表）；
- pack 挂载复用 P5 `BuildPack`（角色过滤 + DLP + 不可信输入围栏原样生效），挂载快照存会话（会话中途不重取，重开会话才刷新）。

**要点**：

- 前端独立一级菜单「AI 对话」（D2 定稿；参考"配置管理"独立先例）：多会话侧栏、流式 Markdown/代码块渲染、上下文挂载选择器；
- **对话 UI 组件级复用（选型定稿 2026-10-04）**：采用 `ant-design-x-vue`（Ant Design X 的 Vue3 社区实现，v1.6.0，2026-01 仍在发版，ant-design-vue 官方 README 生态推荐项）——Bubble（消息气泡/流式打字）、Conversations（会话列表）、Sender（输入框/停止生成）等组件与平台 antd 栈同源；**只复用组件不复用整应用**（Lobe Chat/Open WebUI 等全栈项目自带后端与账号体系，与平台中转层/权限模型冲突，明确不用）；会话持久化、pack 挂载、权限过滤、审计为平台自研（这些是平台资产，本就不该外购）。风险备案：社区库若停更，组件层可替换（接口面小），核心逻辑不依赖它；
- 断连治理：客户端断开 → 上游请求取消（WithoutCancel 只用于用量落库等收尾）、会话内可"停止生成"；
- Monaco 复用于代码块查看；未配置 AI 时页面引导去管理后台配置（优雅降级）；
- casbin 资源点：会话归属用户，pack 挂载范围受用户对项目/主机既有权限约束（**对齐方式 = pack 组装前先过一道资源可见性过滤，与 casbin 判定同源**，不做第二套权限）。

**验收标准（可判定）**：

- 流式输出长回答（>2000 tokens）不断流、不吊死；客户端中途断开/停止后 goroutine 与连接均释放（pprof 实测无泄漏）；
- 上下文挂载后，问"xxx 项目最近一次发布是什么时候"回答与平台记录一致（真机）；
- dev 角色挂载 prod 项目：pack 断言不含配置明文与全量日志（单测复用 P5 用例扩展）；
- DLP 六类注入用例在对话链路同样被 `[REDACTED:type]`（单测）；
- 通用模式不携带任何平台数据（请求体断言，单测）。

### M2 平台 MCP Server 化（约 1~1.5 周）

**模型**：

- 平台能力暴露为标准 MCP tools（**只读先行**）：查主机清单/状态、查容器、查指标、查发布与 cron 历史、读日志尾部、生成 Context Pack；
- tools 层复用 M1 的 pack 供给与角色过滤（"一套协议两处复用"：对话 UI 与 MCP 客户端同权限语义）；
- 管理配置面（admin tab「MCP 接入」）：接入凭证签发/吊销（长期 token，AES 落库）、工具 allowlist 按角色过滤（与 Context Pack 同套机制）、全程审计；
- 传输：HTTP Streamable 为主（单镜像部署天然支持），stdio 不做。

**验收标准**：

- Claude Desktop（或任意标准 MCP 客户端）真机接入：配置 endpoint + token 后，查主机/查日志尾部工具调用往返成功；
- dev 角色凭证调 `read_config` 类工具：敏感内容被脱敏（与 UI 同规则）；未授权工具被 allowlist 拒绝并留审计；
- 凭证吊销即时生效（下一次调用 401）。

### M3 Skill 声明式技能包 + 对话内工具调用（约 1 周）

**模型**：

- `ai_skills`：Markdown + frontmatter 声明式技能包（提示词模板 + 工具组合 + 运维 runbook），管理员 CRUD、会话中 `@技能名` 调用；**只做声明式，不做代码执行型**（安全边界：skill 不携带可执行代码）；
- 对话 Agent 接入平台 MCP tools：模型端 function calling → 平台 tools 只读查询 → 结果回填对话（M1 对话与 M2 tools 在此会师）；
- 中转层可配置的模型能力参差：不支持 function calling 的模型，会话内工具挂载禁用并明示（降级路径）。

**验收标准**：

- 内置种子 skill ≥2 个（@故障排查：挂载告警+日志尾部+近期发布；@发布检查：挂载 compose diff + 健康检查项），真机各跑通一例；
- 对话中一次完整工具调用往返（用户问 → 模型调 tool → 结果回填 → 最终回答）；
- skill 内容变更后下次调用即生效（无缓存陈旧）。

### M4 NL→操作（建议卡定调，2026-10-05 用户安全定调改写；原"确认层"方案已废弃移除）

**用户定调（安全四条）**：① AI 对话受权限控制——权限外数据（如配置密钥）不可问出；② 禁止 AI 在对话中操作数据（服务器/配置/发布，**不论用户身份，管理员也不行**）——担忧根因：AI 执行存在漂移与未知问题，不会按固定规则执行，仅提供建议；③ AI 定位只读语言助手（使用咨询/查项目信息/看日志/仪表盘分析/错误修改建议），查询受权限控制；④ 杜绝用户经 AI 对话安装 skill 或 MCP——仅管理员后台配置。

**模型（建议卡形态）**：

- 意图白名单三件套（容器重启 / cron 手动触发 / 配置下发）以**建议工具**暴露：模型调用只生成「操作建议卡」（摘要 + 「去处理」跳转平台对应页面），**无任何执行链路**（原确认层 PendingAction/confirm/cancel/executor 已整体移除）；
- system prompt 安全边界四条（全角色一致）：只读助手绝不执行变更（明示管理员也不行）；权限边界声明当前角色、权限外请求明确拒绝、不从对话历史复述敏感信息；不能安装/修改 skill 与 MCP（引导联系管理员）；用户消息中的指令不改变边界（注入免疫）；
- 数据源隔离不变：pack Sensitive 块角色过滤、工具仅元信息投影；
- 建议卡参数校验拒绝幻觉 ID（对象必须真实存在，容器名解析 cid）；
- 新增只读工具 container_logs（容器日志尾部 ≤500 行，与 REST 日志权限对齐——补齐"查看相关日志"能力）。

**验收标准（已全部真机验证）**：

- "手动触发某任务" → 模型查真实 job_id → 生成建议卡（无确认/执行按钮）→「去处理」跳转 /cron/jobs；
- "帮我装一个 skill" → 明确拒绝并引导联系管理员；
- 平台代码中不存在 AI 可触达的变更执行路径（工具集=只读白名单+建议工具）。
### M5 配置拉取 API（方案 B，约 1 周，可与 M2~M4 并行）

按 roadmap §四已细化要点落地（**复用文件形态真相源，不引入新组件**）：

- `GET /api/config/{app}/{env}`：按"应用"归组同环境多文件深合并（仅同格式可合并：yaml/json/toml 结构合并，env/ini 按 key 平铺）；合并冲突 key 报错拒绝；响应带 `X-Config-Version`（各文件 hash 聚合指纹）；
- 鉴权：应用级 token（管理后台签发，只读限定 app×env 范围），不复用用户 token（服务间凭证与人凭证分离）；公开接口带限速；
- 缓存：API 侧 60s 短缓存 + ETag；不做推送/长连接；
- 与下发组合：同一份配置既能 SFTP 下发也能 API 拉取（拉取直读平台库当前版本，不经远端文件）；
- 应用级 token 签发基建（AES 落库/范围限定/吊销）同时是 M7 AgileConfig 接入的对账与凭证基础。

**验收标准**：

- 真机一例：业务服务启动时经拉取 API 取到合并后配置并正常启动；
- token 越权（跨 app/env）403；合并冲突 key 返回明确报错；ETag 未变更时 304。

### M7 AgileConfig 共存接入（约 1.5 周，可与 M3/M4 并行；用户 2026-10-04 定向：共存而非收缩）

**共存设计（承 roadmap §三.3 决策记录展开，核心是"一个模型、两种底层、三种消费形态"）**：

- **消费形态三分（应用按需自选，互不排斥）**：① SFTP 文件下发（R2 已交付，应用读文件）；② API 拉取（M5，应用启动 GET）；③ K/V SDK 热更（本里程碑，应用嵌 AgileConfig 客户端长连接）——同一"应用×环境"可同时持有文件配置与 K/V 配置；
- **统一领域模型归一**：UI 与平台 API 只面向平台自有概念（应用/环境/配置文件/配置项/版本/审计/脱敏）；**AgileConfig 是纯后端 provider**，其特有概念（节点状态/客户端在线）降级为可选能力展示，不入模型；
- **UI 形态（2026-10-05 用户修正定调）**：纯配置文件一套 UI——AgileConfig 纯后端通道，不设 K/V 独立存储与独立页面；env/ini 文件「下发」时自动同步（天然键值格式），对账与连接设置收进管理后台「配置中心」tab；
- **同步方向：平台为真相源单向推 AgileConfig**（OpenAPI 写入）；AgileConfig 控制台手改视为漂移，提供对账检测（比对版本号，漂移仅告警不自动覆盖）；
- **共享基建**：敏感值统一走平台脱敏管线（录入即过 DLP 清单）；审计走 server_events；权限走 casbin 既有 config 资源点扩展 `config_kv:*`；
- **部署**：AgileConfig 服务本身走平台项目部署链路（dogfood 同 O2 先例，不直接操作服务器）。

**止损判据（承 roadmap）**：若 AgileConfig OpenAPI 覆盖度不足密钥级权限控制，K/V 场景回退内建轻量存储（平台 KV 表 + M5 拉取 API 透出，UI 零改动）。

**验收标准**：

- 同一应用×环境：文件配置走 SFTP 下发、K/V 配置经 AgileConfig SDK 热更，各真机一例互不干扰；
- 平台侧改 K/V 值 → AgileConfig 侧可见；AgileConfig 控制台手改 → 平台对账告警"检测到漂移"；
- dev 角色在 UI 看不到敏感值明文；AgileConfig provider 凭证 AES 落库；
- 抽掉 AgileConfig（停服/切回内建）：配置管理 UI 文件视图行为不变（解耦验证）。

### M8 gitee+Jenkins 生态适配（前置阶段：执行顺序第一，约 3~4 周；D5 定稿——存量项目接入是当下痛点，先于 AI 主线）

**现状耦合点（起草时逐处核实，全部 gitea 专属）**：① tag push webhook（HMAC X-Gitea-Signature）建 Build 记录；② Poller 30s 轮询 gitea commit status（act_runner 写回）；③ 构建日志走 gitea Actions API；④ 发布时从 gitea 取 tag 对应 compose 部署描述（RawClient）；⑤ `projects.RepoURL/RepoPath` 语义绑死 gitea。

**组合支持方案（两层 provider，均满足 rule of two）**：

- **GitProvider 接口**（roadmap §六已背书的高优先级抽象）：webhook 解析与签名校验 / tag→compose 部署描述 / 仓库元信息。gitea 既有实现迁入接口 + **gitee 适配器**（API v5，webhook 签名 X-Gitee-Token）；
- **CIProvider 接口**（新增抽象，两实现确定要来：gitea Actions + Jenkins）：构建状态查询 / 日志拉取。**Jenkins 适配**（REST：`/job/{name}/api/json` 状态、`consoleText` 日志，CSRF crumb 处理）；
- **projects 加 `provider` 字段**（gitea/gitee/jenkins 组合）路由到适配器，存量数据默认 gitea 零迁移；
- **触发链（gitee+Jenkins 形态）**：gitee push tag → 平台 `/ci/webhook/gitee` 建 Build 记录；gitee 仓库同时 webhook 通知 Jenkins（gitee 插件）构建推 registry（**镜像 tag = git tag，与现有 tag 发布模型对齐**）；Poller 按 provider 分支查 Jenkins build 状态；发布门禁（查 BuildSuccess）与发布/回滚链路零改动；
- 环境管理侧无额外适配：发布从 registry 拉镜像、SSH 部署，本身 provider 无关。

**验收标准**：

- gitee 仓库 + Jenkins 构建的项目：从建 Build 记录 → Jenkins 状态回读 → 日志在平台可看 → tag 发布到目标机，全链路真机一例；
- 既有 gitea 项目回归不受影响（同一套 Build/发布链路双 provider 并行）；
- Jenkins 不可达时状态查询降级为"未知"不阻塞已有成功记录的发布。

**明确不做（本里程碑内）**：Jenkins 构建触发（平台只读状态，构建由 gitee webhook 驱动，避免平台成为 CI 控制面）；GitHub/GitLab 适配器归 M9（不在本里程碑赶工）。

### M9 开源生态适配批次（约 3~5 周；用户 2026-10-04 追加拍板：全做，不留 backlog）

**范围（roadmap §六生态通用化表格中仅剩的未完成适配项，逐块独立）**：

- **Telegram Bot + SMTP 邮件通知**（0.5~1 周）：notify sender 渠道泛化——`notify_groups` 加渠道类型（webhook / telegram / smtp，现有按 webhook 域名探测厂商的逻辑只对 webhook 型生效）；Telegram 走 Bot API `sendMessage`（bot token + chat id，解析模式与截断规则对齐企微调研结论）；SMTP 走标准发信（标题 + 正文，markdown 降级纯文本）；UI 通知群表单加渠道选择；限速与 DLP 同既有链路；
- **OAuthProvider 接口化 + GitHub OAuth 登录**（约 1 周）：auth 既有企微 CorpApp / 钉钉扫码路径抽 OAuthProvider 接口（authorize URL 构造 / code 换 token / 身份映射 / 绑定解绑），GitHub OAuth App 走标准 code 流（`/login/oauth/access_token` + `/user`）；登录页"第三方登录"区块按已配置项渲染（未配置即隐藏）；回调公共接口过限速与伪造审视（对齐 M1 欠账清偿纪律）；
- **GitHub + GitLab 适配器**（1.5~2 周/家，roadmap 明示工作量远大于表格观感：webhook 语义、commit status 回写、token 模型差异，排期单列）：
  - GitProvider 扩展：GitHub（webhook `X-Hub-Signature-256`、tag push 解析、contents API 取 compose 部署描述、commit status）；GitLab（`X-Gitlab-Token`、repository files API）；
  - CIProvider 扩展：GitHub Actions（check runs / workflow runs API 状态与日志）、GitLab CI（pipelines/jobs API）——开源用户在这两家的 CI 形态即各自内置 CI，适配器成对出现；
  - **裁剪线：先 GitHub**（开源门面最大），GitLab 视阶段余量，余量不足则 GitLab 单独留为 M9 尾巴随 M6 收尾段补齐；
  - 两块均依赖 M8 已抽好的 GitProvider/CIProvider 接口，纯加实现，存量 gitea/gitee 零扰动。

**验收标准**：

- Telegram 群与 SMTP 邮箱各真机收到一条告警通知（格式可读、截断规则生效）；
- GitHub OAuth 登录 → 绑定 → 解绑真机一例；企微/钉钉既有扫码登录回归不受影响；
- GitHub 仓库 + Actions 项目全链路真机一例：tag push 建 Build 记录 → 状态回读 → 日志在平台可看 → tag 发布到目标机；gitee+Jenkins 与 gitea 项目回归；
- 未配置的渠道/登录方式在 UI 优雅隐藏（能力探测 + 配置驱动，未配置降级）。

### M10 全站 AI 悬浮助手（优化项，约 1 周；用户 2026-10-05 拍板加入本期，排期靠后——M9 之后、M6 欠账清点之前）

**动机**：运维场景里用户在项目/主机/发布页看到异常时最想问 AI，独立 /chat 菜单是弱入口；云服务商 AI 助手悬浮球是成熟心智（零跳转就地提问）。同时抽屉是 M4 NL→操作的天然常驻载体（后续"帮我把 demo 发到 test"就在抽屉里确认执行）。

**范围**：

- **ChatPanel 组件化重构（前置）**：chat-page.vue 从"页面"拆出可复用组件——消息区 + 输入区（含 / 命令面板、附件、SSE 流式、markdown 消毒渲染），/chat 页与抽屉共用；重构量约一天，一次投入两处受益；
- **悬浮球**：右下角全局悬浮按钮（自定义 logo，layout 层挂载、路由无关；支持 ESC 关闭、位置不遮挡页面关键操作）；
- **抽屉对话壳**：antd Drawer（placement right，约 400px，窄屏自适应）内嵌 ChatPanel，内容 defineAsyncComponent 惰性加载（不点开不加载对话 bundle，全站零负担）；
- **抽屉交互做减法**：400px 放不下会话侧栏——默认接续最近会话或新建，顶部「完整历史 →」链接跳 /chat（历史管理/admin 按用户查看/软删留档仍在完整页）；/命令、附件、停止生成全保留；
- **状态独立**：抽屉与 /chat 页各自独立会话状态（后端会话归属制天然支持，无 store 耦合）。

**明确不做（本期）**：页面上下文隐式继承（在项目页打开抽屉自动聚焦当前项目）——与已否决的挂载配置不同（无用户配置、纯自动继承），但一期先保持全平台上下文语义不变，验证入口价值后再评估；对话历史侧栏进抽屉。

**验收标准**：

- 任意页面右下角可见悬浮球，点击开抽屉可完整对话（流式/`/` 命令/附件/停止生成）；
- 抽屉与 /chat 页同一套 ChatPanel，行为一致；/chat 完整功能（用户下拉/显示已删除/软删留档）回归无损；
- 未点开悬浮球时对话相关 bundle 不加载（network 面板验证）；
- ESC 关闭、再开接续上次抽屉会话。

### M6 欠账批次（约 1~1.5 周，裁剪线之后，但本阶段必须清）

- **堡垒机语义补齐**：终端权限按主机/分组细粒度（casbin 资源点）；**asciinema 会话审计回放 UI**（消化 #20/#38，录制侧 P2 已有基础）；不做 MFA/双人复核/授权窗口；
- **#33 前端 composable 抽取**：抽屉逻辑重复等（P5 留档点名项）；
- **版本化 migration**：AutoMigrate 之上补版本化策略（开源化前置，#9 延伸）。

**验收标准**：

- dev 角色仅能开其授权主机的终端（越权 403）；回放 UI 完整重放一次历史会话；
- composable 抽取后既有页面回归无差异（抽屉/表格行为）；
- 版本化 migration：新库从零建库 + 存量库升级各演练一例。

## 四、明确不做（本阶段防蔓延）

- 自研 Agent 编排框架（调度交给模型端，roadmap 已定）；
- 代码执行型 Skill；
- **MCP Client**（外部 MCP servers 注册表，P6 末~P7 再启动）；
- IM 机器人双向（D4 拍板：顺延 P7，见 §十触发判据）；
- AgileConfig **服务发现**（compose 期需求弱，P7 再评估；接入本身见 M7）；
- Jenkins 构建触发与 pipeline 管理（平台只读 CI 状态，构建由 git webhook 驱动）；
- k3s 主线（触发判据满足后另立计划）；
- 工单/多租户等 roadmap §八清单项。

## 五、开源适配层审视（rule of two 逐项判定）

| 本阶段触点 | 判定 |
|---|---|
| AI 工具协议 | MCP 标准协议即生态接口（Go SDK 选型见前置调研），不自研私有工具格式 |
| 模型接入 | OpenAI 兼容协议已定（P5），不抽多 provider 接口 |
| 对话流式 | SSE 标准协议；WebSocket 仅维持既有终端场景 |
| 配置消费形态 | 文件下发 + API 拉取 + AgileConfig SDK 三种消费形态并存，但平台模型唯一、底层 provider 可插拔（roadmap 2026-10-01 决策：接缝服务于已明确的换底/共存需求，正当例外） |
| Git 托管 | GitProvider 接口（roadmap §六背书）：gitea 迁入 + gitee（M8）、GitHub/GitLab（M9）适配器，实现确定要来，正当抽象 |
| CI 引擎 | CIProvider 接口（新增）：gitea Actions + Jenkins（M8）、GitHub Actions + GitLab CI（M9），实现确定要来，正当抽象 |
| IM 通知 | 企微/钉钉/飞书已是插件式；M9 补 Telegram/SMTP 适配器（通知渠道类型泛化，非新抽象） |
| IM 扫码/第三方登录 | OAuthProvider 接口（M9）：企微/钉钉迁入 + GitHub OAuth，正当抽象 |

## 六、欠账消化安排

- **本阶段必清**：#20/#38 终端审计回放、#33 composable 抽取、版本化 migration（均已顺延一次，M6 承载）；
- **随 M7 收口**：slot_overrides 接管（AgileConfig 接入后由 K/V 形态接管测试槽位配置，或经止损判据回退内建后由内建 KV 承接）；
- **继续挂账**：生产尾巴两项（前置事项 3，运维操作非代码欠账）。

## 七、风险与前置调研

| 风险/调研 | 时点 | 缓解 |
|---|---|---|
| SSE 穿中转层的生命周期治理（日志 follow 死锁前科） | M1 前 | 技术设计一页文档先行评审；pprof 泄漏检测进验收 |
| MCP Go SDK 成熟度（官方 SDK 较新 vs 社区库活跃） | M2 前 1 天 | 双候选评估落附录；锁定后不轻易换 |
| NL 解析可靠性（参数幻觉/注入） | M4 | 白名单操作 + JSON Schema 约束输出 + 确认层兜底；解析失败宁可不执行 |
| 模型端 function calling 能力参差 | M3 | 降级路径：不支持工具调用的模型禁用工具挂载并明示 |
| 流式 Markdown 长对话渲染性能 | M1 | 分段渲染/虚拟滚动，超长会话提示新建 |
| 对话上下文超出模型窗口 | M1/M3 | pack 分块 + 截断策略（保留最近 N 轮 + 挂载摘要） |
| 阶段超期 | 持续 | 裁剪线：AI 主线按序保 M1→M2→M4（对话→协议化→确认层）；M3 可并入 M2 尾段或顺延 P7；M5/M7 独立高价值建议随段并行；M8 按决策点 D5 定位置；M6 不清则升级为 P7 首项（不再默顺延） |
| AgileConfig OpenAPI 覆盖度（密钥级权限） | M7 开工前 1 天调研 | 不足则走止损判据：K/V 回退内建轻量存储，UI 零改动 |
| gitee webhook 签名/Jenkins CSRF 细节与版本差异 | M8 开工前 1 天调研 | 双候选先跑通 curl 级验证再写适配器 |
| GitHub/GitLab webhook 语义与 commit status 回写差异（roadmap 警告工作量远大于观感） | M9 GitProvider 扩展块开工前 1 天调研 | 同上 curl 级验证；GitLab 设裁剪线（视余量，不足随 M6 收尾段补） |

## 八、执行与验收纪律

- 沿用 P4/P5 质量闭环：技术设计先行（M1）→ 里程碑实施 → 深审 → 真机 + UI 双验证 → 收尾评审；
- 每里程碑合入 main 即滚动发版（语义化递增），阶段收尾做总结评审并更新 roadmap；
- 所有新表/新资源点过 casbin 三级角色矩阵评审；新公开接口一律带限速审视；
- **AI 五铁律逐条对照进各里程碑验收**（M1 角色过滤与 DLP、M2 tools 层同套、M4 确认层与 casbin）。

## 九、决策点拍板记录（2026-10-04 全部拍板，D1~D8）

- **D1｜AgileConfig 共存**：✅ 单向同步（平台推 AgileConfig，控制台手改=漂移仅告警）；✅ 配置管理页内"文件/键值"双视图（不另开菜单）；✅ 首批仅 K/V，节点状态等降级可选能力。
- **D2｜对话 UI 形态**：✅ 独立一级菜单页。
- **D3｜NL→操作白名单**：✅ 低危三件套（容器重启/cron 触发/配置下发）；发布/回滚类高危 P7 观察三件套稳定后再议。
- **D4｜IM 机器人双向**：✅ 顺延 P7，本阶段不做。
- **D5｜gitee+Jenkins 排期**：✅ 前置小阶段（M8 执行顺序第一，M1 动手前完成）。
- **D6｜compose pull 修复**：✅ 已落地发版（1d764ee），无需后续动作。
- **D7｜开源生态适配批次**：✅ 用户 2026-10-04 追加拍板——roadmap §六剩余适配项（GitHub/GitLab GitProvider、Telegram/SMTP 通知、GitHub OAuth 登录）**全做进 M9，不留触发式 backlog**；唯 GitLab 适配器设裁剪线（GitHub 先行，余量不足随 M6 补）；ObservBackend（Grafana/Loki）维持 rule of two 判据不进本阶段。
- **D8｜k3s 启动方式**：✅ 选项 A（判据触发 + P6 收尾评审重估）——P6 执行期按 §十一准备项保持出口干净，收官评审时按主机/弹性规划决定是否立 P7；k3s 演进不改变"P6 功能线收官"定位（§十），两条线独立。

## 十、阶段后走向：功能线收官，基建线按 §十一演进

P6 收官后，**功能线转入维护期**——测试补齐、既有功能微调、按需小项，不再预先立项大功能阶段。功能向残留候选全部转为**触发式 backlog**（判据满足或自用刚需出现才重新立项，届时另立计划）：

| 候选项 | 触发判据（不满足就永远不做） |
|---|---|
| MCP Client（外部 MCP servers 接入） | 对话场景确需外部工具（拨测/文档检索）且平台自有 tools 无法覆盖 |
| NL→操作高危白名单（发布/回滚） | 三件套真机运行 ≥1 个月无误操作 |
| IM 机器人双向 | 对话 UI 稳定后仍有"IM 内直接查询/确认"的刚性场景 |
| AgileConfig 服务发现 | 转 k3s 后（ConfigMap/Service 是免费答案前不碰） |
| ObservBackend 抽象（Grafana/Loki 适配） | 开源用户真实需要 Grafana/Loki 替代 O2（rule of two 原判据） |

> 注：Telegram/SMTP 通知、GitHub OAuth、GitHub/GitLab GitProvider 原属本表，D7 拍板后已进 M9 本阶段做掉；k3s 主线不属功能 backlog，是核心基建演进线，见 §十一。

维护期的既定动作：P6 收尾评审时同步改写 roadmap（标记功能主线完结 + backlog 判据表落档 + §十一 k3s 判据重估）、依赖 license 审计与 README 英文化（开源化收尾）、按审核留档清单滚动消化新增隐患。

## 十一、k3s 基建演进（预研档，v1.4 补档；触发后另立 plan-phase7-k3s.md）

**定位（对 roadmap §一原则 3 的展开）**：平台根基是"自建调度与权限壳，复用成熟引擎"。compose 期平台自建了调度壳的 MVP（部署编排/蓝绿/灰度/槽位/载体网络），这些是**权宜自建**——k3s 期由 K8s 原生能力接管，平台自建面收缩、回归权限/审计/观测中转/AI 的本位。因此 k3s 不是普通功能候选，是**核心基建演进线**，独立于 §十的功能收官逻辑。

**判据现状重估（关键）**：roadmap §一原判据（≥3 台主机 / 真实滚动更新需求 / 弹性调度 / 观测超 5 台主机）中，"真实滚动更新需求"已经显形——P4 自建了蓝绿（任务化进度/健康门禁/域名活跃色跟随）与 map/split_clients 灰度，**自建复杂度本身就证明需求真实**。剩余判据（多机/弹性）取决于用户基础设施规划，见决策点 D8。

**现状耦合面盘点（2026-10-04 代码核实，16 文件涉及 compose）**：

| 耦合点 | 现状 | k3s 期形态 |
|---|---|---|
| 灰度/蓝绿渲染 | **已适配器化预留**（CanaryRenderer 接口，canary/model.go 注释明示"k3s 期翻译成原生流量资源"）——唯一现成接缝 | 滚动=Deployment 策略（maxSurge/maxUnavailable）+ Service selector 切换；灰度=Ingress 权重；nginx conf 单写者/活跃色跟随/双 compose 域名切全部退役 |
| 部署编排（resources/compose.go） | DeployComposeTo + compose-go 校验 + scale/recreate | manifest/chart 存储 + `kubectl apply`（镜像 tag→set image→apply），发布门禁/回滚语义复用 |
| 容器/终端/指标 | docker API + docker exec WS | workload/pod API + kubectl exec（WS 协议新建） |
| 观测组件 | 平台项目链路部署 compose（O2/vector/node-exporter/fluent-bit） | DaemonSet/manifest 部署 |
| cron 执行 | SSH + run 载体 --network | K8s Job/CronJob（或维持 SSH——agentless 原则不变，非容器化脚本保留） |
| 测试槽位 | slot_overrides 单机覆盖 | namespace 隔离（形态更自然，随 AgileConfig 接管欠账一并终结） |
| 配置消费 | 文件 SFTP 下发 / API 拉取 / AgileConfig K/V（M5/M7） | **ConfigMap/Secret = 免费第四形态**（roadmap 原判语）；文件/拉取形态对非容器化目标保留 |
| 证书 | lego + nginx -t/reload | cert-manager（或维持 lego + Ingress TLS，预研评估后定） |

**双轨迁移策略**：项目级"部署载体"字段（compose/k8s）升为一等公民——P4 已有载体概念雏形（cron `--network`、任务按项目绑定 compose 载体自动解析部署目标），扩展即可；双轨期 compose 全链路保留（存量项目零迁移），新项目可选 k8s 载体；k3s 安装走 agentless SSH（单机 k3s 起步合法，多机按需加入，与"单实例轻量"不冲突——集群是目标机的事，平台仍单实例）。

**P7 骨架（仅框架，触发后详细计划另立）**：M1 集群接入（agentless 装 k3s/kubeconfig/节点视图）→ M2 部署载体 k8s 化（manifest 存储+apply+发布门禁/回滚复用）→ M3 流量策略 K8s 化（CanaryRenderer 的 k3s 实现+蓝绿原生化）→ M4 观测/cron/终端/槽位迁移 → M5 存量项目迁移工具与文档。估 6~10 周。

**P6 内的准备项（零/低成本，本阶段执行）**：

- 纪律条款：P6 新代码不加深 compose 耦合（M8/M9 适配器与对话 UI 天然无关联；M5/M7 配置形态设计时预留"ConfigMap 是免费出口"——纯平台模型，不渗 compose 概念）；
- P6 收尾评审固定议程：k3s 判据重估（D8 结论落档），满足即起草 plan-phase7-k3s.md。
