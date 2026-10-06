#!/bin/sh
# nginx 容器入口脚本：在 nginx 启动前拉起 CustosMachina 后端进程（带守护）。
# 后端监听 127.0.0.1:8080（CUSTOS_HTTP_ADDR 可覆盖），仅容器内可见。
#
# v0.12.5：后端此前是 fire-and-forget 后台子进程——nginx 是 PID 1，后端一旦
# 崩溃（启动失败/运行期 panic）没有任何东西重启它，nginx 继续跑、站点持续
# 502 且无可见性（2026-10-06 生产事故形态）。改为守护循环：异常退出 2s 后
# 重启，崩溃原因随重启进 docker logs。
set -e

echo "Starting CustosMachina backend (supervised)..."
(
  while true; do
    custos-server || echo "[start-backend] backend exited (code $?), restarting in 2s..." >&2
    sleep 2
  done
) &

# 等后端就绪再放行 nginx（最多 15s；到点放行——静态前端与 502 错误页仍可服务）
i=0
until wget -q -O /dev/null http://127.0.0.1:8080/api/healthz 2>/dev/null; do
  i=$((i + 1))
  [ "$i" -ge 30 ] && echo "backend not ready after 15s, nginx starts anyway" && break
  sleep 0.5
done
echo "Backend ready."
exit 0
