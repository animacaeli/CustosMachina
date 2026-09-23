# 第三阶段计划：环境管理（项目 + CI/CD + 正式 / 灰度 / 测试环境）

> 状态：**已定稿**（v3，2026-09-23 全部决策点确认通过；含 M0~M5 里程碑与既定技术验证）
> 前置：第二阶段（资源管理 M1~M4）已完成——服务器 / 凭据 / SSH 连接池 / 采集 / Web 终端 / Docker API over SSH / compose 部署全部落地，`internal/pkg/jobs` 后台任务框架可用。
> 总体计划关联：FR5（运行时纳管）、FR6.1（Gitea 集成）、FR10（测试环境槽位模型，D14）；原计划把灰度策略放 v1.3，本阶段提前实现灰度环境（理由见需求评估）。
>
> **v2 修订记录（2026-09-23 首轮审核）**：
> ① 运行时路线去掉 docker swarm，直接 docker-compose → k3s；
> ② MVP 只按单一场景实现（gitea + runner + compose），k3s / Jenkins / GHA 全部是后续扩展，不做兼容层预埋；
> ③ 项目管理独立成一级目录 `/projects`（配置项多，不塞在环境管理里）；
> ④ notify 模块本阶段实现（企微群聊通知），CI/CD 通知在项目管理中按环境前缀选择群聊；
> ⑤ 项目管理增加容器 / pod 视图（日志 + 实例数伸缩），含 k3s 多实例日志展示方案；
> ⑥ 工程顺手项：前端菜单取消手风琴模式；后端换 zap 日志库 + docker-compose 把 logs 目录映射宿主机；
> ⑦ 灰度分流承载层定案：compose 期 nginx 承载，k3s 期完全由 k8s 原生能力实现（策略模型只存语义，承载层适配器化）；
> ⑧（v3 定稿）四个决策点确认：镜像仓库可配置支持阿里云/腾讯云/gitea；部署描述与 CI 流水线都在项目仓库（公共流水线），平台只配置路径并按标签 checkout；灰度策略绑定灰度实例、同机不同 compose project；gitea 全局配置 + 项目级可选 token。

## ⚠ 与现状的关键对齐点（实现前必读）

1. **notify 模块目前是空壳**：本阶段实现（见架构决策 4），M2 起的 CI 失败 / 槽位部署结果通知全部走它，不再"落库 + 日志"降级。
2. **CI/CD 外部依赖按单一场景**：只对接自托管 gitea + act_runner。Jenkins / GitHub Actions 不做原生集成也不做兼容层预埋，等真实需求出现再作为扩展项立项。
3. **运行时是 docker-compose 单机**：本阶段所有部署动作复用 M4 的 Docker API over SSH + compose 部署通道，不引入 k3s；数据模型与部署接口按"compose 实现 / k3s 可替换"设计（实例伸缩、日志两个接口天然双形态兼容，见架构决策 5）。
4. **配置中心 AgileConfig 尚未接入**：FR10.4 的配置分层（TEST 基线 + 槽位覆盖）依赖 AgileConfig；本期槽位覆盖配置先落平台库并环境变量注入，AgileConfig 基线接入作为独立小项后置（不阻塞主链路）。

## 一、需求合理性评估

### 总体判断

环境管理是资源管理的自然上游（机器管好了，接下来是"把什么部署到机器上"），需求方向合理。三环境分层（正式 / 灰度 / 测试）与标签驱动的构建发布流符合小团队实际。原始需求里有几处已按评审结论调整：

| 需求 | 评估 | 建议 |
|---|---|---|
| 项目列表组件（三环境共用） | ⚠️ 归属需明确 | **独立 projects 模块 + 独立一级目录 /projects**（见架构决策 1） |
| 构建：标签推送自动 CI，列表轮询状态 | ✅ 合理 | 状态轮询走 `pkg/jobs`，前端 30s 轮询 + 手动刷新；日志内嵌查看（gitea API） |
| 发布：手动点击，显示已通过 CI 的标签 | ✅ 合理 | 发布 = 部署动作走 M4 compose 通道，落发布历史（版本化、可回滚） |
| 灰度"发布"与"策略"分两个抽屉 | ⚠️ 可合并 | **合并为策略抽屉**：策略增删改只落库置"未发布"，一次聚合发布原子生效（见架构决策 2） |
| 流量灰度多策略 + 总量上限 | ⚠️ 待定值 | **允许多条，按比例加权随机；总量上限默认 50%，项目级可配置**（见架构决策 2） |
| 测试槽位：占用 / 释放 / 续期 / 自动部署 | ✅ 合理，是本期重点 | 复用 D14 槽位模型；释放销毁容器直接走 Docker API over SSH |
| 支持 gitea runner / jenkins / github actions | ⚠️ 全做成本高 | **MVP 只做 gitea 单场景**，其余为后续扩展（见架构决策 3） |
| 项目容器 / pod 视图 + 日志 + 实例数 | ✅ 合理 | 项目详情页内嵌容器视图，复用 M4 容器能力（见架构决策 5） |

