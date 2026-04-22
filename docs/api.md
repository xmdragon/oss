# 桌面端接入 API 文档

> 面向：智品（ZhiPin）桌面端（Rust/Tauri）开发者
> 目标：把本地图片上传到 OSS 暂存，拿到一个 Ozon 能 GET 的公网 URL

---

## 1. 接入常量（运维带外给）

| 常量 | 值 | 说明 |
|---|---|---|
| `STAGE_ENDPOINT` | `https://oss.hjdtrading.com` | 固定，HTTPS 强制 |
| `STAGE_BUCKET` | `products` | 固定 |
| `STAGE_REGION` | `us-east-1` | MinIO 不校验 region，但 AWS SDK 必须有，写死即可 |
| `STAGE_ACCESS_KEY` | `ozon-desktop` | 带外发给桌面端开发者 |
| `STAGE_SECRET_KEY` | `<32-byte hex>` | **机密**，带外发，禁止提交 git |

> 发 key 规则：不走 IM，不走邮件。用密码管理工具（1Password / Bitwarden）共享条目，或当面口述。

---

## 2. 协议

**完全兼容 AWS S3 API**。用任何 S3 SDK 即可，**必须开 path-style**（MinIO 不支持 virtual-host style 除非配子域名）。

| 语言 | 推荐 SDK |
|---|---|
| Rust | `aws-sdk-s3 = "1"` + `force_path_style(true)` |
| Node/Electron | `@aws-sdk/client-s3` + `forcePathStyle: true` |
| Python | `boto3` + `Config(s3={'addressing_style': 'path'})` |
| Go | `aws-sdk-go-v2/service/s3` + `UsePathStyle = true` |

---

## 3. 对象 key 规范

```
<uuid-v4>.<ext>
例：  b3f4e7a2-9c1d-4e6b-8a7f-2d4c5e6f7a8b.jpg
```

- 用 UUID v4，**不要**用业务 ID（SKU / 商品号）：
  - 避免批量重传时覆盖
  - 避免从 URL 反推业务信息
- `ext` 取原文件扩展名（小写），允许：`jpg`、`jpeg`、`png`、`webp`
- 路径前缀：**无**。对象直接放 bucket 根，让 URL 最短

完整的对象 URL 形如：
```
https://oss.hjdtrading.com/products/b3f4e7a2-9c1d-4e6b-8a7f-2d4c5e6f7a8b.jpg
```

---

## 4. 上传（PUT）

### 4.1 权限

桌面端 AK 仅有 **`s3:PutObject`**。以下动作都会返回 **403 AccessDenied**：
- `GetObject`（不需要，Ozon 匿名 GET）
- `ListBucket`（不需要，也避免对象名被遍历）
- `DeleteObject`（生命周期自动清，不需要手删）
- `HeadObject`（不需要；要确认上传成功看 PUT 的返回码）

### 4.2 HTTP 请求

```
PUT /products/<key>                HTTP/1.1
Host: oss.hjdtrading.com
Authorization: AWS4-HMAC-SHA256 Credential=ozon-desktop/.../..., ...
x-amz-content-sha256: <sha256 of body>
x-amz-date: 20260419T061500Z
Content-Type: image/jpeg
Content-Length: 1234567

<binary body>
```

签名算法 AWS Sig V4（SDK 自动处理）。

### 4.3 返回

- **200**：上传成功。响应 body 空；`ETag` header 是内容 MD5
- **403 AccessDenied**：AK 禁用 / SK 错 / policy 变更 —— **不要重试**，报给用户"请升级客户端版本"
- **400**：请求格式错（SDK bug 可能）
- **413** `EntityTooLarge`：超过 Caddy 100MB 限制（图片 ≤10MB、短视频 ≤100MB）
- **5xx**：MinIO 或网络故障，按 §6 重试

### 4.4 约束

| 项 | 值 |
|---|---|
| 单对象最大 | 100 MB（Caddy 层）。图片建议客户端压缩到 ≤10 MB；视频 ≤100 MB |
| 单次并发 PUT | 建议 ≤5，VPS 上行带宽是瓶颈 |
| `Content-Type` | **必填**，Ozon 会按这个头决定解码；正确填 `image/jpeg`、`image/png`、`image/webp` |
| 对象 key 字符集 | `[A-Za-z0-9._-]`；避免中文、空格、`/` |

---

## 5. Rust 最小可运行示例

`Cargo.toml`：
```toml
[dependencies]
aws-sdk-s3 = "1"
aws-config = "1"
aws-credential-types = "1"
tokio = { version = "1", features = ["full"] }
anyhow = "1"
uuid = { version = "1", features = ["v4"] }
mime_guess = "2"
```

