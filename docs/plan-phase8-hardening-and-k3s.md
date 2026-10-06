# 第八阶段计划：收官硬化 + k3s 基建演进

> 定稿：2026-10-06（P7 v0.11.0 发版后全量盘点落地；用户拍板：剩余事项全部立 P8，k3s 预研转正）
> 性质：跨阶段路由文档（与 roadmap.md 配合使用）；实施时按里程碑逐项细化。
> 核心判断：功能线 P1~P7 已收官（v0.11.0），P8 不新增产品功能面——做三件事：
> ①安全与欠账清偿（开源/公网暴露前的必要功课）；②体验小尾巴闭环；③k3s 基建演进（P6 §十一预研档转正，D8 拍板=主动时间表）。
> **交付状态：已全量交付**（2026-10-06 当日完成——M1/M2 发 v0.11.1，M3 三小段全量发 v0.12.0；MySQL/PG 双方言实测抓出并修复三组真 bug）
> **终局定位（用户 2026-10-06 确认）：P8 是所有功能开发的终点**——v0.12.0 后平台转入
> 测试/微调/dogfood 迭代维护期；维护期按需小项走判据触发（MCP Client/高危 NL/IM 双向/
> 社区贡献），不再立项新阶段。AI 提示词与工具质量的 dogfood 调优属维护期常规工作。

## 一、总判断

盘点依据：审核留档清单（项目内部留档）、各阶段计划已知边界、P5/P6/P7 收官尾巴。**核对后多项历史欠账已随各阶段硬化修复**（登录限速/锁定 ✅、登录时序侧信道 dummyHash ✅、setup 竞态 EnsureLocalAdmin 幂等 ✅、钉钉 Verify invalid_client 区分 ✅）——P8 只收真实在账项，不重复已修。

## 二、里程碑

### M1 安全硬化与欠账清偿（约 3~4 天，最高优先级）

**安全四项**（审核留档在账）：

| 项 | 现状 | 方案 |
|---|---|---|
| JWT secret 默认值 | `change-me-in-production` 默认仍在，无 fail-fast | release 模式（非 dev 后端直跑）启动检查：secret 为默认值或长度 <32 → 拒绝启动并提示设置 `CUSTOS_AUTH_JWT_SECRET` |
| 内存 RefreshStore 无过期清扫 | Save/Consume 访问时才删，未消费 token 缓慢累积 | Save 时顺带清扫过期项（与 ratelimit.Window 同款惰性模式） |
| imSave 凭证字段完整性 | 残缺凭证到 verify/扫码才暴露 | 保存时按 provider 校验必填字段（clientId/secret 等） |
| 前端 authLogin 死路径 | vben 原生 login 残留在 store/auth.ts | 删除（登录页早已自绘） |

**质量两项**：
- **MySQL / PostgreSQL 双方言冒烟**：版本化迁移 0001~0005 与 casbin gorm adapter 在双库各跑一遍启动+升级路径（SQLite 已验，开源用户主流是 MySQL）
- release.Rollback 两步写收事务（影响小，顺手）

**验收标准**：默认 secret 下 release 启动被拒且提示明确；RefreshStore 长跑内存平稳（单测覆盖清扫）；残缺 IM 凭证保存即报 400；`go test` 全绿 + MySQL/PG 容器各起一遍冒烟通过。

### M2 体验与功能小尾巴（约 2~3 天）

- **终端审计查看/下载 UI**（#20/#38，堡垒机闭环最后一块）：server-terminals 会话列表加"审计回放/下载"（asciinema 格式文件，下载走既有 ticket 通道；回放用 asciinema-player 前端库）
- **cron 运行历史抽屉自动轮询**（运行中 2s 刷新，复用蓝绿实时日志模式）
- **observ O2 地址支持空串清空**（binding required 放宽）
- **M3 后置场景补齐**：发布前 GO/NO-GO 检查（advisory——AI 综合构建/配置/回滚基线给建议，发布抽屉旁入口）。YAML⇄env 转换建议**明确不做**（终局裁定：前端五格式转换视图已覆盖该需求，需要 AI 增强时走判据触发，不再后置悬置）
- **前端 composable 抽取**（#33）：usePagedDrawerList + LogViewer，四个环境抽屉去重
- **P7 验收遗留 UI 复测**：configs 编辑器 ✨、admin 窗口输入框（人工或浏览器实测补齐）

**验收标准**：终端会话可在线回放与下载；cron 运行中抽屉日志自动滚动；发布前检查入口给出 GO/NO-GO 建议（含依据，明确"仅供参考不构成闸门"）；四个环境抽屉逻辑收敛到 composable（重复代码删除量 > 新增）。

