# Changelog

## v0.12.17 (2026-10-07)

v0.12.16 双报告复核（第四轮逐提交复核 + 独立复核）补丁：两项 P1（RBAC 升级复活已删权限、发布门禁无效）+ 断言检测力修复。对照 `reverify-report-v0.12.4-16.md` §九 / `reverify-report-v0.12.16-independent.md` R1/R2/R3/R9。

### RBAC 升级正确性（P1，独立复核 R1）

- **v27 迁移改精确补种——升级不得复活管理员删除的内置角色权限**。v0.12.16 跳级修复引入的扩大面：`v<27` 触发的 `entryLevelSeed` 是角色级全量补齐语义，管理员经 role.vue（支持编辑内置角色动作集与 HTTP 矩阵）删除的默认权限（如 ops 的 `/certs`）会在任意升级/跳级后静默恢复——授权边界扩大。独立复核以临时 overlay 反例实证。修法：删 v27 的 entryLevelSeed 分支，`oldVersion < 27` 时只对 admin/ops/dev 与存量自定义角色精确补 home 两条 GET；跳级补种与版本推进语义不变（22/24/25 用例保持绿）。回归测试：删除内置角色权限后升级，home 补上而删除项保持删除

### 发布治理（P1，独立复核 R2）

- **consistency 门禁挂入发布依赖链**：`images` 增 `needs: consistency`——tag 与 status.md 版本不一致时，镜像推送/包公开/GitHub Release 全部阻断（此前 jobs 并行，失败只红 workflow 拦不住产物）。workflow_dispatch（无 tag）时脚本自行成功退出，避免下游 needs 被 skip 连坐

### 测试有效性（P3，复核 P3-N1 / 独立复核 R3/R9）

- **tokenization 断言检测力修复**：mtk span 计数 >0 改为 distinct mtk class ≥2——两报告反例实证未注册语言 Monaco 仍产出单类纯文本 span，旧断言两态恒真；反例验证：注释 shell 注册后新断言必红，恢复后绿
- **删除 E2E 的 AI 悬浮球 CSS 注入**（独立复核 R3）：实测无注入正常点击通过——悬浮球从未真实遮挡（真遮挡者=创建项目后自动弹出的详情抽屉，v0.12.16 已修）；E2E 不再修改被测 UI

### 未做（后续批次）

R4 发布确认提交与敏感 reveal 的 E2E、R5 移动端记录布局、R6 顶栏图标按钮可访问名称、R7 大文件拆分（`bridge.go`/`configs/files.vue`）、R8 Vben Form slot 迁移、R10 console.error 治理。

## v0.12.16 (2026-10-07)

v0.12.15 双报告复核（第三轮逐提交复核 + 独立复核）的补丁批：并发竞态修复 + 跳级升级根治 + 回归盲区补测。对照 `reverify-report-v0.12.4-15.md` §八 / `reverify-report-v0.12.15-independent.md` R1~R3。

### 并发（P1，独立复核 R1 / 复核 §八.3）

- **notify `EnqueueBusiness`/`Stop` send-close 竞态互斥化**：`stopMu` RWMutex——读锁内完成 stopping 检查+非阻塞发送、写锁内完成置位+close，close 时不可能有并发发送者。旧实现（v0.12.15 引入的 atomic.Bool 检查）存在 TOCTOU 窗口：检查通过后、发送前 Stop 完成 close，命中即 `send on closed channel` panic（停机时在途 webhook 请求可触发进程 panic；`-race` 检测不到此类时序错误）。回归测试：50 轮 × 8 goroutine 循环入队与 Stop 真并发压力

### 升级路径（P2，复核 §八.1）

- **casbin 种子跳级漏种与版本卡死根治**：v27 补种分支由精确匹配 "26" 改为数值比较（对齐 `deNarrowPolicies` 既有模式）——v0.12.1("24")/v0.12.2("25") 直升的部署此前既漏种 home（非超管首屏继续 403）且版本号永不推进；`migrateSeedVersion` 返回值语义明确为「需要推进版本号」，无策略变更也推进。自定义角色补种加版本 gate（v27+ 不再重放，人为删除不复活）。22/24/25 跳级与 v27+ 不复活用例补齐

### 编辑器回归（P3，复核 §八.5/§八.6）

- **sql 语言注册遗漏修复**：editor.api 精确入口重构时未注册 sql，告警模板/策略的三处 PromQL 编辑静默降级纯文本
- **editor E2E 加 tokenization 断言**（mtk 高亮 span）：原断言只检测装配崩溃/白屏，对静默降级不敏感——正是 sql 回归漏检的原因

### E2E 门禁（P2/P3，独立复核 R2）

- **pageerror 全局门禁**（fixtures page 覆写）：每用例默认断言无未捕获页面错误
- **guest 403 拒绝用例**：与 ops 200 构成权限双向判据，防种子误放开
- **发布取消无副作用断言**：关闭发布抽屉期间不发出任何 `/api/releases` POST
- **去除 force:true**：真实遮挡查明——创建项目后产品自动打开详情抽屉（引导配置部署目标的既有行为），force 一直在掩盖其对表格按钮的拦截；helper 显式关闭后正常点击。顺带修两类脆弱点：antd 抽屉/Modal 关闭后 DOM 残留（断言须 `:visible`）、两字按钮全角空格（行内定位）

