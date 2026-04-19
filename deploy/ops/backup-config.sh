#!/usr/bin/env bash
# 打包部署配置（不含 bucket 内的图片数据—那些是暂存，丢了无所谓）
# 产出: /root/oss-config-<date>.tgz
set -euo pipefail

DATE=$(date +%F-%H%M)
OUT="/root/oss-config-${DATE}.tgz"

tar czf "$OUT" \
    /opt/oss/.env \
    /opt/oss/init-bucket.sh \
    /opt/oss/ops \
    /etc/caddy/Caddyfile \
    /etc/systemd/system/minio.service \
    /etc/systemd/system/caddy.service.d/ \
    2>/dev/null

ls -lh "$OUT"
echo ""
echo "下载到本地："
echo "  scp oss:$OUT ~/oss-backup/"
echo ""
echo "恢复时注意 .env 里的 AK/SK 要是当时配套的那套，"
echo "否则桌面端旧版本会失效，需要重新轮换 + 发版。"
