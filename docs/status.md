# 当前状态（唯一真相源）

> 各阶段计划文档（plan-*.md）自此只作决策历史，不再承担当前状态说明。
> 更新时机：每次发版后同步本文件。

- **版本**：v0.12.8（2026-10-07）
- **阶段**：P1~P8 全量交付，功能冻结，转维护期（安全加固 + 体验 + 工程债）
- **部署形态**：单镜像（nginx 前端 + 内置后端，compose/k3s 双轨部署载体）

## 稳定能力

主机纳管（agentless SSH / Web 终端 + 会话审计 / SFTP）、compose 与 k3s 双轨发布（蓝绿 / 灰度 / 回滚）、CI（gitea Actions + Jenkins，gitea/gitee webhook）、配置文件管理（版本 / diff / 下发 / 拉取 API / AgileConfig 同步）、定时任务（沙箱执行）、观测（O2 日志指标 / 告警模板化 / 告警事件留痕）、统一通知路由（企微 / 钉钉 / 飞书 / SMTP）、备份（本地 / S3）、证书（ACME lego）、三级角色权限（自定义角色 + 项目范围 + 终端主机 ACL）、AI（对话 / 告警诊断 / 建议卡——只建议不执行）。

## 实验能力（可用但未大规模验证）

k3s 载体全链路（真机 spike 通过，生产双轨并行中）、AI function calling 与 Skill、MCP Server。

## 已知限制

- 单实例假设（调度 / 限速 / 互斥均为进程内），多实例需重构
- 凭据加密为字段级 AAD 绑定：行级互换仍可解、仅对新密文生效、新密文旧二进制解不开（回滚约束，见 SECURITY.md）
- 前端业务测试仅 3 个文件（utils/composable 层），无 E2E——独立审核 Q1，最大质量短板

## 明确不做

审批流 / 工单 / CMDB / 多租户计费 / 服务网格 / Operator / 完整 Kubernetes 生命周期托管 / AI 自动执行 / 消息队列 / 独立网关（详见 roadmap §八）。

## 维护期路线（独立审核三批整改）

1. **第 1 批 默认安全**：✅ 已完成（v0.12.4~v0.12.7：HTTP 超时 / body 上限 / 500 脱敏 / AgileConfig TLS / 安全响应头 / 限速清扫 / 通知有界投递 / 主密钥策略统一）；供应链扫描（govulncheck 已在 CI；npm audit 受 npmmirror 限制待换官方源）
2. **第 2 批 核心体验（✅ 全部完成）**：首页运维态势化（✅ v0.12.8：五状态卡 + 待处理告警 + 最近发布）、系统就绪度（✅ v0.12.10：/home/readiness 七项检查 + 首页常驻可折叠面板）、管理后台四域分组（✅ v0.12.7 首步：分组锚点 + tab 深链）、项目详情聚合（✅ v0.12.11：/projects/:id 三环境卡片 + 环境深链）
3. **第 3 批 可维护性**：大文件拆分（app.go / configs/files.vue 等）、Playwright 主链路 E2E、Monaco 裁剪与 bundle 预算、provider 扩展契约文档

## 兼容矩阵

- 数据库：SQLite（生产实测）/ MySQL / PostgreSQL（双库冒烟通过）
- 浏览器：Chromium 系 / Firefox / Safari 近两年版本
- 升级：任意历史版本直升最新（跳级迁移自愈，v0.12.6）；AAD 密文不可回滚到 <v0.12.2 二进制
