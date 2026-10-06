# CustosMachina

轻量级 AI DevOps 运维平台：单镜像交付、无 Agent（SSH 直连纳管主机）、compose 与 k3s 双轨部署载体、与既有生态集成而非重造（gitea / gitee / Jenkins / AgileConfig / OpenObserve）。AI 全程 advisory——只建议，绝不执行。

> **生态定位（先读）**：本平台面向国内中小团队的**自建** Git 与 CI 生态（gitea / gitee + Jenkins）。GitHub / GitLab 适配器已于 2026-10-05 评估后**主动移除**（内部平台用户权限审计复杂度是硬判据，决策记录见 `docs/roadmap.md` §六）。如果你的团队使用 GitHub：CI 模块预留了 `CIProvider` 接口扩展点（gitea / Jenkins 双实现是现成样板），欢迎按 `CONTRIBUTING.md` 贡献适配器。

## 功能总览

- **主机与终端**：agentless SSH 纳管（不装 Agent）；Web 终端堡垒机，按主机细粒度授权、会话审计回放（asciinema）
- **项目与环境**：多项目隔离，test / canary / prod 环境强语义
- **部署与发布**：双轨载体——docker-compose（SSH 主机，蓝绿 / 灰度发布，域名跟随活跃色切换）或 **k3s**（集群 API 零 SSH，蓝绿=原生 rollout、灰度=Ingress canary 权重、域名=Ingress 自动组装）；快速执行、失败重试、一键回滚
- **CI 集成**：gitea Actions 与 Jenkins 双适配（gitea / gitee webhook 驱动），构建与发布记录串联
- **配置管理**：文件管理器 UI（目录树 / 版本历史 / diff / 环境同步 / 导入导出，Monaco 多语言编辑）；与 AgileConfig 共存一套 UI（env / ini 下发自动同步）；应用侧配置拉取 API（应用级 token + ETag 短缓存）
- **任务调度**：标准 5 段 cron（未来 5 次预览）、手动触发、执行日志下载
- **观测与告警**：OpenObserve 日志 / 指标查询；node-exporter + fluent-bit 采集栈一键部署；PromQL 告警，模板化 + 项目实例化
- **统一通知路由**：企微 / 钉钉 / 飞书 webhook、SMTP；静默时段与同类聚合（Telegram 已移除：国内不用，2026-10-05 定调）
- **备份与证书**：定时备份（本地 / 对象存储）；ACME 证书自动签发续期（lego，多 DNS provider）
- **AI 能力**：SSE 流式对话（多模态、悬浮助手、页面上下文感知）、平台即 MCP Server、Skill + function calling、NL → 操作建议卡、告警 AI 诊断摘要
- **权限**：自定义角色（动作集 / 项目范围）、业务动作粒度（密钥查看 / 按环境发布 / 终端 / 手动执行独立权限）、环境隔离、项目级授权

## 部署

### 方式一：docker-compose 拉取公共镜像（推荐）

```bash
cd deploy
cp .env.example .env   # 必改：JWT 密钥；按需：主密钥、公网地址、MySQL/PostgreSQL、Redis
docker compose pull && docker compose up -d
```