### 文档与门禁（P2/P3，独立复核 R3 / 复核 §八.7/§八.8）

- providers/README 四处失实纠正（删误入的 Telegram 渠道、`observ.ObservBackend`/`certs.DNSProvider` 符号修正、常量位置 model.go→channels.go）
- status.md 测试口径核准（vitest 67 文件/457 条、E2E 5 spec/11 条）+ 浏览器兼容如实（Chromium 持续验证，Firefox/Safari 手工范围）
- **release CI 新增一致性门禁**：tag 推送时校验 status.md 版本与 tag 一致
- `SetReadDeadline` 失败记 debug（自定义 Writer 环境期限静默失效可见化）+ h2 边界注释；audit 例外条目 module 一致性校验；AI 悬浮球 aria-label

### 未做（维护期第三批）

R4 大文件拆分（`bridge.go` 749 行 / `configs/files.vue` 1218 行）、R5 移动端宽表列表卡重排、R7 Vben Form slot 迁移——工程量大且独立复核亦归为后续批次，不阻塞本补丁。

## v0.12.15 (2026-10-07)

双报告（逐提交复核 + 独立审核 v0.12.14）合并整改：供应链清零 + 契约纠偏 + 可靠性收口 + 权限修复。对照 `reverify-report-v0.12.4-14.md` §九 合并裁定。

### 权限（P1，复核 §7.1）

- **home casbin 种子 v26→v27 补种**：v0.12.8 上线首页/就绪度时漏种策略——admin/ops/dev 登录后首屏 `/home/summary`、`/home/readiness` 全 403（E2E 全绿是因超管旁路）。补 defaultPolicies 六条 + 存量库 entry-level 迁移 + 存量自定义角色补基础读集（customBaseRoutes 同步）；真机 curl 以 ops token 实测两接口 200

### 供应链（P1，独立审核 N1）

- 生产依赖官方 registry 审计 **54 项（3C/26H）→ critical 0、high 2（均为修复版未发布的例外）**：catalog 升级 vue 3.5.42 / axios 1.20 / tiptap 3.30.5 / dompurify 3.4.16；overrides 钉传递依赖 tinypool/seroval/prosemirror-view/undici/svgo/fast-uri/js-yaml/brace-expansion/glob/source-map-js/qs/postcss-selector-parser
- **CI 新增两道硬门禁**：`pnpm-audit-gate.mjs`（官方源审计，critical/high 未处置即失败，例外须登记理由与到期日）+ `check-bundle-size.mjs`（gzip 总量预算 3.5MB + ts/css/html worker 禁入清单）

### HTTP 可靠性（P1，独立审核 N3 + T6 遗留）

- **分层 body 读取期限**：普通 API 30s / 上传与 compose 10 分钟（ResponseController 按请求设期限，无 body 的 SSE/WebSocket 天然豁免）——慢速 body 不再能无限期占用连接；此前仅 ReadHeaderTimeout
- **`ratelimit.StopAll()` 接入 app 关停**（main cleanup 链）——v0.12.7「app 关停统一回收」声称落地

### 通知停机语义（P2，独立审核 N4）

- `Service.Stop()` 完整化：拒绝新入队 → 关队列 → worker 限时排空在途事件（5s，先于 DB 关闭）→ 幂等；删 `bizCap` 死字段；429 收敛 httpx.Fail；Lockout 清扫抽方法 + 注释归位

### 契约纠偏（P1/P2，独立审核 N2/N7）

- **`docs/providers/README.md` 按真实接口重写**：CIProvider 实为 Name/Status/Log（只读，构建由 webhook 驱动）；删除不存在的 `RegisterProvider` 注册方式，改为真实修改点清单（ciEngineFor 工厂 / handler 路由 / 配置模型 / 前端枚举）；契约测试注释与签名对齐（删虚标的 5 项契约与不存在的 BuildRef.ExternalID）
- `docs/status.md` 更新至当前版本，修复第 3 批残缺重复行与失实的「✅ 完成」标记

### Monaco 体积与 JSON 语言服务（P2，独立审核 N6 + 复核 §7.4）

- **editor.api 精确入口**：编辑器组件不再从根入口 import（根入口注册全部语言）——dist 21MB→12MB、gzip 3.2MB，**ts/css/html worker 从产物消失**；json/shell/python/ini 显式注册（v0.8.2 教训：防惰性挂载）
- **json worker 路由补齐**：此前一律回落 editor.worker，JSON 诊断/补全/JSONC 容忍项从不生效——「不报注释错误」实为诊断整体缺席

### 首页与窄屏（P3，独立审核 N9/N10/N11）

