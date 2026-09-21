# 轻量级 AI DevOps 运维平台 · 计划文档

| | |
|---|---|
| **版本** | v0.3(测试环境槽位模型) |
| **日期** | 2026-09-20 |
| **状态** | 首轮评审意见已吸收 |
| **关联材料** | [轻量级运维基建设计](/Users/wanna/Documents/blog/work/轻量级运维基建设计.md) · [架构图 SVG](/Users/wanna/Documents/blog/work/assets/轻量级运维基建架构图.svg) |
| **项目名称** | **CustosMachina**(拉丁语"机器守护者"),仓库名 `custos-machina`(2026-09-20 定名) |

**修订记录(v0.2)**:① 运行时三形态(compose / swarm / k3s)均为项目支持范围,架构按集群扩展设计,完整 K8s 明确排除;② 平台自库默认 SQLite,通过 env 可切 MySQL / PostgreSQL;③ 开源仓库确定单仓库(monorepo)策略。
**修订记录(v0.3)**:④ 新增 FR10 测试环境槽位模型(占用制、配置与容器解耦、分支推送自动部署),定为决策 D14;AgileConfig 职责收敛为 PROD + TEST 基线;灰度策略与闲置回收放 v1.x。
**修订记录(v0.3 补充)**:⑤ 项目定名 **CustosMachina**(T1 关闭);前端模板 2026-09-20 搭建时定 vue-vben-admin(T4 关闭,由原 vue-pure-admin 改定);启动分批实施,新增第 10 章实施批次,批次 1(骨架 + 权限管理)开工。
**修订记录(v0.4,2026-09-21 第一阶段定稿)**:⑥ 第一阶段(骨架 + 认证 + 权限管理)完成定稿:monorepo / Go 后端(wire 模块化)/ 前端 vben 落地;本地超管 + 企微扫码登录(T3 关闭)+ JIT 注册 + casbin RBAC(前后端双层)全部端到端验证通过;工程化(Makefile / git hooks / CI 含 govulncheck+CodeQL)就位。⑦ 阶段划分调整:第一阶段就此封版,剩余事项归入「优化项」清单(见 10.1);原批次 2~5 并入第二阶段,实施计划由新一轮沟通重新制定(第 10 章旧批次表作废)。

---

## 1. 背景与目标

### 1.1 背景

底层运维基建(见关联材料)由成熟开源组件拼装:Gitea + Runner(CI/CD)、OpenObserve + OTel(可观测)、AgileConfig(配置中心)、Yearning(SQL 审计)、JumpServer(堡垒机,二期)、MySQL/Redis(存储)。运行时从 docker-compose 起步,未来过渡 swarm / K3s。

当前痛点:

- 每个组件各自一个 UI、一套账号,日常操作要记五六个地址;
- 故障发生时,需要人肉跨系统拼凑日志、指标、K8s/容器事件、最近部署、最近配置变更,才能定位原因;
- 通知手段原始(群机器人),无法精准到责任人。

### 1.2 产品定位

**一个开源的、轻量级的 AI DevOps 运维平台**,两个核心价值:

1. **统一控制台**:统一扫码登录、统一入口、高频功能自建页面(基于各组件 OpenAPI),复杂场景深链跳转原 UI;
2. **告警 AI 诊断**:故障发生时自动汇聚五源上下文,LLM 输出"问题分析 + 修改建议",推送到具体的人。**仅诊断 + 建议,不自动执行。**

### 1.3 目标用户

小团队 / 个人开发者的自托管场景,单组织部署(对标基建文档的适用场景:团队 < 10 人,项目 < 20 个,单机)。

### 1.4 非目标(本期明确排除)

| 排除项 | 说明 |
|---|---|
| 重写被集成组件 | 平台是集成者,不是替代者 |
| AI 自动执行变更 | 仅输出建议,执行永远由人完成(后续版本考虑"审批后代跑") |
| 多租户 / SaaS | 单组织自托管 |
| 完整 K8s 纳管 | 项目定位轻量级,标准 K8s 属于重型运维;集群化需求由 swarm / k3s 覆盖。k3s 兼容 K8s API,业务未来若需升级完整 K8s 可平滑迁移,但平台适配器不承诺 |
| IM 交互卡片回调 | 二期;MVP 通知只推送到人 + 控制台待办 |
| 告警规则管理 | MVP 直接在 OpenObserve 配置,平台只接收和处理 |

