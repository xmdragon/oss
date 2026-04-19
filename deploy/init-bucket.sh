#!/usr/bin/env bash
# ozon-image-stage 首次初始化脚本
# 在 VPS 上的 /opt/ozon-image-stage/ 目录执行
# 前置：docker compose up -d 已经跑起来，.env 已配好

set -euo pipefail

# 加载 .env
if [ ! -f .env ]; then
    echo "ERROR: .env 不存在，先 cp .env.example .env 并改好值"
    exit 1
fi
set -a
# shellcheck disable=SC1091
source .env
set +a

: "${MINIO_ROOT_USER:?must be set in .env}"
: "${MINIO_ROOT_PASSWORD:?must be set in .env}"
: "${DESKTOP_ACCESS_KEY:?must be set in .env}"
: "${DESKTOP_SECRET_KEY:?must be set in .env}"
: "${BUCKET_NAME:?must be set in .env}"
: "${LIFECYCLE_EXPIRE_DAYS:?must be set in .env}"

ALIAS="local"

# mc 用 docker 运行，加入 internal 网络直接连 minio:9000
MC() {
    docker run --rm \
        --network ozon-image-stage_internal \
        -v "$(pwd)/mc-config:/root/.mc" \
        minio/mc:latest "$@"
}

echo "==> 配置 mc alias"
MC alias set "$ALIAS" http://minio:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"

echo "==> 等待 MinIO 就绪"
for i in {1..30}; do
    if MC admin info "$ALIAS" >/dev/null 2>&1; then
        break
    fi
    echo "   still waiting... ($i/30)"
    sleep 2
done

echo "==> 创建 bucket: $BUCKET_NAME"
if MC ls "$ALIAS/$BUCKET_NAME" >/dev/null 2>&1; then
    echo "   bucket 已存在，跳过"
else
    MC mb "$ALIAS/$BUCKET_NAME"
    echo "   ✓ Bucket '$BUCKET_NAME' created"
fi

echo "==> 设置 bucket 匿名只读（Ozon 拉图用）"
MC anonymous set download "$ALIAS/$BUCKET_NAME"
echo "   ✓ Anonymous read policy applied"

echo "==> 创建桌面端专用 user + put-only policy"
POLICY_FILE="$(pwd)/mc-config/put-only-policy.json"
mkdir -p "$(dirname "$POLICY_FILE")"
cat > "$POLICY_FILE" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:PutObject"
      ],
      "Resource": [
        "arn:aws:s3:::$BUCKET_NAME/*"
      ]
    },
    {
      "Effect": "Allow",
      "Action": [
        "s3:ListBucket"
      ],
      "Resource": [
        "arn:aws:s3:::$BUCKET_NAME"
      ],
      "Condition": {
        "StringLike": {
          "s3:prefix": [""]
        }
      }
    }
  ]
}
EOF

# 先删再建，便于反复运行这个脚本（幂等）
MC admin policy create "$ALIAS" put-only /root/.mc/put-only-policy.json \
    || echo "   policy put-only 已存在，继续"

if MC admin user info "$ALIAS" "$DESKTOP_ACCESS_KEY" >/dev/null 2>&1; then
    echo "   user $DESKTOP_ACCESS_KEY 已存在，更新 secret"
    MC admin user enable "$ALIAS" "$DESKTOP_ACCESS_KEY"
else
    MC admin user add "$ALIAS" "$DESKTOP_ACCESS_KEY" "$DESKTOP_SECRET_KEY"
fi

MC admin policy attach "$ALIAS" put-only --user "$DESKTOP_ACCESS_KEY" \
    || echo "   policy 已绑定"
echo "   ✓ User '$DESKTOP_ACCESS_KEY' created with put-only policy"

echo "==> 配置 lifecycle: ${LIFECYCLE_EXPIRE_DAYS} 天后自动删除"
MC ilm rule remove --id auto-expire "$ALIAS/$BUCKET_NAME" 2>/dev/null || true
MC ilm rule add \
    --expire-days "$LIFECYCLE_EXPIRE_DAYS" \
    --id auto-expire \
    "$ALIAS/$BUCKET_NAME"
echo "   ✓ Lifecycle rule: expire after $LIFECYCLE_EXPIRE_DAYS days"

echo ""
echo "================================================================"
echo "  初始化完成"
echo "================================================================"
echo "  公网 endpoint :  https://${PUBLIC_HOST}"
echo "  Bucket        :  $BUCKET_NAME"
echo "  桌面端 AK     :  $DESKTOP_ACCESS_KEY"
echo "  桌面端 SK     :  （见 .env 的 DESKTOP_SECRET_KEY，不要发到群里）"
echo ""
echo "  自测："
echo "    curl -T <local.jpg> \\"
echo "      -u ${DESKTOP_ACCESS_KEY}:<sk> \\"
echo "      https://${PUBLIC_HOST}/${BUCKET_NAME}/test.jpg"
echo "    curl -I https://${PUBLIC_HOST}/${BUCKET_NAME}/test.jpg"
echo "================================================================"