- 首页状态卡/就绪度项/功能卡改 **router-link 语义导航**（键盘可 Tab、读屏可识别）；emoji 换 Lucide SVG（@vben/icons 既有体系）
- **删除超管旧首登弹窗**（localStorage 一次性引导）——统一到常驻就绪度面板
- 管理后台 4 个宽表（通知路由 9 列等）加 `scroll.x`——窄屏表头不再逐字换行；抽屉宽度 `min(100vw, …)` 自适应

### E2E（P2/P3，独立审核 N5 + 复核 §7.5）

- **每次运行唯一临时 SQLite**（退出清理）——不再固定复用 `/tmp/e2e-custos.db` 跨运行累积；workers 串行化防 SQLite 并发写
- **新增非超管（ops）用例**：手签 JWT 注入登录态，直断言 `/home/summary` 200——**堵住「仅超管登录恰好绕过权限回归」的结构性盲区**（§7.1 即因此漏检）
- 加深 3 条浅断言：项目总览→详情页深链（v0.12.11 卖点此前无覆盖）、发布走确认抽屉+取消、配置文件页结构断言；新增 Monaco 编辑器加载用例（语言注册回归点）

### 其他

- `.gitignore` 修 `.ideae2e-server` 粘连错（107MB e2e 二进制未被忽略的根因）
- `ai-assist.vue` 错误提取收敛 extractErrMsg（v0.12.3 残留批漏网最后一处）；SSE 截断文案「字节」→「字符」；`frontend/scripts/deploy/nginx.conf` location 块安全头重申（add_header 继承规则导致静默失效）；gorm SQL 分级测试改绑实现（弱断言加固）；T6 sweep/T7 停机各补测试

## v0.12.14 (2026-10-07)

独立审核第 3 批全部完成：E2E 扩链路 / app.go 拆分 / Monaco chunk 预算 / provider 扩展契约。

### E2E 扩链路（4→8 条主链路）

- 新增 4 条：项目创建+表格行可见（v0.12.2 N1 回归）、正式环境发布按钮可达（v0.12.1 确认层级）、角色页高危摘要列（v0.12.12 U4）、配置中心列表可达（reveal 权限前置）——每条对应一次历史修复，回归防线成形

### 大文件拆分（T9）

- `app.go` 1055→323 行：跨模块桥接层（alertContextBridge / chatContextBridge / toolsBridge / chatActionBridge 约 750 行）整体搬移到 `bridge.go`——装配根只留 init/ProvideDB/ProvideModules/seedSkills
- `configs/files.vue` 1218 行：整体搬移因深度闭包不可行（多轮尝试后回退），标记为下一版本渐进拆分（状态页如实记录）

### Monaco bundle 预算（T11）

- `chunkSizeWarningLimit: 1200`——单 JS chunk 超 1.2MB 构建告警。当前超限的 ts.worker（6.9MB）为已知大块：MonacoEnvironment 按 label 路由，运行时只 fetch yaml+json 两个 worker，其余 worker chunk 存在于产物但浏览器不加载——影响构建产物体积而非运行时性能。worker external 对 Vite worker 机制无效，完整裁剪需按需入口（monaco-editor/esm/vs/editor/editor.api 缺类型声明，待上游修复）

### Provider 扩展契约（P4）

- **`docs/providers/README.md`**：六类扩展点能力矩阵（CI 引擎/Git 托管/通知渠道/DNS/观测/配置中心）、CIProvider+GitProvider 接口签名与关键行为说明、通知渠道添加四步指南、向前兼容规则（未知枚举跳过/接口嵌入默认实现）、注册方式（编译期 init）、不做清单
- **`ci/provider_contract_test.go`**：CIProvider 契约测试套件——新引擎适配器跑一遍即验证 Name 合法性/接口满足；Status/Log 行为由各引擎 fake server 测试覆盖

## v0.12.13 (2026-10-07)

独立审核第 3 批启动：主链路 E2E（Q1 最大质量短板）。

- **Playwright E2E 基建**：双 webServer（后端 go 二进制 + 临时 sqlite / vite dev 代理 /api），本地与 CI 同配置；chromium 经 npmmirror 镜像安装
- **四条主链路**：①setup/admin 初始化 + UI 账密登录（含 IM 未配置自动降级视图的兼容）；②首页运维态势五状态卡 + 系统就绪度渲染（第 2 批交付回归）；③超管默认展开就绪度、必配缺失计数可见；④管理后台四域导航切换 + URL 双参断言 + 项目总览页加载（v0.12.9/v0.12.11 回归）
- **CI 新增 e2e job**：构建后端二进制 → playwright install chromium → 跑测试，失败上传 trace 截图工件
- 前端业务测试从 3 个文件增至 5（E2E 2 个 spec + 单测 3），核心 UI 回归首次可自动发现

## v0.12.12 (2026-10-07)

独立审核第 2 批收官：角色权限页高危摘要（U4）。

- 角色表「动作集」列从全量 tag 平铺改为**摘要**：动作总数 + 高危徽标（prod 发布 / 回滚 / 密钥查看 / 终端 / 手动执行，红色）+ 「详情」入口——一眼回答"这个角色能做什么高危的事"
- 新增**权限详情抽屉**：动作按类别分组（含描述）+ 路由策略全量列表