### 1.5 运行时演进策略(v0.2 修订)

单节点是起点,不是最终态。三种运行时形态都在项目支持范围内,对应不同的集群规模:

| 运行时 | 形态 | 交付阶段 |
|---|---|---|
| docker-compose | 单机起步;多机时由人工指定服务分布 | MVP(M2) |
| docker swarm | 小集群:内置调度、overlay 网络、滚动更新 | v1.x |
| k3s | 轻量集群:K8s API 兼容,自愈 / 伸缩完备 | v1.x |

设计约束:统一 `RuntimeAdapter` 抽象;数据模型(`runtime_targets` / `services`)自第一天支持多目标主机注册、多运行时形态并存,单个平台实例可同时纳管不同形态的目标;完整 K8s 不在支持范围(见 1.4)。

---

## 2. 决策记录(讨论已确认)

| # | 议题 | 结论 |
|---|---|---|
| D1 | 平台定位 | 集成 + AI 增强,不重写任何组件 |
| D2 | 前端 | 开源 Vue3 管理后台模板 + casbin 自建权限体系 |
| D3 | 登录方式 | 企微 / 钉钉 / 飞书扫码登录,**初始化向导中由管理员选择**;本地超管账号兜底(break-glass) |
| D4 | 通知 | IM 自建应用消息推送到具体的人,不用群机器人 |
| D5 | 组件 UI 策略 | 高频功能自建 + 深链兜底,不做 1:1 复刻 |
| D6 | AI 边界 | 仅诊断 + 建议,不执行 |
| D7 | MVP 环境拓扑 | 单套生产环境起步、docker-compose 运行时;架构按集群扩展设计(v0.2 强化) |
| D8 | 项目形态 | 目标开源;license 待定(建议 Apache-2.0 / MIT) |
| D9 | 告警规则归属 | MVP 在 OpenObserve 内配置,平台不重复造 |
| D10 | IM 三家策略 | 接口与数据模型按三家设计(插件化),MVP 只实现一家 dogfood |
| D11 | 运行时矩阵 | compose / swarm / k3s 均为支持范围、分阶段交付;完整 K8s 明确排除(v0.2) |
| D12 | 平台自库 | 默认 SQLite,compose env 可切换 MySQL / PostgreSQL(v0.2) |
| D13 | 仓库策略 | 开源单仓库 monorepo:backend / frontend / deploy / docs 同仓(v0.2) |
| D14 | 测试环境模型 | 槽位制 + 配置容器解耦:占用(人 + 分支)→ 推送自动部署 → 释放销毁容器、保留配置;AgileConfig 仅存 PROD + TEST 基线,槽位覆盖配置存平台库(v0.3) |
| D15 | UI 语言 | 单语言中文:保留 vue-i18n 机制(拆除需 fork vben 上游,成本高)但只锁 zh-CN、隐藏语言切换;自研页面直接硬编码中文,不维护第二语言,国际化留给未来社区贡献(2026-09-21 定) |

---

## 3. 需求清单

### FR1 初始化与引导

- FR1.1 首次启动进入 setup 模式,仅当用户表为空时开放,完成后永久关闭;
- FR1.2 向导步骤:创建本地超管 → 选择并配置 IM 提供商(凭证 + 扫码连通性测试)→ 纳管组件(逐个填 URL + Token,健康检查)→ 配置 AI 模型(base_url / api_key / model,连通性测试)→ 通知路由初始值;
- FR1.3 本地超管账号长期保留,作为 IM 故障时的兜底登录方式;
- FR1.4 IM 提供商支持后期更换,用户绑定关系按 provider 隔离。

### FR2 用户与认证

- FR2.1 扫码登录:企业微信(自建应用 + 扫码授权)/ 钉钉(OAuth 扫码)/ 飞书(网页应用授权)三种 `IdentityProvider` 插件,管理员激活一家;
- FR2.2 JIT 注册:首次扫码登录自动建用户,默认角色 = 访客,提权由超管操作;
- FR2.3 会话:JWT(Redis 可选缓存),登出与踢下线;
- FR2.4 用户表不存密码,只存平台用户 ↔ IM userid 绑定关系。

### FR3 权限

- FR3.1 casbin RBAC(角色继承),后端为唯一裁决方,所有 API 鉴权;
- FR3.2 角色:超管 / 运维 / 开发 / 访客;资源 = 功能点(服务操作、CI 触发、配置读写、告警处理、系统设置);
- FR3.3 前端路由与按钮权限由后端下发权限列表驱动(模板自带权限组件复用)。

