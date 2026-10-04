# 第六阶段计划：AI 主线（对话 UI / MCP Server / NL→操作）+ 配置双形态与生态适配 + 欠账收口

> 状态：**已定稿**（v1.2，2026-10-04 讨论稿当日评审定稿；决策点 D1~D6 全部按建议拍板落地，见 §九。开工时序：M8 前置 → SSE 设计文档 → M1，等用户发起）
> 范围决策记录（承 roadmap §四/§五与 P5 收官留档，含定稿决策）：**对话 UI 为 AI 主线首项**（旗舰体验，独立一级菜单）；**MCP Server 化与对话 UI 同期**（对话 UI 本身就是"内置 MCP 客户端 + 界面"）；**NL→操作带人工确认层**（首批白名单低危三件套，高危 P7 观察后再议）；**配置拉取 API（方案 B）+ AgileConfig 共存接入**（一个模型/两种底层/三种消费形态；平台单向推送+漂移告警；首批仅 K/V）；**gitee+Jenkins 组合适配前置**（存量项目主力组合，自用刚需优先于 AI 主线）；**IM 机器人双向顺延 P7**；堡垒机语义/审计回放/composable 抽取/版本化 migration 四笔顺延欠账**本阶段必须清**（均已顺延一次）。
> 阶段定位补充（定稿新增）：P6 是**功能开发的最后一个立项阶段**——收官后平台转入维护期（测试/微调/按需小项），大功能仅按触发判据重新立项，见 §十。
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
M6 欠账批次（独立，阶段收尾段，但本阶段不清则再顺延一次）
```

**执行顺序（定稿）**：M8（3~4 周）→ SSE 设计文档 → M1 → M2 → M3 → M4 主线推进；M5/M7 与主线并行插入；M6 收尾段清账。M4 对 M3 为虚线依赖（NL→操作可不经"对话内工具调用"独立实现：意图解析→确认卡→执行）；M5/M7 相互独立。总周期约 9~12 周。

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

### M4 NL→操作（确认层，约 1~1.5 周）

**模型**：

- 意图解析：NL → 待执行操作（操作类型 + 参数 JSON Schema 约束输出），**首批白名单三件套（低危）**：容器重启、cron 手动触发、配置文件下发；
- 确认层：对话内确认卡（展示解析出的操作与参数，人点确认才执行）→ 走既有 casbin 权限与 server_events 审计（**AI 只生成意图，执行权在人**）；
- MCP tools 侧同构：写操作工具默认挂确认层（M2 的预留在此启用）。

**验收标准**：

- 真机："帮我重启 xxx 项目的 yyy 容器" → 确认卡 → 确认 → 容器重启 → 审计留痕（含"由 AI 对话发起"标记）；
- 不确认不执行（关页/取消即丢弃）；解析失败给明确错误而非猜测执行；
- 越权白名单操作（dev 触发 admin 级操作）被 casbin 拒绝（真机）。

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
- **UI 形态**：配置管理页内"文件 / 键值"双视图切换（同一应用×环境下并列，不另开一级菜单）；
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

**明确不做（本里程碑内）**：Jenkins 构建触发（平台只读状态，构建由 gitee webhook 驱动，避免平台成为 CI 控制面）；GitHub/GitLab 适配器（另立，roadmap 已注明工作量远大于观感）。

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
- GitHub/GitLab 适配器（M8 只做 gitee+Jenkins；roadmap 已注明其工作量远大于表格观感）；
- k3s 主线（触发判据满足后另立计划）；
- 工单/多租户等 roadmap §八清单项。

## 五、开源适配层审视（rule of two 逐项判定）

| 本阶段触点 | 判定 |
|---|---|
| AI 工具协议 | MCP 标准协议即生态接口（Go SDK 选型见前置调研），不自研私有工具格式 |
| 模型接入 | OpenAI 兼容协议已定（P5），不抽多 provider 接口 |
| 对话流式 | SSE 标准协议；WebSocket 仅维持既有终端场景 |
| 配置消费形态 | 文件下发 + API 拉取 + AgileConfig SDK 三种消费形态并存，但平台模型唯一、底层 provider 可插拔（roadmap 2026-10-01 决策：接缝服务于已明确的换底/共存需求，正当例外） |
| Git 托管 | GitProvider 接口（roadmap §六背书）：gitea 迁入 + gitee 适配器，两实现确定 |
| CI 引擎 | CIProvider 接口（新增）：gitea Actions + Jenkins 两实现确定，正当抽象 |

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

## 八、执行与验收纪律

- 沿用 P4/P5 质量闭环：技术设计先行（M1）→ 里程碑实施 → 深审 → 真机 + UI 双验证 → 收尾评审；
- 每里程碑合入 main 即滚动发版（语义化递增），阶段收尾做总结评审并更新 roadmap；
- 所有新表/新资源点过 casbin 三级角色矩阵评审；新公开接口一律带限速审视；
- **AI 五铁律逐条对照进各里程碑验收**（M1 角色过滤与 DLP、M2 tools 层同套、M4 确认层与 casbin）。

## 九、决策点拍板记录（2026-10-04 用户全部按建议落地）

- **D1｜AgileConfig 共存**：✅ 单向同步（平台推 AgileConfig，控制台手改=漂移仅告警）；✅ 配置管理页内"文件/键值"双视图（不另开菜单）；✅ 首批仅 K/V，节点状态等降级可选能力。
- **D2｜对话 UI 形态**：✅ 独立一级菜单页。
- **D3｜NL→操作白名单**：✅ 低危三件套（容器重启/cron 触发/配置下发）；发布/回滚类高危 P7 观察三件套稳定后再议。
- **D4｜IM 机器人双向**：✅ 顺延 P7，本阶段不做。
- **D5｜gitee+Jenkins 排期**：✅ 前置小阶段（M8 执行顺序第一，M1 动手前完成）。
- **D6｜compose pull 修复**：✅ 已落地发版（1d764ee），无需后续动作。

## 十、阶段后走向：P6 是功能开发的最后一个立项阶段（定稿）

P6 收官后，平台**转入维护期**——测试补齐、既有功能微调、按需小项，不再预先立项大功能阶段。roadmap 中残留的候选项全部转为**触发式 backlog**（判据满足或自用刚需出现才重新立项，届时另立计划）：

| 候选项 | 触发判据（不满足就永远不做） |
|---|---|
| k3s 主线 | ≥3 台主机等多机需求 / 真实滚动更新需求 / 弹性调度需求 / 观测超 5 台主机（roadmap §一原判据） |
| MCP Client（外部 MCP servers 接入） | 对话场景确需外部工具（拨测/文档检索）且平台自有 tools 无法覆盖 |
| NL→操作高危白名单（发布/回滚） | 三件套真机运行 ≥1 个月无误操作 |
| IM 机器人双向 | 对话 UI 稳定后仍有"IM 内直接查询/确认"的刚性场景 |
| AgileConfig 服务发现 | 转 k3s 后（ConfigMap/Service 是免费答案前不碰） |
| GitHub/GitLab 适配器 | 开源社区真实贡献需求出现（roadmap §六已注明工作量远大于观感） |
| Telegram/SMTP 通知适配 | 海外用户真实出现 |

维护期的既定动作：P6 收尾评审时同步改写 roadmap（标记主线完结 + backlog 判据表落档）、依赖 license 审计与 README 英文化（开源化收尾）、按审核留档清单滚动消化新增隐患。