第 2 批（核心体验）五项至此全部完成；下一步进入第 3 批（E2E / 大文件拆分 / Monaco 裁剪 / provider 契约）。

## v0.12.11 (2026-10-07)

独立审核第 2 批：项目详情聚合三环境（独立路由页）。

- **`/projects/:id` 项目详情页**：以项目为主对象聚合三环境子视角——每环境一张卡（蓝绿活跃色 / 最近发布状态·标签·时间 / 部署目标载体），逐环境"进入该环境"深链；项目总览列表的项目名可点击进入（此前详情只能从环境页抽屉打开，不可深链不可分享——独立审核 P3）
- 路由 hideInMenu（不占菜单位），环境页保留为全局横切视图

## v0.12.10 (2026-10-07)

独立审核第 2 批：系统就绪度（替代 localStorage 一次性首登引导）。

- **`GET /home/readiness`**：七项检查从真实配置状态计算——IM 扫码（可选）/ Redis 会话（可选）/ 通知群（必配）/ CI 全局（可选）/ 主机或集群（必配）/ 备份任务（可选）/ AI 中转（可选），跨模块只读投影
- **首页就绪度面板**：常驻可折叠卡片（不再一次性消失）——必配缺失红色标记 + 计数、可选缺失灰色提示；每项点击直达对应设置页（含管理后台 section/tab 深链）；超仅在有待配置项时默认展开

## v0.12.9（补记，2026-10-07）

管理后台二级导航双参深链（U1 完结，tag v0.12.9 → `74f3d4e`）：11 个域内 tab 与 URL `?section=&tab=` 双向同步、`replace` 无循环、深链含登录回跳。当时未同步 CHANGELOG，本条补记保持发布记录连续。

## v0.12.8 (2026-10-07)

独立审核第 2 批核心体验·首项：首页运维态势化。

- **新模块 `home`**：`GET /home/summary` 单点聚合——未处理告警（计数/严重数/Top5）、近 1 小时不可达主机去重数、7 天失败发布与定时任务数、14 天到期证书数、最近 5 次发布；跨模块只读投影、无敏感明细；casbin 种子 v27（admin/ops/dev 可读，自定义角色基础读集附带，v26→v27 迁移补种）
- **首页改态势页**：五个状态数字卡（异常红色高亮 + 点击直达对应页面）、待处理告警列表、最近发布列表；功能入口卡与超管首登引导保留。登录后第一眼回答"现在是否健康、哪里需要处理"（独立审核产品 P1）

## v0.12.7 (2026-10-07)

独立三视角审核（audit-report-v0.12.3-independent.md）第 1 批收尾 + 文档真相源。

### 可靠性（独立审核 T6/T7/T8）

- 限速器（Window/Lockout）周期清扫：不再访问的 key 此前永久驻留，长跑内存只增不减；构造即注册清扫，app 关停统一回收
- 业务告警改有界投递：固定 4 worker + 256 深队列，满载回 429——此前每请求裸 goroutine，突发告警会放大下游阻塞且停机不可控
- Redis 密码保存策略与其他凭据对齐：无主密钥时拒绝明文落库（无密码 Redis 可留空），不再是库内唯一明文凭据

### UI（独立审核 U1 首步）

- 管理后台 11 个平铺 tab 增四域分组锚点（身份与安全 / 通知与告警 / 交付与基础设施 / AI），tab 状态同步 URL 支持深链与刷新保持

### 文档（独立审核 D1）

- 新增 `docs/status.md` 作为唯一当前状态真相源（版本 / 稳定与实验能力 / 已知限制 / 不做清单 / 三批整改路线 / 兼容矩阵），阶段计划文档降为决策历史

### 独立审核对照

- P1 安全项 T1~T5 已于 v0.12.4 全修；本批清 T6/T7/T8 与 U1/D1 首步
- 未在本批：首页运维态势化、系统就绪度、Playwright E2E（Q1）、大文件拆分（T9/T10）、Monaco 裁剪（T11）——均属审核自评 2~4 周批次，已排入 docs/status.md 维护期路线

## v0.12.6 (2026-10-07)

生产 502 事故根因修复：跳级升级迁移断裂（v0.9.x 存量库直升 v0.12.3 启动即炸）。

### 根因与修复

