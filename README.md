# CustosMachina

轻量级 AI DevOps 运维平台：统一控制台 + 告警 AI 诊断（仅建议，不执行）。

基于已有轻量级运维基建（Gitea / OpenObserve / AgileConfig / Yearning / K3s）做集成增强，不重写任何组件。总体设计见 [docs/plan.md](docs/plan.md)。

## 仓库结构（monorepo，决策 D13）

```
custos-machina/
├── backend/     # Go 后端（gin + gorm + wire 依赖注入，模块化单体）
├── frontend/    # 前端，基于 vue-vben-admin（主应用 apps/web-antd）
├── deploy/      # docker-compose、env 示例、一键部署
└── docs/        # 文档（含总纲 plan.md）
```

## 部署（docker-compose 一键拉起）

### 方式一：拉取公共镜像（推荐）

```bash
cd deploy
cp .env.example .env   # 修改 JWT 密钥、主密钥、公网地址、按需开 MySQL/Redis
docker compose pull && docker compose up -d
```

镜像随版本 tag 发布在 ghcr.io，支持 amd64 / arm64。锁版本可将 compose 中 `:latest` 改为具体 tag。

**单镜像模式**（nginx 基座，前后端同一容器，适合最小部署）：

```bash
docker run -d -p 80:80 --name custos \
  -v custos-data:/data \
  -e CUSTOS_AUTH_JWT_SECRET=$(openssl rand -hex 32) \
  -e CUSTOS_SECRETS_MASTER_KEY=$(openssl rand -hex 32) \
  -e CUSTOS_IM_PUBLIC_URL=https://你的域名 \
  -e CUSTOS_IM_FRONTEND_URL=https://你的域名 \
  ghcr.io/animacaeli/custosmachina:v0.2.0
```

环境变量与 compose 方式一致（见 `deploy/.env.example`）。

### 方式二：本地构建

```bash
cd deploy && docker compose up -d --build
```

访问 `http://<主机>`，首次启动自动进入初始化向导（IM 提供商三选一 → Redis（可跳过）→ 本地超管）。
环境变量（MySQL/PostgreSQL 切库、Redis、JWT 密钥等）见 `deploy/.env.example`。

## 快速开始（开发）

### 后端

```bash
cd backend
go generate ./cmd/server   # 依赖变更后重新生成 wire 注入代码
go run ./cmd/server        # 默认 :8080，SQLite 存储 backend/data/
```

### 一键启动（make dev）

```bash
make setup   # 首次：安装依赖 + 启用 git hooks
make dev     # 并行启动后端(:8080) + 前端(:5666，/api 代理到后端)
```

本机 8080 被其他服务占用时换端口：`make dev HTTP_ADDR=:18080`（前端代理自动跟随）。
启动前若有残留进程：`lsof -nP -iTCP:8080 -sTCP:LISTEN` 查看并清理。

环境变量均带 `CUSTOS_` 前缀（`CUSTOS_HTTP_ADDR`、`CUSTOS_DATABASE_DRIVER`、`CUSTOS_DATABASE_DSN`、`CUSTOS_AUTH_JWT_SECRET`…），完整清单见 `internal/config/config.go`。

### 前端

```bash
cd frontend
pnpm install
pnpm dev:antd   # 主应用 apps/web-antd，dev 代理 /api → localhost:8080
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

模块规划：auth、identity、setup、health 已有骨架；runtime / integration / alerting / ai / notify / timeline 为批次 2~4 预留（各目录 doc.go 说明职责）。

**新增模块三步**：实现 `server.Module` → 导出 wire `Set` → 在 `internal/app` 登记一行。

## 状态

批次 1（骨架 + 权限管理）进行中，进度见 [docs/plan.md 第 10 章](docs/plan.md)。