### 为什么灰度提前到本期（相对总计划的 v1.3）

原计划把灰度策略放 v1.3 是因为当时优先级是槽位模型。现在实际使用中三环境是一体的操作面（同一个项目列表、同一套构建记录），拆两期做会把项目列表和构建表做两遍；且灰度的实现核心是"策略数据模型 + 分流配置生成"，与运行时形态无关，提前做不产生返工。

### 运行时路线修订：去掉 docker swarm，compose 与 k3s 按项目共存

swarm 长期半死、社区活跃度低，轻量场景下它相对 compose 的增益（内置调度 / overlay 网络 / 滚动更新）不足以抵引入成本。**路线修订为 docker-compose（MVP）→ k3s（v1.x），但不是全局一刀切切换，而是按项目共存**（2026-09-23 审核定案）：

- **粒度**：运行时是 `project_env_targets`（项目的环境部署目标）上的属性（`runtime: compose|k3s`），不同项目、同一项目的不同环境可以各自选择；迁移 = 逐项目逐环境自愿切换，无截止日；
- **约束 1**：同一项目同一环境的部署目标只有一种运行时（隔离域内不混编排）；
- **约束 2**：四个双形态能力（部署、实例/日志/伸缩、灰度渲染 `CanaryRenderer`、CI 触发结果落地）后端按 runtime 分发到适配器，**前端完全不感知**（同一套 UI）——if/else 不允许散落业务代码；
- **约束 3**：资源管理的环境探测扩展为识别服务器运行时（现有 docker/compose 探测 + 未来 k3s 探测），配置部署目标时按探测结果提示可选 runtime，防止配出"k3s 项目部署到纯 compose 机器"；
- 对本期的影响：MVP 只实现 compose 适配器，数据模型与四个接口按双形态设计，不做任何 k3s 预实现。此修订需同步回总计划文档（plan.md 1.5 运行时演进表）。

## 二、架构决策

### 1. 项目管理：独立一级目录 `/projects`，与环境管理平级

**不放资源管理、也不塞在环境管理子页里**——项目管理承载大量配置（仓库 / CI 凭据 / 环境部署目标 / 通知群聊 / 槽位个数），是一个独立实体，放环境管理下语义混乱。理由（相对资源管理）：

- 资源管理面向**机器**（服务器 / 容器 / 凭据），项目是 **CI/CD 实体**，生命周期与操作者不同；
- 项目是横切实体：正式 / 灰度 / 测试只是同一项目的三种部署目标，环境管理页按项目上下文组织，但项目本身的 CRUD 与配置不属于任何单一环境；
- RBAC 边界不同：资源管理 admin/ops 管、dev 只读；项目管理（含槽位个数、部署目标、通知配置）默认仅 admin。

落地形态：

- 新增 `internal/modules/projects`：项目 CRUD + 全部配置项。
- 前端新增一级入口 `/projects`：项目列表页 → 项目详情页（**概览 + 容器 / pod 视图 + 配置** 多 tab），配置含 CI、环境部署目标（env_type → server）、通知群聊（按环境分组选择）、槽位个数。
- `/envs` 一级入口（与 /resources、/projects 平级）：顶部项目选择器，下方 **正式 / 灰度 / 测试 三个 tab**（复用 /admin 横向 tabs 交互模式），三 tab 共享选中的项目上下文。
- 项目管理与环境管理的交叉点只有一处：**部署目标 → 服务器**的外键引用（部署 / 槽位销毁复用 M4 能力），这是引用关系不是归属关系。

### 2. 灰度：策略与发布合并入口，流量上限默认 50%

**入口合并**：灰度环境操作栏只有"构建"和"策略"两个按钮。策略抽屉内完成全部发布语义：