### FR4 通知中心

- FR4.1 `Notifier` 插件与 IM 提供商同源,通过自建应用消息 API 发送到具体人;
- FR4.2 通知场景:告警诊断结果、CI 构建失败、槽位部署结果与占用变更、待办提醒(工单 / 审批)、平台自身异常;
- FR4.3 路由规则 MVP 为"服务 → 订阅人"(服务详情页管理订阅),平台级异常发给超管;
- FR4.4 通知记录落库,可追溯;

### FR5 运行时纳管

- FR5.1 `RuntimeAdapter` 抽象,固定操作集:列服务 / 查状态 / 拉日志 / 查事件 / 部署 / 回滚 / 重启 / 伸缩;
- FR5.2 MVP 实现 docker-compose:gitops-lite —— compose 文件托管在 Gitea 仓库,平台读仓库后 SSH 到目标机执行 `pull + up -d`,部署有版本、可审计;
- FR5.3 平台按固定间隔采集服务状态与事件,写入本地库;
- FR5.4 回滚 = 切回 compose 文件历史版本(依赖 Gitea 历史);
- FR5.5 分阶段交付 swarm 与 k3s 适配器(k3s 基于 client-go);接口与数据模型自第一天按三形态设计,支持注册多目标主机、多形态并存;
- FR5.6 compose 阶段的服务分布为人工指定目标主机;swarm / k3s 阶段由各自调度器接管。

### FR10 测试环境槽位管理(v0.3 新增)

环境是平台的一等对象(槽位模型),**配置与容器解耦**:

- FR10.1 环境类型:prod(每应用必有且唯一)/ test 槽位池(dev1、dev2…,数量与命名可配)/ gray 灰度(仅数据模型预留,策略化灰度放 v1.x);
- FR10.2 槽位状态机:空闲 → 占用(指定人员 + 开发分支)→ 分支推送自动构建并物化容器 → 释放(容器销毁、覆盖配置保留)→ 空闲;
- FR10.3 占用制:槽位独占(避免同槽混入他人实例互相干扰);槽内每个服务各自绑定分支,默认仅拉起本人开发的服务,联调所需的其他服务可手动加入(基线版本或指定分支);
- FR10.4 配置分层:AgileConfig 存 TEST 基线(全部槽位共享、支持热更新);槽位差异化配置存平台库,部署时以环境变量注入,优先级:**环境变量 > AgileConfig 基线**;覆盖项变更 = 重新物化(重部署该槽位);
- FR10.5 无人占用的槽位零容器、零资源占用,槽位配置数据始终存在于平台库;
- FR10.6 自动部署链路:Gitea push webhook → 平台匹配占用该分支的槽位 → 触发 Runner 构建(镜像 tag 带槽位标识)→ 部署至槽位隔离域(compose:独立 project + network;swarm:overlay network;k3s:namespace)→ 结果通知占用者;
- FR10.7 v1.x:槽位闲置自动回收、依赖拓扑自动拉起(按服务依赖图一键物化联调环境)、灰度策略(权重 / 双实例切换)、跨槽依赖拓扑可视化。

### FR6 组件集成层

- FR6.1 **Gitea**:仓库列表、CI 运行列表与日志、手动触发(自建页面);
- FR6.2 **OpenObserve**:简化日志搜索(服务 + 时间窗 + 关键字,底层转 SQL)+ 固定仪表盘(自建页面);复杂查询深链原 UI;
- FR6.3 **AgileConfig**:应用 / 环境 / 键值管理、发布与历史(自建页面)。API 已验证(2026-09-20,Basic Auth):配置 CRUD / 发布 / 回滚 / 发布历史均带 `env` 参数;**环境本身无管理 API**(自定义环境需直接改库)、**无环境间继承 / 克隆**——因此测试环境横向扩展(dev1/dev2/…)不经 AgileConfig 承载,采用平台槽位模型(见 FR10、决策 D14);
- FR6.4 **Yearning**:M0 验证 API 覆盖度后决定自建或深链;
- FR6.5 深链兜底:原 UI 保留部署,统一入口页 + 跳转;
- FR6.6 组件凭证集中管理:AES-256-GCM 加密落库,主密钥走环境变量;组件健康状态巡检。

### FR7 告警与 AI 诊断

