# 多阶段：pnpm 构建主应用 → nginx 托管 + /api 反代后端
FROM node:22-alpine AS build
WORKDIR /src
# corepack 下载 pnpm 本体也走国内镜像（默认 npmjs.org 在无外网环境会永久挂起）
ENV COREPACK_NPM_REGISTRY=https://registry.npmmirror.com \
    COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable \
    && pnpm config set registry https://registry.npmmirror.com
COPY pnpm-lock.yaml pnpm-workspace.yaml package.json ./
COPY apps/web-antd/package.json apps/web-antd/
# vben monorepo 依赖声明分散，先拷全部 package.json 再装（利用层缓存）
COPY scripts ./scripts
RUN find . -name package.json -not -path "*/node_modules/*" | xargs cat >/dev/null
COPY . .
RUN pnpm install --frozen-lockfile
RUN pnpm build:antd

FROM nginx:1.27-alpine
COPY apps/web-antd/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/apps/web-antd/dist /usr/share/nginx/html
EXPOSE 80
