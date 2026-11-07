# 命名规则统一改造（starveil 接入驱动，2026-11-08 定稿）

> 背景：starveil-drama-server 接入过程中用户对容器/镜像命名规则提出统一诉求，
> 经讨论定稿（讨论记录见对话/记忆 starveil-cust-integration）。本计划是定稿落地。

## 一、终态命名规则

| 环境 | 稳态容器名 | 发布期 | 镜像（平台填充占位符） |
|---|---|---|---|
| 正式 | `<proj>-prod-1`（转正后） | `<proj>-prod-blue/green-1` 并存→切换→销毁旧域 | `<proj>:<v*tag>` |
| 灰度 | `<proj>-canary-<yyyymmdd-缩写>-1`（新版部署后自动销毁旧域） | — | `<proj>:<canary-tag>` |
| 测试 | `<proj>-<slot>-1`（如 `<proj>-dev1-1`） | push→CI 质检+构建→回调部署 | `<proj>-test:<yyyymmddHHMMSS>` |

设计要点（讨论定论）：

1. **容器名注入**：compose 不写 container_name，平台部署时给入口服务（server）
   注入 `container_name: <隔离域名>-1`，去掉 compose 强拼的服务段。
   代价：注入 container_name 的服务不可 `--scale`（compose 限制）——单实例入口
   无碍；需要多副本的服务不注入（保留默认命名）。
2. **蓝绿保留**（单机 compose 正式环境专属；k3s 载体原生 rollout 不适用）。
   色段是发布期的物理标识；稳态通过「转正」动作收敛为无色段名。
3. **转正**：发布成功后（手动按钮/可配自动）docker rename 活跃容器为
   `<proj>-prod-1` + conf upstream 改恒定名 + reload。不进发布关键路径——
   失败最坏只影响名字，流量无损。
4. **镜像占位符统一填充**：compose 的入口服务 image 写 `PLATFORM_IMAGE` 占位符，
   平台按环境+发布上下文填充（registry 从项目绑定 registry 推导，repo 约定
   `<proj>` / `<proj>-test`，tag 来自发布参数/灰度 tag/CI 回调）。
   发版流程中「手改 compose image tag」纪律消失。
5. **测试链路改 CI 回调**：dev push 不再由平台直接重建（旧镜像空转），
   改为 Jenkins 质检+构建时间戳镜像→推 registry→回调平台 API（应用级 token
   鉴权）→平台填充 image 并部署槽位。质检失败测试环境停留旧版（合理门禁）。

## 二、批次

### 批 1（小，先行）
- [x] 容器 tab 三列分列：容器名/状态/镜像（镜像去 registry 前缀显示 repo:tag）
- [x] 测试隔离域去 `-test` 段：`<proj>-test-<slot>` → `<proj>-<slot>`
      （含 containers-tab 过滤适配 + 旧域名兼容清理：rebuild 部署新城前销毁
      存量 `<proj>-test-<slot>` 旧域，一次性自动迁移）
- [x] starveil Jenkinsfile GWT 放行 `canary-*` tag（tag 模式正则+filter+job 同步）

### 批 2（中大）
- [ ] fetchCompose → 渲染管线：image 占位符填充 + container_name 注入
      （占位符 `PLATFORM_IMAGE`；registry 推导：项目绑定 registry；repo 约定；
      只动入口服务，sidecar 不动；非占位符的 image 保持原文=兼容存量项目）
- [ ] 项目配置：镜像 repo 覆盖（默认 `<proj>`/`<proj>-test`，特殊命名的项目可覆盖）
- [ ] Jenkins（starveil）branch 模式：质检后构建 `<proj>-test:<ts>` 推 registry，
      post 段回调平台
- [ ] 平台回调 API：POST /api/ci/slot-callback（应用级 token，参数 project/branch/
      image-tag），触发对应槽位重建（复用 dispatchRebuild，携带 image tag 进渲染）
- [ ] 槽位 OnBranchPush 直接重建停用（由回调接管；webhook 分支推送事件仍落
      构建通知等既有语义按需保留）

### 批 3（中，2026-11-08 完成）
- [x] canary 版本化域名：灰度发布域 `<proj>-<tag>`（tag=canary-yyyymmdd-缩写）；
      canary_states 表记录活跃域+容器名；新灰度部署成功后自动销毁旧域
      （DestroyCompose，失败告警不阻断，输出留痕于发布 Output）
- [x] nginx conf 渲染适配：backendsConf 参数化（转正容器 > 活跃域容器记录 >
      旧规则推断），canary upstream 用 canary_states 实际容器名
- [x] 转正操作：POST /releases/promote（rename 活跃容器为 <proj>-prod-1 +
      conf 恒定名 + reload；重复转正/未记录容器名明确报错；发布抽屉「转正」
      按钮——prod+有活跃色+未转正时显示）
- [ ] 「发布后自动转正」项目开关：**后置**（机制就绪，等真实偏好反馈再加 UI）

### 批 4 多实例（设计定稿 2026-11-08，实现后置——等第一个真实扩容需求）
- 实例 = 独立隔离域：扩容=新城 `<proj>-prod-<N+1>`（同一 compose 渲染，
  PLATFORM_CONTAINER 实例号参数化）；缩容=末尾淘汰（conf 先行移除成员→销毁域）
- upstream 多 server 行 = 负载均衡；实例清单以容器扫描为真源（不存 DB 防漂移）
- 发布策略分化：N=1 蓝绿+转正；N≥2 逐实例滚动（摘除→更新→健康→加回，
  与多机滚动同一状态机）

### 批 5 多机 compose / nginx 集群（设计入档，实现后置——rule of two）
- 架构：1 台入口机（nginx 管全部项目流量，HA=keepalived VIP 后置）+
  N 台应用机（compose + 平台分配 hostPort 段；「不占宿主端口」铁律在多机
  场景让步）；前提=入口机与应用机内网互通（接入条件写明）
- 滚动状态机与批 4 统一（滚动单元=隔离域，摘除/更新/恢复）
- 与 k3s 重叠度高：多机需求默认引导 k3s（单节点也轻），本路径仅服务
  「客户环境装不了 k3s」场景，等真实需求立项

### 收尾
- [ ] deploy-conventions.md 全面更新（§一隔离域命名/§二 nginx/§三自查清单）
- [ ] starveil compose.custos.yaml 切换占位符 + 发版流程文档更新
- [ ] 全量测试 + CI 绿 + v0.12.18 发版（攒批：流式日志/slots 修复/供应链钉版/本命名批次）

## 三、风险与对策

1. **compose×container_name 注入**：注入值写入落盘的 compose.yaml（每域独立
   文件），域名即注入值来源——域销毁重建自然一致；rename（转正）造成的名字
   漂移发生在域生命末期（下次发布即销毁），容忍。
2. **回调 API 安全**：应用级 token（同配置拉取 pull token 机制，sha256 落库、
   可吊销、限定 app）；请求携带 project+branch+tag，服务端校验 token 归属。
3. **槽位部署语义变化**：从 push 即部署到回调部署——Jenkins 故障时测试环境
   不更新（监控质检构建失败率；槽位页显示「等待 CI 回调」状态提示）。
4. **存量兼容**：非占位符 image 的项目（demo 等）走原文部署零影响；
   测试域旧名容器由兼容清理自动迁移。
