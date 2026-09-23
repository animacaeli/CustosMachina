# 第二阶段计划：资源管理（服务器 + 容器）

> 状态：**已定稿**（v2，2026-09-23 评审通过；审核发现的硬伤与空白已并入正文）
> 前置：第一阶段（认证 / RBAC / 三级角色 / 单镜像部署 / IA 重组）已完成，v0.2 已发版。
> **2026-09-23 补充决策：采集与观测长期路线交给 OpenObserve**，不自建。含义见文末"观测路线（O2 分工）"一节。

## ⚠ 与现状的两个关键对齐点（实现前必读）

1. **alerting 与 timeline 目前是空壳**：两个模块只有 `doc.go` 注释桩，无任何实现。且 alerting 的既定设计是"OpenObserve webhook 接收 → 去重 → ai 诊断"，即**外部告警的入口管道，不是内部阈值评估引擎**。因此：
   - M2 的"服务器不可达告警"**不走 alerting 模块**，而是新增轻量内部路径：采集调度器检测到连续 N 次失败 → 直接调 notify 模块发通知，同时落 timeline 表（timeline 若届时仍未实现，先写库表、UI 后补）。
   - 内部阈值告警（如 CPU 持续 >90%）**不在本期范围**，等 alerting 模块按其原设计实现后再桥接。
2. **项目当前没有任何后台任务基础设施**（无 ticker/cron 调度框架），而本期有三处依赖常驻任务：采集调度、指标批量 flush、保留策略清理。见"后台任务框架"一节。

## 一、需求合理性评估

### 总体判断

服务器管理是本项目的自然主线（CustosMachina 定位就是机器看护），需求方向**合理**，与现有 health 模块及 notify 通知渠道天然衔接。但原始需求里有几处建议调整：

| 需求 | 评估 | 建议 |
|---|---|---|
| 新增服务器（SSH 端口 + 密钥/密码登录） | ✅ 合理 | 密码与私钥必须加密存储（复用 `internal/pkg/crypto`），私钥口令支持 |
| 服务器列表 + CPU/内存缩略折线图 | ✅ 合理 | 注意采集架构与存储（见下） |
| 一键登录（Web 终端模态框） | ✅ 合理，是核心卖点 | WebSocket + xterm.js；必须做会话审计与 RBAC 收敛 |
| 删除/修改/分组服务器 | ✅ 常规 CRUD | 分组用简单的 name 标签即可，不必上树形结构 |
| 容器管理（启停/日志/资源占用） | ✅ 合理 | 走 Docker Engine API over SSH，不要 shell 拼 docker 命令 |
| 新增容器（compose YAML 编辑器 + 校验 + 导入） | ⚠️ 命名有歧义 | 实际是"部署 compose 应用"；YAML 校验前端 + 后端双侧都要做 |
| 集群管理（swarm/k3s 一键加入） | ⚠️ 过早 | **本期只做数据模型预留，不实现**。理由：① 单机/少量服务器场景下集群是伪需求；② k3s 接入（kubeconfig、API server 版本差异）工作量等于再做半个产品；③ swarm 已半死。等真实有 3+ 节点编排需求再启动 |

### 关键架构决策（本期最大的分叉口）

**采集架构选 Agentless（SSH 通道），不装 Agent：**

- 我们的定位是轻量自用/小团队工具，部署成本低是卖点；装 Agent（类似 1Panel/宝塔模式）会带来版本管理、升级、卸载残留等一堆问题。
- SSH 通道复用同一个凭据体系：指标采集、Docker API、Web 终端、compose 部署全部走 SSH，一套凭据管所有能力。
- 代价：采集依赖目标机 SSH 可达 + 采集频率不能太高。对 CPU/内存这类指标，15s~60s 间隔完全够用。
- 预留退路：数据模型里给服务器加 `agent_version`（空 = agentless），未来要上 Agent 不用改表。

## 二、技术选型（优先复用开源库）

### 后端（Go）

> 注：下表 `gorilla/websocket`、`docker/docker/client`、`compose-spec/compose-go` 均为**新增依赖**（当前 go.mod 无），其中 `docker/docker` 传递依赖较多，引入前过一遍体积与维护活跃度；`golang.org/x/crypto/ssh` 已在依赖树。

