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
