# CustosMachina 复核报告（v0.12.4 ~ v0.12.7）

> **复核范围**：`8c3754c..7e744ae`（HEAD），共 9 个提交 + 工作树（复核期间仓库持续推进，末轮已收敛为干净树，仅余 `?? audit-report-v0.12.3-independent.md`）。
> **复核方式**：全程只读；**提交信息不作为证据**，每条结论回源码裁定；关键结论用实证链（项目自带编译器产物 / JS 语言语义复现 / 门禁实跑 / `-race`）。
> 复核时间：2026-10-07。前一版本批次（v0.12.1~v0.12.3）复核报告见 `reverify-report-v0.12.3.md`。

---

## 一、结论先行

| # | 提交 | 内容 | 判定 |
|---|------|------|------|
| 1 | `bdf472a` | gorm 日志改自定义 Interface（Trace 直连） | ✅ 真修（附 2 注意点） |
| 2 | `64a9358` | 独立审核 T1~T5（超时/body 上限/500 脱敏/TLS 开关/安全头） | ✅ 五项全部成立（附 1 处 nginx 继承瑕疵） |
| 3 | `c2d2999` | 前端复核残留批（Monaco/extractErrMsg/401 刷新/SSE 上限/深色集中） | ✅ 大部分真修（附 1 处漏网 + 1 处口径小瑕） |
| 4 | `197a9fe` | v0.12.4 发版文档（含更正 v0.12.3 虚假声称） | ✅ 更正到位，措辞与代码事实一致 |
| 5 | `1e74fbe` | v0.12.5 单镜像后端守护脚本 | ✅ 真修（`set -e` 与 and-list 交互正确） |
| 6 | `6ae13c0` | v0.12.6 跳级迁移断裂根治 | ✅ 高质量（跳级重放无裸 DDL 风险，测试真断言） |
| 7 | `bdd83d0` | v0.12.6 发版文档 | ✅ 事故叙述与代码一致，时间线诚实 |
| 8 | `876cb71` | 独立审核 T6/T7/T8（v0.12.7 tag） | ⚠️ T8 ✅ 真修；T6/T7 实现正确但 **声称与接线不符**（见 §三） |
| 9 | `7e744ae` | U1 管理后台四域分组 + D1 status.md | ❌ **P0：管理后台打开即崩（TDZ，实锤）** + typecheck 红（见 §二） |

**客观门禁**：后端 build ✅ / vet ✅ / 全包 test ✅（缓存命中＝与上轮实测全绿内容一致）/ 受影响包 `-race` ✅；**前端 `vue-tsc` 红（2 个类型错，`7e744ae` 引入）**，CI 前端 job 含 `pnpm check:type`（`.github/workflows/ci.yml:84-85`）→ **CI 现值必红**。

---

## 二、P0：`7e744ae` 管理后台打开即崩（TDZ）

**位置**：`frontend/apps/web-antd/src/views/admin/index.vue:59` vs `:61`

```
:59  const activeTab = ref<string>(initTab());   ← 立即调用 initTab()
:61  const route = useRoute();                    ← route 在此才初始化
:64  function initTab(): string { ... route.query.tab ... }   ← 函数体内读 route
```

JS 语义（同层作用域、函数提升、调用在前）：`initTab()` 执行时 `route` 的 `const` 绑定仍在 TDZ（初始化未执行）→ **`ReferenceError: Cannot access 'route' before initialization`**。若构建链把 `const` 降级为 `var`，则退化为 `TypeError: Cannot read properties of undefined (reading 'query')`——**两条路都崩**：Vue `setup()` 抛错 → 组件渲染失败 → 打开「管理后台」页即白屏/错误兜底。

**实锤链（三方交叉）**：

