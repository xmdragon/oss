# 桌面端集成指南

## 1. 客户端需要知道什么

部署完成后，只需 **4 个常量** 就能集成（在 Tauri / Rust 端硬编码或编译时注入）：

| 常量 | 来源 | 示例 |
|---|---|---|
| `STAGE_ENDPOINT` | 你的公网域名 | `https://img-stage.example.com` |
| `STAGE_ACCESS_KEY` | `.env` 的 `DESKTOP_ACCESS_KEY` | `ozon-desktop` |
| `STAGE_SECRET_KEY` | `.env` 的 `DESKTOP_SECRET_KEY` | 32 位随机 |
| `STAGE_BUCKET` | `.env` 的 `BUCKET_NAME` | `products` |

**凭证管理建议**：
- 把 `STAGE_SECRET_KEY` 放在 Tauri 构建配置里（`src-tauri/.env.production` + `.gitignore`），**不要提交 git**
- Rust 端用 `env!("STAGE_SECRET_KEY")` 编译期注入
- 泄漏了就在 VPS 换一组 key，桌面端发新版带新 key（不影响已上架的 Ozon 商品）

## 2. Rust 依赖

`src-tauri/Cargo.toml`：

```toml
[dependencies]
# 官方 AWS SDK，支持 MinIO（S3 兼容）
aws-sdk-s3 = "1"
aws-config = "1"
aws-credential-types = "1"

# 或更轻量的社区 crate（可选）
# rust-s3 = "0.34"

tokio = { version = "1", features = ["full"] }
anyhow = "1"
uuid = { version = "1", features = ["v4"] }
mime_guess = "2"
```

## 3. 最小可运行代码

`src-tauri/src/image_stage.rs`：

```rust
use aws_sdk_s3::config::{Credentials, Region};
use aws_sdk_s3::{Client, Config};
use aws_sdk_s3::primitives::ByteStream;
use std::path::Path;

const STAGE_ENDPOINT: &str = env!("STAGE_ENDPOINT");
const STAGE_ACCESS_KEY: &str = env!("STAGE_ACCESS_KEY");
const STAGE_SECRET_KEY: &str = env!("STAGE_SECRET_KEY");
const STAGE_BUCKET: &str = env!("STAGE_BUCKET");

/// 构建 S3 客户端（进程生命周期内复用）
pub fn build_client() -> Client {
    let creds = Credentials::new(
        STAGE_ACCESS_KEY,
        STAGE_SECRET_KEY,
        None, None,
        "static-stage",
    );
    let conf = Config::builder()
        .endpoint_url(STAGE_ENDPOINT)
        .region(Region::new("us-east-1"))   // MinIO 不关心 region，但 SDK 要求有
        .credentials_provider(creds)
        .force_path_style(true)              // ⭐ MinIO 必须：bucket 作为 URL path
        .behavior_version_latest()
        .build();
    Client::from_conf(conf)
}

/// 上传一张本地图片，返回可被 Ozon 拉取的公网 URL
pub async fn upload_image(
    s3: &Client,
    local_path: &Path,
) -> anyhow::Result<String> {
    // 1. 读文件
    let bytes = tokio::fs::read(local_path).await?;
    if bytes.len() > 10 * 1024 * 1024 {
        anyhow::bail!("图片超过 10 MB");
    }

    // 2. 生成对象 key: products/<uuid>.<ext>
    let ext = local_path.extension()
        .and_then(|s| s.to_str())
        .unwrap_or("jpg")
        .to_lowercase();
    let key = format!("{}.{}", uuid::Uuid::new_v4(), ext);
    let content_type = mime_guess::from_path(local_path)
        .first_or_octet_stream()
        .to_string();

    // 3. PUT
    s3.put_object()
        .bucket(STAGE_BUCKET)
        .key(&key)
        .body(ByteStream::from(bytes))
        .content_type(&content_type)
        .send()
        .await?;

    // 4. 返回公开 URL（路径风格）
    Ok(format!("{}/{}/{}", STAGE_ENDPOINT, STAGE_BUCKET, key))
}

/// 批量并发上传
pub async fn upload_batch(
    s3: &Client,
    paths: Vec<std::path::PathBuf>,
) -> anyhow::Result<Vec<String>> {
    use futures::stream::{self, StreamExt};

    let results: Vec<_> = stream::iter(paths)
        .map(|p| {
            let s3 = s3.clone();
            async move { upload_image(&s3, &p).await }
        })
        .buffer_unordered(5)   // 并发 5 条够了，VPS 带宽再高也别开太高
        .collect()
        .await;

    results.into_iter().collect::<Result<Vec<_>, _>>()
}
```

