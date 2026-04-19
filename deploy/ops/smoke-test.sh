#!/usr/bin/env bash
# 端到端自测：
#   1) 用桌面端 AK PUT 一张图 → 200
#   2) 匿名 GET 同一 URL → 200 + 字节一致
#   3) 桌面端 AK DELETE 同对象 → 403（权限最小化）
set -u
ENV_FILE="/opt/oss/.env"
# shellcheck disable=SC1090
source "$ENV_FILE"
: "${PUBLIC_HOST:?}"; : "${BUCKET_NAME:?}"
: "${DESKTOP_ACCESS_KEY:?}"; : "${DESKTOP_SECRET_KEY:?}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT
IN="$TMPDIR/in.bin"
OUT="$TMPDIR/out.bin"
KEY="smoke-$(date +%s)-$RANDOM.bin"
URL="https://${PUBLIC_HOST}/${BUCKET_NAME}/${KEY}"

# 生成 128 KB 随机内容（不挑图，任意二进制即可）
head -c 131072 /dev/urandom > "$IN"
MD5_IN=$(md5sum "$IN" | awk '{print $1}')

fail() { echo "✗ $*"; exit 1; }
ok() { echo "✓ $*"; }

echo "==> 1. PUT（桌面端 AK，用 S3 v4 签名）"
# 为了不依赖 aws cli，用 mc 做签名上传
mc alias set smoke "http://127.0.0.1:9000" "$DESKTOP_ACCESS_KEY" "$DESKTOP_SECRET_KEY" >/dev/null
mc cp --quiet "$IN" "smoke/${BUCKET_NAME}/${KEY}" \
    && ok "PUT 成功 ($KEY)" \
    || fail "PUT 失败（检查 AK/SK / policy）"

echo "==> 2. 匿名 GET via 公网域名 ${PUBLIC_HOST}"
CODE=$(curl -s -o "$OUT" -w "%{http_code}" -m 30 "$URL")
[ "$CODE" = "200" ] || fail "匿名 GET 返回 $CODE（应 200）"
MD5_OUT=$(md5sum "$OUT" | awk '{print $1}')
[ "$MD5_IN" = "$MD5_OUT" ] || fail "字节不一致 in=$MD5_IN out=$MD5_OUT"
ok "匿名 GET 成功，字节一致（md5=$MD5_IN）"

echo "==> 3. DELETE（桌面端 AK，应被拒绝）"
# mc rm 失败不返回 curl 的 HTTP code，直接退出码
if mc rm "smoke/${BUCKET_NAME}/${KEY}" 2>&1 | grep -q -i -E "access denied|permission denied|forbidden"; then
    ok "DELETE 被拒（符合只写 policy）"
else
    # 再拿 root 拿回来看是否真被删了
    if mc stat "local/${BUCKET_NAME}/${KEY}" >/dev/null 2>&1; then
        ok "DELETE 未成功（对象仍在）—— policy 生效"
    else
        fail "DELETE 竟然成功了！policy 有漏洞"
    fi
fi

echo "==> 4. 清理测试对象（用 root AK）"
mc alias set local "http://127.0.0.1:9000" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null
mc rm "local/${BUCKET_NAME}/${KEY}" >/dev/null 2>&1 || true

echo ""
echo "================================================================"
echo "  端到端自测全部通过 ✓"
echo "  公网 endpoint : https://${PUBLIC_HOST}"
echo "  Bucket        : ${BUCKET_NAME}"
echo "================================================================"
