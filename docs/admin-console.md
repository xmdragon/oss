# OSS Admin Console（自建管理 UI）

> 替代 MinIO 官方 Console（社区版自 2025 中已砍掉 lifecycle / IAM 等管理界面）。
> 监听 `127.0.0.1:9002`，由 Caddy 反代到 `https://ossmanage.hjdtrading.com`。

## 1. 能做什么

| 模块 | 操作 |
|---|---|
| 概览 | 总用量 / 对象数 / Bucket 数 / MinIO 健康；近 24h 净增量 SVG 图 |
| Buckets | 列表、详情、bucket policy 只读、lifecycle 规则 CRUD |
| Objects | 浏览（前缀过滤 + 100/200/500/1000 分页）、单对象详情（含 5min presigned URL）、单对象删除、按时间批量删除（早于 N 天） |
| Access Keys | 列表、新建桌面端 AK（自动绑 `put-only` policy）、启用/停用、轮换、删除 |

所有写操作记审计日志：`/var/log/oss-admin/audit.log`，每行一条 JSON。

**对象操作的边界（重要）**

- 分页强制服务端校验，下拉只允许 100/200/500/1000，默认 100；不提供"上一页"（cursor 单向，前进用 `?cursor=`，回退点"⟲ 回到首页"）。
- presigned URL 签名指向 `PUBLIC_HOST` + HTTPS（Caddy `header_up Host {host}` 保证 MinIO 校验通过），有效期固定 5 分钟。
- 批量删除走"扫描预览 → 确认执行"两步，单次硬上限 10000 个对象；超出需多次点击，或改用 lifecycle 规则自动清理。预览页面的 `cutoff_unix` 会回传给确认表单，避免两次点击之间因时差多删/少删。
- 批量删除同步阻塞执行，浏览器不要刷新；服务端 60s 超时（HTTP 层），MinIO 多对象 DELETE 批量很快，10000 个对象典型耗时几秒。

## 2. 首次安装

`install.sh` 已经做完了：建 `oss-admin` 系统用户、安装二进制 `/usr/local/bin/oss-admin`、装 systemd unit、建日志目录。但 `oss-admin.service` **没启动**——还需要你设管理员凭证：

```bash
sudo /usr/local/bin/oss-admin setup
# 交互式：
#   1) 管理员用户名（默认 grom）
#   2) 密码（≥10 字符，二次确认；argon2id 哈希）
#   3) 是否启用 TOTP（强烈推荐）；终端打印 ASCII 二维码 + base32 secret
#   4) 自动生成 SESSION_SECRET（32 字节）
#   5) 写 /opt/oss/admin.env (chmod 600, oss-admin:oss-admin)

sudo systemctl start oss-admin
sudo systemctl status oss-admin --no-pager
```

然后浏览器打开 `https://ossmanage.hjdtrading.com` 登录。

## 3. 忘密码 / 改密码

```bash
sudo /usr/local/bin/oss-admin setpw
# 只重设密码哈希；用户名、TOTP、SESSION_SECRET 不变（不会让其它人重登）
```

如果连 TOTP 都丢了：

```bash
sudo /usr/local/bin/oss-admin setup
# 重跑完整 setup，可以选择不再启用 TOTP；SESSION_SECRET 会重新生成，
# 所有现有会话会立刻失效（包括你自己）——属于预期行为。
```

## 4. 跟 init-bucket.sh 的关系

`init-bucket.sh` 仍然是部署期的"声明式真相"：bucket、桌面端 policy、初始 lifecycle 规则都从那里来。oss-admin 是**运行期可视化操作**，跟 init-bucket.sh 共享同一份 MinIO 元数据。

> ⚠️ 在 oss-admin 里改了 lifecycle / AK 之后，**不要再跑 init-bucket.sh**——脚本里的规则会覆盖你在 UI 上做的改动。要么以脚本为准（改 `.env` 重跑），要么以 UI 为准。

## 5. 端口拓扑（速记）

```
公网 :443  → Caddy
              ├─ {PUBLIC_HOST}    → 127.0.0.1:9000  (MinIO S3 API)
              └─ {ADMIN_HOST}     → 127.0.0.1:9002  (oss-admin) ← 本服务

loopback only:
  127.0.0.1:9000   MinIO S3
  127.0.0.1:9001   MinIO 自带 Console（应急 SSH 隧道用）
  127.0.0.1:9002   oss-admin
```

应急访问 MinIO 自带 Console：

```bash
ssh -L 9001:127.0.0.1:9001 root@<vps>
# 浏览器开 http://localhost:9001
```

## 6. 排错

**`systemctl status oss-admin` 显示 failed，日志说 `missing ADMIN_PASSWORD_HASH`**
没跑 `oss-admin setup`。

**登录后所有页面 502 / 显示空数据**
oss-admin → MinIO 连接失败。检查 `/opt/oss/.env` 里的 `MINIO_ROOT_USER/PASSWORD` 是否对得上 MinIO 当前账号；看日志 `journalctl -u oss-admin -n 50 --no-pager`。

**新建 AK 后页面没显示 SecretKey**
SecretKey 通过一次性 cookie 传到下个页面。如果客户端禁了 cookie 或浏览器跨域阻断了，会拿不到。降级：`mc admin user info local <ak>` 看不到 SK（MinIO 不存明文）；只能再轮换一次。

**TOTP 时间窗对不上**
服务器时区是 Asia/Singapore，但 TOTP 用 UTC。手机/电脑时间偏移 >30s 就会失败。`timedatectl` 看 NTP 同步状态。

**审计日志在哪**
```bash
sudo tail -f /var/log/oss-admin/audit.log | jq
```

## 7. 架构脚注

- 单二进制 ~13MB，CGO=0 静态链接，不依赖系统 glibc 之外的东西。
- 模板和 CSS 都用 `embed.FS` 编译进去，文件系统上只有一个 binary。
- 无数据库、无 redis；session 是 HMAC 签名 cookie，登录限流是进程内令牌桶。
- 写入唯一目录是 `/var/log/oss-admin/`（systemd `ProtectSystem=strict`）。
- HTTPS 在 Caddy 那边终结；oss-admin 自己只听 loopback HTTP。