| 能力 | 选型 | 说明 |
|---|---|---|
| SSH 协议 | `golang.org/x/crypto/ssh`（已在依赖树） | 密码/密钥登录、连接池、KeepAlive |
| SSH 复用 | 自建简单连接池（每服务器 1~2 条连接 + 健康检查） | 不引第三方池，逻辑很薄 |
| Docker 管理 | 官方 `github.com/docker/docker/client` + `client.WithHTTPClient` 走 SSH 隧道（`ssh.Dial` 返回的 net.Conn 包成 transport） | 生产级 API，避免解析 CLI 输出；老机器 unix socket 通过 SSH 端口转发到 `/var/run/docker.sock` |
| 指标采集 | 优先：目标机有 `docker stats --format` 之外的系统指标，通过 SSH 执行短命令采集（读 `/proc/stat`、`/proc/meminfo`，或 `vmstat`/`free -b` 兜底）；容器指标直接走 Docker API `stats` 流 | **不装 node_exporter**；如果目标机已跑 node_exporter，可探测 9100 端口直接抓，作为可选加速路径 |
| Web 终端 | `gorilla/websocket` + `x/crypto/ssh` 的 `session` 转发 PTY | 网关只做字节搬运，`creack/pty` 不需要（那是本机终端用的） |
| compose 校验/部署 | 后端 `compose-spec/compose-go` 做 loader 校验；部署通过 SSH 把 YAML 写到目标机临时目录后调 `docker compose up -d`（要求目标机有 compose 插件，探测并提示） | 不在网关侧解析 compose 语义 |
| 环境探测 | SSH 执行固定枚举命令：`docker version` / `docker compose version` 探测运行时，读 `/etc/os-release` 识别发行版（centos/ubuntu/debian/其他） | 探测结果缓存到服务器记录，新增容器向导首次进入时刷新 |
| 时序存储 | 本期不引 Prometheus/TimescaleDB。新表 `metric_samples(server_id, ts, cpu, mem_used, mem_total, …)`，内存里保留最近 N 小时环形缓冲供列表页快速出图 | 见"性能"一节。多库兼容：**SQLite** 用 WAL + 简单 DELETE 清理（写入量小，够用）；**MySQL** 建表时按 `(server_id, ts)` 联合索引 + 定时 DELETE，**不做分区表**（当前规模不值得，写注释预留）；**Postgres** 同 MySQL 策略。三库统一用 GORM 抽象，不写方言 SQL |

### 后台任务框架（本期新增，M2 的前置）

现有代码无任何 ticker/cron 基础设施，本期在 `internal/pkg`（或 runtime 模块内）新增一个极薄的 `jobs` 包，不引第三方库：

- `jobs.Group`：注册命名任务（采集调度、批量 flush、保留清理），统一由 wire 注入、随 http server 生命周期启停。
- 每个任务一个 goroutine + `time.Ticker`，`context.Context` 取消即退出；优雅停机时 `WaitGroup` 等待当前轮次跑完（flush 任务停机前强制落盘一次）。
- 单实例假设成立（单镜像部署），**不做分布式锁**；如未来多实例，再补 DB 乐观锁选主。

### 前端（vben + antd）

| 能力 | 选型 |
|---|---|
| 终端组件 | `xterm` + `@xterm/addon-fit`（vben 生态常用，模态框内自适应宽度） |
| 缩略折线图 | ECharts（vben 自带封装 `EchartsUI`），mini sparkline 用 `graphic` 简化，单图 <10 个点时直接 SVG 手绘更轻 |
| YAML 编辑器 | Monaco（`@guolao/vue-monaco-editor` 或 vben 已有封装若在）+ `monaco-yaml`（带 schema 校验）；本地导入用原生 `<input type=file>` FileReader |
| YAML 二次校验 | 前端 `yaml` 包 parse + 提交后端 compose-go 复核（前端校验只管语法，语义以后端为准） |

### 明确不引入的

- Prometheus / VictoriaMetrics / influxdb —— 当前规模杀鸡用牛刀，存储成本高
- node_exporter / cadvisor 强依赖 —— agentless 原则
- k3s/swarm SDK —— 集群延期

## 三、安全与合规（本期必须做，不进"留档隐患"）

1. **凭据加密**：SSH 密码/私钥用 `pkg/crypto`（AES-GCM）加密落库，密钥来自启动密钥（复用现有 JWT/加密密钥体系），列表接口永不回传明文，回传 `has_password`/`has_key` 布尔。
2. **终端会话审计**：每次 Web 终端会话记录 operator、server、起止时间，会话输出落 `data/` 下文件，**默认开启**，按 server 滚动保留 7 天（与指标保留策略同一清理任务，防磁盘无限增长），后期接 timeline 模块。终端路由的 RBAC 单独成 `terminal` action，默认仅 admin 以上。
3. **SSH 命令白名单思路**：指标采集命令固定枚举；compose 部署的命令模板化，用户自由输入只存在于终端会话（被审计）。
4. **凭据测试接口限速**，沿用登录限速的中间件思路（顺带把审核留档里的登录限速一起落地）。
5. 连接池空闲超时回收，避免长期占用目标机 MaxSessions。
6. **凭据修改与存量连接**：修改密码/私钥后，连接池立即关闭该服务器的全部旧连接（打"已失效"标记），在跑的采集轮次自然失败并在下一轮用新凭据重建；活跃终端会话不强制断开，但在会话列表中标记"凭据已变更"，审计里记录凭据变更事件。

