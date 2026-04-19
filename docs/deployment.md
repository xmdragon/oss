# 部署手册

从裸机 VPS 到跑起来的**完整可复制命令**。预计耗时 30 分钟（含买机器和 DNS 等待）。

---

## 0. 前置准备

- [ ] 新加坡 VPS 一台（2c/2G/50G/5TB，≤$5/月）
- [ ] 一个你已经在用的域名（本文示例用 `example.com`）
- [ ] DNS 管理台（Cloudflare / 阿里云 DNS / 域名注册商面板）
- [ ] 本机能 `ssh root@<vps>` 登进去

---

## 1. VPS 初始化

### 1.1 基础加固

```bash
ssh root@<vps-ip>

# 更新
apt update && apt upgrade -y

# 时区（日志用）
timedatectl set-timezone Asia/Singapore

# 创建部署目录
mkdir -p /opt/ozon-image-stage
cd /opt/ozon-image-stage

# 防火墙只开 22 / 80 / 443
apt install -y ufw
ufw default deny incoming
ufw default allow outgoing
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable
ufw status
```

### 1.2 安装 Docker + Compose

```bash
# 官方一键脚本
curl -fsSL https://get.docker.com | sh

# Compose v2 已随 Docker 安装，验证
docker version
docker compose version
```

---

## 2. DNS 解析

去域名管理台加一条 A 记录：

| 类型 | 主机记录 | 值 | TTL |
|---|---|---|---|
| A | `img-stage` | `<vps-ip>` | 600 |

等待 `dig img-stage.example.com` 解析到 VPS IP 后再进下一步。不到位就跳去 Caddy 签证会失败。

---

## 3. 拷贝部署文件

本机操作：

```bash
cd /home/grom/oss/deploy
scp docker-compose.yml Caddyfile init-bucket.sh .env.example root@<vps-ip>:/opt/ozon-image-stage/
```

VPS 操作：

```bash
cd /opt/ozon-image-stage
cp .env.example .env
chmod +x init-bucket.sh
```

---

## 4. 配置 .env

```bash
vim /opt/ozon-image-stage/.env
```

必填字段：

```ini
# 公开域名（Caddy 用此申请证书）
PUBLIC_HOST=img-stage.example.com

# Let's Encrypt 邮箱（证书到期提醒）
ACME_EMAIL=you@example.com

# MinIO 根账号（仅 VPS 上用）
MINIO_ROOT_USER=admin
MINIO_ROOT_PASSWORD=<32 位随机字符串，建议 openssl rand -base64 24>

# 桌面端专用 AK/SK（后面 init-bucket.sh 会建）
DESKTOP_ACCESS_KEY=ozon-desktop
DESKTOP_SECRET_KEY=<另一段 32 位随机>

# Bucket 名
BUCKET_NAME=products

# 对象存活天数（Ozon 多数 1~2 天内拉完，7 天保险）
LIFECYCLE_EXPIRE_DAYS=7
```

生成随机字符串：`openssl rand -base64 24`

---

## 5. 启动服务

```bash
cd /opt/ozon-image-stage
docker compose up -d
```

看看是否都起来了：

```bash
docker compose ps
# 应该看到 minio 和 caddy 两个 running
```

Caddy 会在 **首次收到 HTTPS 请求时** 自动申请 Let's Encrypt 证书。触发一次：

```bash
curl -I https://img-stage.example.com/
# 第一次可能要 5-10 秒，HTTP 状态 200 或 403 都 OK，说明证书签完了
```

查证书日志：

```bash
docker compose logs caddy | tail -30
# 找到 "certificate obtained successfully"
```

失败常见原因：
- DNS 未生效 → `dig img-stage.example.com` 确认
- 80 端口没开 → Let's Encrypt 要 HTTP-01 验证
- VPS 防火墙（云厂商的安全组）没放 80/443

---

## 6. 初始化 Bucket 与权限

运行一次性脚本（在 VPS 上）：

```bash
cd /opt/ozon-image-stage
./init-bucket.sh
```

脚本会做：
1. 用根账号连上 MinIO
2. 建 bucket `products`
3. 设置 bucket policy：**匿名 GET 可读** —— 供 Ozon 拉图
4. 建用户 `ozon-desktop` + 只写 policy —— 桌面端专用
5. 配置 lifecycle：对象上传 `LIFECYCLE_EXPIRE_DAYS` 天后自动删除

成功输出应包含：

```
✓ Bucket 'products' created
✓ Anonymous read policy applied
✓ User 'ozon-desktop' created with put-only policy
✓ Lifecycle rule: expire after 7 days
```

---

## 7. 自测

### 7.1 用根账号传一张图（验证写入）

```bash
# 本地随便找张图
curl -T /tmp/test.jpg \
  -u admin:<MINIO_ROOT_PASSWORD> \
  https://img-stage.example.com/products/test.jpg
# 返回 200
```

### 7.2 匿名 GET（模拟 Ozon 拉图）

```bash
curl -I https://img-stage.example.com/products/test.jpg
# 应该返回 200 + Content-Type: image/jpeg + Content-Length
```

浏览器打开 URL 应能看到图。

### 7.3 桌面端专用 AK 的写权限

```bash
curl -T /tmp/test2.jpg \
  -u ozon-desktop:<DESKTOP_SECRET_KEY> \
  https://img-stage.example.com/products/test2.jpg
# 返回 200
```

### 7.4 验证只写不能删（权限最小化）

```bash
curl -X DELETE \
  -u ozon-desktop:<DESKTOP_SECRET_KEY> \
  https://img-stage.example.com/products/test2.jpg
# 应该返回 403 AccessDenied
```

都通过就完事了。

---

## 8. MinIO 控制台（可选）

如果想用 Web UI 看 bucket 状态：

```bash
# 默认 docker-compose 只把 9000（S3 API）暴露到外网
# 控制台在 9001 端口，仅本机能访问
ssh -L 9001:localhost:9001 root@<vps-ip>
# 浏览器打开 http://localhost:9001，用 MINIO_ROOT_USER / ROOT_PASSWORD 登录
```

**不要把 9001 暴露到公网**。SSH 隧道就够用。

---

## 9. 下一步

- 接入桌面端：见 [`integration.md`](integration.md)
- 监控与运维：见 [`operations.md`](operations.md)
