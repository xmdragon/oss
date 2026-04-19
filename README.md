# Ozon 图片暂存服务（ozon-image-stage）

自建轻量 S3 兼容图片暂存，专用于 **智品（ZhiPin）桌面端批量上架到 Ozon** 的场景。

## 它解决什么问题

批量上架流程里，桌面端需要把商品图放到一个 **Ozon 能公网 GET 到的 URL** —— Ozon 自己的 `/v1/product/pictures/import` 会拉图、转存到 Ozon CDN，此后与我们无关。

原设计是走"桌面端 → 智品后端 → 阿里云 OSS → Ozon"，但智品后端服务器只有 **2 Mbps 带宽**，中转 10MB 图片要 ~40s，批量上架完全跑不动。

本项目在**一台新加坡小 VPS** 上跑一个 **MinIO（S3 兼容）+ Caddy（自动 HTTPS）** 的最小组合：

```
桌面端 ──(S3 SDK 直传, 写 AK/SK 硬编码)──→ MinIO (Singapore VPS)
                                           ↓ 公开只读 URL
                                        Ozon pulls
                                           ↓
                                      Ozon 转存到自家 CDN
                                           ↓
                              MinIO lifecycle 7 天后自动删除
```

**结果**：
- 智品主服务器 0 Bytes 图片流量
- 桌面端到 MinIO 直传，固定 VPS 带宽（轻量套餐也有 2.5 Gbps 峰值）
- AK/SK 放在桌面端没关系 —— VPS 是固定月费，被盗用顶多空间满，不会账单爆炸
- 7 天自动清盘，磁盘占用有上限

## 组件

| 组件 | 用途 | 版本锁定 |
|---|---|---|
| **MinIO** | S3 兼容对象存储 | `minio/minio:latest`（定期手动 `docker pull`） |
| **Caddy** | 反向代理 + 自动 Let's Encrypt | `caddy:latest` |
| （可选）**mc** | MinIO 客户端，用于初始化 bucket 与 lifecycle | `minio/mc:latest` |

## 目录结构

```
/home/grom/oss/
├── README.md                 # 本文件
├── docs/
│   ├── architecture.md       # 架构、数据流、为什么这么设计
│   ├── deployment.md         # VPS 从零部署步骤
│   ├── integration.md        # 智品桌面端（Rust/Tauri）接入指南
│   └── operations.md         # 日常运维：监控、清理、备份、故障处理
└── deploy/
    ├── docker-compose.yml    # MinIO + Caddy 编排
    ├── Caddyfile             # Caddy 反代配置
    ├── init-bucket.sh        # 首次初始化 bucket + 公开读 + lifecycle
    └── .env.example          # 环境变量模板（MINIO_ROOT_PASSWORD 等）
```

## 快速开始（30 分钟）

1. **买 VPS**：新加坡节点，2 vCPU / 2 GB RAM / 40 GB SSD / ≥2 TB/月流量。推荐 RackNerd、BandwagonHost、CloudCone、Vultr，约 $3–5/月。
2. **准备域名**：把一条子域（如 `img-stage.example.com`）解析到 VPS 公网 IP。
3. **部署**：
   ```bash
   scp -r deploy/ root@vps:/opt/ozon-image-stage/
   ssh root@vps
   cd /opt/ozon-image-stage
   cp .env.example .env && vim .env   # 改掉 MINIO_ROOT_PASSWORD 和 PUBLIC_HOST
   docker compose up -d
   ./init-bucket.sh                    # 建 bucket + 公开只读 + 7 天 lifecycle
   ```
4. **测试**：
   ```bash
   # 本地
   curl -X PUT "https://img-stage.example.com/products/test.jpg" \
     -u ozonstage:<pwd> --data-binary @test.jpg -H "Content-Type: image/jpeg"
   # 浏览器访问 https://img-stage.example.com/products/test.jpg 应能看到图
   ```
5. **接入桌面端**：见 [`docs/integration.md`](docs/integration.md)。

## 文档入口

- [架构说明](docs/architecture.md) — 为什么选 MinIO、数据流、成本估算
- [部署手册](docs/deployment.md) — 从裸机到跑起来的完整命令
- [桌面端集成](docs/integration.md) — Rust/Tauri 客户端代码样例
- [运维手册](docs/operations.md) — 日常、监控、应急

## 许可与责任

- 本服务**只暂存图片到 Ozon 拉走为止**（≤7 天），不做长期存储
- AK/SK 为"产品级共享凭证"，有意地分发给桌面端；**严禁放到公网仓库 / 对外分享**
- 如果出现异常流量（例如 AK 泄漏被刷），直接在 MinIO 控制台换一个 AK 即可，桌面端下个版本带新 key