## 四、性能考量

1. **采集调度**：单个 goroutine 池按服务器错峰采集（间隔默认 30s，可在服务器级配置 15s/30s/60s），失败退避 + 标记 `unreachable`，连续失败走 notify 模块直接发通知（**不走 alerting**，见文首对齐点）。
2. **写入合并**：指标写库按批（如每 5 个点或每 10s flush 一次），SQLite 开 WAL；保留策略默认 7 天原始点 + 降采样（5m 均值）保留 90 天，后台任务定时清理，防止库无限膨胀。
3. **列表页 sparkline**：不打时序库——从内存环形缓冲（进程内最近 1h）出数；重启后冷启动显示为空并在几分钟内填满，可接受。
4. **容器日志**：Docker API 的日志流走 SSE/分页拉取，前端默认 tail 200 行 + 跟随开关，不整段倒灌。
5. **SSH 连接复用**：采集、Docker API、终端各自独立连接（终端独占一条，避免被 API 操作挤掉 MaxSessions），但同用途内复用。

## 五、里程碑拆分（建议 4 个可独立交付的小阶段）

### M1：服务器 CRUD + 凭据 + 连通性（后端为主）✅ 2026-09-23 完成
- 已交付：`servers`/`server_groups` 表、CRUD/连通性测试/分组 API（internal/modules/resources）、凭据 AES-256-GCM 加密落库、casbin 种子 v4（admin/ops 可管理、dev 只读）、前端服务器与分组管理页（/resources）
- 表：`servers`（id, name, host, port, group_id, auth_type, encrypted_credential, agent_version, status, last_seen, metric_interval）、`server_groups`
- API：CRUD、连通性测试、分组管理
- 前端：列表/表单页（密钥支持粘贴与文件上传）、分组筛选与分组 CRUD
- 验收：能添加密码/密钥两种服务器并测试连通；能建分组、按分组筛选列表；修改凭据后旧连接被关闭（下一轮重建）

### M2：后台任务框架 + 指标采集 + 列表曲线 ✅ 2026-09-23 完成
- 已交付：`internal/pkg/jobs`（极薄任务组：Ticker+context+优雅停机）、SSH 采集 /proc（CPU 差值+内存）、内存环形缓冲（360 点）、批量落库（10s flush）、保留清理（7 天）、`server_events` 状态事件表（unreachable/recovered，notify 桥接点在 `emitEvent`）、API `/server-metrics/latest|:id`、`/server-events/:id`；前端列表 SVG sparkline（30s 轮询）+ 详情抽屉 ECharts 曲线（1h/6h/24h/7d）+ 事件时间线
- 已知偏差：notify 模块为空壳，不可达通知暂为"事件落库+日志"，IM 推送待 notify 实现后桥接
- `jobs` 后台任务包（见选型一节）——**M2 的前置工作**
- SSH 采集 /proc + meminfo，环形缓冲 + 批量落库 + 保留清理任务
- 列表页 CPU/内存 sparkline，详情页大图（可选时间段）
- 断连标记 + notify 直发通知（不走 alerting）
- 验收：列表页实时曲线；拔网线后状态变化并在配置的通知渠道收到消息

### M3：Web 终端 ✅ 2026-09-23 完成
- 已交付：`GET /servers/:id/terminal`（WebSocket 升级 → SSH PTY 字节搬运；输入走二进制帧、resize 走 JSON 文本帧）、auth 中间件支持 `?token=`（浏览器 WS 无法带 Authorization 头）、会话审计默认开启（`data/terminal-logs/<serverID>/<时间戳>.log`，首行 JSON 元数据，7 天滚动清理挂 jobs）、会话关闭落 `terminal_session` 事件、RBAC 仅 admin（ops/dev 无该路由策略，casbin 兜底）
- 前端：xterm + fit addon 模态框（可全屏），按钮仅 admin/superadmin 可见；timeline 正式实现前的最小事件表沿用 server_events
- WebSocket 网关 → SSH PTY，xterm 模态框（全屏可切换）
- 会话审计落盘（默认开、滚动 7 天）+ timeline 事件（timeline 未实现则先入库）、`terminal` RBAC action
- 验收：一键登录可交互；审计文件有记录且 7 天后被清理；普通角色无 terminal 权限时按钮不可见