- 策略增 / 删 / 改只落库，对应策略（集合）状态置"未发布"；
- 抽屉提供**聚合"发布"按钮**：一次发布 = 把当前全部"未发布"策略原子生效（生成分流配置并重载，整体替换旧版本）；新增策略表单可提供"保存并发布"二合一（等价于保存后立刻走发布确认）；
- **保留发布确认步骤**：修改策略意味着流量切分瞬间变化，误操作直接打到线上，不能全自动。

**策略模型**：

- 策略类型两种：`header`（指定请求头，如 `x-canary: zhangsan`）、`traffic`（流量比例 N%）；
- 匹配顺序：**请求头策略优先**，命中即进对应灰度实例；全部未命中 → 落入流量策略池，多条流量策略按比例加权随机（如 10% 和 20% 两条，命中流量池的请求按 1:2 分流）；
- **流量总量上限默认 50%，项目级可配置**。理由：灰度语义是"小流量验证"，>50% 意味着多数用户已在未完全验证版本上，接近直接发布；30% 对多策略叠加太容易撞墙，70% 太激进。校验规则：该项目所有已启用 traffic 策略比例之和 ≤ 上限，新增 / 修改时前后端双侧实时校验；
- **同一项目同一时间只允许一个"已发布"的策略集合**（版本化，发布 = 整体替换 + 版本号递增），否则"哪些策略在生效"说不清，回滚没有抓手；
- **策略绑定灰度实例**（2026-09-23 定稿）：每条策略在发布时绑定对应的 canary 标签部署；灰度实例与正式实例**同机不同 compose project**（`<project>-prod` / `<project>-canary`），nginx 分流 = 在两个 upstream 之间按策略切；
- **分流承载层定案（2026-09-23 审核）**：按运行时形态分两段——**docker-compose 时期由 nginx 承载**（目标机宿主 nginx 或 compose 前置反代容器，平台生成 `split_clients`/map 配置片段 → SSH 写入 + reload，复用 M4 命令模板化通道）；**k3s 时期完全由 k8s 原生能力实现**（ingress 权重 / Gateway API HTTPRoute 的流量切分，平台只把策略翻译成对应资源声明，不再生成 nginx 片段）。因此策略模型只存语义（header 匹配 / 流量比例），**承载层适配器化**（`CanaryRenderer` 小接口，compose→nginx、k3s→原生资源各一个实现），与运行时路线（compose → k3s）同步演进。M4 开工前保留 0.5 天实测（nginx 写入 + reload 链路的权限与生效性），方案本身不再有分叉。

### 3. CI/CD 接入：MVP 单场景（gitea + runner + compose），多场景后续扩展

不做多 CI 平台抽象、不做 webhook 兼容层预埋——当前没有 Jenkins / GHA 需求，为不存在的场景写适配层是过度设计。**扩展方式是后端 CI 客户端接口化（`CiProvider` 小接口：触发 / 状态 / 日志 / 分支列表），MVP 只注册 gitea 一个实现**；未来接 Jenkins/GHA 时加实现 + 项目表 `ci_type` 枚举扩值即可，不动上层。

- gitea 原生能力：触发构建（打标签 / workflow dispatch）、轮询 run 状态、拉取日志内嵌展示、push webhook 自动触发测试环境链路；
- 凭据 AES-GCM 加密落库（复用 `pkg/crypto`）；
- 构建状态轮询用 `pkg/jobs` 常驻任务，失败退避；
- 测试环境全自动链路（push → 构建 → 部署）依赖 gitea push webhook：项目配置里生成 webhook 地址 + 密钥，平台匹配占用该分支的槽位后自动触发（FR10.6）。

### 4. notify 模块：本阶段实现，企微群聊通知

notify 从空壳转正，第一期只做一个渠道：**企微群机器人 webhook**（自建应用逐人推送维持后置）。