1. **项目自带编译器产物**（`@vue/compiler-sfc` 3.5.41，pnpm store 实调）：编译该文件后 `setup()` 内语句顺序保持——`:66 ref<string>(initTab())` 先于 `:68 const route = useRoute()`，同层作用域，无重排。
2. **JS 语言语义复现**（node v24，等价结构）：实抛 `ReferenceError: Cannot access 'route' before initialization`。
3. **门禁盲区确认**：`vue-tsc` **不报** TDZ（TS 对经函数间接的前置调用不做控制流分析）；vite/esbuild 不做类型检查（构建可通过）；前端无组件级测试——**所以 CI 拦不住，只有运行时暴露**。

**修复方向**（一处顺序调整）：把 `const route = useRoute(); const router = useRouter();` 两行上移到 `const activeTab = ...` **之前**；或 `activeTab` 先给 `'im'`，挂载/`watchEffect` 后再按 `?tab=` 同步。改后请本地打开一次管理后台并跑 `pnpm typecheck` 验证。

**连带：同文件 2 个 typecheck 错（独立于 TDZ，`vue-tsc` 实报）**：

- `:58` `TS2322`：`SECTIONS.flatMap(...)` 在 `as const` 元组联合上推断失败；
- `:66` `TS18046`：`'t' is of type 'unknown'`（同上根源）。

修法：给 `allTabs` 显式类型（如 `computed<string[]>` 并改收集 `t.key`，或给 SECTIONS 标注 `readonly { key: string; label: string }[]`）。三处（TDZ + 2 类型错）在同一文件、可一次修完。

**其余 U1 元素经查成立**：`SECTIONS` 11 项与模板 11 个 `a-tab-pane` 的 key 集合完全一致（实核）；`initTab()` 对 `?tab=` 做了合法性校验（非法值回落 `im`）；`router.replace` 不污染浏览历史。小瑕：底部分组锚点用无 `href` 的 `<a>`（键盘不可达，建议换可聚焦元素）。

---

## 三、`876cb71`（T6/T7/T8，v0.12.7 tag）详审

### T8 Redis 密码策略对齐 —— ✅ 真修，升级路径闭环

- 写入侧（`auth/tokenpair.go:152-156`）：`cfg.Password != "" && s.cipher == nil` → 明确报错拒绝明文落库（此前该组合走明文回退，确为库内唯一明文凭据）。无密码 Redis 留空即可，报错文案已指明。
- 读取侧（`decryptRedisConfigPassword`，:166-176）：`cipher == nil` 直接返回；解密失败视为旧版明文——**历史明文配置仍可读**，不因策略收紧而失效。升级兼容闭环 ✅。

### T6 限速器周期清扫 —— 实现正确，但"关停统一回收"声称不实

- 实现逐点核对 ✅：`Window.mu`/`Lockout.mu`（:20/:116）与 `Allow`/`Blocked`/`ReportFail` 同锁；sweep 判活口径与主路径一致（`now.Sub(t) <= window` / `now.After(until)`）；ticker goroutine 带停止通道、`sync.Once` 幂等；`registerSweeper` 加锁注册。
- ❌ **`StopAll` 无任何调用者**：全仓非测试 Go 代码仅 `ratelimit.go:31/38/39`（注释 ×2 + 定义）。而 CHANGELOG v0.12.7 写「构造即注册清扫，**app 关停统一回收**」、代码注释写「（app cleanup 调用）」——**声称了未发生的行为**（与 v0.12.3 gorm「写在死方法上」同型）。实害低（长跑进程无碍、进程退出由 OS 回收），但按本项目既定标准须对齐：要么在 app 关停路径真正调一次 `ratelimit.StopAll()`（一行），要么把措辞改为"提供 StopAll 供关停使用（当前未接线）"。
- 无 sweep 测试（`ratelimit_test.go` 仅 `TestWindowAllow`/`TestLockout` 两用例）。`sweep(now)` 已参数化，补"过期 key 被清、窗口内 key 保留"用例成本极低。
- 注释错位（上轮遗留，未修）：`// Gin 中间件形态：失败锁定（Blocked 即 429…）`（:185）挂在 `Lockout.StartSweeper` 上方，而 `Lockout.Middleware`（:223）反而没有文档——godoc 会误导（`Window` 那边位置是对的）。

