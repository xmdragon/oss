# 架构说明

## 1. 场景与目标

批量上架桌面端一次操作可能涉及 10~500 张商品图，单张 1~10 MB。Ozon 平台侧的处理路径是：

```
桌面端 ──→ 任意公开 URL ──→ Ozon POST /v1/product/pictures/import
                                 ↓
                       Ozon 后台异步下载图片
                                 ↓
                  转存到 Ozon CDN（cdn1.ozone.ru 等）
                                 ↓
                  商品卡绑定的是 Ozon CDN URL，与我方无关
```

**关键观察**：
- 我方只需要提供 **Ozon 拉图那一会儿** 能访问的公网 URL
- Ozon 拉完之后，图片在我方的副本就可以删除
- 同一图片被 Ozon 拉取的频率通常是 **1 次**（最多几次重试）

## 2. 不合适的方案

| 方案 | 为什么不适合本场景 |
|---|---|
| 桌面端 → 智品后端 → 阿里云 OSS | 智品主服务器只有 **2 Mbps**，10MB 图要 40s，批量跑废 |
| 直接用桌面端 AK 访问阿里云 OSS | Aliyun 是按流量计费，AK 泄漏 = 账单爆炸 |
| 阿里云 OSS + 后端签名 URL（presign） | 可行但需要改后端、桌面端、文档三处，本服务让桌面端完全独立升级 |
| Lsky Pro / EasyImages 等 PHP 图床 | 多余的用户/相册系统；上传/下载仍过图床自己的服务器带宽 |
| 自建 S3 SDK + 自写上传服务 | 重复造轮子，MinIO 已经是这件事的标准答案 |

## 3. 选型：MinIO + Caddy

### MinIO

- **单 Go 二进制**，没有数据库、没有 PHP-FPM、没有任何外部依赖
- 装法：官方 Linux 二进制 → `/usr/local/bin/minio`，systemd 托管。**不走 Docker**（VPS 内存只有 1GB，省 docker daemon 的 ~200MB）
- **S3 协议**：Rust 用 `aws-sdk-s3`，Python 用 `boto3`，语言无关
- **lifecycle policy**：原生支持"对象 N 天后自动删除"，免写清理脚本
- **bucket policy**：原生支持"桶内对象匿名可读"，无需改反代
- 健康时内存占用 ~100 MB，磁盘按需

### Caddy

- 官方 apt 包自带 systemd 服务和 `caddy` 用户
- 自动申请 / 续签 Let's Encrypt 证书，`Caddyfile` 里几行搞定 HTTPS
- 反向代理到 MinIO（`127.0.0.1:9000`，仅本机可达），把对外端口和对象存储隔离

## 4. 数据流

```
┌────────────────────────────┐       1. PUT  /products/xxx.jpg           ┌────────────────┐
│  桌面端（Rust / Tauri）    │ ─────────────────────────────────────────→ │ Caddy (443)   │
│  S3 SDK，硬编码 AK/SK      │                                           │  ↓ reverse    │
└────────────────────────────┘                                           │  ↓ proxy      │
                                                                         │ MinIO (9000)  │
       2. 返回 200                                                        │  ↓ 写盘       │
     ←───────────────────────────────────────────────────────────────────┤               │
                                                                         └───────┬────────┘
                                                                                 │
┌────────────────────────────┐       3. 把 URL 塞进 batch-listing             │
│  智品后端（Python）         │ ←─ 4. POST /ozon/batch-listing/create         │
└────────────┬───────────────┘                                                  │
             │  5. 后端调 Ozon /v1/product/pictures/import                      │
             ↓                                                                  │
    ┌──────────────────┐      6. Ozon 拉图 GET /products/xxx.jpg                │
    │ api-seller.ozon  │ ───────────────────────────────────────────────────────┤
    └─────────┬────────┘                                                        │
              │                                                                 │
              │ 7. Ozon 内部转存到自家 CDN                                     │
              ↓                                                                 │
     ┌──────────────────┐                                                       │
     │  cdn1.ozone.ru   │                                                       │
     └──────────────────┘                                                       │
                                                                                │
                               8. 7 天后 MinIO lifecycle 自动删除 ──────────────┘
```

关键点：

- 步骤 1 是**桌面端直连 MinIO**，不经过智品主服务器。这是本设计的核心。
- 步骤 6 来自 Ozon 的俄罗斯公网 IP，**必须是公开可读 URL**（bucket policy 允许匿名 GET）
- 步骤 8 自动删除，不需要额外的清理脚本

## 5. 凭证模型

| 凭证 | 类型 | 保管位置 | 权限 |
|---|---|---|---|
| `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` | MinIO 根凭证 | 仅 VPS 上的 `.env` 文件 | 全局管理（建 bucket、改 policy） |
| `ozon-desktop` AK/SK | 写入凭证（专用） | 桌面端 Tauri 编译注入 | 仅允许 `PUT s3://products/*` |
| （无） | Ozon 拉图 | — | bucket policy 匿名 `GET s3://products/*` |

**分工**：
- 根凭证只在 VPS 初始化时用一次 + 以后改 policy 时用
- 桌面端用专用 AK/SK，只能写不能删/列表，权限面最小
- Ozon 走匿名 HTTP GET，不需要凭证

**AK 泄漏应急**：
1. SSH 上 VPS，`bash /opt/oss/ops/rotate-ak.sh`
2. 该脚本建新 AK 并 disable 旧 AK
3. 桌面端新版本替换 AK，发版

整个过程不需要改阿里云控制台 / 不会有超额账单 / 不影响已上架商品（商品图已在 Ozon CDN）。

## 6. 容量与成本估算

- **单次批量上架**：100 个 SKU × 平均 6 张图 × 平均 2 MB = ~1.2 GB 上传流量
- **日均**：假设每天 10 批次 = ~12 GB 上传 + 少量 Ozon GET（Ozon 只拉一次）≈ **25 GB 日流量**
- **月累计**：~750 GB/月

主流 VPS 产品对照（新加坡节点）：

| 提供商 | 套餐 | 月费 | 流量 | 适配度 |
|---|---|---|---|---|
| RackNerd SG | 1c/1G/25G/2TB | ~$3 | 2 TB/月 | ⭐⭐ 够 |
| BandwagonHost SG | 1c/1G/20G/1TB | ~$5 | 1 TB/月 | ⭐ 紧 |
| CloudCone SG | 2c/2G/50G/5TB | ~$5 | 5 TB/月 | ⭐⭐⭐ 余量足 |
| Vultr Singapore | 1c/1G/25G/1TB | $5 | 1 TB/月 | ⭐ |
| Linode Singapore | 1c/1G/25G/1TB | $5 | 1 TB/月 | ⭐ |

**磁盘**：MinIO lifecycle 7 天清一次，峰值占用 ~7 × 12 GB = **~85 GB**，所以磁盘至少 100 GB。若走 25 GB 的便宜套餐，需要把 lifecycle 降到 3 天。

## 7. 未来扩展方向

如果以后场景变成"桌面端业务还要长期保存原图"（超出本项目的暂存定位），有两条路：

1. **接入阿里云 OSS**：桌面端上传时就按业务分流 —— 临时图走 MinIO、长期素材走 Aliyun OSS（走后端 presign）。
2. **MinIO 启用纠删码 + 多盘**：把单节点改成 4 节点集群（MinIO 原生支持），具备生产级可用性。

目前这两条都不需要，保持单节点 + lifecycle 已足够。