- **群聊管理**：管理后台新增"通知群聊"页（admin）——群名称 + webhook 地址 + 用途前缀标签。群的登记靠管理员从企微群里复制机器人 webhook 粘贴进来（企微无"列出我的群"API，只能手工登记）；
- **前缀约束**：群名称以 `【P】`开头 = 生产类（正式 / 灰度环境可选），`【dev】`开头 = 测试类（测试环境可选）。前缀在登记时校验、不可混用；
- **项目通知配置**（项目管理内）：按环境分组三个下拉框——正式环境通知群（仅 `【P】`群）、灰度环境通知群（仅 `【P】`群）、测试环境通知群（仅 `【dev】`群），均可留空 = 不通知；
- **通知事件**（本阶段接通）：CI 构建失败（成功是否通知做成项目级开关，默认只报失败）、发布完成 / 失败、槽位部署结果、槽位占用变更（被占用 / 释放 / 到期预警）、槽位宽限期到期自动回收。消息模板带项目 / 环境 / 标签 / 操作人 / 结果 / 日志链接；
- 渠道抽象留一个小接口（`Notifier`），企微第一个实现，未来加钉钉 / 飞书不动的上层；
- 顺带消化第二阶段的两个空壳桥接点：M2 的"服务器不可达通知"、M3 的终端审计事件，从"落库 + 日志"升级为真实推送。

### 5. 项目容器 / pod 视图与实例伸缩（含 k3s 多实例日志评估）

项目详情页的"容器 / pod"tab：展示当前项目**全部相关实例**（可能包含 server + worker 多个服务，来自该项目 compose 部署的全部服务），复用 M4 的 Docker API over SSH（按 compose project 命名前缀 `<project>-*` 过滤）：

- **列表列**：服务名、实例名、状态、运行时长、CPU / 内存（M4 已有 stats 能力）；
- **日志查看**：复用 M4 日志流（tail 200 + SSE 跟随）；
- **实例数伸缩**：`docker compose up --scale svc=N`（compose 场景即可用），**最少 1 个**，无上限；伸缩是高危操作需确认 + 落审计事件。

**k3s 多实例日志展示评估**（v1.x k3s 场景的既定方案，本期按此设计交互、compose 阶段天然单实例退化）：

- 默认**聚合尾随视图**：同一服务的全部实例日志合并、按时间排序输出，每行加 `[实例名]` 前缀（等价 `kubectl logs --prefix`）；聚合视图默认 tail 最近 N 行（如 200）防止多实例刷屏；
- 顶部提供**实例筛选下拉**（全部 / 单实例），排障时切单实例看完整日志；
- 伸缩控件在 k3s 下映射为 Deployment replicas，交互不变（最少 1 个）；
- 数据模型上把"实例"作为一等概念（compose 容器 = 实例、k3s pod = 实例），日志接口签名按 `服务 + 可选实例列表` 设计，双形态共用。

### 7. 镜像仓库与部署描述来源（2026-09-23 定稿）

**镜像仓库（Registry）可配置，支持三种提供方**：阿里云 ACR / 腾讯云 TCR / gitea 内置 registry。

- `registries` 表：名称、类型（aliyun/tencent/gitea）、地址、加密凭据；管理后台登记（admin），可登记多个；
- 项目绑定一个 registry（下拉选择），镜像名 = `<registry 地址>/<项目镜像前缀>/<service>:<tag>`；
- 构建侧：runner 所在机器用 registry 凭据登录后推送（凭据配置下发到项目仓库的 CI 变量，由公共流水线使用）；
- 部署侧：目标机 `docker login` 由平台部署前经 SSH 会话内注入，凭据不落目标机磁盘。

**部署描述与 CI 流水线都在项目仓库（现状延续，公共流水线）**：

- 各项目的 CI/CD 写在项目仓库里，构建逻辑复用公共流水线（平台不托管、不生成 workflow 内容）；
- 平台在项目配置里只填**部署描述文件在仓库中的路径**（compose 文件路径）；发布 = 按标签 checkout 该文件 → M4 通道部署到目标机；回滚 = 切历史标签，版本化天然成立（gitops-lite 变体）；
- 平台与 CI 的交互面收窄为"观测 + 触发"：打标签触发构建（项目 workflow 已监听标签）、拉取 run 状态与日志展示、发布时按标签取部署描述。

### 8. 测试环境槽位（本期重点，按 D14 落地）