- FR7.1 告警源:OpenObserve 规则告警 → webhook → 平台;
- FR7.2 事件处理:入库、按 dedup_key 去重聚合、静默窗口;
- FR7.3 AI 诊断上下文五源:① 告警前后日志窗口(OO 查询)② 该服务最近容器事件(运行时适配器)③ 最近部署记录(Gitea)④ 最近配置变更(AgileConfig)⑤ 该服务历史告警(平台库);
- FR7.4 输出:诊断报告(问题分析 / 疑似根因 / 修改建议——命令或步骤),标注"仅供参考,请人工确认后执行";
- FR7.5 模型:OpenAI 兼容接口,配置驱动,支持任意兼容服务(DeepSeek / GLM / 其他);
- FR7.6 诊断报告推送订阅人(IM)并进入控制台待办列表。

### FR8 审计与变更时间线

- FR8.1 平台内所有写操作(部署、回滚、配置变更、权限变更…)全量审计;
- FR8.2 变更时间线:部署 / 配置变更 / SQL 工单 / 告警事件统一落一张时间线,既供 AI 上下文消费,也供人工按服务回溯。

### FR9 备份

- FR9.1 compose 栈内增加 cron 容器:平台 SQLite 文件、MySQL dump、OpenObserve / AgileConfig 数据目录定时快照;
- FR9.2 恢复流程文档化(MVP 手动执行)。

### NFR 非功能需求

| 项 | 要求 |
|---|---|
| 部署形态 | 平台自身 docker-compose 一键拉起;目标机 4C8G 可承载平台 + 全部被管组件 |
| 网络前提 | 公网 HTTPS 域名(IM 扫码回调 + 应用消息必需);内网部署需自备反代,文档写明 |
| 平台自库 | 默认 SQLite(零依赖、备份即拷文件);docker-compose 环境变量(`DATABASE_DRIVER` / `DATABASE_DSN`)切换 MySQL / PostgreSQL;迁移与查询需三方言兼容 |
| 开源工程 | license 待定;UI 单语言中文(决策 D15:保留 i18n 机制、不维护第二语言);README / 部署文档从 MVP 当功能做 |
| 安全 | 见 4.5 安全要点 |

---

## 4. 总体架构

### 4.1 三条主线

```
┌─────────────────────────────────────────────────────────────┐
│                     统一控制台 (Vue 3)                        │
│   登录/向导 · 服务管理 · CI · 日志 · 配置 · 告警中心 · 审计    │
└───────────────────────────┬─────────────────────────────────┘
                            │ REST / JWT
┌───────────────────────────┴─────────────────────────────────┐
│                     平台后端 (Go)                             │
│                                                              │
│  ① 运行时适配层        ② 组件集成层          ③ AI 诊断层     │
│  RuntimeAdapter        Gitea / OpenObserve   上下文汇聚器     │
│  ├ compose (M2)        AgileConfig / Yearning LLM 客户端      │
│  ├ swarm  (v1.x)       凭证管理 · 健康巡检     Prompt 模板     │
│  └ k3s    (v1.x)                                       │
│                                                              │
│  横切:认证(IM插件) · casbin权限 · 通知中心 · 审计/时间线      │
└──────┬──────────────────┬─────────────────┬─────────────────┘
       │ SSH              │ OpenAPI         │ webhook
  目标主机            被管组件           OpenObserve 告警
  (docker-compose)    (Gitea/OO/AC/YN)
```

- **① 运行时适配层**:屏蔽 compose / swarm / k3s 差异,是平台的核心抽象。可观测(OTel 主动推送)与 CI(Gitea)天然与运行时无关,随运行时变化的只有部署与状态层;
- **② 组件集成层**:各组件 OpenAPI 的统一封装 + 凭证管理,支撑自建页面与 AI 上下文取数;
- **③ AI 诊断层**:告警事件管道 → 五源上下文汇聚 → LLM 推理 → 报告分发。

### 4.2 后端模块划分

| 模块 | 职责 |
|---|---|
| `setup` | 首次启动向导 API |
| `auth` | 本地超管登录、IM IdentityProvider 插件(企微/钉钉/飞书)、会话 |
| `rbac` | casbin 策略与用户/角色管理 |
| `runtime` | RuntimeAdapter 接口 + compose 实现 + SSH 执行器 |
| `integration` | gitea / openobserve / agileconfig / yearning 客户端,凭证管理,健康巡检 |
| `alerting` | 告警 webhook 接收、去重/静默、事件存储 |
| `ai` | 上下文汇聚器、OpenAI 兼容客户端、Prompt 模板、报告生成 |
| `notify` | Notifier 插件(与 IM 提供商同源)、路由规则、发送记录 |
| `timeline` | 变更时间线 + 审计日志 |