### M3 k3s 基建演进（预研转正，约 3~5 周，P6 §十一 升格）

**定位**（维持预研档结论）：自建调度壳 MVP → K8s 原生接管，平台自建面收缩。**不是**做 K8s 管理平台——不碰 operator/多集群/CRD 编排。

**判据重估论据**（已显形的需求证据）：P4 自建蓝绿+灰度的复杂度本身就是滚动更新需求显形；观测栈（node-exporter/fluent-bit）逐台部署已是 DaemonSet 形态的手工模拟；CanaryRenderer（canary/model.go）设计时已留"k3s 期翻译原生流量资源"适配器注释=现成接缝。

**双轨迁移设计**（存量零迁移是硬约束）：
1. **项目级部署载体一等公民**：project 的 target 增加 `runtime: compose | k3s`（数据库字段已预留 oneof），UI 按载体分栏
2. **k3s 载体最小闭环**：Kubernetes Go client（官方 client-go）直连项目配置的 kubeconfig；部署=Deployment+Service+Ingress 翻译现有 compose 语义（镜像/端口/环境变量/健康检查探针）；蓝绿=Deployment rollout；灰度=Ingress 权重注解（或 Gateway API 后续）
3. **自建面收缩**：k3s 项目的发布/回滚/域名切换不再走 SSH+nginx conf——平台只调 K8s API；compose 期设施（蓝绿 conf.d、split_clients）对 k3s 项目不生效不干扰
4. **观测栈 k3s 化**：Helm 值等价物（node-exporter/fluent-bit DaemonSet + O2 sink 复用现有配置）
5. **k3s 安装路径**：平台纳管主机上单机/小集群安装向导（离线镜像清单——国内 gcr.io 不可达用 daocloud 代理的既有事实）；**不托管集群生命周期**（升级/备份 etcd 由管理员）

**阶段拆分**：
- **M3.1 调研 spike（3~5 天）**：k3s 单机离线安装实测、client-go 选型（对比 server-side apply 库）、Ingress 权重方案验证（nginx ingress canary annotation）——产出物为真机可复现步骤落 docs
- **M3.2 最小承载（1~1.5 周）**：一个项目以 k3s 载体部署/回滚/查看状态（Deployment 日志经 O2 或 kubectl 等价 API）；UI 载体切换
- **M3.3 发布语义翻译（1~1.5 周）**：蓝绿→rollout、灰度→canary annotation、域名→Ingress；观测栈 DaemonSet 化

**明确不做**（k3s 边界）：集群生命周期管理（安装向导除外）、多集群联邦、CRD/operator 生态、Service Mesh。

**验收标准**：新建 k3s 项目从镜像发布到域名可访问全链路走 K8s API（零 SSH）；存量 compose 项目行为完全不变；蓝绿发布在 k3s 载体上为原生 rollout 且可回滚；灰度流量比例生效（curl 验证分布）。

### M4 部署侧尾巴清单（非代码，随发版窗口执行）

生产 3 处界面配置 + 证书 CA 切换 + remote_db 裁剪；HTTPS 前置反代终止 TLS 与 CORS 收紧（`CUSTOS_CORS_ORIGINS` 已支持）；gitee webhook 公网真机验证；GHCR 新包手动设 public 提醒；存量部署超管 roles='admin'→superadmin 迁移提示（#17，发版说明携带）。

## 三、明确不做（本阶段防蔓延）

- K8s 全功能面（见 M3 边界清单）
- P7 已裁定项维持：审批流、IM 双向、Agent 编排框架、GitHub/GitLab OAuth 与适配器、多租户/工单
- MCP Client / 高危 NL 操作：继续按 backlog 判据观察，不在本阶段

## 四、执行顺序

```
M1 安全硬化（3~4 天，最高优先级）
  → M2 体验尾巴（2~3 天）
  → M3 k3s（M3.1 spike → M3.2 → M3.3，3~5 周）
```

M4 随 v0.12.0 发版窗口执行。预估总工期：5~8 周（含缓冲）。
建议节奏：M1+M2 完成后发 v0.11.1（硬化版）；M3 完成后发 v0.12.0（k3s 版）。

## 五、与其他文档的关系

- **roadmap.md**：新增 P8 概要节引用本文件
- **审核留档**（custosmachina-review-debt）：M1/M2 完成后逐项销账；已修过时项（登录限速/侧信道/setup 竞态/钉钉 Verify）本文件定稿时即核销
- **P6 计划 §十一**（k3s 预研档）：内容被 M3 吸收升格，原文档保留历史轨迹
