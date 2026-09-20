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

## 快速开始（开发）

### 后端

```bash
cd backend
go generate ./cmd/server   # 依赖变更后重新生成 wire 注入代码
go run ./cmd/server        # 默认 :8080，SQLite 存储 backend/data/
```

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