- 槽位池 dev1、dev2…，**个数在项目管理中配置，仅管理员**；
- 状态机：空闲 → 占用（人 + 分支 + 时长）→ 分支推送自动构建部署 → 到期 / 手动释放（销毁容器，配置保留）→ 空闲；
- 占用表单：槽位下拉（仅空闲）、时长（默认 1 天，数值 + 单位小时/天/周）、分支下拉（gitea API 拉分支列表，支持筛选）；
- 释放 / 续期操作**仅当前使用人和管理员可见可操作**（前端按钮 + 后端 RBAC 双层校验）；释放后立即通过 Docker API over SSH 销毁该槽位隔离域（compose 独立 project + network，命名带槽位标识）；
- 预估释放时间 = 占用时间 + 时长；到期不强制销毁（只标记"已过期"并通过 notify 通知占用者，避免半夜干掉正在调试的环境），连续 N 天过期再自动回收（配置化，默认 3 天，回收动作也通知）；
- 槽位差异化配置：本期先平台库存覆盖项，部署时环境变量注入（AgileConfig TEST 基线接入后置，见对齐点 4）。

## 三、数据模型（新表概要）

- `registries`：id、name、type（aliyun/tencent/gitea）、address、encrypted_credential（AES-GCM）；admin 登记多个；
- `projects`：id、name、repo_url、default_branch、registry_id（FK）、image_prefix、compose_path（部署描述文件仓库路径）、test_slot_count、traffic_cap（默认 50）、slot_expire_grace_days、notify_prod_group_id、notify_canary_group_id、notify_test_group_id、notify_on_success（bool，默认 false）等；
- `notify_groups`：id、name（`【P】xxx` / `【dev】xxx`）、webhook_url（加密存储）、scope（prod/dev）；企微机器人 webhook 含密钥参数，按凭据等级加密落库；
- `builds`：id、project_id、env_type（prod/canary/test）、tag、builder、started_at、status（pending/running/success/failed）、ci_log_ref（gitea run id）、source（manual/webhook/auto）；统一承接三环境构建记录，构建抽屉按 project+env 查询分页；
- `releases`：id、project_id、env_type、tag、release_by、released_at、status、rollback_of（回滚指向）；发布历史，正式 / 灰度共用；
- `canary_policies`：id、project_id、type（header/traffic）、header_key/value、traffic_percent、bound_tag（绑定的 canary 标签部署）、enabled、published_version（所属发布版本，0=未发布）；
- `project_env_targets`：project_id、env_type、server_id（FK 资源管理服务器）、runtime（compose/k3s，默认 compose）；环境 → 部署目标映射（独立表，一个环境将来可扩展多目标）；
- `env_slots`：id、project_id、slot_name、branch、occupied_by、occupied_at、duration、expire_at、status；槽位部署目标取该项目测试环境的 `project_env_targets`；
- `slot_overrides`：槽位覆盖配置（key/value，释放保留）。

标签规则：正式 `v*` 或项目配置的通配；灰度 `canary-yyyymmdd-[姓名首字母小写]`（后端校验格式）；测试环境镜像 tag 带槽位标识。

CI 客户端不建表（`ci_type` 枚举暂只有 `gitea`，凭据并入 projects 表加密字段；未来多 CI 时再拆 `project_ci_configs`）。

## 四、安全与合规

1. CI 凭据、通知群 webhook URL 均按凭据等级 AES-GCM 加密落库，接口只回传 `has_credential` / 群名称脱敏展示。
2. gitea webhook 接收端签名校验（HMAC + 时间戳防重放），失败 401 且落事件。
3. RBAC 新增 action 面：`project`（管理，admin）、`build`（查看构建 / 日志）、`release`（发布，prod 默认 admin、canary admin+ops）、`scale`（实例伸缩，admin+ops）、`slot`（占用 / 续期本人、释放本人或 admin、槽位个数配置 admin）；casbin 种子再升一版（v5）。
4. 发布、槽位释放、实例伸缩均为高危操作：落审计事件（复用 server_events 模式），UI 二次确认；槽位释放明示"容器将立即销毁"。
5. 分流配置写入目标机走命令模板化（同 M4 compose 部署通道），不接受用户自由输入的 nginx 片段。

## 五、里程碑拆分（建议 6 个可独立交付的小阶段）

### M0：工程顺手项（半天）
- 后端日志库换 **zap**（sugared logger 渐进替换现有 log 调用点，统一 JSON/控制台双模式配置）；
- docker-compose 一键部署把容器内 `logs/` 目录映射到宿主机（`./data/logs`，与现有 /data 卷同级），zap 输出写文件 + 滚动；
- 前端菜单**取消手风琴模式**（可同时展开多个一级目录）。
- 验收：docker-compose up 后宿主机能直接看日志文件；菜单多目录同时展开。

