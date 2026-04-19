# 部署手册

从裸机 VPS 到跑起来的**完整可复制命令**。预计耗时 30 分钟。

> 本项目**不使用 Docker**——直接装 MinIO + mc + Caddy 的官方二进制，systemd 管理。
> 相比 Docker 方案，省 ~200MB 内存、启动更快、问题也更好排查。

---

## 0. 前置

- [ ] 新加坡 VPS 一台（≥1c/1G/25G/2TB，≤$5/月）；Ubuntu 22.04 或 24.04
- [ ] 一个域名 + 一条子域（本文 `oss.hjdtrading.com` 举例）
- [ ] **DNS 是灰云 / DNS-only** 直接指向 VPS 公网 IP —— 不要走 Cloudflare 代理
  （否则 Caddy 签 LE 证书会出问题；且 CF 对 Ozon 爬虫 IP 可能有挑战）
- [ ] 本机能 `ssh oss`（或 `ssh root@<vps>`）登进去

---

## 1. 准备 deploy 文件

本机：
```bash
cd /home/grom/oss
scp -r deploy root@<vps>:/root/oss-deploy
```

---

## 2. 一键安装（VPS）

```bash
ssh root@<vps>
cd /root/oss-deploy
bash install.sh
```

install.sh 会做：

1. apt 更新，装 `curl wget ca-certificates ufw` 等基础包
2. 设时区 `Asia/Singapore`
3. 启 ufw 防火墙，只放 22/80/443
4. 下载 MinIO + mc 二进制到 `/usr/local/bin/`
5. 添加 Caddy 官方 apt 源，装 Caddy
6. 创建 `minio-user` 用户、`/opt/oss/data/` 目录
7. 从 `.env.example` 生成 `/opt/oss/.env`（**自动随机化 secret**）
8. 装 systemd 单元：`minio.service`、`caddy.service.d/override.conf`
9. 装 Caddyfile 到 `/etc/caddy/`
10. 启动 minio + caddy

---

## 3. 改 .env

```bash
vim /opt/oss/.env
```

重点字段：

```ini
PUBLIC_HOST=oss.hjdtrading.com        # 必须已在 DNS 生效
ACME_EMAIL=xm.dragon@gmail.com        # LE 到期提醒
MINIO_ROOT_USER=admin
MINIO_ROOT_PASSWORD=<install 已随机>   # 不用改，记得备份
DESKTOP_ACCESS_KEY=ozon-desktop
DESKTOP_SECRET_KEY=<install 已随机>    # 不用改，记得备份
BUCKET_NAME=products
LIFECYCLE_EXPIRE_DAYS=7
```

让 Caddy 读到新的 `PUBLIC_HOST`：
```bash
systemctl restart caddy
```

首次访问触发 LE 签证书：
```bash
curl -I https://oss.hjdtrading.com
# 第一次可能 5-10s（签证书），之后毫秒级
```

查看证书日志：
```bash
journalctl -u caddy -n 30 --no-pager
# 找 "certificate obtained successfully"
```

**签失败排查**：
- DNS 未指向 VPS：`dig +short oss.hjdtrading.com`
- CF 代理还开着：Cloudflare 面板点灰小云朵
- 80 端口被占：`ss -tlnp | grep :80`

---

## 4. 初始化 Bucket

```bash
bash /opt/oss/init-bucket.sh
```

成功输出应包含：
```
✓ Bucket 'products' created
✓ Anonymous GET allowed
✓ policy 'put-only' ensured
✓ user + policy 绑定
✓ lifecycle: 7d
```

---

## 5. 端到端自测

```bash
bash /opt/oss/ops/smoke-test.sh
```

该脚本：
1. 用桌面端 AK PUT 一个 128KB 随机文件 → 应 200
2. 匿名 GET 同 URL 并校验 md5 → 应一致
3. 桌面端 AK DELETE → 应 403（权限最小化）
4. 用 root AK 清理测试对象

最后应打印 `端到端自测全部通过 ✓`。

---

## 6. 体检

```bash
bash /opt/oss/ops/healthcheck.sh
```

输出涵盖：服务状态 / 端口 / 磁盘 / 证书剩余天数 / MinIO 内部探活 / 公网探活 / bucket 概览。

---

## 7. MinIO Web 控制台（可选）

控制台默认绑在 `127.0.0.1:9001`，**不对公网暴露**。要看就 SSH 隧道：

```bash
ssh -L 9001:127.0.0.1:9001 root@<vps>
# 浏览器打开 http://localhost:9001，用 MINIO_ROOT_USER / PASSWORD 登录
```

---

## 8. 把 AK/SK 交给桌面端

```bash
# VPS 上打出来
grep -E '^DESKTOP_' /opt/oss/.env
```

**传递方式**：密码管理工具（1Password / Bitwarden）共享条目。**不要** 走 IM / 邮件 / 截图。

桌面端接入规范见 [`api.md`](api.md)。

---

## 9. 备份配置

```bash
bash /opt/oss/ops/backup-config.sh
# 产出 /root/oss-config-<date>.tgz
scp root@<vps>:/root/oss-config-*.tgz ~/oss-backup/
```

把 tgz 放到安全的地方。bucket 里的图片不用备（暂存数据）。