### T7 业务告警有界投递 —— 实现正确，两处残留

- 实现逐点核对 ✅：`bizCh` 256 深 + 4 worker（`service.go` 常量）；`EnqueueBusiness` 非阻塞 `select`，满载 false → handler 回 429（`handler.go` businessAlert）；worker 走 `jobs.GoSafe`（`jobs.go:96-105`，recover + 堆栈日志，实核）；worker 传 `context.Background()` 与旧 `context.WithoutCancel(request ctx)` 对 `NotifyEvent` **等价**（notify 包无 `ctx.Value` 依赖，`WithContext` 查询两者均无截止）——无回归。有界投递后内存上限可预测，CHANGELOG「此前每请求裸 goroutine」叙述属实。
- ⚠️ **`bizCap` 死字段**（`service.go:47`）：定义后从未赋值、从未读取——写了一半的残留，建议删。
- 429 响应手写 `c.JSON(..., gin.H{...})` 而非 `httpx.Fail`：输出 JSON 等价（`Body{code,message}` 同构），但与刚做过的"统一响应出口"收敛方向不一致（v0.12.3 才收敛过 500 出口），建议改用 `httpx.Fail(c, 429, 429, ...)`。
- T7 无新增测试：`EnqueueBusiness` 满/未满语义几行可测（写满 channel 断言 false）。
- 停机语义注意：队列内未投递事件随进程终止丢弃（无 drain）——相比旧状不算退步，CHANGELOG 亦未声称解决；如未来要"关停 drain"再议。

---

## 四、前七项详审（摘要）

1. **`bdf472a` gorm 日志 ✅ 真修**：自定义 `gormLogger` 完整实现接口（`LogMode` 返自身；`Info/Warn/Error` 直连 zap；`Trace` → `routeSQLLog` 分流），调用点 `db.Logger.Trace(...)` 链路闭环；`writerAdapter` 死代码全删（`Writer()` 无残留调用）。
   - 注意点 ①：`TestRouteSQLLogClassification` 用测试内**复刻的 classify 闭包**做断言，未绑定 `routeSQLLog` 输出——实现漂移不会红（弱断言）。
   - 注意点 ②：正常 SQL 现全落 Info（旧 `LogLevel=Warn` 时正常查询不输出）——日志体量变化，CHANGELOG 已明示为有意。
2. **`64a9358` T1~T5 ✅**：T1 超时（刻意不设 Read/WriteTimeout，SSE 理由成立）；T2 body 上限（豁免前缀对真实路由 group 命中：`server-files` 110MB / `server-compose` 16MB）；T3 500 脱敏（全仓唯一出口）；T4 TLS 开关（默认安全，`CUSTOS_AGILE_INSECURE_TLS=1` 才放行，`.env.example` 已注）；T5 三份 nginx + 后端中间件安全头。
   - 瑕疵：`frontend/scripts/deploy/nginx.conf` 的 `location /` 自带 5 个 CORS `add_header` → 同块三行安全头被继承规则覆盖**不生效**（该 conf 全仓无引用，另两份 conf 正确）。低优先。
3. **`c2d2999` 前端残留批 ✅**：Monaco 实例级 schema 守卫 + 共享 env 单次装配；`extractErrMsg` 38 处收敛；chat 401 → `refreshTokenApi` 刷新重试（依赖签名实核）；SSE 渲染末 64,000 截断；深色集中 `--custos-term-bg`；selfHeal 监听摘除（无 TDZ，调用时序在后）；两个新测试文件均真断言（假定时器驱动、会真红）。
   - 漏网 1 处：`ai-assist.vue:94` 仍在手写 `error?.response?.data?.message ?? ...`——可一并收敛。
   - 口径小瑕：截断文案"字节"实为 UTF-16 码元。
