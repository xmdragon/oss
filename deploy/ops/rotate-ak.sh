#!/usr/bin/env bash
# AK 轮换：建新 AK + 停用旧 AK（保留 N 小时后再完全删除）
# 用法:
#   rotate-ak.sh                      # 自动生成新 SK，AK 名取 DESKTOP_ACCESS_KEY-<ts>
#   rotate-ak.sh <new-ak> <new-sk>    # 指定新 AK/SK
set -euo pipefail

ENV_FILE="/opt/oss/.env"
# shellcheck disable=SC1090
source "$ENV_FILE"
: "${MINIO_ROOT_USER:?}"; : "${MINIO_ROOT_PASSWORD:?}"
: "${DESKTOP_ACCESS_KEY:?}"; : "${BUCKET_NAME:?}"

NEW_AK="${1:-ozon-desktop-$(date +%Y%m%d)}"
NEW_SK="${2:-$(openssl rand -hex 24)}"

mc alias set local "http://127.0.0.1:9000" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null

echo "==> 建新 AK: $NEW_AK"
if mc admin user info local "$NEW_AK" >/dev/null 2>&1; then
    echo "ERROR: $NEW_AK 已存在，换个名字"; exit 1
fi
mc admin user add local "$NEW_AK" "$NEW_SK"
mc admin policy attach local put-only --user "$NEW_AK"

echo "==> 停用旧 AK: $DESKTOP_ACCESS_KEY"
mc admin user disable local "$DESKTOP_ACCESS_KEY" || true

cat <<SUMMARY

================================================================
  轮换完成（旧 AK 已 disable，未删除）
================================================================
  新 AK : $NEW_AK
  新 SK : $NEW_SK

  下一步：
    1. 桌面端构建时用这两个值替换旧的，发版
    2. 确认所有客户端都升到新版（观察一周 or 你方法）
    3. 彻底删除旧 AK:
       mc admin user remove local $DESKTOP_ACCESS_KEY
    4. 更新 $ENV_FILE 里的 DESKTOP_ACCESS_KEY / DESKTOP_SECRET_KEY
       （改完 systemctl restart minio 不必，因为子账号不在 root env）
================================================================
SUMMARY
