# 运维手册

## 1. 日常观察

### 1.1 一键体检

```bash
ssh oss
bash /opt/oss/ops/healthcheck.sh
```

涵盖：服务 active 状态、端口、磁盘、证书剩余天数、MinIO 健康、公网探活、bucket 大小。

### 1.2 服务状态

```bash
systemctl status minio caddy --no-pager
journalctl -u minio -n 100 --no-pager
journalctl -u caddy -n 50 --no-pager
```

### 1.3 磁盘占用

```bash
df -h /opt/oss/data
du -sh /opt/oss/data
mc alias set local http://127.0.0.1:9000 <root-user> <root-pw>
mc du local/products
```

预期峰值：`LIFECYCLE_EXPIRE_DAYS × 日均上传量`。超过 80% 磁盘时：
- 缩短 `LIFECYCLE_EXPIRE_DAYS`（改 `.env` 后重跑 `init-bucket.sh` 更新 lifecycle 规则）
- 或升级 VPS 磁盘

### 1.4 流量

```bash
apt install -y vnstat    # 首次
vnstat -d                # 每日
vnstat -m                # 每月
```

### 1.4 Web 管理端（MinIO Console）

浏览器打开 **https://ossmanage.hjdtrading.com/** ，用 `/opt/oss/.env` 里的 `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` 登录。

可用操作：浏览 bucket、上传/下载/删除对象、查看 lifecycle、管理 AK/SK、监控 IO。

查密码：
```bash
ssh oss 'grep ^MINIO_ROOT /opt/oss/.env'
```

防护：独立子域名、标准 443 端口、LE 证书、密码 48 位随机。**勿把该密码发 IM**。

---

## 2. 定期任务

| 频率 | 操作 | 命令 |
|---|---|---|
| 每周 | 体检一次 | `bash /opt/oss/ops/healthcheck.sh` |
| 每月 | 升级 MinIO 二进制 | `wget -O /usr/local/bin/minio.new https://dl.min.io/server/minio/release/linux-amd64/minio && mv /usr/local/bin/minio{.new,} && chmod +x /usr/local/bin/minio && systemctl restart minio` |
| 每月 | 升级 Caddy | `apt update && apt install -y --only-upgrade caddy && systemctl restart caddy` |
| 每月 | 查流量余量 | `vnstat -m` / VPS 控制台 |
| 每季度 | 轮换桌面端 AK/SK | `bash /opt/oss/ops/rotate-ak.sh` |
| 每半年 | 续费 VPS / 域名 | — |

---

## 3. 故障排查

### 3.1 桌面端传图失败

**403 AccessDenied**
```bash
mc admin user info local ozon-desktop
mc admin policy list local            # 应有 put-only
```
一般是：
- AK 被禁用 → `mc admin user enable local ozon-desktop`
- `.env` 里 SK 改了但没同步到 MinIO → `mc admin user remove ozon-desktop && bash /opt/oss/init-bucket.sh`

**连接超时 / 拒绝**
```bash
curl -I https://oss.hjdtrading.com/minio/health/live
# 本机探 minio
curl -I http://127.0.0.1:9000/minio/health/live
# 防火墙
ufw status
```

### 3.2 Ozon 拉不到图

先模拟 Ozon：
```bash
curl -I https://oss.hjdtrading.com/products/<key>
# 200 + Content-Type: image/* = 我们这边没问题
```

本地 200 但 Ozon 拉不到：
- 少见的俄罗斯 IP 封禁 —— `curl --interface <cf/ru proxy> ...` 验证
- 证书过期（Caddy 应自动续）：
  ```bash
  echo | openssl s_client -connect oss.hjdtrading.com:443 2>/dev/null \
    | openssl x509 -noout -dates
  ```

### 3.3 MinIO 启动失败

```bash
journalctl -u minio -n 100 --no-pager
```

常见：
- 磁盘满 → 清日志 / 调 lifecycle / 扩盘
- 权限错 → `chown -R minio-user:minio-user /opt/oss/data`
- `.env` 里 `MINIO_VOLUMES` / `MINIO_OPTS` 被误删 → 从 `.env.example` 补回

### 3.4 Caddy 证书续签失败

```bash
journalctl -u caddy -n 100 --no-pager | grep -i -E 'error|warn|obtain'
```

常见：
- 80 被占 / DNS 改了 / LE 限流（短时间内同域名多次失败）
- 手动重试：
  ```bash
  rm -rf /var/lib/caddy/.local/share/caddy/certificates/*oss.hjdtrading.com*
  systemctl restart caddy
  ```

---

## 4. 应急操作

### 4.1 AK 泄漏

```bash
bash /opt/oss/ops/rotate-ak.sh
# 会打印新 AK/SK。旧 AK 立即被 disable，不会再通过校验。
```

然后：
1. 把新 AK/SK 填进桌面端构建配置，发版
2. 观察所有客户端升到新版（一周）
3. 彻底删除旧 AK：`mc admin user remove local <old-ak>`
4. 更新 `/opt/oss/.env` 的 `DESKTOP_ACCESS_KEY` / `DESKTOP_SECRET_KEY`

**商品影响**：Ozon 已拉到的图不受影响（已在 Ozon CDN）。

### 4.2 磁盘占满

```bash
# 急救：手动清两天前的
mc alias set local http://127.0.0.1:9000 <root> <pw>
mc find local/products --older-than 2d --exec "mc rm {}"

# 长期：改 /opt/oss/.env 的 LIFECYCLE_EXPIRE_DAYS，再跑
bash /opt/oss/init-bucket.sh
```

### 4.3 整机挂了

图片是暂存，直接重建：
```bash
# 1. 新 VPS 跑一遍 deployment.md
# 2. 改 DNS 指新 IP（等 TTL）
# 3. 恢复配置：
scp oss-config-<date>.tgz root@<new-vps>:/tmp/
ssh root@<new-vps>
tar xzf /tmp/oss-config-<date>.tgz -C /
systemctl daemon-reload
systemctl restart minio caddy
```

进行中的批量上架会失败 —— 桌面端走 fallback（中转上传）或重试。

---

## 5. 备份策略

**bucket 数据**：**不做备份**，是暂存数据。

**配置**：每月跑一次：
```bash
bash /opt/oss/ops/backup-config.sh
# /root/oss-config-<date>.tgz
```
scp 拉到本地或同步到私有 git。包含 `.env`、`Caddyfile`、systemd 单元、init/ops 脚本。

`.env` 里的 AK/SK 另外存进密码管理工具，这样即使 tgz 泄漏，secret 也不外流。

---

## 6. 监控告警（可选）

初期手工巡检即可。有余力可加：

- **Uptime Kuma / HetrixTools**：`HEAD https://oss.hjdtrading.com/minio/health/live` 每 5 分钟，连续 3 次失败告警
- **证书过期前 7 天**：同上工具一般自带
- **VPS CPU/磁盘**：VPS 面板一般有

---

## 7. FAQ

**Q: 图留多久？**
默认 7 天。Ozon 实际 1~2 天拉完。想省磁盘可降到 3 天。

**Q: 要 CDN 吗？**
不要。Ozon 每张只拉 1 次，CDN 无复用收益。

**Q: 要 HA 吗？**
不要。暂存场景挂了走 fallback 就行。

**Q: 桌面端 AK 泄漏会被刷爆吗？**
不会。VPS 是固定月费；最坏情况是磁盘打满 → 暂停 7 天自动清空恢复。即时应急走 `rotate-ak.sh`。