### 4.3 关键数据流

**部署流(gitops-lite)**

```
开发者 push → Gitea → Runner 构建镜像推送 Registry
    → Gitea webhook 通知平台(或 CI 尾部步骤调平台 API)
    → 平台 runtime 模块 SSH 到目标机:git pull compose 仓库 && docker compose pull && up -d
    → 写入变更时间线 → 通知订阅人
```

**测试环境部署流(槽位)**

```
开发者 push 分支 → Gitea webhook → 平台匹配占用该分支的槽位
    → 触发 Runner 构建(镜像 tag 带槽位标识)→ 部署到槽位隔离域
    → 结果通知占用者;释放槽位 = 销毁该隔离域全部容器,覆盖配置保留
```

**告警诊断流**

```
OpenObserve 告警规则触发 → webhook → 平台 alerting 模块
    → 入库 + 去重/静默(重复告警只升级不重发)
    → ai 模块汇聚五源上下文(日志窗口/容器事件/最近部署/最近配置变更/历史告警)
    → LLM 生成诊断报告(仅建议)→ 报告入库
    → notify 模块推送到订阅人(IM 应用消息)+ 控制台待办
```

### 4.4 数据模型(草案)

| 表 | 关键字段 |
|---|---|
| `users` | id, display_name, is_local_admin, status |
| `user_im_bindings` | user_id, provider, im_user_id(unique: provider + im_user_id) |
| `casbin_rule` | casbin 标准表 |
| `im_provider_configs` | provider, credentials_encrypted, enabled |
| `components` | name, type, base_url, credentials_encrypted, health_status |
| `runtime_targets` | name, type(compose/swarm/k3s), host, ssh_key_ref, compose_repo |
| `services` | name, runtime_target_id, gitea_repo, compose_service, description |
| `env_slots` | name(dev1/dev2…), type(test/gray), status(空闲/占用), occupant_user_id, occupied_at |
| `slot_services` | slot_id, service_id, branch(默认基线), enabled |
| `env_overrides` | slot_id, service_id, key, value(部署时注入环境变量) |
| `subscriptions` | service_id, user_id |
| `alert_events` | source, service_id, severity, title, dedup_key, status, occurred_at, raw |
| `diagnosis_reports` | alert_event_id, context_snapshot(JSON), model, prompt_version, result_md, status |
| `timeline_events` | service_id, type(deploy/config_change/sql_ticket/alert/manual_op), summary, payload, operator |
| `audit_logs` | user_id, action, resource, detail, ip |
| `notify_records` | user_id, scene, channel, content_ref, status |

### 4.5 安全要点

- setup 模式仅在用户表为空时开放,完成即关闭;
- 组件凭证与 SSH 私钥 AES-256-GCM 加密落库,主密钥仅存环境变量;
- 平台对被管组件统一以 service account 访问;平台权限体系与组件自身权限体系**不强同步**(深链跳转原 UI 时可能需要二次登录,属于已知边界,文档声明);
- AI 层全程只读;诊断报告固定附"仅供参考,请人工确认后执行";
- SSH 执行的命令白名单化(固定命令模板 + 参数校验),不做任意命令透传。

---

## 5. 技术选型

| 层 | 选型 | 备注 |
|---|---|---|
| 后端 | Go 1.22+ / gin / gorm / casbin / x/crypto-ssh | IM OAuth 三家协议简单,自实现不引重型库 |
| 前端 | Vue 3 + TypeScript + Vite | 模板已定:vue-vben-admin(Ant Design Vue,主应用 apps/web-antd) |
| 平台自库 | SQLite(默认)/ MySQL / PostgreSQL | env 切换;gorm 抹平差异;DDL / upsert / JSON 字段注意三方言兼容 |
| LLM | OpenAI 兼容 HTTP | dogfood:DeepSeek / GLM;prompt 版本化管理 |
| 部署 | docker-compose(平台自身) | 未来补 k3s manifest / chart |
| 指标补充 | cAdvisor 独立容器 | compose 运行时下补齐容器指标(K3s 阶段由内置替代) |

### 5.1 开源仓库策略:单仓库(monorepo)