镜像发布在 [ghcr.io](https://github.com/animacaeli/CustosMachina/pkgs/container/custosmachina)（`custosmachina-backend` / `custosmachina-frontend` / `custosmachina` 单镜像），随版本 tag 发布，当前仅 **linux/amd64**。锁版本可将 compose 中 `:latest` 改为具体 tag（如 `:v0.12.5`）。

### 方式二：单镜像（nginx 基座，前后端同容器，最小部署）

```bash
docker run -d -p 80:80 --name custos \
  -v custos-data:/data \
  -e CUSTOS_AUTH_JWT_SECRET=$(openssl rand -hex 32) \
  -e CUSTOS_SECRETS_MASTER_KEY=$(openssl rand -hex 32) \
  ghcr.io/animacaeli/custosmachina:v0.12.5
```

环境变量与 compose 方式一致，完整清单见 `deploy/.env.example`。

### 方式三：本地构建

```bash
cd deploy && docker compose up -d --build
```

访问 `http://<主机>`，首次启动向导创建本地超管账号；IM 扫码登录、Redis、token 有效期等全部在登录后的「管理后台」配置。

## 快速开始（开发）

### 一键启动

```bash
make setup   # 首次：安装依赖 + 启用 git hooks
make dev     # 并行启动后端(:8080) + 前端(:5666，/api 代理到后端)
```

本机 8080 被占用时换端口：`make dev HTTP_ADDR=:18080`（前端代理自动跟随）。启动前若有残留进程：`lsof -nP -iTCP:8080 -sTCP:LISTEN` 查看并清理。

### 后端（Go）

```bash
cd backend
go generate ./cmd/server   # 依赖变更后重新生成 wire 注入代码
go run ./cmd/server        # 默认 :8080，SQLite 存储 backend/data/
```

### 前端（Vue3 + antd）

```bash
cd frontend
pnpm install
pnpm dev:antd   # 主应用 apps/web-antd，dev 代理 /api → localhost:8080
```

环境变量均带 `CUSTOS_` 前缀（`CUSTOS_HTTP_ADDR`、`CUSTOS_DATABASE_DRIVER`、`CUSTOS_DATABASE_DSN`、`CUSTOS_AUTH_JWT_SECRET`…），完整清单见 `backend/internal/config/config.go`。

## 仓库结构（monorepo）

```
custos-machina/
├── backend/     # Go 后端（gin + gorm + wire 依赖注入，模块化单体）
├── frontend/    # 前端，基于 vue-vben-admin（主应用 apps/web-antd）
├── deploy/      # docker-compose、env 示例、恢复脚本
└── docs/        # 文档（roadmap 总纲 + 各阶段计划）
```

## 后端架构

模块化单体 + wire 组装：

- `cmd/server` — 入口与 wire 注入器（`wire.go` 声明，`wire_gen.go` 生成）
- `internal/app` — 组装根：聚合各模块 ProviderSet 与数据库迁移
- `internal/server` — HTTP 引擎、全局中间件、`Module` 路由注册契约
- `internal/modules/<模块>` — 业务模块，三层结构：
  - `handler.go` 路由与参数绑定（gin）
  - `service.go` 业务逻辑，依赖仓储**接口**
  - `repository.go` 接口 + gorm 实现（单测用 mock 替换）
- `internal/pkg/*` — 无业务语义的基础设施（database / jwt / httpx / crypto）

业务模块（21 个已实现，按域分组；`alerting` / `timeline` / `runtime` 为规划占位未计入）：

| 域 | 模块 |
|---|---|
| 基础 | `auth` `identity` `rbac` `setup` `health` |
| 资源与运行时 | `resources` `slots`（Web 终端） |
| 项目与交付 | `projects` `release` `canary` `ci` `k3s`（集群凭证 / 双轨载体部署） |
| 配置与任务 | `configs` `cron` |
| 观测与通知 | `observ` `notify`（`alerting` / `timeline` / `runtime` 为规划占位，未实现） |
| 生态集成 | `integration`（组件凭证 / 健康巡检） |
| 运维服务 | `backup` `certs` |
| AI | `ai`（对话 / 建议卡 / 诊断摘要） `mcp`（MCP Server） |

**新增模块三步**：实现 `server.Module` → 导出 wire `Set` → 在 `internal/app` 登记一行。

## 文档

- [docs/roadmap.md](docs/roadmap.md) — 演进总纲（P1~P8 全量交付，功能线收官）
- 各阶段计划：[P2 资源管理](docs/plan-phase2-resources.md) · [P3 环境管理](docs/plan-phase3-envs.md) · [P4 运行时](docs/plan-phase4-runtime.md) · [P5 服务化](docs/plan-phase5-services.md) · [P6 AI 主线](docs/plan-phase6-ai-mainline.md) · [P7 权限 + AI 深化](docs/plan-phase7-permissions-and-ai.md) · [P8 硬化 + k3s](docs/plan-phase8-hardening-and-k3s.md)
- [docs/research-k3s-spike.md](docs/research-k3s-spike.md) — k3s 载体排障与环境事实参考
- [docs/deploy-conventions.md](docs/deploy-conventions.md) — 部署与运维约定
- [docs/plan.md](docs/plan.md) — 第一阶段总纲（历史存档）

## 状态

**P1~P8 全量交付（当前 v0.12.5）——功能开发收官转维护期；v0.12.1~v0.12.5 为三视角安全审计、三轮复核与生产事故响应的修复批次。** 第七阶段：权限体系与 AI 深化（告警 AI 分析 / 编辑器 AI 助手 / 上下文管理 / 业务告警 API）；第八阶段：安全硬化（MySQL / PostgreSQL 双方言实测）、体验尾巴、k3s 基建演进（双轨部署载体 + 观测栈 DaemonSet），见 [P7](docs/plan-phase7-permissions-and-ai.md) / [P8 计划](docs/plan-phase8-hardening-and-k3s.md)。