- **根因**：生产库停留在 v0.9.x（无版本表、无 v0.10+ 引入的表），直升 v0.12.3 时增量迁移从 0001 全部重放——`0005` 对从未建过的 `ai_conversations` 表做 `ALTER ... ADD COLUMN`，`no such table` 启动失败；叠加单镜像后端进程无人守护（v0.12.5 已修），表现为持续 502
- **修复（两层）**：①存量库存在待应用迁移时，版本迁移执行后追加**全量模型补齐**（AutoMigrate）——跳级缺失的表/列按当前模型补出，此类问题一次根治（此前同类已两次咬人：N7 两张表）；②0004/0005/0007 的 `ALTER ADD COLUMN` 改带 `HasTable`/`HasColumn` 守卫的 Go 钩子（SQLite 无 `IF NOT EXISTS`，补齐先行后裸 ALTER 会报 duplicate column / no such table）
- **顺序关键（迭代中发现的第二层问题）**：改名类迁移（0006 key→skey）必须先于模型补齐——否则 AutoMigrate 会把改名目标当成缺列补一个无主键的普通列，`UpsertSetting` 全线崩。最终顺序：版本迁移（守卫式）→ 模型补齐；稳态启动（无待应用项）不跑补齐
- **验证**：v0.9.1 真二进制造库 → 当前版升级，本地无菌房完整复现事故（修复前逐字节同款报错）→ 修复后 healthz 200、0001~0010 全应用、`platform_settings.skey` 为主键且数据保留、`ai_conversations` 建出、稳态二启正常；v0.11.1 时代库路径无回归
- 回归测试：`TestJumpUpgradeFromPreMigrationEra` 与 `TestJumpUpgradeExistingTableMissingColumn`

## v0.12.5 (2026-10-07)

生产 502 事故响应：单镜像后端进程加守护。

- **start-backend.sh 守护循环**：后端此前是 nginx 入口脚本拉起的 fire-and-forget 子进程——nginx 是 PID 1，后端一旦崩溃（启动失败 / 运行期 panic / sqlite 锁竞争）没有任何东西重启它，nginx 继续运行、站点持续 502 且无可见性。改为异常退出 2s 后自动重启，崩溃原因随重启持续输出到 docker logs（自愈 + 可诊断）。本地已验证崩溃两次自愈并放行 nginx
- 排查记录：v0.11.1 老库 → v0.12.3/v0.12.4 升级路径在本地完整复现（占位库 + 增量迁移 0007~0010 + 种子 v26），启动正常——生产崩溃属环境/数据特有，需后端日志定位（见事故通报）

## v0.12.4 (2026-10-07)

v0.12.3 复核批：更正一条虚假声称（gorm 日志）+ 独立审核 T1~T5 加固 + 前端残留清理。

### 更正（v0.12.3 的 gorm 日志条目为虚假声称）

- v0.12.3 声称「gorm 日志按级别分流」——实际分流写在 gorm 永不调用的 `Write` 方法上（探针实测失败 SQL 下 Write 调用 0 次），且 SQL Trace 行本身无级别标记可嗅探，**SQL 错误仍全落 INFO**。本版改为完整实现 `logger.Interface`：Trace 直连 zap（错误→Error、慢查询→Warn、正常→Info），分级纯函数配测试

### 安全（独立审核 T1~T5）

- **T4** AgileConfig 客户端默认校验 TLS（原无条件 `InsecureSkipVerify`，通道携带配置中心 admin 凭据）；自签内网显式豁免 `CUSTOS_AGILE_INSECURE_TLS=1`
- **T1** `http.Server` 补 `ReadHeaderTimeout=10s`/`IdleTimeout=3m`（Slowloris）；不设 Read/WriteTimeout——会拦腰掐断 SSE 流式响应
- **T3** 500 响应不再回显 `err.Error()`（表名/驱动/路径外泄面），详情落服务端日志
- **T5** 基础安全响应头（gin 中间件 + 三份 nginx：nosniff/SAMEORIGIN/Referrer-Policy；HSTS 留待 TLS 终止处）
- **T2** 全局请求体上限兜底（默认 2MB，SFTP 上传/compose 部署按路由放宽）

### 测试（复核指出的空转测试）

- `deNarrowPolicies` 提为包级函数；原 `TestDeNarrowDoesNotResurrectDeleted` 是空转测试（被测函数是局部闭包且从未被调用），改为四场景直测

### 前端（连续三轮在表的残留清理）

- Monaco 全局装配抽共享模块单次执行（无 schema 的实例不再清掉其他编辑器的 compose 补全）；`extractErrMsg` 工具化收敛 19 处手写；chat 裸 fetch 补 401 刷新（会话过期不再停在登录假象）；SSE 日志显示视图封顶 64KB（高频日志重渲染有界）；深色终端色集中 `--custos-term-bg`；拖拽 selfHeal 正常收尾摘监听；新增 `use-paged-list-polling` 行为测试（active 停表契约）

## v0.12.3 (2026-10-06)

v0.12.2 复核批：补上上轮遗漏的存量库迁移 P0，收掉 guest 测试槽位越权链，AAD 落档。

### 数据库（P0，上轮复核遗漏项）

- **存量库缺表迁移**：`alert_events`（v0.12.1）与 `k3s_clusters`（v0.12.0）此前只注册进 AutoMigrate——新库无感，但升级自 ≤v0.11.1 的存量库走增量迁移路径（不跑 AutoMigrate），两张表永不建出：告警历史页 500、k3s 集群管理整块不可用、k3s 部署目标保存报 no such table。补 `0009`/`0010` 占位迁移 + Go 钩子建表（项目既有范式）；回归测试模拟存量库形态（servers 表 + 版本表到 0008）验证升级路径建表成功

### 安全

