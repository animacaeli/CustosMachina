# CustosMachina monorepo 开发入口
# 依赖：go >= 1.26 / node >= 20 / pnpm >= 9
.PHONY: dev dev-backend dev-frontend setup wire test test-backend test-frontend \
        lint lint-backend lint-frontend vuln build build-backend build-frontend \
        hooks clean help

FRONTEND_DIR := frontend
BACKEND_DIR  := backend

## 一键本地开发：并行启动后端(:8080) + 前端(:5666，代理 /api)
dev:
	@trap 'kill 0' INT TERM; \
	$(MAKE) -j2 dev-backend dev-frontend

dev-backend:
	@cd $(BACKEND_DIR) && go run ./cmd/server

dev-frontend:
	@cd $(FRONTEND_DIR) && pnpm dev:antd

## 首次安装依赖 + 启用 git hooks
setup:
	@cd $(FRONTEND_DIR) && pnpm install
	@git config core.hooksPath .githooks
	@echo "✅ 依赖安装完成，git hooks 已启用（.githooks）"

## 后端：wire 依赖注入代码再生成
wire:
	@cd $(BACKEND_DIR) && go generate ./cmd/server && gofmt -l -w .

test: test-backend test-frontend

test-backend:
	@cd $(BACKEND_DIR) && go test -race -count=1 ./...

test-frontend:
	@cd $(FRONTEND_DIR) && npx vitest run --dom --exclude '**/ui-kit/form-ui/**'

lint: lint-backend lint-frontend

lint-backend:
	@cd $(BACKEND_DIR) && \
	  out=$$(gofmt -l .) && [ -z "$$out" ] || (echo "gofmt 未通过：$$out"; exit 1); \
	  go vet ./...

lint-frontend:
	@cd $(FRONTEND_DIR) && pnpm lint

## 漏洞检测：govulncheck（Go 官方漏洞库）
vuln:
	@cd $(BACKEND_DIR) && go run golang.org/x/vuln/cmd/govulncheck@latest ./...

build: build-backend build-frontend

build-backend:
	@cd $(BACKEND_DIR) && CGO_ENABLED=0 go build -o bin/server ./cmd/server

build-frontend:
	@cd $(FRONTEND_DIR) && pnpm build:antd

clean:
	@cd $(FRONTEND_DIR) && pnpm clean || true
	@rm -rf $(BACKEND_DIR)/bin $(BACKEND_DIR)/data

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //'