### M1：notify 模块 + 项目模块（后端为主）✅ 2026-09-23 完成
- 已交付：`internal/modules/notify`（企微群机器人 Sender、`notify_groups`/`notify_records` 表、群 CRUD + 测试消息 + 运维群设置 API、按群 3s 限频闸门、发送留痕）、`internal/modules/projects`（项目 CRUD + 部署目标整体替换 + 通知群 scope 匹配校验：prod/canary→【P】、test→【dev】）、collector 桥接 NotifyOps（unreachable/recovered 推运维群，未配置群时只落库+日志）、casbin 种子 v5（admin 管项目/群，ops/dev 项目只读）、前端 /projects（列表+详情配置/部署目标/容器占位 tab）、/envs 三 tab 骨架、管理后台"通知群聊" tab（含运维群选择）、cmd/jwtgen 本地冒烟辅助
- 已知偏差：终端审计事件仍只落库不推群（审计类不适合刷群消息，维持现状）；webhook 限频是简单 3s 闸门非队列
- 待真实联调：企微群机器人 webhook 推送（需用户登记真实群）
- notify：`Notifier` 接口 + 企微群机器人实现、`notify_groups` 表 + 管理页（admin 登记群 webhook + 前缀校验）、消息模板；
- 顺带桥接二阶段空壳：服务器不可达通知、终端审计事件接 notify；
- projects 模块：表 + CRUD + 环境部署目标映射 + 通知群配置（按前缀过滤的下拉数据源）+ 槽位个数；
- 前端：`/projects` 一级入口、项目列表 / 详情（概览 + 配置 tab）、`/envs` 三 tab 骨架。
- 验收：登记 `【P】`/`【dev】` 群后，项目配置里各环境下拉只出现对应前缀的群；不可达事件能推到群里。

### M2：gitea CI 集成 + 构建（正式环境打通）✅ 2026-09-23 完成（待真实实例联调）
- 已交付：`internal/modules/ci`——全局 CI 配置（gitea 地址 + 全局 token 加密 + webhook 密钥，单行表 ci_global_config）、`registries` 表与 CRUD（aliyun/tencent/gitea 三类型、凭据加密）、gitea 极薄客户端（commit status / branches / Actions 外链）、标签 webhook 接收（X-Gitea-Signature HMAC-SHA256 校验、项目按 repo_path 大小写不敏感匹配、v*→正式 / canary-yyyymmdd-缩写→灰度格式校验、未登记项目与非标签事件忽略）、`builds` 分页查询、状态轮询 jobs（30s，pending/running → commit status 映射，终态按项目配置推通知）、casbin v5 并入 CI/registries/builds 资源点、前端：管理后台"CI / 镜像仓库" tab、/envs 构建抽屉（分页 + 30s 轮询 + 日志外链）
- **关键假设（待实测自托管 gitea）**：构建状态来自 act_runner 写入的 commit status（`/repos/{path}/commits/{sha}/status` 聚合接口）；日志为 gitea Actions 页面外链、未内嵌。gitea MCP 未登录无法提前实测，用户实例联调时验证，若 act_runner 不写 status 则改轮询 Actions 任务接口
- 已知偏差：webhook 无时间戳防重放（gitea 原生签名不含时间戳，靠密钥保密性；公网部署建议平台侧限制来源）；测试环境 push 自动链路留 M5
- **第一件事：实测自托管 gitea 的 workflow API 能力**（act_runner 状态 / 日志 / 触发），按实际版本适配；
- 平台全局 CI 配置（gitea 地址 + 全局凭据，管理后台）+ `registries` 表与登记页（阿里云 / 腾讯云 / gitea 三类型）；
- gitea API 客户端（`CiProvider` 接口 + gitea 实现：触发 / 状态 / 日志 / 分支列表）、tag + push webhook 接收；
- `builds` 表 + 构建记录落库 + 状态轮询任务（`pkg/jobs`）+ 构建失败通知（notify，成功按项目开关）；
- 前端：构建抽屉（分页表格 + 状态轮询 + 日志内嵌查看），三环境共用。
- 验收：gitea 打标签后平台自动出现构建记录，状态轮询到 success/failed，失败推送到项目配置的群，日志可看。