- **guest 收掉 `/slots` 写权**（种子 v26）：IM 扫码 JIT 自注册即得 guest 账号，原授权等于「组织内任意扫码者可占用/重建任意项目的测试环境」；guest 保留只读。迁移沿用 v25 机制并修正两处：仅当确实移除了旧条目才补新（不复活管理员人为删除的策略）；收权块移出策略迭代循环
- AAD 口径修正与收尾：绑定字段数实为 **18 新增 / 20 字段**（上轮 CHANGELOG 写 15）；`registries.credential` 与 `projects.ci_token` 两处跨模块字段标识收敛到 `pkg/crypto` 共享常量（消灭双真相源）；Decrypt 双路径均失败时报 AAD 路径错误（区分「字段错配」与「主密钥不符」）。**范围如实声明**：行级绑定未做（同字段跨行互换仍可解）、保护仅对新密文生效、新密文旧二进制解不开（详见 SECURITY.md 回滚约束）

### 工程 / 前端

- gorm 日志按级别分流：`[error]`→Error、`[warn]`→Warn（此前 SQL 错误全被降级为 INFO）
- 终端授权弹窗：加载成功前禁用确定按钮——防「GET 失败 + 误点 = 静默清空该主机全部终端授权」；账号列表拉取补 catch
- 终端全屏高度 94vh→100%（矮屏末行不再溢出）；分页轮询宿主不可见时直接停表；cron 运行详情 2s 轮询补抽屉开关判定；ci 工具输出 8000 字节截断改 rune 安全

## v0.12.2 (2026-10-06)

v0.12.1 修复复核（reverify）批：修复上一批引入的 3 条高危回归 + AAD 全字段化 + 残留收尾。

### 回归修复（v0.12.1 引入）

- **N1（P0）**：清空部署目标落 `server_id=0` 零行——校验循环跳过但事务循环照写，击穿 release/configs/slots/canary 四处存在性守卫；顺带修复 k3s 目标被「服务器 0 不存在」误杀（校验按 runtime 分流到 k3s_clusters）
- **N2（P0）**：服务异常退出码为 0，`restart=on-failure` 不拉起（静默死亡）——显式 cleanup+Sync 后 `os.Exit(1)`
- **N3（P1）**：项目范围守卫 fail-open——dev 收掉告警 DELETE（种子 v25，迁移先删旧策略再补新）；dev/guest+自定义角色混挂时显式项目行优先（不再被 dev 头衔绕过），纯 dev 行为不变

### 安全

- **N4**：GCM AAD 从单字段扩到全部 15 个凭据字段成对绑定（webhook/SMTP/O2/Redis/IM/AI key/AgileConfig/k3s kubeconfig/证书/CI token/备份口令/registry 凭据），存量密文经回退分支兼容、下次保存升级

### 健壮性

- backup 拉流 io.Pipe 读端失败 CloseWithError（goroutine+SFTP 连接泄漏）；zapRecovery 补堆栈；certs 手动签发与调度扫描互斥（os.Setenv 竞态全覆盖）

### 工程 / 前端

- gorm 慢查询/错误日志走 zap（消灭标准库 log 并存）；strx 补测试（0%→覆盖 Truncate/NormalizeName）；剩余 5 处字节截断委托 rune 安全实现（含审计点名的中文告警正文）
- 终端全屏弹窗 CSS 选择器修正（此前为死代码）；ai-assistant 监听卸载与拖拽 userSelect 自愈；分页轮询补宿主可见性判定（关抽屉即停）；告警策略防抖 timer 清理；终端 ACL 加载失败不残留上一台勾选

## v0.12.1 (2026-10-06)

对 v0.12.0 三视角审核报告（安全 6 严重 + 中等 + 前端/UI/产品项）的全量修复。

### 安全（严重）

- **终端越权（hotfix）**：`/servers/:id/terminal` 不再走 casbin 通配裁决（keyMatch 前缀语义下 `/servers/*` 会放行 dev/ops/自定义角色）；改为「内置 admin 角色 + 登录名逐主机精确 ACL」双 gate（中间件 + handler 兜底），M6 细粒度终端授权从「能编辑不生效」变为真实生效
- **cron 命令注入（hotfix）**：validateCarrier 的 shell/python × compose 分支提前 return 旁路了 svcRe 白名单——删除旁路；buildCommand 的 domain/service/composeFile/hostScriptPath 全量 shellQuote；ActiveDomainFor 兜底分支不再原样回吐用户输入
- **configs 命令注入（hotfix）**：SIGHUP / restart 生效目标加字符白名单（`%q` 不是 shell 转义，`$(...)` 与反引号在双引号内照常展开）；两处命令拼装引号化（新增 `pkg/shellx.Quote` 共享实现）
- **backup 命令注入（hotfix）**：RemotePath 绝对路径 + 安全字符白名单；tar 命令 parent/base 引号化；内层 tar 条目名 `..` 校验（不再依赖 filepath.Join 的清理语义）
- **SSH 输出数据竞争**：outBuf/streamBuf 读写全量持锁（x/crypto/ssh 双 goroutine 并发 Write + 超时路径先读后写）；cron onChunk 串行化
- **registry logout 时序**：prod 蓝绿发布的 docker login/logout 移入后台任务（此前 Execute 立即返回触发 defer logout，私有 registry 的 prod 发布每次 401）

