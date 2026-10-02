# M3 前置调研：OpenObserve alerts API 覆盖度（2026-10-02）

> 结论：**按稳定版 v1 API 实现，覆盖度足够**；O2 部署固定 stable tag（不追 HEAD，2026 版仓库结构已大改且 alerts 端点迁到 /api/v2、destination 模型重构，稳定性未验证）。

## 采用的 API 形态（稳定版，0.1x 系列）

- **Alerts**：`GET|POST /api/{org}/alerts`；`GET|PUT|DELETE /api/{org}/alerts/{name}`（name 唯一键）
- **Destinations**：`GET|POST /api/{org}/alerts/destinations`；`PUT|DELETE .../destinations/{name}`
  - 字段：`name / url / method(post|get|put) / headers / template / skip_tls_verify`
- **Templates**：`POST|PUT /api/{org}/alerts/templates`（Handlebars body，变量 `{alert_name} {stream_name} {org_name} {alert_type} {rows} ...`）
- **认证**：`Authorization: Basic base64(email:password)`（root 用户）
- **Alert 关键字段**（snake_case）：`stream_type / stream_name / is_real_time / query_condition{type: "sql"|"custom"|"promql", sql, conditions, aggregation} / trigger_condition{period, operator(>=|<=|>...), threshold, frequency, frequency_type("minutes"|"cron"|"seconds"), cron, silence} / destinations[] / enabled / description / tz_offset`

## 平台侧设计决策

1. **平台模型自持、O2 是投影**：`observ_alerts` 表存告警定义，CRUD 后同步 O2（失败可重试，留 sync 状态）——与"UI 只面向平台模型"的路线图原则一致。
2. **destination/template 平台自动维护**：初始化确保 O2 存在专用 destination `custos-platform`（URL=平台 webhook + `X-Custos-Token` 共享密钥防伪造）与默认模板；同步的告警 destinations 一律指向它。
3. **webhook 接收器**：`POST /api/observ/alerts/webhook`（公开 + token 校验 + 限速），默认模板输出 `{alert_name, stream_name, org_name, rows}` → `NotifyEvent(o2_alert, ...)`。
4. **级别映射**：O2 稳定版模板无 severity 概念（新版才有 warning/critical 分级）——平台侧按告警条数触发级别可选配置（每告警定义里配 info/warn/critical，默认 warn）。
5. **透传认证（免登）降级**：O2 无官方 token 深链机制，需要反代注入 session cookie，复杂度高——**M3 一期只做直链**（O2 地址 + stream 搜索参数直达登录页），免登注入留 P6 评估。此为对 plan 的偏差，已记录。
6. **vector→O2 投递验证**：真机部署日志采集组件后，从平台侧调 O2 `_search` API 验证流存在（dogfood 验收用）。

## 风险

- 稳定版与新版字段差异：平台同步按上述 v1 字段构造；若部署的 O2 版本校验失败，sync 状态会带出 O2 错误原文便于排查（部署固定 tag 消除漂移）。

## 真机补充（v0.91.0 企业版，2026-10-02 实测）

用户指定部署 `public.ecr.aws/zinclabs/openobserve-enterprise:v0.91.0`（非稳定版 0.1x），实测 API 为**混合形态**，上面"稳定版 v1"的结论作废，以下为实测事实：

- **Alerts 在 v2**：`GET|POST /api/v2/{org}/alerts`、`PUT|DELETE /api/v2/{org}/alerts/{alert_id}`（**路径参数是 alert_id 不是名称**——传名称 PUT 会被当新建、DELETE 恒 404）
- **POST 重名不报错**：同组织同名 POST 静默创建重复条目（真机踩坑：upsert 不能依赖 "already exists" 错误，必须先 GET 列表按名反查 alert_id，命中 PUT、未命中 POST）
- **Destinations/Templates 在无版本前缀**：`/api/{org}/alerts/destinations|templates`（加 /api/v1 或 /api/v2 前缀均 404）
- **模板变量**：单大括号 `{var}`，且只有部分变量被渲染（实测 alert_name/stream_name/org_name/alert_type 渲染，trigger_time/rows_count 保留占位符）；**模板体全部值加引号**，未渲染时整体仍是合法 JSON，接收端 flexInt + 原文兜底容错
- **streams 列表 doc_num 是持久化滞后统计**：内存数据立即可查（_search 能命中、doc_num 仍为 0），验证投递别看 doc_num，直接 search
- **访问日志自激环**：vector docker_logs 采 O2 自身 stdout，O2 的 axum access_log（INFO，每请求一条）会被采回 O2 形成 1/s 无限自循环。`ZO_LOG_LEVEL=warn` 与 `ZO_ACCESS_LOG_ENABLED=false` 在企业版 v0.91 均不生效（后者出自 fork 的 issue，主线不认）——**根治靠 vector 管道过滤 `middlewares::access_log` 签名行**（已进默认模板）
- **告警真实触发链路已验证**：synced 告警在真机 O2 评估→触发→回调 destination（X-Custos-Token 头正确携带）；平台侧接收→通知路由已在本地 e2e 验证，生产域名升级 v0.5.x 后全链自动闭环