### M3：发布 + 项目容器视图（正式环境完整闭环）
- `releases` 表 + 发布 API：校验"已通过 CI"→ M4 compose 通道部署 → 落发布历史 + 发布结果通知；
- 前端发布抽屉：已通过 CI 的标签分页表格 + 发布（二次确认）+ 发布历史 / 回滚；
- 项目详情"容器 / pod"tab：实例列表（服务 / 状态 / stats）+ 日志查看 + 实例数伸缩（`--scale`，最少 1，确认 + 审计）。
- 验收：构建 → 发布 → 容器查看 / 伸缩全程 UI 完成；发布失败能看到原因；能回滚。

### M4：灰度环境（策略 + 分流）
- `canary_policies` 表 + 策略 CRUD + 流量总和校验（≤ 项目 traffic_cap）；
- **前置实测（0.5 天）**：nginx 承载方案已定案（见架构决策 2），仅实测"平台生成 nginx split_clients/map 配置片段 → SSH 写入 + reload"链路的权限与生效性；结论写回本文档；
- 聚合发布：版本化整体替换 + 灰度构建标签格式校验（`canary-yyyymmdd-缩写`）+ 发布通知；
- 前端策略抽屉：策略表格（分页）+ 新增表单（标签下拉筛选、策略类型、流量比例联动输入框）+ 聚合发布 / 回滚。
- 验收：请求头命中进灰度实例；未命中按比例加权分流；改策略后旧版本仍在生效直至重新发布。

### M5：测试环境槽位（重点）
- `env_slots` / `slot_overrides` 表 + 占用 / 释放 / 续期 API（本人 / admin 权限双层校验）+ 占用变更通知；
- 自动链路：push webhook → 匹配占用该分支的槽位 → 触发构建（镜像 tag 带槽位标识）→ compose 部署到槽位隔离域 → 部署结果通知占用者；
- 释放 / 到期销毁：Docker API over SSH 销毁隔离域容器；到期标记 + 宽限期自动回收任务（回收前通知）；
- 前端槽位抽屉：占用情况表格（释放 / 续期按钮按权限显隐）+ 占用表单（槽位 / 时长 / 分支筛选下拉）+ 构建进行中状态展示。
- 验收：占用 dev1 选分支后推送代码，1~2 分钟内槽位环境更新且收到通知；释放后容器立即销毁、覆盖配置保留；他人槽位不可操作。

### 后置项（不进本期）
- k3s 适配器（实例 / 日志 / 伸缩接口已按双形态设计，见架构决策 5）；
- Jenkins / GHA CI 接入（`CiProvider` 加实现即可）；
- AgileConfig TEST 基线接入与配置分层完整形态；
- 通知渠道扩展（钉钉 / 飞书 / 企微应用逐人推送）、槽位闲置回收精细化、依赖拓扑自动拉起。

## 六、风险与开放问题

1. **nginx 承载的具体落点**（M4 前实测）：宿主机 nginx 还是 compose 前置反代容器，两者的配置写入路径与 reload 权限不同（宿主机需 root / sudo，前置容器只需重载容器）；实测结论写回本文档。k3s 时期切原生承载，无此问题。
2. **gitea 版本 / API 差异**：自托管 gitea 版本可能较旧，workflow 相关 API（act_runner）需按实际版本适配；M2 第一件事是实测当前部署的 gitea API 能力。
3. **测试环境全自动链路的构建时长**：push → 可用可能超过开发者预期，槽位详情里展示构建进行中状态，而非让用户误以为部署失败。
4. **多项目共享同一台服务器时的隔离**：槽位隔离域用 compose project 命名前缀（`<project>-<slot>`）隔离，命名规范里带项目标识防撞名；项目容器视图按 `<project>-*` 前缀过滤，也依赖该命名规范。
5. **构建记录量**：测试环境每次 push 都落 builds，高频分支下增长快；保留策略（默认 30 天）挂 jobs 清理任务。
6. **`--scale` 的服务前提**：compose 伸缩要求服务无固定容器名 / 端口绑定冲突；项目模板需遵循（文档写明约束，伸缩前探测可伸缩性并给出可读错误）。
7. **企微群机器人限频**：每机器人每分钟 20 条；通知按事件聚合 / 去重（同一构建不重复推），高峰期限流排队。
8. **zap 替换的回归面**：现有 log 调用点分散，替换时统一走 pkg 内 logger 包装（禁止散落直接 import zap），避免两套日志并存。