## 4. Tauri 编译期注入凭证

`src-tauri/build.rs`：

```rust
fn main() {
    // 从 env 或 .env.production 读
    let endpoint = std::env::var("STAGE_ENDPOINT")
        .expect("STAGE_ENDPOINT must be set at build time");
    let ak = std::env::var("STAGE_ACCESS_KEY")
        .expect("STAGE_ACCESS_KEY must be set at build time");
    let sk = std::env::var("STAGE_SECRET_KEY")
        .expect("STAGE_SECRET_KEY must be set at build time");
    let bucket = std::env::var("STAGE_BUCKET")
        .expect("STAGE_BUCKET must be set at build time");

    println!("cargo:rustc-env=STAGE_ENDPOINT={}", endpoint);
    println!("cargo:rustc-env=STAGE_ACCESS_KEY={}", ak);
    println!("cargo:rustc-env=STAGE_SECRET_KEY={}", sk);
    println!("cargo:rustc-env=STAGE_BUCKET={}", bucket);

    tauri_build::build();
}
```

CI 构建前：

```bash
export STAGE_ENDPOINT=https://img-stage.example.com
export STAGE_ACCESS_KEY=ozon-desktop
export STAGE_SECRET_KEY=<secret>
export STAGE_BUCKET=products
pnpm tauri build
```

或本地开发用 `src-tauri/.env.production`（放 `.gitignore`）配 `dotenvy`。

## 5. 与智品后端 batch-listing 的对接

上传成功后，把返回的 URL 数组直接塞进批量上架请求：

```rust
let image_urls = upload_batch(&s3, user_selected_files).await?;

let resp: ApiResp<serde_json::Value> = http_client
    .post("https://euraflow.hjdtrading.cn/api/ef/v1/desktop/batch-listing/create")
    .bearer_auth(&token)
    .json(&serde_json::json!({
        "shop_ids": [shop_id],
        "warehouse_ids": [warehouse_id],
        "stock": 10,
        "upload_interval": 2,
        "items": [
            {
                "sku": "123456789",
                "price": "100.50",
                "images": image_urls,   // ⭐ 直接给后端
            }
        ]
    }))
    .send().await?.json().await?;
```

后端 `/desktop/batch-listing/create` 会把 `items[].images` 原样透传给 Ozon 的 `/v1/product/pictures/import`，Ozon 收到后会主动 GET 这些 URL。

## 6. 生命周期心智模型

```
 t=0        桌面端 PUT https://img-stage.example.com/products/abc.jpg
            后端 POST /ozon/batch-listing/create  {images: [...]}

 t=0~5min   Ozon /v1/product/pictures/import 返回 task_id
            Ozon 后台 GET https://img-stage.example.com/products/abc.jpg
            Ozon 转存到 cdn1.ozone.ru

 t=5min~1h  商品在 Ozon 上线，图片已在 Ozon CDN

 t=7day     MinIO lifecycle 自动删除 /products/abc.jpg
            （此时 Ozon 早已有自己的副本，无影响）
```

**重要**：桌面端不要对暂存 URL 做长期依赖，任何需要"永久链接"的场景（比如历史商品审计）都应该引用 Ozon CDN URL，而不是我们的 stage URL。

## 7. 错误处理要点

| 场景 | SDK 返回 | 处理建议 |
|---|---|---|
| 网络断开 | `SdkError::DispatchFailure` | 最多重试 3 次，每次间隔 2s |
| 403 `AccessDenied` | AK/SK 被停用或 policy 错 | 不要重试，提示用户"联系管理员更新版本" |
| 503 `SlowDown` | MinIO 限速（几乎不会） | 重试 |
| 413 | 文件超 MinIO 限制（默认 5 GiB，远大于 10 MB） | 本地预校验不应该到这步 |

## 8. 回退路径

桌面端应同时保留调用智品后端中转上传（`/ozon/listings/media/upload-file`）的能力，作为 stage 服务挂掉时的 fallback：

```rust
async fn upload_with_fallback(...) -> anyhow::Result<String> {
    match try_upload_to_stage(&s3, &path).await {
        Ok(url) => Ok(url),
        Err(e) => {
            log::warn!("stage upload failed: {e}, falling back to server");
            upload_via_server(&token, &path).await
        }
    }
}
```

正常情况下永远走 stage 直传；出故障时自动切到慢速中转保障业务不中断。
