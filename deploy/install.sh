#!/usr/bin/env bash
# Ozon 图片暂存 —— 一键安装脚本（Ubuntu 22.04/24.04, root 执行）
# 不使用 Docker；直接装 MinIO + mc 官方二进制 + Caddy apt 包
# 幂等：可反复执行
set -euo pipefail

die() { echo "ERROR: $*" >&2; exit 1; }
log() { echo "==> $*"; }

[ "$(id -u)" = "0" ] || die "请用 root 执行：sudo bash install.sh"
[ -f /etc/os-release ] && . /etc/os-release
[ "${ID:-}" = "ubuntu" ] || echo "WARN: 非 Ubuntu 系统，继续但未测试"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
OSS_DIR="/opt/oss"

# ─── 1. 基础依赖 ─────────────────────────────────────────
log "更新 apt 源并装基础包"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl wget ca-certificates ufw gnupg debian-keyring debian-archive-keyring apt-transport-https

log "设置时区 Asia/Singapore"
timedatectl set-timezone Asia/Singapore || true

# ─── 2. 防火墙 ───────────────────────────────────────────
log "ufw 放行 22/80/443"
ufw --force default deny incoming >/dev/null
ufw --force default allow outgoing >/dev/null
ufw allow 22/tcp >/dev/null
ufw allow 80/tcp >/dev/null
ufw allow 443/tcp >/dev/null
# 历史版本曾放行 9443（Console 走非标端口），切到子域名后关掉
ufw delete limit 9443/tcp >/dev/null 2>&1 || true
ufw --force enable >/dev/null
ufw status | head -8

# ─── 3. MinIO 用户 & 目录 ────────────────────────────────
log "创建 minio-user 与 $OSS_DIR"
if ! id minio-user >/dev/null 2>&1; then
    groupadd -r minio-user
    useradd -r -g minio-user -s /sbin/nologin -d "$OSS_DIR" minio-user
fi
mkdir -p "$OSS_DIR/data" "$OSS_DIR/ops"
chown -R minio-user:minio-user "$OSS_DIR"

# ─── 4. 下载 MinIO 二进制 ─────────────────────────────────
MINIO_URL="https://dl.min.io/server/minio/release/linux-amd64/minio"
MC_URL="https://dl.min.io/client/mc/release/linux-amd64/mc"
if [ ! -x /usr/local/bin/minio ]; then
    log "下载 MinIO 二进制"
    wget -q -O /usr/local/bin/minio "$MINIO_URL"
    chmod +x /usr/local/bin/minio
else
    log "MinIO 已存在（跳过下载，如需升级手动 wget 覆盖）"
fi
if [ ! -x /usr/local/bin/mc ]; then
    log "下载 mc 二进制"
    wget -q -O /usr/local/bin/mc "$MC_URL"
    chmod +x /usr/local/bin/mc
fi
# 显示版本（用 || true 防止 SIGPIPE 传递；MinIO 的 --version 输出可能触发 pipefail）
/usr/local/bin/minio --version 2>&1 | sed -n '1p' || true
/usr/local/bin/mc --version 2>&1 | sed -n '1p' || true

# ─── 5. 安装 Caddy（官方 apt 源） ─────────────────────────
if ! command -v caddy >/dev/null 2>&1; then
    log "添加 Caddy apt 源"
    curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
        | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
    curl -fsSL 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
        > /etc/apt/sources.list.d/caddy-stable.list
    apt-get update -qq
    apt-get install -y -qq caddy
else
    log "Caddy 已安装（跳过）"
fi
caddy version

# ─── 6. 配置 .env ────────────────────────────────────────
if [ ! -f "$OSS_DIR/.env" ]; then
    log "生成 $OSS_DIR/.env（自动生成随机 secret）"
    ROOT_PW="$(openssl rand -hex 24)"
    DESK_SK="$(openssl rand -hex 24)"
    # 从模板生成
    sed -e "s|^MINIO_ROOT_PASSWORD=.*|MINIO_ROOT_PASSWORD=$ROOT_PW|" \
        -e "s|^DESKTOP_SECRET_KEY=.*|DESKTOP_SECRET_KEY=$DESK_SK|" \
        "$SCRIPT_DIR/.env.example" > "$OSS_DIR/.env"
    chmod 600 "$OSS_DIR/.env"
    chown minio-user:minio-user "$OSS_DIR/.env"
    echo "   ✓ 新生成 .env（secret 已随机化，记得备份 $OSS_DIR/.env）"