### 安全（中等）

- slots 全组补项目范围守卫；observ 告警删除/列表校验项目归属（dev 不再看/删全部项目告警）
- 超管禁用/降级即时生效（IsAdmin 每请求查库，不再有 30 分钟全权真空期）
- gitee webhook 密码 sha256 哈希存储（迁移 0008 存量哈希化，旧明文兼容比对）；Redis 密码 AES 加密落库
- AI 中转 endpoint url.Parse 结构校验 + 禁私网/回环（SSRF 防护，`CUSTOS_AI_ALLOW_PRIVATE_ENDPOINT=1` 豁免内网自建 LLM）；http.DefaultClient 换带超时 client
- IM 渠道判定按 URL Host（子串包含可被查询参数伪造）；O2 org/stream 标识符白名单；certs 证书/私钥路径白名单（此前零校验）
- 服务器凭据密文加 GCM AAD 字段绑定（跨字段互换失效，旧密文回退兼容）；observ webhook token 常量时间比对；`/auth/refresh` 补限速；IM 扫码 JIT 注册可用 `CUSTOS_IM_JIT_REGISTER=0` 关闭
- `ON CONFLICT` 原生 SQL 全部换 `identity.UpsertSetting`（gorm 方言适配，MySQL 下不再语法错误）

### 前端

- 运行历史抽屉打开即加载（watch 补 open/jobId/nonce 触发，nonce 死 prop 修复）
- 权限矩阵删行 `splice(index, 1)`（单参删一片）；`footer-only-close` 假 prop → `:footer="null"`；SMTP 群测试按钮按渠道放开；AI contextWindow 清空生效；告警模板静默期可编辑；部署目标可清空（后端 SaveTargets 支持 0=清空）
- 资源泄漏：右键菜单监听卸载 / 终端 ticket 竞态 / 回放 Blob URL / Monaco model / 孤儿日志面板
- 确认层级：正式环境发布（回显蓝绿切色）/ 按文件重建容器 / compose 部署与导入覆盖 / 清空 O2 地址全部加确认；分组删除禁用态改 tooltip 说明
- 卸载观测组件按台如实报告成败；5 处 load 补 catch；文件导入先判大小；下载先占窗再跳转（防弹窗拦截）；中文输入法回车不误发；渲染预览 400ms 防抖；Monaco 高亮跟随文件切换

### 产品 / UI

- **告警事件落库**：新增 `alert_events` 表与「告警历史」页（级别/状态过滤 + 手动标记处理 + 详情）——「告警→诊断→通知」主线的历史回溯断点补齐
- **通知投递记录读取端**：`GET /notify/records` + 告警历史页 admin 页签（排障「为什么没收到通知」不再连库）
- **项目总览页**：新增 `/projects` 入口（三环境状态横看：版本/状态/时间/活跃色）；环境页加「当前版本 / 状态 / 上次发布 / 活跃色」列（后端批量 enrich，无 N+1）
- 首页重写（过期文案/swarm 清除，卡片可点直达）；登录页 IM 未配置自动降级账密表单；管理后台副标题与「配置拉取」tab 更名；cron 菜单 order 冲突修复
- AI 对话独立路由**有意不做**：维持用户定调（功能全收悬浮抽屉）

### 工程化

- 游离后台 goroutine 收敛到 `jobs.GoSafe`（recover + 结构化日志：发布/备份/任务通知等 5 处高危）
- 重复实现清理：`shellQuote` ×2 / `EventNotifier` ×6 / `min` ×2 收敛；`strx.Truncate` 升级 rune 安全并承接本地拷贝
- **viper 移除**：config 换纯 os.Getenv + 默认值表（能力全部闲置却拖入 30+ 间接包）
- notify 聚合清扫 goroutine 可停止（wire cleanup 链）；CI 新增 wire 生成物新鲜度门禁（wire_gen 完整可再生成）
- panic 恢复走 zap（不再明文堆栈直写 stderr）；main.go 服务错误经 channel 回主流程（Fatalf 不再跳过 defer）；certs 续期串行化（os.Setenv 竞态）+ 到期告警按天去重；pullCache 惰性清扫

## v0.12.0 (2026-10-06)

P8 收官：k3s 转正（双轨载体 / 蓝绿=rollout / 灰度=canary annotation / 观测栈 DaemonSet）。

## v0.11.1 (2026-10-06)

P8-M1 安全硬化 + M2 体验尾巴（JWT fail-fast / MySQL+PG 双库冒烟 / 终端审计下载 / cron 列表轮询）。

## v0.11.0 (2026-10-06)

P7 收官：权限专项（perm 三表 + 动作目录 + handler gate）、业务告警 API、告警 AI 分析（五工具 Context Pack）、AI 助手三场景、上下文窗口管理。

