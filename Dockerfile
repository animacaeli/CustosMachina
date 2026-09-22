# CustosMachina 单镜像：nginx 基座
#   - nginx 托管前端静态资源（80 端口）+ /api 反代容器内后端进程（:8080）
#   - 后端为静态 Go 二进制，由 nginx 镜像的 docker-entrypoint.d 拉起
# 使用：docker build -t custos-machina . && docker run -p 80:80 -e CUSTOS_AUTH_JWT_SECRET=... custos-machina

# --- 阶段 1：前端构建 ---
FROM node:22-alpine AS frontend
ENV COREPACK_NPM_REGISTRY=https://registry.npmmirror.com \
    COREPACK_ENABLE_DOWNLOAD_PROMPT=0
WORKDIR /src
COPY frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml frontend/package.json ./
COPY frontend/apps/web-antd/package.json apps/web-antd/
COPY frontend/internal internal
COPY frontend/scripts scripts
COPY frontend .
RUN corepack enable && pnpm install --frozen-lockfile \
    && pnpm build:antd

# --- 阶段 2：后端构建（静态二进制）---
FROM golang:1.26-alpine AS backend
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend .
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

# --- 运行时：nginx 基座 ---
FROM nginx:1.27-alpine

COPY --from=backend /out/server /usr/local/bin/custos-server
COPY --from=frontend /src/apps/web-antd/dist /usr/share/nginx/html
COPY deploy/nginx-single.conf /etc/nginx/conf.d/default.conf

# nginx 镜像约定：docker-entrypoint.d 下的脚本在 nginx 启动前执行（拉起后端）
COPY deploy/start-backend.sh /docker-entrypoint.d/90-start-backend.sh
RUN chmod +x /docker-entrypoint.d/90-start-backend.sh /usr/local/bin/custos-server \
    && mkdir -p /data && chown nginx /data

ENV CUSTOS_HTTP_ADDR=:8080 CUSTOS_DATABASE_DSN=/data/custos.db
VOLUME /data
EXPOSE 80
