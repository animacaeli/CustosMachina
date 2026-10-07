# 当前状态（唯一真相源）

> 各阶段计划文档（plan-*.md）自此只作决策历史，不再承担当前状态说明。
> 更新时机：每次发版后同步本文件。

- **版本**：v0.12.18（2026-10-08）
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
- 前端测试口径（v0.12.15 补丁批核准）：vitest 67 文件/457 条（含 vben 上游单测）；E2E 5 spec/11 条主链路（登录/态势/就绪度/管理后台/项目创建/项目详情/发布取消无副作用/角色摘要/配置列表/编辑器 tokenization/ops 可读与 guest 拒绝）

## 明确不做

审批流 / 工单 / CMDB / 多租户计费 / 服务网格 / Operator / 完整 Kubernetes 生命周期托管 / AI 自动执行 / 消息队列 / 独立网关（详见 roadmap §八）。

## 维护期路线（独立审核三批整改 + v0.12.15 双报告合并批）

1. **第 1 批 默认安全**：✅ 完成（v0.12.4~v0.12.7：HTTP 超时 / body 上限 / 500 脱敏 / AgileConfig TLS / 安全响应头 / 限速清扫 / 通知有界投递 / 主密钥策略统一；v0.12.15 补齐：body 分层读取期限 / StopAll 接线 / 供应链审计门禁 critical 清零）
2. **第 2 批 核心体验**：✅ 完成（首页运维态势化 / 系统就绪度 / 管理后台四域分组+双参深链 / 项目详情聚合；v0.12.15 补齐：语义化导航 + 删旧首登弹窗 + 窄屏表格）
3. **第 3 批 可维护性**：✅ 完成（E2E 主链路含非超管与编辑器用例 / 唯一临时库 / app.go 拆分 / Monaco editor.api 精确入口（dist 21→12MB）+ bundle 硬门禁 / provider 文档与契约测试按真实接口对齐）
4. **v0.12.16 补丁批**：✅ 完成（notify send-close 竞态互斥化 / casbin 种子跳级根治 / sql 注册回归 / E2E pageerror 门禁 + guest 拒绝面 + 取消无副作用 / 文档口径核准 / release 一致性门禁）
5. **v0.12.17 补丁批**：✅ 完成（RBAC v27 精确补种 / consistency 挂入发布依赖链 / tokenization 断言检测力 / E2E 移除 CSS 注入）
6. **v0.12.18 补丁批**：✅ 完成（RBAC 种子迁移根治：显式引入版本表取代全部历史 entryLevelSeed——任意历史版本直升只补新增条目、绝不复活管理员删除的权限，seed 2~26 全版本矩阵测试锁定；R2~R7 归后续批次）

## 兼容矩阵

- 数据库：SQLite（生产实测）/ MySQL / PostgreSQL（双库冒烟通过）
- 浏览器：Chromium 系持续验证（CI E2E）；Firefox / Safari 近两年版本为手工验证范围（自动化未覆盖跨浏览器）
- 升级：任意历史版本直升最新（跳级迁移自愈，v0.12.6）；AAD 密文不可回滚到 <v0.12.2 二进制