## v0.10.0 (2026-10-05)

P6 AI 主线 + 配置双形态 + 生态适配：AI 成为主线功能。

### AI 对话（M1）

- SSE 流式对话（通用/平台上下文双模式），全平台实时数据自动注入（项目/主机/构建/发布/事件/cron/配置元信息）
- 多模态附件（图片 image_url、文本类并入正文，≤3 个/条，单文件 ≤4MB）
- 会话管理：惰性创建（空对话不落库）、软删除留档、管理员按用户查看、切换页面对话不中断
- AI 中转层：OpenAI 兼容协议、端点/密钥/模型可配、DeepSeek 真机验证
- 安全边界（全角色一致）：只读助手绝不执行变更（管理员也不行）、权限边界声明+越权拒答、杜绝经 AI 安装 skill/MCP、注入免疫

### MCP Server（M2）

- 官方 Go SDK（modelcontextprotocol/go-sdk v1.8.0），Streamable HTTP 单端点
- 8 个只读工具：list_servers/projects/builds/releases/cron_runs/cron_jobs/configs/containers
- 应用级 token 签发（admin/dev 角色）、调用审计、限速

### Skill 声明式技能（M3 前半）

- ai_skills 表：Prompt 模板 `{{q}}` 占位符 + Runbook，纯 prompt 注入无代码执行
- `/` 命令面板（Claude Code 风格）：↑↓ 选择、Tab/Enter 确认、Esc 关闭
- 管理后台「AI 技能」tab：技能 CRUD + 角色白名单
- 内置种子技能：troubleshoot（结构化排查）、release-check（发布体检）

### 对话内工具调用（M3 后半）

- Function Calling：模型可自动调用平台只读工具（与 MCP tools 同源），结果回填续答
- 流式 tool_calls 增量聚合（按 index 分片聚合 id/name/arguments）
- 上游不支持 function calling 时自动降级（HTTP 400 → 去工具重试）
- 工具调用留痕（ChatMessage.Tools JSON 审计）+ 前端 🔧 标签实时渲染

### NL→操作建议卡（M4）

- AI 只生成意图（容器重启/cron 触发/配置下发），操作建议卡引导用户到平台页面自行操作
- 不执行任何变更（不论用户身份，管理员也不行）——产品红线

### 配置拉取 API（M5）

- `GET /api/config/{app}/{env}`：按应用×环境深合并（yaml/json/toml 结构合并，env/ini key 平铺，冲突报错）
- 应用级 token（pull_ 前缀，sha256 落库，限 app×env 范围）
- 聚合指纹 X-Config-Version/ETag + If-None-Match 304 + 60s 短缓存（鉴权在缓存前）
- 管理后台「配置中心」tab：凭证签发 + AgileConfig 连接 + 手动同步/对账

### AgileConfig 共存（M7）

- 一套 UI：配置文件是唯一真相源与编辑入口，AgileConfig 是纯后端通道
- env/ini 文件「下发」时自动同步到 AgileConfig（已配置才触发，异步不阻塞）
- 对账三态漂移报告（missing/extra/drifted），告警不自动覆盖
- 合并视图：项目×环境聚合最终生效配置（JSON/YAML 只读预览）
- SDK 热更真机验证：WebSocket 推送 40s 内 OnChange 回调触发

### gitee + Jenkins 生态适配（M8）

- GitProvider/CIProvider 双接口抽象：gitea（既有）/ gitee（API v5）git 适配 + Jenkins CI 状态查询
- 存量 gitea 项目零扰动（回归验证通过）
- gitee tag push → 平台 Build 记录 → Jenkins 构建 → 状态回读 → tag 发布

### 通知渠道泛化（M9）

- SMTP 邮件通知：多收件人群发、465 端口隐式 TLS 支持、markdown 降级纯文本
- 通知规则路由：一个事件可路由到多个通知群（webhook + SMTP 混合）
- Telegram 已移除（国内不用，用户定调）

### 全站 AI 悬浮助手（M10）

- 悬浮球（自绘 SVG 机器人：渐变+天线+呼吸/回答动画）→ 悬浮面板（无遮罩、拖拽调宽高并记忆）
- 挂 App 根：路由切换对话不中断（流式中切页输出继续）
- 页面上下文感知：发送携带当前路由，AI 主动调工具取当前页数据
- 快捷提问卡片 + 对话历史子视图（admin 筛选/软删留档）
- 全功能与独立页面同源（ChatPanel 组件化共用）

### 堡垒机补齐（M6）

- 终端权限按主机细粒度：casbin 用户级策略（登录名级授权，admin 角色通配不受影响）
- 终端会话审计回放：asciinema v2 cast 录制（带时序帧）、旧 .log 即时转单帧兼容、网页回放
- 版本化 migration：schema_migrations + embed 顺序迁移文件，新库 AutoMigrate 快速起步、存量库走版本化增量

### 基础设施

- 版本化 migration（开源化前置）
- usePanel composable 沉淀
- rbac 种子 v19（终端审计/授权/配置中心/AI 技能等资源点）
