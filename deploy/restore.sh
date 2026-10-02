#!/bin/sh
# CustosMachina 一键恢复脚本（P5 M2，docs/RESTORE.md）
#
# 用法（空机或原机恢复均可，宿主机上执行）：
#   ./restore.sh <备份产物.tar.gz> <备份口令> [数据目录]
#
# 前置：仅依赖 docker 与 openssl（产物口令份额格式 = openssl enc -aes-256-cbc -pbkdf2）。
# 流程：解包校验 → 口令解出 MASTER KEY → 恢复数据目录 → 打印 docker run 启动命令。
set -eu

ARTIFACT="${1:?用法: restore.sh <备份产物.tar.gz> <备份口令> [数据目录]}"
PASSPHRASE="${2:?缺少备份口令}"
DATA_DIR="${3:-./custos-data}"

command -v openssl >/dev/null || { echo "❌ 需要 openssl"; exit 1; }
command -v docker >/dev/null || { echo "⚠️  docker 未安装：本次只恢复数据目录，请自行启动容器"; }
[ -f "$ARTIFACT" ] || { echo "❌ 备份文件不存在: $ARTIFACT"; exit 1; }

WORK=$(mktemp -d /tmp/custos-restore.XXXXXX)
trap 'rm -rf "$WORK"' EXIT
echo "==> 解包 $ARTIFACT"
tar -xzf "$ARTIFACT" -C "$WORK"

[ -f "$WORK/manifest.json" ] || { echo "❌ 缺 manifest.json（非平台备份产物？）"; exit 1; }
echo "==> manifest: $(cat "$WORK/manifest.json")"

if [ -f "$WORK/keyshare.enc" ]; then
    echo "==> 用口令解密密钥份额"
    MASTER_KEY=$(openssl enc -d -aes-256-cbc -pbkdf2 -pass "pass:$PASSPHRASE" \
        -in "$WORK/keyshare.enc" \
        | sed -n 's/.*"masterKey"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    [ -n "$MASTER_KEY" ] || { echo "❌ 口令错误或份额损坏"; exit 1; }
    echo "✅ MASTER KEY 已解出（请妥善保管，不要提交到任何仓库）"
else
    MASTER_KEY=""
    echo "⚠️  产物不含密钥份额（remote_dir 类型备份）：沿用现有环境主密钥即可"
fi

if [ -d "$WORK/data" ]; then
    echo "==> 恢复数据目录 → $DATA_DIR"
    mkdir -p "$DATA_DIR"
    # cp -a 保留权限（容器内 nginx 属主）
    cp -a "$WORK"/data/. "$DATA_DIR"/
    echo "✅ 数据已恢复（$(find "$DATA_DIR" -type f | wc -l | tr -d ' ') 个文件）"
else
    echo "⚠️  产物不含 data/（remote_dir 类型）：跳过平台数据恢复"
fi

echo ""
echo "================ 恢复完成 ================"
if [ -n "$MASTER_KEY" ]; then
    cat <<EOF
启动命令（按需改镜像 tag / 端口 / 域名）：

  docker run -d --name custos-machina \\
    -p 80:80 -v custos-data:/data \\
    -e CUSTOS_SECRETS_MASTER_KEY=$MASTER_KEY \\
    ghcr.io/animacaeli/custosmachina:<版本tag>

或 docker-compose（deploy/docker-compose.yml）中设置环境变量：
  CUSTOS_SECRETS_MASTER_KEY=$MASTER_KEY

注意：
  - 数据目录若用 named volume（custos-data），请先把 $DATA_DIR 内容
    拷入卷：docker run --rm -v "$DATA_DIR":/src -v custos-data:/data alpine cp -a /src/. /data/
  - 启动后用备份前的账号密码登录验证；企微扫码等回调配置沿用原环境变量。
EOF
fi
