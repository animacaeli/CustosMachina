#!/bin/sh
# nginx 容器入口脚本：在 nginx 启动前拉起 CustosMachina 后端进程。
# 后端监听 127.0.0.1:8080（CUSTOS_HTTP_ADDR 可覆盖），仅容器内可见。
set -e

echo "Starting CustosMachina backend..."
custos-server &
# 等后端就绪再放行 nginx（最多 15s）
i=0
until wget -q -O /dev/null http://127.0.0.1:8080/api/healthz 2>/dev/null; do
  i=$((i + 1))
  [ "$i" -ge 30 ] && echo "backend not ready after 15s, nginx starts anyway" && exit 0
  sleep 0.5
done
echo "Backend ready."
exit 0
