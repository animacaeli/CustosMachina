# 当前状态（唯一真相源）

> 各阶段计划文档（plan-*.md）自此只作决策历史，不再承担当前状态说明。
> 更新时机：每次发版后同步本文件。

- **版本**：v0.14.2（2026-11-11）
- **阶段**：P1~P8 全量交付，功能冻结，转维护期（安全加固 + 体验 + 工程债）
- **部署形态**：单镜像（nginx 前端 + 内置后端，compose/k3s 双轨部署载体）

## 稳定能力

主机纳管（agentless SSH / Web 终端 + 会话审计 / SFTP）、compose 与 k3s 双轨发布（蓝绿 / 灰度 / 回滚）、CI（gitea Actions + Jenkins，gitea/gitee webhook）、配置文件管理（版本 / diff / 下发 / 拉取 API / AgileConfig 同步）、定时任务（沙箱执行）、观测（O2 日志指标 / 告警模板化 / 告警事件留痕）、统一通知路由（企微 / 钉钉 / 飞书 / SMTP）、备份（本地 / S3）、证书（ACME lego）、三级角色权限（自定义角色 + 项目范围 + 终端主机 ACL）、AI（对话 / 告警诊断 / 建议卡——只建议不执行）。

## 实验能力（可用但未大规模验证）

k3s 载体全链路（真机 spike 通过，生产双轨并行中）、AI function calling 与 Skill、MCP Server。

## 已知限制

- 单实例假设（调度 / 限速 / 互斥均为进程内），多实例需重构
- 凭据加密为字段级 AAD 绑定：行级互换仍可解、仅对新密文生效、新密文旧二进制解不开（回滚约束，见 SECURITY.md）
- 供应链例外 2 项（braces / node-forge，修复版上游未发布，仅 dev/lint 链不进产物）——见 `frontend/scripts/pnpm-audit-allowlist.json`，到期日 2027-01-07
- 前端测试口径（v0.12.15 补丁批核准）：vitest 67 文件/457 条（含 vben 上游单测）；E2E 5 spec/12 条主链路（登录/态势/就绪度/管理后台×2/项目创建/项目详情/发布取消无副作用/角色摘要/配置列表/编辑器 tokenization/ops 可读与 guest 拒绝）

## 明确不做

审批流 / 工单 / CMDB / 多租户计费 / 服务网格 / Operator / 完整 Kubernetes 生命周期托管 / AI 自动执行 / 消息队列 / 独立网关（详见 roadmap §八）。

## 维护期路线（独立审核三批整改 + v0.12.15 双报告合并批）

