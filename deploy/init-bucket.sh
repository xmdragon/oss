#!/usr/bin/env bash
# 首次初始化 bucket + 策略 + 生命周期
# 前置：minio 与 caddy 已 systemctl 起来，/opt/oss/.env 已配好
# 幂等：可反复执行
set -euo pipefail

ENV_FILE="/opt/oss/.env"
[ -f "$ENV_FILE" ] || { echo "ERROR: $ENV_FILE 不存在"; exit 1; }

# shellcheck disable=SC1090
set -a; source "$ENV_FILE"; set +a

: "${MINIO_ROOT_USER:?}"; : "${MINIO_ROOT_PASSWORD:?}"
: "${DESKTOP_ACCESS_KEY:?}"; : "${DESKTOP_SECRET_KEY:?}"
: "${BUCKET_NAME:?}"; : "${LIFECYCLE_EXPIRE_DAYS:?}"

ALIAS="local"

echo "==> 配置 mc alias"
mc alias set "$ALIAS" "http://127.0.0.1:9000" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null

echo "==> 等待 MinIO 就绪"
for i in {1..30}; do
    if mc admin info "$ALIAS" >/dev/null 2>&1; then break; fi
    [ "$i" = "30" ] && { echo "ERROR: MinIO 超时未就绪"; exit 1; }
    sleep 1
done

echo "==> 创建 bucket: $BUCKET_NAME"
if mc ls "$ALIAS/$BUCKET_NAME" >/dev/null 2>&1; then
    echo "   已存在，跳过"
else
    mc mb "$ALIAS/$BUCKET_NAME"
    echo "   ✓ Bucket '$BUCKET_NAME' created"
fi

echo "==> 设置 bucket 匿名只读（Ozon 拉图用）"
mc anonymous set download "$ALIAS/$BUCKET_NAME" >/dev/null
echo "   ✓ Anonymous GET allowed"

echo "==> 创建桌面端 put-only policy"
POLICY_FILE="$(mktemp)"
trap 'rm -f "$POLICY_FILE"' EXIT
cat > "$POLICY_FILE" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:PutObject"],
      "Resource": ["arn:aws:s3:::$BUCKET_NAME/*"]
    }
  ]
}
EOF
mc admin policy create "$ALIAS" put-only "$POLICY_FILE" 2>&1 | grep -v "already exists" || true
echo "   ✓ policy 'put-only' ensured"

echo "==> 创建/更新桌面端用户 $DESKTOP_ACCESS_KEY"
if mc admin user info "$ALIAS" "$DESKTOP_ACCESS_KEY" >/dev/null 2>&1; then
    # 用户已存在；如果 .env 里改了 SK，需要先删再建
    echo "   用户已存在，若 SK 变更请先手动 mc admin user remove 再重跑"
    mc admin user enable "$ALIAS" "$DESKTOP_ACCESS_KEY" >/dev/null
else
    mc admin user add "$ALIAS" "$DESKTOP_ACCESS_KEY" "$DESKTOP_SECRET_KEY"
fi
mc admin policy attach "$ALIAS" put-only --user "$DESKTOP_ACCESS_KEY" 2>&1 | grep -v "already attached" || true
echo "   ✓ user + policy 绑定"

echo "==> 配置 lifecycle: ${LIFECYCLE_EXPIRE_DAYS} 天后自动删除"
# 新版 mc 的 rule add 不接受 --id（自动生成）；幂等做法是先清空所有再添
mc ilm rule remove --all --force "$ALIAS/$BUCKET_NAME" >/dev/null 2>&1 || true
mc ilm rule add --expire-days "$LIFECYCLE_EXPIRE_DAYS" "$ALIAS/$BUCKET_NAME" >/dev/null
echo "   ✓ lifecycle: ${LIFECYCLE_EXPIRE_DAYS}d"

cat <<SUMMARY

================================================================
  初始化完成
================================================================
  公网 endpoint :  https://${PUBLIC_HOST}
  Bucket        :  $BUCKET_NAME
  桌面端 AK     :  $DESKTOP_ACCESS_KEY
  桌面端 SK     :  （见 .env，不要发到群里）
  生命周期      :  ${LIFECYCLE_EXPIRE_DAYS} 天

  自测（在 VPS 上）:
    bash /opt/oss/ops/smoke-test.sh
================================================================
SUMMARY
