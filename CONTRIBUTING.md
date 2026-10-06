# 贡献指南

感谢关注 CustosMachina —— 面向国内中小团队的轻量级 AI DevOps 运维平台。

## 定位先读

平台有意收敛范围（详见 `docs/roadmap.md` 的「不做清单」）：

- **内部平台心智**：面向自建 gitea / gitee + Jenkins 生态；GitHub / GitLab 适配器已于 2026-10-05 评估后主动砍除（用户权限审计复杂度是硬判据）。如果你的团队用 GitHub，CI 模块需要你自行扩展 `CIProvider` 接口（gitea / Jenkins 双实现是现成样板）
- **轻量红线**：不引入重型组件（外部 OAuth、独立消息队列等）；开源组件级复用优先
- **AI 安全红线**：AI 只建议不执行、绝不自动安装 skill / MCP；平台数据只读工具化

## 开发环境

```bash
# 后端（Go 1.26+）
cd backend && go build ./... && go test ./...

# 前端（Node 22+ / pnpm 11）
cd frontend && pnpm install && pnpm dev:antd

# 一键（根 Makefile）
make dev
```

## 提交前检查

```bash
cd backend
gofmt -l .            # 必须为空
go vet ./...
go test -race ./...
go generate ./cmd/server && git diff --exit-code -- cmd/server/wire_gen.go  # wire 同步

cd ../frontend
pnpm lint && pnpm check:type
```

CI（`.github/workflows/ci.yml`）会完整执行以上 + govulncheck + CodeQL。

## 约定

- **模块结构**：`internal/modules/<域>` 各自 handler/service/model/wire；跨模块只读投影允许 Table 直查（见 `deploy-conventions`），不引模块间依赖
- **命令拼装**：任何用户输入进入远端 shell 必须经 `pkg/shellx.Quote`，且入参侧有白名单正则
- **新公开端点**：一律带 token 校验 + `pkg/ratelimit` 限速
- **迁移**：SQL 文件（双方言）+ 必要时 Go 钩子（`RegisterGoHook`，见 0002/0006/0008）
- **注释**：关键取舍写「为什么」——决策记录在 `docs/` 分阶段计划里，不在代码里复述过程
- **提交信息**：中文，动词开头，说明做了什么与为什么（参考 git log 既有风格）

## 报告问题

- Bug：使用 issue 模板，附版本号与日志片段（勿贴凭据）
- 安全漏洞：见 `SECURITY.md`（不要开公开 issue）