4. **`197a9fe` v0.12.4 文档 ✅**：对 v0.12.3 gorm 条目"虚假声称"的更正**如实且准确**（引用了探针实测"失败 SQL 下 Write 调用 0 次"）；README 版本引用同步；`.env.example` 补 TLS 开关注释；顺带清理 `monaco-env.ts` re-export（调用方已各自直连 `monaco-editor` 导入，实核无断裂）。
5. **`1e74fbe` 守护脚本 ✅**：`( while true; ... ) &` 守护 + 15s 就绪门；`set -e` 与 and-list 短路交互推演正确（`custos-server || echo ...` 不会因管道/短路误退出）。
6. **`6ae13c0` 跳级迁移 ✅ 高质量**：`pending` 检测 → 版本迁移后补 AutoMigrate 兜底；0001~0008 全部为 `SELECT 1;` 占位（**跳级重放无裸 DDL 风险**，实核）；0006 改名守卫先于补齐；`TestJumpUpgradeFromPreMigrationEra` 造 v0.9.x 形态真断言（建表/改名/数据保留/后续可写）。架构从"严格版本化迁移"转"版本 + AutoMigrate 双轨"——决策变化已在测试注释说明，status.md 亦落档。
7. **`bdd83d0` v0.12.6 文档 ✅**：事故时间线与代码一致（v0.12.5 时未定论 → v0.12.6 定位跳级断裂），诚实。

---

## 五、文档与声称核验（`7e744ae` 的 D1 部分）

- `docs/status.md` ✅ 新增合理，自称"唯一真相源"与 plan-*.md 降级说明一致。抽验两项声称**均属实**：govulncheck 在 CI（`.github/workflows/ci.yml:49-50` 实见）；前端业务测试恰 3 个文件（`use-paged-list-polling` / `config-format` / `extract-err`，`find` 实见）。
- README / CHANGELOG 版本引用已同步 v0.12.7（`:v0.12.7` ×2 + 状态行）。
- ⚠️ CHANGELOG v0.12.7 引用 `audit-report-v0.12.3-independent.md`——该文件**未跟踪**（`git status: ??`），clone 者不可见。要么入库，要么改指 status.md。

---

## 六、客观门禁

| 项 | 结果 |
|---|---|
| 后端 `go build ./...` | ✅ |
| 后端 `go vet ./...` | ✅ |
| 后端 `go test ./...` | ✅ 全包 ok（缓存命中，内容与上轮实测全绿一致） |
| 后端 `go test -race`（ratelimit/notify/auth） | ✅ 三包全绿 |
| 前端 `vue-tsc`（web-antd） | ❌ 2 错（`7e744ae`，见 §二） |
| CI 前端 job | 含 `pnpm check:type` → **现值必红** |
| CI 后端 job | 含 `gofmt/vet/wire 新鲜度/test -race/govulncheck/build`（实见） |

---

## 七、建议修复顺序

1. **P0（`7e744ae`，一次修完同文件三处）**：`admin/index.vue` 行序（`route/router` 声明上移）修 TDZ；给 `allTabs` 显式类型修 2 个 `vue-tsc` 错；改后本地打开管理后台 + `pnpm typecheck` 验证。
2. **P1（声称对齐，二选一）**：`ratelimit.StopAll()` 接入 app 关停路径（一行），或改 CHANGELOG/注释措辞为"提供 StopAll（当前未接线）"。
3. **P2**：删 `bizCap` 死字段；429 改走 `httpx.Fail`；`Lockout` 注释归位；补 T6 sweep 与 T7 Enqueue 各 1 条测试。
4. **P3**：`frontend/scripts/deploy/nginx.conf` 安全头继承；`ai-assist.vue:94` 收敛；SSE 文案"字节"→"字符"；独立审核报告入库或改引用。
5. `bdf472a` 弱断言（`routeSQLLog` 测试绑定实现输出）可顺手加固。

---

*本报告全程只读复核，未改动任何代码。提交信息未作为证据，上述每条结论均可按文中文件:行号回源码复验。*
