# P7-M2 告警 AI 分析·细化设计

> 定稿：2026-10-06（P7 第二仗，依据 plan-phase7 §二 M2 与审核修订）
> 目标：所有平台告警先过 AI 分析再进通知（warn/info），critical 先发再分析补发；
> 分析基于五个新只读工具主动查证（升级替代 P5 digest 的单次 Context Pack）。

## 一、关键决策

### D1 接入点：notify.NotifyEvent 统一入口 + 接口注入

notify 是所有告警的唯一汇聚点（observ/resources/certs/backup/cron 全部经它）。
notify 定义 `AlertAnalyzer` 接口（app 层注入 ai 实现）——ai 已依赖 notify，
反向注入避免环。分析在 NotifyEvent **入口**执行（分析段并入 detail 后进规则
匹配/聚合/静默判断，聚合场景分析段随首条保留）。

### D2 两阶段时序（用户定调 + 审核修订落地）

- **critical**：立即投递原始告警 → 异步分析 → 结果以 `ai_digest` 源补发第二条
  （沿用 P5 补发形态，"AI 分析（参考）"）
- **warn/info**：先分析再投递；**硬超时 60s**——超时/失败/未配置 AI 一律降级为
  原样投递（不只失败不阻塞，"慢"同样不阻塞；不补发，避免延迟噪声）
- 调用方多在 goroutine/WithoutCancel 上下文；同步等待 60s 上限只影响通知链路
  自身（business 已异步、定时器路径低频可容忍）

### D3 分析源集合与自激防护

进分析的源：`cron_failed` / `observ_failed` / `platform_ops` / `o2_alert` /
`cert_expiring` / `backup_failed`。
**排除**：`ai_digest`（补发事件再分析=自激环，vector 自激环教训）、`business`
（业务服务自判，平台上下文无归因价值）、rule-test（人工测试）。

### D4 升级替代：P5 digest 旧路径删除

observ 的 `MaybeDigest` 调用与 ai 的 `DigestService.MaybeDigest`（单次 Context Pack）
移除；`ai_digest` 通知源保留（critical 补发用）。分析不再预打包上下文——
模型经 function calling **主动查**五工具（git 提交/发版/配置变更/构建日志/日志/指标），
平台侧只给告警本身 + 项目归属提示。

## 二、五个只读工具（挂 app.toolsBridge，对话与告警分析共用，ops 视角）

| 工具 | 参数 | 数据来源（全部平台已有集成，AI 不直连外部） |
|---|---|---|
| `search_o2_logs` | query(SQL where), stream 可选, minutes, limit | observ.Service 新导出 `O2SearchLogs`（O2 `_search` API，Basic 认证） |
| `query_o2_metrics` | promql, minutes | observ.Service 新导出 `O2QueryMetrics`（O2 prometheus `query_range`） |
| `get_build_logs` | project_id 可选, limit | ci.Service 既有 `BuildLog`（外联 consoleText / test 环境走部署输出）+ `log_tail` 兜底 |
| `get_git_commits` | repo_path, since_hours, limit | GitProvider 新增 `Commits` 方法 + gitea/gitee 双实现 |
| `get_config_changes` | limit | configs.Service 新导出 `RecentChanges`（config_versions 最近版本：文件/环境/操作人） |

工具结果统一 8000 字符截断（既有纪律）；O2 未配置/CI 不可达时返回
"（暂不可用：原因）"文本——模型据此降级分析（计划要求：不因外部挂了就完全不分析）。

## 三、log_tail 可靠性保障

- `builds` 表加 `log_tail TEXT`（迁移 0004：ADD COLUMN，双方言兼容纯 SQL）。
- Poller 轮询到终态（success/failed）时调既有 `CIProvider.Log` 取尾部 200 行存入。
- `get_build_logs` 工具优先 DB log_tail（快、Jenkins 不可达时可用），
  构建失败且无 log_tail 时实时调 provider 补拉（回退路径）。

## 四、分析服务（ai 包 alert.go）

```
AnalyzeAlert(ctx, source, title, detail) (analysis string, ok bool)
```

- relay 未配置 → `(nil, false)`（静默降级）
- 工具循环：messages=[system(诊断提示词+防注入声明), user(告警)] +
  `ToolSource.ChatTools(ctx, ["ops"])` → `CompleteStream`（onDelta nil）
  → tool_calls 则 `execChatTool` 执行回填 → 循环，**最多 5 轮**；
- **总预算 60s**（外层 ctx 超时，超时返回 false）；
- 输出 ≤1200 字符截断；用量留痕 caller=`alert_analyze`（复用 relay 记账）。

## 五、验收（对应 plan M2）

1. warn 告警（AI 已配置）→ 通知正文含"AI 分析"段，AI 引用了真实工具数据
2. critical 告警 → 原始通知先到（不等 AI），分析结果第二条 ai_digest 补发
3. AI 未配置 → 通知原样发出（降级），无报错
4. 分析超时（60s）→ 原样投递不阻塞
5. 构建失败告警分析能引用 log_tail（CI 不可达时仍可分析）
6. business/ai_digest 源不触发分析（防自激）

## 六、已知边界

- 分析段并入 detail 参与聚合/静默语义（静默时段 warn 被静默则分析也一起被静默——符合预期）。
- 项目归属推断（告警标题→项目）靠模型自己用 list_projects 工具查，平台不做预关联。
- M2 无新管理界面；分析开关=AI 中转层是否配置（既有语义）。

## 七、验收记录（2026-10-06 实测）

单测：notify 两阶段五用例（warn 先析后发含分析段/超时预算截断降级/critical 立即投递+异步补发/ai_digest+business 自激防护/未注入直推）+ 0004 迁移存量库用例，全量绿。

冒烟（真服务 + mock OpenAI SSE）：
1. ✅ AI 未配置 → warn 告警直推、无分析段
2. ✅ AI 端点不可达 → 0.1s 快速降级直推，ai_usages 留痕 caller=alert_analyze 失败记录（接线生效证据）
3. ✅ critical → 0.1s 立即投递原始告警（不等分析），followup 异步
4. ✅ 完整循环：mock 模型发起 get_config_changes 工具调用 → 平台执行（查真实 config_versions）→ 回填 → 输出分析 → 「【AI 分析（参考，非结论）】」段并入通知正文（notify_records 留痕可见）

已知边界补充：rule-test 管理端点同步触发分析会占满 60s 预算（生产告警源均在异步上下文，不受影响）。
