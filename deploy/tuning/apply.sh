#!/usr/bin/env bash
# 应用 VPS 参数调优
# 用法（root）:
#   bash apply.sh
# 幂等。改配置后需要 sysctl --system / systemctl reload/restart，脚本已处理。
set -euo pipefail

[ "$(id -u)" = "0" ] || { echo "需 root"; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> 1. sysctl（BBR + 缓冲 + swappiness）"
cp "$SCRIPT_DIR/sysctl.conf" /etc/sysctl.d/99-oss.conf
# 让 BBR 模块重启后自动加载
echo "tcp_bbr" > /etc/modules-load.d/bbr.conf
modprobe tcp_bbr
sysctl --system >/dev/null
sysctl net.ipv4.tcp_congestion_control net.core.default_qdisc vm.swappiness

echo "==> 2. journald 日志上限 200M"
mkdir -p /etc/systemd/journald.conf.d
cp "$SCRIPT_DIR/journald.conf" /etc/systemd/journald.conf.d/oss.conf
systemctl restart systemd-journald

echo "==> 3. SSH 加固（关密码登录）"
mkdir -p /etc/ssh/sshd_config.d
cp "$SCRIPT_DIR/sshd-hardening.conf" /etc/ssh/sshd_config.d/99-oss-hardening.conf
# 先 sshd -t 验证再 reload，绝对不能 restart 中断当前 session
if ! sshd -t 2>&1; then
    echo "ERROR: sshd 配置校验失败，回滚"
    rm /etc/ssh/sshd_config.d/99-oss-hardening.conf
    exit 1
fi
# Ubuntu 24.04 的服务单元名是 ssh.service
systemctl reload ssh
echo "   当前生效值："
sshd -T | grep -E "^(passwordauth|permitrootlogin|kbdinteractive|challengeresp)"

echo "==> 4. mask 掉 snapd（dormant 状态也彻底不启动）"
systemctl mask snapd snapd.socket snapd.seeded.service 2>/dev/null || true

cat <<DONE

================================================================
  调优完成
================================================================
  关键变更：
    net.ipv4.tcp_congestion_control = bbr
    net.core.default_qdisc          = fq
    vm.swappiness                   = 10
    journald SystemMaxUse           = 200M
    SSH PasswordAuthentication      = no
    SSH PermitRootLogin             = prohibit-password
    snapd                           = masked

  验证建议：
    1. 保留当前 SSH session，另开一个新 terminal 试 ssh oss
       —— 能连上说明 key 登录仍然正常
    2. bash /opt/oss/ops/healthcheck.sh
================================================================
DONE
