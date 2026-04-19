# 运维手册

## 1. 日常观察

### 1.1 容器健康

```bash
ssh root@<vps>
cd /opt/ozon-image-stage

docker compose ps
# 两个容器都应 Up 且 healthy

docker compose logs --tail=100 minio
docker compose logs --tail=50 caddy
```

### 1.2 磁盘占用

```bash
df -h /opt/ozon-image-stage/data
du -sh /opt/ozon-image-stage/data

# MinIO 视角（bucket 级别）
docker compose exec minio mc admin info local
docker compose exec minio mc du local/products
```

预期峰值：`LIFECYCLE_EXPIRE_DAYS × 日均上传量`。日均 12 GB × 7 天 = ~85 GB。超过 80% 磁盘时：
- 缩短 lifecycle 天数（3 天够用）
- 或升级 VPS 磁盘

### 1.3 流量

```bash
# VPS 面板一般带流量统计；也可用 vnstat
apt install -y vnstat
vnstat -d   # 每日
vnstat -m   # 每月
```

---

## 2. 定期任务

| 频率 | 操作 | 命令 |
|---|---|---|
| 每周 | 升级 MinIO / Caddy 镜像 | `cd /opt/ozon-image-stage && docker compose pull && docker compose up -d` |
| 每月 | 查看 VPS 流量使用，余量 <20% 时考虑升级 | VPS 控制面板 / `vnstat` |
| 每季度 | 轮换桌面端 AK/SK（安全实践） | 见 §4.1 |
| 每半年 | 续费 VPS / 域名 | — |

---

## 3. 故障排查

### 3.1 桌面端传图失败

**403 AccessDenied**
```bash
# VPS 上查当前用户状态
docker compose exec minio mc admin user info local ozon-desktop
# 检查 policy 是否还在
docker compose exec minio mc admin policy list local
```
一般是 `.env` 里 DESKTOP_SECRET_KEY 改了但没重跑 `init-bucket.sh`，或者 policy 被误删。

**连接超时 / 拒绝**
```bash
# 从外网探
curl -I https://img-stage.example.com/
# 应该 200/403，不应超时

# VPS 本地探
docker compose exec caddy wget -O- http://minio:9000/minio/health/live
# 应该 200

# 端口 / 防火墙
ufw status
# 必须看到 443/tcp ALLOW
```

### 3.2 Ozon 拉不到图

先自己 curl 一次模拟 Ozon：
```bash
curl -I https://img-stage.example.com/products/<key>
# 200 + Content-Type: image/* = 没问题
```

如果本地 200 但 Ozon 拉不到：
- Ozon 服务器是俄罗斯 IP，检查 VPS 是否被俄罗斯段封锁（少见，RackNerd/CloudCone 不会）
- 证书过期（Let's Encrypt 到期前 30 天 Caddy 会自动续；手动查：`echo | openssl s_client -connect img-stage.example.com:443 2>/dev/null | openssl x509 -noout -dates`）

### 3.3 MinIO 启动失败

```bash
docker compose logs minio
```

常见：
- 磁盘满 → 清日志 / 调 lifecycle / 扩盘
- 权限错 → 确认 `/opt/ozon-image-stage/data` 是 docker 用户可写

### 3.4 Caddy 证书续签失败

```bash
docker compose logs caddy | grep -i error
# 常见原因：80 端口被占 / DNS 改了 / Let's Encrypt 限流
```

手动重新申请：
```bash
docker compose down
rm -rf caddy_data/caddy/certificates   # 清缓存
docker compose up -d
```

---

## 4. 应急操作

### 4.1 AK 泄漏

假设桌面端 AK 通过反编译被拿到，且已经看到异常流量。

```bash
# 1. 立即停用泄漏的 AK
docker compose exec minio mc admin user disable local ozon-desktop

# 2. 建新 AK
docker compose exec minio mc admin user add local ozon-desktop-v2 <new-32-byte-secret>
docker compose exec minio mc admin policy attach local put-only --user ozon-desktop-v2

# 3. 修改桌面端编译常量，发布新版本
# 4. 确认新版本铺开后，彻底删掉旧 AK
docker compose exec minio mc admin user remove local ozon-desktop
```

**商品影响**：Ozon 已拉到的图不受影响，继续正常显示（图在 Ozon CDN）。

### 4.2 磁盘占满

```bash
# 临时急救：手动清早于 2 天的对象
docker compose exec minio mc find local/products --older-than 2d --exec "mc rm {}"

# 长期：改 LIFECYCLE_EXPIRE_DAYS，然后重跑 init-bucket.sh 里的 lifecycle 部分
```

### 4.3 整机挂了

图片只是暂存，不是核心资产。直接重建一台 VPS：

```bash
# 1. 新 VPS，跑一遍 deployment.md
# 2. 改 DNS 记录指向新 IP（等 TTL）
# 3. 桌面端用户下次重启应用时自动恢复上传能力
```

此时正在进行的批量上架任务会失败 —— 后端会走降级路径（fallback 到中转上传，详见桌面端 `integration.md §8`）或直接报错。

---

## 5. 备份策略

**本服务**：**不做备份**。图片是暂存数据，Ozon 拉走就是归档，本地副本删了无所谓。

**配置**：只有 `.env` 和 `Caddyfile` 需要备份，加上本 repo 的 `deploy/` 目录即可。可以把完整 `deploy/` 提交到私有 git 仓库，`.env` 单独放 1Password / Bitwarden。

```bash
# 快速备份脚本
tar czf /tmp/ozon-stage-backup-$(date +%F).tgz \
  /opt/ozon-image-stage/{.env,Caddyfile,docker-compose.yml,init-bucket.sh}
# 然后 scp 到本地或上传到某个非同一机房的地方
```

---

## 6. 监控告警（可选）

初期完全可以手工巡检。有时间加 Uptime Kuma / HetrixTools 做外部探测即可：

- HTTPS 证书过期前 7 天告警
- `HEAD https://img-stage.example.com/` 每 5 分钟探测，连续 3 次失败告警
- VPS ping 丢包 / CPU >80% 告警

---

## 7. 常见问题 FAQ

**Q: Ozon 拉图失败，需要把图在我方保留多久？**
实际观察是 1~2 天内必拉完。`LIFECYCLE_EXPIRE_DAYS=7` 是留了很宽的保险。

**Q: 要不要上 CDN（Cloudflare 等）？**
**不要**。Ozon 每张图就拉 1 次，上 CDN 没有复用收益，反而多一层证书和防火墙配置。

**Q: 要不要双节点 MinIO 做 HA？**
现阶段**不要**。暂存业务挂了就 fallback 到智品后端中转，不是强一致性场景。双节点复杂度上去了反而运维压力大。

**Q: 能不能让桌面端不直连 MinIO，走智品后端 presign？**
可以，但就是我们一开始想避开的"拿不到服务器带宽自由"方案。本项目核心价值就是直连。

**Q: 桌面端开源给第三方后 AK 会泄漏怎么办？**
看 §4.1，轮换 AK + 发新版。固定费 VPS 的特性决定了被滥刷上限就是"磁盘打满，服务暂停 7 天自动清空恢复"。可接受。