```
custos-machina/
├── backend/     # Go 后端(独立构建、独立镜像)
├── frontend/    # Vue 3 前端(独立构建、独立镜像 / 或 embed 进后端)
├── deploy/      # docker-compose、env 示例、一键部署脚本
└── docs/        # 文档(含本计划)
```

选 monorepo 而非前后端分仓的理由:

- **发布原子性**:一个 tag 同步覆盖前后端,镜像版本永不错位;一键部署产物天然横跨前后端,同仓同步成本最低;
- **开源早期聚合效应**:issue / PR / star 集中一处,贡献者门槛最低,CI 一套流水线覆盖全栈;
- **license 声明单一**,合规最简单;
- 工程上仍是标准前后端分离:目录、构建、镜像彼此独立,monorepo 只是仓库组织形式,不引入代码耦合;前端产物可选 go embed 进后端二进制,额外获得单镜像交付形态。

拆分双仓仅在出现独立复用诉求时再考虑(例如前端被第三方作为独立产品二次开发),当前阶段不成立。

---

## 6. 里程碑(兼职节奏)

| 阶段 | 内容 | 出口标准(完成标志) | 预估 |
|---|---|---|---|
| **M0 调研验证** | Yearning API 覆盖度验证;选定 IM 的扫码登录 + 应用消息 demo;OpenObserve 告警 webhook + 查询 API demo;前端模板跑通并二选一(AgileConfig API 已于 2026-09-20 验证完毕,见 FR6.3) | 验证报告;两个决策落定(前端模板、Yearning 集成方式) | 0.5~1 周 |
| **M1 骨架与认证** | 前后端脚手架;DB 与迁移;setup 向导;本地超管;casbin;首家 IM 扫码登录 + JIT 注册;前端布局与权限路由 | 能扫码登录进入空控制台,权限生效 | 1~2 周 |
| **M2 运行时纳管与环境槽位** | compose 适配器(8 操作);服务列表 / 详情(状态 / 日志 / 事件);gitops-lite 部署流(生产);测试环境槽位(占用 / 释放 / 分支推送自动部署 / 覆盖注入);变更时间线 | demo 服务 push 到部署全程可见;占用 dev 槽位后推送分支自动部署,释放后容器销毁、配置保留 | 3 周 |
| **M3 组件集成** | Gitea CI 页;OpenObserve 日志搜索 + 仪表盘;AgileConfig 配置页;凭证管理 + 健康巡检;Yearning(按 M0 结论) | 日常高频操作不再需要直访组件地址 | 2 周 |
| **M4 告警与 AI 诊断** | webhook 接收 / 去重 / 静默;五源上下文汇聚;LLM 诊断管道;报告页 + 待办;IM 通知 + 订阅路由 | 人为制造一次故障,自动收到推送的诊断报告 | 2~3 周 |
| **M5 开源化打磨** | README(中)、部署文档、一键 compose、备份 cron、审计页、license 定稿、roadmap 卡片 | 陌生用户按文档可独立部署成功 | 1~2 周 |

**合计约 10~13 周(兼职;v0.3 因槽位功能 +1 周)。** M1 → M2 → M3 为串行主线;M4 依赖 M2/M3 的数据基础;M5 可穿插进行。

MVP(M0~M5)交付 compose 运行时。**后续版本(不占 MVP 工期)**:v1.1 swarm 适配器 → v1.2 k3s 适配器 → v1.3 灰度策略、依赖拓扑自动拉起、槽位闲置回收 → IM 交互卡片回调 → 审批后代跑(AI 建议的受控执行)。

---

## 7. 风险与待验证项

| # | 风险 / 待验证 | 影响 | 应对 |
|---|---|---|---|
| R1 | Yearning OpenAPI 覆盖度不足 | 自建页面降级为深链 | M0 首项验证,不阻塞主线 |
| R2 | IM 回调要求公网 HTTPS 域名 | 内网用户部署门槛 | 文档显著声明;给出反代方案 |
| R3 | AI 诊断质量依赖上下文工程 | 报告不准影响信任 | 迭代 prompt 与上下文裁剪;报告固定免责声明;保留人工路径 |
| R4 | compose 无滚动更新 | 部署有秒级中断 | MVP 接受;集群化路径(swarm / k3s)原生解决;过渡期备选双实例 + 反代蓝绿 |
| R5 | 三家 IM 插件的长期维护 | 开源后贡献压力 | MVP 一家;接口稳定后社区共建 |
| R6 | 平台用户 ≠ 组件用户,深链无法自动登录 | 体验打折 | 组件侧统一 service account 或共享账号;文档声明边界 |
| R7 | SQLite 并发写 | 极端情况下锁竞争 | 平台写入量小可接受;env 一键切换 MySQL / PostgreSQL |
| R8 | LLM 输出的建议命令有误 | 误导执行 | 只给建议不给执行;重要命令建议附带原文链接 / 文档引用 |