```rust
use aws_sdk_s3::config::{Credentials, Region};
use aws_sdk_s3::primitives::ByteStream;
use aws_sdk_s3::{Client, Config};
use std::path::Path;

const ENDPOINT: &str = "https://oss.hjdtrading.com";
const BUCKET:   &str = "products";
// AK/SK 从编译期 env! 注入（build.rs 从 env / .env.production 读取）
const AK: &str = env!("STAGE_ACCESS_KEY");
const SK: &str = env!("STAGE_SECRET_KEY");

pub fn build_client() -> Client {
    let creds = Credentials::new(AK, SK, None, None, "static");
    let conf = Config::builder()
        .endpoint_url(ENDPOINT)
        .region(Region::new("us-east-1"))
        .credentials_provider(creds)
        .force_path_style(true)          // ⭐ MinIO 必须
        .behavior_version_latest()
        .build();
    Client::from_conf(conf)
}

/// 上传一张图，返回可被 Ozon GET 的公网 URL
pub async fn upload(s3: &Client, path: &Path) -> anyhow::Result<String> {
    let bytes = tokio::fs::read(path).await?;
    if bytes.len() > 10 * 1024 * 1024 {
        anyhow::bail!("图片超过 10 MB，请客户端先压缩");
    }
    let ext = path.extension().and_then(|s| s.to_str())
        .map(|s| s.to_ascii_lowercase()).unwrap_or_else(|| "jpg".into());
    let key = format!("{}.{}", uuid::Uuid::new_v4(), ext);
    let ct = mime_guess::from_path(path).first_or_octet_stream().to_string();

    s3.put_object()
        .bucket(BUCKET).key(&key)
        .body(ByteStream::from(bytes))
        .content_type(ct)
        .send().await?;

    Ok(format!("{}/{}/{}", ENDPOINT, BUCKET, key))
}
```

批量并发（限 5）：

```rust
use futures::stream::{self, StreamExt};

pub async fn upload_batch(s3: &Client, paths: Vec<std::path::PathBuf>)
    -> anyhow::Result<Vec<String>>
{
    let r: Vec<_> = stream::iter(paths)
        .map(|p| { let s3 = s3.clone(); async move { upload(&s3, &p).await } })
        .buffer_unordered(5)
        .collect().await;
    r.into_iter().collect()
}
```

---

## 6. 重试策略

| 错误 | 重试？ | 退避 |
|---|---|---|
| 连接超时 / `DispatchFailure` | ✅ 最多 3 次 | 2s、4s、8s |
| 5xx | ✅ 最多 3 次 | 同上 |
| 403 | ❌ 不重试，弹窗提示"联系管理员更新客户端" |
| 4xx（413/400）| ❌ 本地预校验问题 |

---

## 7. 生命周期心智模型

```
t=0        桌面端 PUT → 拿到 URL，塞给智品后端
t=0~5min   后端调 Ozon /v1/product/pictures/import
           Ozon 异步 GET 我们的 URL，转存到它自己的 CDN
t=5min~1h  商品上架成功，图片已在 cdn1.ozone.ru
t=7day     MinIO lifecycle 自动删 /products/<key>
```

**重要：桌面端拿到的 URL 只保证 7 天内可访问**，任何需要"永久引用"的场景都该用 Ozon 返回的 CDN URL，不要用本服务的 URL。

---

## 8. 故障回退（可选）

建议桌面端同时保留"通过智品后端中转上传"的老通道（例如 `POST /ozon/listings/media/upload-file`），在直传失败时作为 fallback：

```rust
async fn upload_with_fallback(...) -> anyhow::Result<String> {
    match upload_to_stage(&s3, &path).await {
        Ok(url) => Ok(url),
        Err(e) => {
            log::warn!("stage upload failed: {e}, falling back to server relay");
            upload_via_server(&token, &path).await
        }
    }
}
```

正常永远走 stage 直传；stage 挂了不阻断业务。

---

## 9. curl 自测（不依赖 SDK）

上传（要装 `aws-cli` 或用 `mc`；纯 curl 做 Sig V4 很烦，这里用 mc）：

```bash
# 本地装 mc (macOS: brew install minio/stable/mc)
mc alias set stage https://oss.hjdtrading.com ozon-desktop <SK>
mc cp ~/test.jpg stage/products/$(uuidgen).jpg
```

拉图（匿名）：
```bash
curl -I https://oss.hjdtrading.com/products/<key>
# HTTP/2 200
# content-type: image/jpeg
# content-length: 1234567
```

浏览器直接访问同一 URL 应能看到图片。

---

## 10. 安全与合规

- **AK/SK 机密级**：禁止进公共仓库 / 截图外发；泄漏立刻通知运维轮换
- **CORS**：服务端已开 `Access-Control-Allow-Origin: *`（Ozon 是后端拉图，不过桌面端如果是 Tauri/Electron webview 调 SDK 也不受限）
- **HTTPS 强制**：所有 HTTP 请求会被 Caddy 301 到 HTTPS
- **内容合规**：桌面端负责上传前校验（图片格式、尺寸、大小），服务端不做内容审查

---

## 11. 运维联系

- 服务异常：查 https://oss.hjdtrading.com 是否可达
- 升级请求（改配额、改生命周期、轮换 AK）：找运维人
- 历史变更见 `docs/operations.md`
