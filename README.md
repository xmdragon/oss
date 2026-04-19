# Ozon 图片暂存服务（ozon-image-stage）

自建轻量 S3 兼容图片暂存，专用于 **智品（ZhiPin）桌面端批量上架到 Ozon** 的场景。

## 它解决什么问题

批量上架流程里，桌面端需要把商品图放到一个 **Ozon 能公网 GET 到的 URL** —— Ozon 自己的 `/v1/product/pictures/import` 会拉图、转存到 Ozon CDN，此后与我们无关。

原设计是走"桌面端 → 智品后端 → 阿里云 OSS → Ozon"，但智品后端服务器只有 **2 Mbps 带宽**，中转 10MB 图片要 ~40s，批量上架完全跑不动。

本项目在**一台新加坡小 VPS** 上跑一个 **MinIO（S3 兼容）+ Caddy（自动 HTTPS）** 的最小组合，**原生二进制 + systemd**，不用 Docker（省内存 ~200MB）：

```
桌面端 ──(S3 SDK 直传, AK/SK 编译期注入)──→ MinIO (Singapore VPS)
                                           ↓ 公开只读 URL
                                        Ozon pulls
                                           ↓
                                      Ozon 转存到自家 CDN
                                           ↓
                              MinIO lifecycle 7 天后自动删除
```

**结果**：
- 智品主服务器 0 Bytes 图片流量
- 桌面端到 MinIO 直传，固定 VPS 带宽
- AK/SK 放在桌面端没关系 —— VPS 是固定月费，被盗用顶多空间满，不会账单爆炸
- 7 天自动清盘，磁盘占用有上限

## 组件

| 组件 | 用途 | 安装方式 |
|---|---|---|
| **MinIO** | S3 兼容对象存储 | 官方二进制 → `/usr/local/bin/minio` + systemd |
| **Caddy** | 反向代理 + 自动 Let's Encrypt | 官方 apt 包（自带 systemd） |
| **mc** | MinIO 管理 CLI | 官方二进制 → `/usr/local/bin/mc` |

## 目录结构

```
.
├── README.md                          # 本文件
├── docs/
│   ├── architecture.md                # 架构、数据流、选型理由
│   ├── deployment.md                  # VPS 从零部署
│   ├── api.md                         # ⭐ 桌面端接入 API 文档
│   └── operations.md                  # 日常运维：监控、AK 轮换、故障
└── deploy/
    ├── install.sh                     # 一键安装（装二进制 + 起服务）
    ├── init-bucket.sh                 # 建 bucket + 权限 + lifecycle
    ├── Caddyfile                      # Caddy 反代配置
    ├── .env.example                   # 环境变量模板
    ├── systemd/
    │   ├── minio.service              # MinIO systemd 单元
    │   └── caddy.override.conf        # Caddy drop-in（加载 .env）
    └── ops/
        ├── healthcheck.sh             # 一键体检
        ├── smoke-test.sh              # PUT/GET/DELETE 端到端
        ├── rotate-ak.sh               # AK 轮换
        └── backup-config.sh           # 配置打包
```

## 快速开始（30 分钟）

1. **买 VPS**：新加坡节点，≥1c/1G/25G/2TB 流量。推荐 Linode SG、RackNerd SG、CloudCone SG。
2. **准备域名**：把一条子域（如 `oss.hjdtrading.com`）**灰云（DNS-only，不走 CF 代理）** 解析到 VPS 公网 IP。
3. **部署**（本机 → VPS）：
   ```bash
   # 本机
   scp -r deploy/ root@<vps>:/root/oss-deploy/
   ssh root@<vps>
   # VPS
   cd /root/oss-deploy
   bash install.sh                    # 装 MinIO + mc + Caddy + 起服务
   vim /opt/oss/.env                  # 改 PUBLIC_HOST / 确认 secret 已随机生成
   systemctl restart caddy            # 让 Caddy 读新的域名
   bash /opt/oss/init-bucket.sh       # 建 bucket + 权限 + lifecycle
   bash /opt/oss/ops/smoke-test.sh    # PUT+GET+DELETE 自测
   ```
4. **给桌面端发 API 文档**：见 [`docs/api.md`](docs/api.md)。
5. **把 `.env` 和 AK/SK 备份到密码管理工具**（1Password / Bitwarden）。

## 文档入口

- [架构说明](docs/architecture.md) — 为什么 MinIO + Caddy、数据流、成本
- [部署手册](docs/deployment.md) — 从裸机到跑通的完整命令
- [**桌面端 API**](docs/api.md) — 桌面端开发者用
- [运维手册](docs/operations.md) — 日常、监控、应急

## 许可与责任

- 本服务**只暂存图片到 Ozon 拉走为止**（≤7 天），不做长期存储
- AK/SK 为"产品级共享凭证"，有意地分发给桌面端；**严禁放到公网仓库 / 对外分享**
- AK 泄漏应急：`bash /opt/oss/ops/rotate-ak.sh`，桌面端下个版本带新 key