---

## 8. 待定项

| # | 事项 | 建议决策时点 |
|---|---|---|
| T1 | 项目域名 | 开源发布前(M5);项目名已定 CustosMachina(2026-09-20) |
| T2 | 开源 license | M5(建议 Apache-2.0;若深度捆绑 AGPL 组件需复审) |
| T3 | 首家实现的 IM | 已定:企业微信(自建应用扫码 + 应用消息,2026-09-21 关闭;接口按三家插件化设计,本地联调用 mock provider) |
| T4 | 前端模板 | 已定:vue-vben-admin(Ant Design Vue,主应用 apps/web-antd;2026-09-20 搭建时由 vue-pure-admin 改定,理由:模板工程化更完整、turbo monorepo 便于裁剪多形态应用) |
| T6 | 跨环境调用的定位(user-dev1 → order-dev2 是常态还是例外) | M2 前确认;当前按"例外"设计(FR10.4 覆盖项);若为常态需增加槽位依赖拓扑编辑与可视化 |
| T7 | 灰度环境策略的详细设计(权重 / 双实例 / 按运行时能力分级) | v1.3 前;MVP 仅预留环境类型字段 |

---

## 9. 下一步

1. 评审本计划,修正范围与里程碑;
2. 敲定 T3 / T4 两个待定项(数据库多库支持已定为决策 D12);
3. 启动 M0 调研验证(Yearning API / IM demo / OO webhook)。

---

## 10. 阶段划分与当前状态(v0.4 重写)

### 10.1 第一阶段:骨架 + 认证 + 权限管理 ✅ 定稿(2026-09-21)

已完成并经浏览器端到端验证:

| 模块 | 内容 |
|---|---|
| 工程骨架 | monorepo(backend/frontend/deploy/docs);Go 后端模块化单体 + wire 依赖注入(仓储面向接口,便于 mock 单测);前端 vue-vben-admin(主应用 apps/web-antd) |
| 初始化向导 | 首启检测 + 创建本地超管(break-glass),完成即永久关闭(FR1.1/1.3) |
| 认证 | 本地超管登录 + JWT;企微扫码登录(T3:企业微信,IdentityProvider 插件按三家设计,mock provider 供本地联调)+ JIT 注册(默认 guest)+ IM 绑定(FR2.1/2.2/2.4) |
| 权限 | casbin RBAC,后端唯一裁决(默认拒绝、超管旁路、改角色即时生效);前端 meta.authority 菜单过滤;角色权限矩阵可视化编辑;权限下发(FR3) |
| 工程化 | Makefile(一键 dev);pre-commit / pre-push;GitHub CI(前后端 lint / test / govulncheck / CodeQL);凭证 AES-256-GCM 加密落库 |

**优化项清单(第二阶段前后择机处理,不阻塞)**:

- 登录无速率限制 / 无账号锁定(防爆破);登录时序侧信道(用户名枚举);setup 建超管并发竞态
- 无状态 JWT:登出 / 踢下线 / token 吊销需 Redis 可选缓存(FR2.3 剩余部分)
- 企微真实环境联调(需公网 HTTPS 回调 + 真实凭证);应用消息通知(Notifier,FR4)
- AutoMigrate 仅 SQLite 验证过,MySQL / PostgreSQL 迁移冒烟未做
- CORS 收敛为可配置白名单;JWT secret 生产环境 fail-fast;组件健康巡检
- vben 自带 demos / 演示路由清理;README / 部署文档完善;中文 UI 文案统一(目前部分为 vben 英文默认)
- audit_logs 审计落库(FR8.1,目前仅权限体系,写操作审计未接)

### 10.2 第二阶段:待规划

原批次 2~5(运行时纳管 + 测试槽位 / 组件集成 / 告警 + AI 诊断 / 开源化打磨)并入第二阶段;
范围、优先级与拆批由新一轮沟通重新确定,届时更新本章。
