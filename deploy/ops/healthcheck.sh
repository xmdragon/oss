#!/usr/bin/env bash
# 一键体检：systemd 状态 / 端口 / 磁盘 / 证书 / 外网探活
set -u
ENV_FILE="/opt/oss/.env"
# shellcheck disable=SC1090
[ -f "$ENV_FILE" ] && { set -a; source "$ENV_FILE"; set +a; }

hr() { printf '%.0s─' {1..64}; echo; }
ok() { echo "  ✓ $*"; }
warn() { echo "  ⚠ $*"; }
err() { echo "  ✗ $*"; }

hr; echo "服务状态"; hr
for svc in minio caddy; do
    if systemctl is-active --quiet "$svc"; then
        ok "$svc active ($(systemctl show -p ActiveEnterTimestamp --value "$svc"))"
    else
        err "$svc NOT active"
        systemctl status "$svc" --no-pager -n 5 || true
    fi
done

hr; echo "端口监听"; hr
ss -tlnp 2>/dev/null | awk 'NR==1 || /:(80|443|9000|9001) /'

hr; echo "磁盘"; hr
df -h /opt/oss/data 2>/dev/null || df -h /
DU=$(du -sh /opt/oss/data 2>/dev/null | awk '{print $1}')
echo "  bucket 实际占用: ${DU:-?}"
USE_PCT=$(df /opt/oss/data 2>/dev/null | awk 'NR==2{gsub("%","",$5); print $5}' || df / | awk 'NR==2{gsub("%","",$5); print $5}')
if [ "${USE_PCT:-0}" -ge 80 ]; then
    warn "磁盘使用率 ${USE_PCT}% ≥ 80%，考虑缩短 LIFECYCLE_EXPIRE_DAYS"
else
    ok "磁盘使用率 ${USE_PCT}%"
fi

hr; echo "证书"; hr
if [ -n "${PUBLIC_HOST:-}" ]; then
    EXPIRE=$(echo | openssl s_client -servername "$PUBLIC_HOST" -connect "$PUBLIC_HOST:443" 2>/dev/null \
        | openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)
    if [ -n "$EXPIRE" ]; then
        END_TS=$(date -d "$EXPIRE" +%s 2>/dev/null || echo 0)
        NOW_TS=$(date +%s)
        DAYS=$(( (END_TS - NOW_TS) / 86400 ))
        if [ "$DAYS" -lt 15 ]; then
            warn "证书剩余 $DAYS 天（<15）—— Caddy 应该会自动续，若未续查 journalctl -u caddy"
        else
            ok "证书到期 $EXPIRE（剩 $DAYS 天）"
        fi
    else
        err "无法读取 $PUBLIC_HOST 的证书"
    fi
fi

hr; echo "MinIO 内部探活"; hr
if curl -sf -m 5 "http://127.0.0.1:9000/minio/health/live" -o /dev/null; then
    ok "MinIO /health/live 200"
else
    err "MinIO /health/live 失败"
fi

hr; echo "对外 HTTPS 探活（本机 → 公网 → 回）"; hr
if [ -n "${PUBLIC_HOST:-}" ]; then
    CODE=$(curl -so /dev/null -w "%{http_code}" -m 10 "https://${PUBLIC_HOST}/minio/health/live" || echo ERR)
    if [ "$CODE" = "200" ]; then
        ok "https://${PUBLIC_HOST}/minio/health/live 200"
    else
        err "返回 $CODE（可能 DNS / 证书 / Caddy / MinIO 之一有问题）"
    fi
fi

hr; echo "Bucket 概览"; hr
if [ -n "${MINIO_ROOT_USER:-}" ] && [ -n "${MINIO_ROOT_PASSWORD:-}" ]; then
    mc alias set local "http://127.0.0.1:9000" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" >/dev/null 2>&1 || true
    mc du "local/${BUCKET_NAME:-products}" 2>/dev/null | head -5 || warn "mc du 失败，bucket 可能未初始化"
    echo "  lifecycle:"
    mc ilm rule ls "local/${BUCKET_NAME:-products}" 2>/dev/null | head -5 || true
fi

hr