1. **第 1 批 默认安全**：✅ 完成（v0.12.4~v0.12.7：HTTP 超时 / body 上限 / 500 脱敏 / AgileConfig TLS / 安全响应头 / 限速清扫 / 通知有界投递 / 主密钥策略统一；v0.12.15 补齐：body 分层读取期限 / StopAll 接线 / 供应链审计门禁 critical 清零）
2. **第 2 批 核心体验**：✅ 完成（首页运维态势化 / 系统就绪度 / 管理后台四域分组+双参深链 / 项目详情聚合；v0.12.15 补齐：语义化导航 + 删旧首登弹窗 + 窄屏表格）
3. **第 3 批 可维护性**：✅ 完成（E2E 主链路含非超管与编辑器用例 / 唯一临时库 / app.go 拆分 / Monaco editor.api 精确入口（dist 21→12MB）+ bundle 硬门禁 / provider 文档与契约测试按真实接口对齐）
4. **v0.12.16 补丁批**：✅ 完成（notify send-close 竞态互斥化 / casbin 种子跳级根治 / sql 注册回归 / E2E pageerror 门禁 + guest 拒绝面 + 取消无副作用 / 文档口径核准 / release 一致性门禁）
5. **v0.12.17 补丁批**：✅ 完成（RBAC v27 精确补种 / consistency 挂入发布依赖链 / tokenization 断言检测力 / E2E 移除 CSS 注入）
6. **v0.12.18 补丁批**：✅ 完成（RBAC 种子迁移根治：显式引入版本表取代全部历史 entryLevelSeed；后经 v0.12.19 独立复核修正为——新面直达 + 历史形态替换/废弃建模 + 清空角色不重种，见第 7 批）
7. **v0.12.19 补丁批**：✅ 完成（迁移模型补全：替换/废弃建模 + 清空角色不重种 + 真实历史 fixture；后经复核发现三处 P1 于第 8 批修正）
8. **v0.12.20 补丁批**：✅ 完成（迁移五项 P1：oldVer 数据修正 / 全表清空后重启不再重种 / 替换持久化错误上抛 / 替换可重试序 / seed 查询 fail-closed + 占位续种）
9. **v0.12.21 补丁批**：✅ 完成（Go 工具链安全补丁：1.26.6→1.26.9 + x/net v0.60.0——上游标准库新公告命中旧工具链致 CI govulncheck 红；可达漏洞清零）
10. **v0.12.22 补丁批**：✅ 完成（体验修复：管理后台内部切换不裂顶栏页签——fullPathKey 归一 + E2E 回归 12 条）
11. **v0.13.0 配置文件环境语义返工**：✅ 完成（2026-11-09 用户指出与原设计两偏差，按 2026-09-26 槽位级隔离定档返工）——目录首段 `prod/canary/test` 枚举 → **prod + 槽位名 dev1..devN**（联动项目 TestSlotCount）；canary 全面引用 prod（树无 canary 目录，合并视图/拉取读 prod）；脱敏从文件级开关改**字段级自动识别**（键名含 token/secret/pwd/password）；reveal 环境分流（测试槽位任何登录用户可看明文，正式环境需 config.reveal，均落审计）。安全收紧三项（二轮审核）：合并视图默认脱敏（曾为 reveal 旁路）、源=prod 的环境同步需 config.reveal（防 deploy 权限绕过）、多对 JSON 行逐对脱敏（曾泄漏行内敏感对）。迁移 0011：`test/devN/*→devN/*`、`test/其余→dev1/*`、canary 与 prod 一致删除 / 不一致**中止启动等人工**（fail-closed）。E2E 16 条（新增 reveal 分流全链 + UI 弹窗盲区两条）
12. **v0.14.0 维护期批次 12（五项体验/能力）**：✅ 完成（2026-11-09 用户讨论定档）——① 项目备注字段（基础弹窗/详情抽屉可编辑，总览 name 副行悬浮）；② **test 构建关联 Jenkins 日志**：`/api/slots/callback` 接收 `buildNumber`（Jenkins GWT 注入 `BUILD_NUMBER`，流水线一行改动）落 `builds.ci_ref=job#number`，日志接口有 ci_ref 直连构建号拉 console（`kind=deploy` 强制部署输出），构建抽屉 test 记录双视图「CI 构建 / 部署输出」（存量无 ci_ref 保持单视图）；③ **配置远端路径 `{env}` 占位符**：下发按 rel_path 首段渲染（canary→prod；无占位符原样=存量零影响），`FileOut.renderedPath` 统一预览，EnvSync 源路径无占位符且同主机时返回覆盖风险 warnings（此前 devN 与 prod 同主机同路径互相覆盖仅剩 .bak 兜底）；④ 历史版本「查看」改 **diff 弹窗**（左=历史 右=当前，只读；原实现把历史内容塞主编辑器可编辑可误存）；⑤ 槽位续期改**顺延** `max(now,expireAt)+时长`（原从 now 重算=提前续期反而缩短剩余时间）+ 列名「到期时间」+ 过期行附宽限截止；项目详情抽屉补**镜像仓库绑定下拉**（v0.12.24 只补了后端列）；新增拉取 API 保留端点 `GET /api/config/{app}/registry`（项目绑定 registry 地址，流水线免维护 ci.yaml 中转；凭据仍留 Jenkins credentials）。迁移 0012（守卫式加列 remark/ci_ref）。全量门禁：go test -race / vitest 457 / E2E 16（新 e2e-server 二进制）/ lint / build / 体积门禁全绿；本地 dev 浏览器实测七项全过（实测抓出并修复：漏迁移 0012 致存量库白屏、registry 解绑后下拉显示裸 0）。**接入方待办**：starveil Jenkinsfile 回调加 `buildNumber`；`prod/ci.yaml` 确认后删（平台不解析，改从 registry 端点拉地址）；starveil 远端路径改 `{env}` 模板防多环境互覆
13. **v0.14.1 compose 渲染 `{env}`（挂载路径环境隔离闭环）**：✅ 完成（2026-11-11 starveil 接入实装发现缺口）——v0.14.0 只做了配置文件下发路径的 `{env}`，**compose 宿主挂载路径无环境隔离机制**（devN 容器与 prod 挂同一配置文件，多环境隔离最后一块缺口）。`RenderComposeEnv`/`DeployComposeToImageEnv`：envSeg 非空时全文 `{env}` 替换、空段原样（存量零影响）；三处调用方显式传段——prod 发布与蓝绿恒 `prod`（canary 引用 prod 配置同源）、测试槽位传槽位名 `devN`。与配置文件 path `{env}` 同语义闭环：挂载写 `/opt/x/{env}/config.yaml` → 各环境容器各挂各段。配套接入方改造（starveil，随平台升级后推送）：compose 挂载改 `{env}`；Jenkinsfile 仓库地址改 `/api/config/{app}/registry` 端点（全 address 含 namespace，与平台 imageFor 同源，login 取 host 段）、`prod/ci.env` 瘦身为凭据两项、回调带 `buildNumber`。另修 SftpWrite 写前逐级创建父目录（`{env}` 模板路径首下发子目录不存在直接失败，生产实测抓到）。生产 47 已先升 v0.14.0（0011/0012 迁移通过）
14. **v0.14.2 配置页四项（用户讨论定档）**：✅ 完成（2026-11-11）——① **槽位目录固化**：`projects.test_slot_peak`（只增不减，迁移 0013 存量以当前槽数初始化），目录树环境层 `max(当前槽数, 峰值)`——槽数缩减后既有 dev1..peak 目录不消失等用户处理、扩容跟随；`checkSlotCap` 按峰值放宽（峰值内 devN 可继续新建/编辑）；右键「删除目录」（confirm 明示数量，`DELETE /config-files/dir` 前缀批量删含版本链，config.edit gate、空删报错防手滑）；新建/删除按 `config.edit` 权限显隐（`getAccessCodesApi` 改取 `/auth/permissions` 的 **actions**——action key 与后端 rbac.Can 同语义，原取 permissions 是 casbin path:act 形态对不上业务动作）。② **格式转换注释感知**（`convertViewWithComments` 重写）：注释随键迁移到目标格式（yaml AST `Pair.value.commentBefore`/行式格式行扫描/JSONC 栈路径），**JSON 视图=JSONC**（`//` 注释 + jsonc 高亮，保存回写照旧去注释解析）；env/ini 扁平化父注释下放首叶；边界=数组元素注释/行尾注释不迁移、往返后注释位置可能从叶子移到父键上方。③ 编辑器高度 `calc(100vh-320px)`→容器自适应（真触底；悬空与 AI 悬浮球无关纯属算式错位）。④ **历史版本双改**：「查看」抽屉内嵌 diff 区（单选=vs 当前/双选=两版本对比同一区域互斥，带关闭，不再弹窗）；**Deploy 不再落版本**（下发内容必然等于某编辑版本，deploy 快照只制造无变更对比污染版本链），存量 deploy 版本列表默认隐藏（库保留供审计直查）。全量门禁 + 浏览器两轮实测（目录固化缩容/JSONC 与 ENV 注释迁移/触底/内嵌 diff 单双选/右键删除全链路/权限显隐/保存回写注释保留）

## 兼容矩阵

- 数据库：SQLite（生产实测）/ MySQL / PostgreSQL（双库冒烟通过）
- 浏览器：Chromium 系持续验证（CI E2E）；Firefox / Safari 近两年版本为手工验证范围（自动化未覆盖跨浏览器）
- 升级：任意历史版本直升最新（跳级迁移自愈，v0.12.6）；**v0.13.0 例外**——canary 目录存在与 prod 不一致的配置时迁移 0011 中止启动并指名文件（fail-closed，人工合并/删除后重启即可）；AAD 密文不可回滚到 <v0.12.2 二进制；**v0.13.0 起旧二进制不识别 devN 首段配置路径**（降级回 <v0.13.0 前须评估配置目录形态）
