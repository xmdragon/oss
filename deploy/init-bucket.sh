#!/usr/bin/env bash
# 初始化 MinIO 上的 buckets / policies / users / lifecycle。
#
# 设计原则：
#   - **数据驱动**：bucket 拓扑写在 BUCKETS / EXTRA_RULES 数组里，加 / 改新 bucket
#     只在这两块，下面的 ensure_* 函数不动
#   - **幂等**：可反复执行；mc mb 已存在跳过，lifecycle 全清后重设，user 已存在 enable
#     而非 add，policy create 静默吞 already-exists
#   - **依赖 .env**：admin secret + 各 service AK/SK，敏感不进 git，模板见 deploy/.env.example
#
# 前置：minio + caddy 已 systemctl 起来，/opt/oss/.env 已配好（含 AI_IMAGE_* 字段，
#       否则下面 `: "${...:?}"` 会立即报错）。
set -euo pipefail

ENV_FILE="/opt/oss/.env"
[ -f "$ENV_FILE" ] || { echo "ERROR: $ENV_FILE 不存在"; exit 1; }

# shellcheck disable=SC1090
set -a; source "$ENV_FILE"; set +a

: "${MINIO_ROOT_USER:?}"; : "${MINIO_ROOT_PASSWORD:?}"
: "${DESKTOP_ACCESS_KEY:?}"; : "${DESKTOP_SECRET_KEY:?}"
: "${AI_IMAGE_ACCESS_KEY:?}"; : "${AI_IMAGE_SECRET_KEY:?}"

ALIAS="local"

echo "==> 配置 mc alias"
mc alias set "$ALIAS" "http://127.0.0.1:9000" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null

echo "==> 等待 MinIO 就绪"
for i in {1..30}; do
    if mc admin info "$ALIAS" >/dev/null 2>&1; then break; fi
    [ "$i" = "30" ] && { echo "ERROR: MinIO 超时未就绪"; exit 1; }
    sleep 1
done

# ────────────────────────────────────────────────────────────────────
# Bucket 拓扑（数据驱动；改 bucket / lifecycle 改这两个常量即可）
# 字段：bucket_name, global_lifecycle_days, anonymous_policy
# ────────────────────────────────────────────────────────────────────
BUCKETS=(
  # name        days  anonymous   用途
  "products      7   download"  # ozon 图片暂存（legacy 批量上架）
  "desktop       365 download"  # 桌面端 release 包 / 升级包
  "extension     365 download"  # 扩展资源（图标 / 静态文件）
  "ai-image      7   download"  # image-api: 入参图 inputs/ + 结果图
)

# Prefix-scoped 额外 lifecycle 规则（在每 bucket 全局 rule 之外）
# 字段：bucket_name, prefix, expire_days
EXTRA_RULES=(
  # ai-image inputs/ — image-api OSS-key 改造（蓝图 v3）
  # 跟全局 7d 重复但保留，未来可独立调整入参 vs 结果保留期
  "ai-image      inputs/   7"
)

# ────────────────────────────────────────────────────────────────────
ensure_bucket() {
  local name=$1 days=$2 policy=$3
  if mc ls "$ALIAS/$name" >/dev/null 2>&1; then
    echo "  - bucket $name 已存在"
  else
    mc mb "$ALIAS/$name"
    echo "  - bucket $name 创建"
  fi
  mc anonymous set "$policy" "$ALIAS/$name" >/dev/null
  # 幂等：先清所有 lifecycle 再重设全局规则；prefix 规则由 add_prefix_rule 接着加
  mc ilm rule remove --all --force "$ALIAS/$name" >/dev/null 2>&1 || true
  mc ilm rule add --expire-days "$days" "$ALIAS/$name" >/dev/null
  echo "    anonymous=$policy  lifecycle=${days}d (global)"
}

add_prefix_rule() {
  local name=$1 prefix=$2 days=$3
  mc ilm rule add --expire-days "$days" --prefix "$prefix" "$ALIAS/$name" >/dev/null
  echo "    + prefix=$prefix lifecycle=${days}d"
}

ensure_user() {
  local ak=$1 sk=$2 policy=$3
  if mc admin user info "$ALIAS" "$ak" >/dev/null 2>&1; then
    # 已存在：enable（防被 disable 过）。若 SK 在 .env 里改了不会自动生效 — 需手动
    # mc admin user remove + 重跑本脚本
    mc admin user enable "$ALIAS" "$ak" >/dev/null
    echo "  - user $ak 已存在（如改 SK 需先手动 remove）"
  else
    mc admin user add "$ALIAS" "$ak" "$sk"
    echo "  - user $ak 创建"
  fi
  mc admin policy attach "$ALIAS" "$policy" --user "$ak" 2>&1 \
    | grep -v "already attached" || true
  echo "    policy=$policy"
}

# ────────────────────────────────────────────────────────────────────
# 1. Buckets + 全局 lifecycle
# ────────────────────────────────────────────────────────────────────
echo "==> Buckets"
for entry in "${BUCKETS[@]}"; do
  # shellcheck disable=SC2086
  ensure_bucket $entry  # word-split intentional
done

echo "==> Extra prefix-scoped lifecycle"
for entry in "${EXTRA_RULES[@]}"; do
  # shellcheck disable=SC2086
  add_prefix_rule $entry  # word-split intentional
done

# ────────────────────────────────────────────────────────────────────
# 2. Policies
# ────────────────────────────────────────────────────────────────────
POLICY1="$(mktemp)"
POLICY2="$(mktemp)"
trap 'rm -f "$POLICY1" "$POLICY2"' EXIT

echo "==> policy: put-only (legacy 桌面端批量上架，仅 PUT products/*)"
cat > "$POLICY1" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["s3:PutObject"],
    "Resource": ["arn:aws:s3:::products/*"]
  }]
}
EOF
mc admin policy create "$ALIAS" put-only "$POLICY1" 2>&1 \
  | grep -v "already exists" || true

echo "==> policy: scoped-rw-ai-image (image-api 入参 + 结果图)"
cat > "$POLICY2" <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    { "Effect": "Allow",
      "Action": ["s3:PutObject"],
      "Resource": ["arn:aws:s3:::ai-image/*"] },
    { "Effect": "Allow",
      "Action": ["s3:GetObject"],
      "Resource": ["arn:aws:s3:::ai-image/*"] },
    { "Effect": "Allow",
      "Action": ["s3:GetBucketLocation","s3:ListBucket"],
      "Resource": ["arn:aws:s3:::ai-image"] }
  ]
}
EOF
mc admin policy create "$ALIAS" scoped-rw-ai-image "$POLICY2" 2>&1 \
  | grep -v "already exists" || true

# ────────────────────────────────────────────────────────────────────
# 3. Users
# ────────────────────────────────────────────────────────────────────
echo "==> Users"
ensure_user "$DESKTOP_ACCESS_KEY"  "$DESKTOP_SECRET_KEY"  put-only
ensure_user "$AI_IMAGE_ACCESS_KEY" "$AI_IMAGE_SECRET_KEY" scoped-rw-ai-image

cat <<SUMMARY

================================================================
  初始化完成
================================================================
  endpoint     :  https://${PUBLIC_HOST}
  buckets      :  products desktop extension ai-image
  users        :
    ${DESKTOP_ACCESS_KEY}  → put-only        → PUT products/*
    ${AI_IMAGE_ACCESS_KEY} → scoped-rw-ai-image → PUT/GET ai-image/*

  自测（在 VPS 上）:
    bash /opt/oss/ops/smoke-test.sh
================================================================
SUMMARY