else
    log "$OSS_DIR/.env 已存在，跳过生成"
    # 迁移：补 ADMIN_HOST（Console 子域名）
    if ! grep -q '^ADMIN_HOST=' "$OSS_DIR/.env"; then
        log "  ↳ 补写 ADMIN_HOST（默认 ossmanage.hjdtrading.com，按需修改）"
        echo 'ADMIN_HOST=ossmanage.hjdtrading.com' >> "$OSS_DIR/.env"
    fi
    # 迁移：老版本的 MINIO_BROWSER_REDIRECT_URL 用 :9443，切子域名后改写
    ADM_HOST="$(grep '^ADMIN_HOST=' "$OSS_DIR/.env" | cut -d= -f2-)"
    if grep -q '^MINIO_BROWSER_REDIRECT_URL=.*:9443' "$OSS_DIR/.env"; then
        log "  ↳ 更新 MINIO_BROWSER_REDIRECT_URL 指向 ${ADM_HOST}"
        sed -i "s|^MINIO_BROWSER_REDIRECT_URL=.*|MINIO_BROWSER_REDIRECT_URL=https://${ADM_HOST}|" "$OSS_DIR/.env"
    elif ! grep -q '^MINIO_BROWSER_REDIRECT_URL=' "$OSS_DIR/.env"; then
        log "  ↳ 补写 MINIO_BROWSER_REDIRECT_URL（Console 公网 URL）"
        echo "MINIO_BROWSER_REDIRECT_URL=https://${ADM_HOST}" >> "$OSS_DIR/.env"
    fi
fi

# ─── 7. systemd 单元 ─────────────────────────────────────
log "安装 systemd 单元"
cp "$SCRIPT_DIR/systemd/minio.service" /etc/systemd/system/minio.service
mkdir -p /etc/systemd/system/caddy.service.d
cp "$SCRIPT_DIR/systemd/caddy.override.conf" /etc/systemd/system/caddy.service.d/override.conf

# ─── 8. Caddyfile ────────────────────────────────────────
log "安装 Caddyfile"
mkdir -p /etc/caddy /var/log/caddy
cp "$SCRIPT_DIR/Caddyfile" /etc/caddy/Caddyfile
# 预创日志文件，避免 caddy validate (以 root 跑) 把文件建成 root:root 导致后续 caddy 进程无写权限
touch /var/log/caddy/access.log /var/log/caddy/admin-access.log
chown -R caddy:caddy /var/log/caddy
# 语法校验（用 .env 里的变量展开）
set -a; . "$OSS_DIR/.env"; set +a
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile || die "Caddyfile 校验失败"
# validate 可能以 root 身份 touch 了 log 文件，再次校正属主
chown -R caddy:caddy /var/log/caddy

# ─── 9. 拷贝 ops 脚本 ────────────────────────────────────
log "安装 ops 脚本到 $OSS_DIR/ops"
cp "$SCRIPT_DIR"/ops/*.sh "$OSS_DIR/ops/"
cp "$SCRIPT_DIR"/init-bucket.sh "$OSS_DIR/init-bucket.sh"
chmod +x "$OSS_DIR/ops/"*.sh "$OSS_DIR/init-bucket.sh"

# ─── 10. 起服务 ──────────────────────────────────────────
log "启动 minio"
systemctl daemon-reload
systemctl enable --now minio
log "重启 caddy（拿 .env 里的域名）"
systemctl restart caddy
systemctl enable caddy

# 等一下让 Caddy 去签 LE
sleep 3
systemctl --no-pager status minio caddy | head -30

cat <<DONE

================================================================
  安装完成
================================================================
  下一步：
    bash $OSS_DIR/init-bucket.sh    # 建 bucket + 策略 + lifecycle
    bash $OSS_DIR/ops/smoke-test.sh # 端到端自测

  注意：首次访问 https://${PUBLIC_HOST:-<domain>} 时 Caddy 会自动签证书，
  若失败请：
    journalctl -u caddy -n 60 --no-pager

  .env 里的 AK/SK（勿外传）：
    cat $OSS_DIR/.env | grep -E '^(DESKTOP|MINIO_ROOT)'
================================================================
DONE