### M4：容器管理 + compose 部署 ✅ 2026-09-23 完成
- 已交付：Docker API over SSH 隧道（每请求/流一条 SSH 连接 unix dial docker.sock）、容器列表/启停/日志（tail 200 JSON 或 SSE 跟随，自写 8 字节头 demux 替代 stdcopy 以砍掉整棵 moby/moby 依赖）/一次性 stats、环境探测（os-release + docker/compose 版本）+ 按发行版安装步骤（centos/rhel/ubuntu/debian 配置化文案，未识别回退官方链接，只展示不代执行）、compose 部署（前端 yaml 语法校验 + 后端 compose-go 语义校验 → stdin 上传目标机 /tmp/custos-compose → `docker compose up -d` 同步执行返回输出，3 分钟超时）、compose_deploy/container_op 事件落库
- **选型偏差**：YAML 编辑器用等宽 textarea + `yaml` 包校验，未上 Monaco——Monaco 打包 +2MB 且离线单镜像场景配置复杂；textarea+校验+文件导入覆盖了需求。若后续需要补全/高亮再换 CodeMirror 6（比 Monaco 轻）
- 二进制体积：`-s -w` 后约 33MB（moby client 引入 otel 等所致），可接受；若敏感可评估手写 Docker Engine HTTP 客户端
- RBAC：容器/探测/部署 admin+ops 可操作（dev 只读容器与探测），compose 部署不含 dev
- Docker API over SSH：容器列表/启停/日志（SSE）/stats 资源条
- **环境探测与安装引导**：新增容器向导第一步检测目标机 Docker / compose 插件是否可用；缺失时按 `/etc/os-release` 识别的发行版给出对应安装步骤（centos → yum 源 + docker-ce + compose 插件，ubuntu/debian → apt 源 + docker-ce + compose 插件，未识别发行版 → 官方 docs 链接 + 通用 get.docker.com 脚本），**只展示命令不代执行**，装好后点"重新检测"通过才进入下一步
- compose 编辑器（Monaco+monaco-yaml）、文件导入、后端 compose-go 校验、目标机部署
- **部署执行降级为第一期方案**：同步执行 `docker compose up -d`（带超时）+ 前端轮询部署日志接口；实时事件流后置，不作为本期验收项
- 验收：从 YAML 导入到容器跑起来全程在 UI 完成；部署失败能在 UI 看到失败原因；对无 Docker 的 CentOS/Ubuntu 目标机能展示正确的安装步骤，安装后重新检测可继续

### 集群管理（延期，仅本期预留）
- `servers` 表加 `cluster_id` 可空外键 + `clusters` 表骨架（不暴露 API）
- 触发条件重新评估：节点数 ≥ 3 且有真实编排需求

## 六、风险与开放问题

1. **目标机 Docker 环境差异**：老版本无 compose 插件或未装 Docker → 环境探测 + 按发行版给安装步骤（见 M4），只展示命令不代执行；未识别的发行版回退到官方文档链接。安装步骤文案以后端配置下发（而非前端硬编码），便于随版本更新。
2. **SSH 隧道到 docker.sock 的权限**：需要 SSH 用户在 docker 组或在 root 下操作，文档写清楚，UI 提示探测失败原因。
3. **新依赖体量**：`docker/docker`、`gorilla/websocket`、`compose-go` 均为新增，`docker/docker` 传递依赖最多，M4 开工前先在分支试引，评估对二进制体积和构建时间的影响。
4. **timeline 模块依赖倒挂**：M3 的审计事件按"timeline 未实现则先入库"处理，需在 M3 里定义最小的审计事件表结构，避免和未来 timeline 正式表冲突。
5. **审核留档清单**（登录限速、setup 竞态等）建议在 M1 期间顺带消化，避免安全债随功能面扩大。
6. **企微登录**：等 ICP 备案下来后回归测试，不阻塞本期。

## 七、观测路线（O2 分工）——2026-09-23 决策

**长期采集与观测交给 OpenObserve，本项目不自建时序/大盘/告警引擎。** 与 FR7（alerting = O2 webhook 入口）的既定设计一致。

分工边界：
- **本项目保留**：M2 的 agentless 轻量采集（SSH /proc → 环形缓冲 + metric_samples 7 天），定位降级为"列表页 sparkline 概览"，**不再扩展**——降采样 90 天等规划作废；不可达事件照常落 server_events
- **O2 负责**：指标长期存储、大盘、告警规则评估；告警经 webhook 打到 alerting 模块（去重 → ai 诊断）→ notify 发通知
- **结合点（下期候选）**：环境探测顺带探测目标机 O2 采集端（OTEL collector/O2 agent）的存在；用 SSH 能力代为安装/配置采集端，这是资源管理与 O2 最有价值的交叉
- **砍掉/后置**：指标详情大盘增强、自建阈值告警
