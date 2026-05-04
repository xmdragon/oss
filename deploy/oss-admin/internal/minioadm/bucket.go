package minioadm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/minio/madmin-go/v3"
	"github.com/minio/minio-go/v7"
)

type BucketSummary struct {
	Name         string
	Size         uint64 // bytes
	ObjectCount  uint64
	HasLifecycle bool
}

// ListBuckets returns every bucket on the cluster with size/count from
// AccountInfo (cheap — single admin call).
func (c *Client) ListBuckets(ctx context.Context) ([]BucketSummary, error) {
	info, err := c.Admin.AccountInfo(ctx, madmin.AccountOpts{})
	if err != nil {
		return nil, fmt.Errorf("AccountInfo: %w", err)
	}
	out := make([]BucketSummary, 0, len(info.Buckets))
	for _, b := range info.Buckets {
		out = append(out, BucketSummary{
			Name:         b.Name,
			Size:         b.Size,
			ObjectCount:  b.Objects,
			HasLifecycle: hasLifecycleRules(ctx, c, b.Name),
		})
	}
	return out, nil
}

func hasLifecycleRules(ctx context.Context, c *Client, bucket string) bool {
	cfg, err := c.S3.GetBucketLifecycle(ctx, bucket)
	if err != nil || cfg == nil {
		return false
	}
	return len(cfg.Rules) > 0
}

// bucketNameRE mirrors the AWS S3 bucket-name rules MinIO actually enforces:
// 3-63 chars, lowercase alphanumeric or `.`/`-`, must start/end with alphanum.
// We pre-validate before hitting MinIO so the UI shows a clean error.
var bucketNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]{1,61}[a-z0-9]$`)

// CreateBucket makes a new bucket. When applyDefaults is true the bucket gets
// the same shape init-bucket.sh produces for `products`: anonymous download
// policy + a single expiration lifecycle rule of `lifecycleDays` days. Caller
// supplies the days value (typically from .env LIFECYCLE_EXPIRE_DAYS) to keep
// minioadm free of config-loading concerns.
func (c *Client) CreateBucket(ctx context.Context, name string, applyDefaults bool, lifecycleDays int) error {
	if !bucketNameRE.MatchString(name) {
		return fmt.Errorf("invalid bucket name %q (3-63 chars, lowercase, [a-z0-9.-], starts/ends alphanumeric)", name)
	}
	exists, err := c.S3.BucketExists(ctx, name)
	if err != nil {
		return fmt.Errorf("BucketExists: %w", err)
	}
	if exists {
		return fmt.Errorf("bucket %q already exists", name)
	}
	if err := c.S3.MakeBucket(ctx, name, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("MakeBucket: %w", err)
	}
	if !applyDefaults {
		return nil
	}
	// Anonymous download policy — same statements as `mc anonymous set download`.
	policy := anonymousDownloadPolicy(name)
	if err := c.S3.SetBucketPolicy(ctx, name, policy); err != nil {
		return fmt.Errorf("SetBucketPolicy: %w", err)
	}
	if lifecycleDays > 0 {
		if err := c.SaveLifecycle(ctx, name, []Rule{{
			ID:         "rule-1",
			Status:     "Enabled",
			ExpiryDays: lifecycleDays,
		}}); err != nil {
			return fmt.Errorf("SaveLifecycle: %w", err)
		}
	}
	return nil
}

func anonymousDownloadPolicy(bucket string) string {
	tmpl := `{
  "Version": "2012-10-17",
  "Statement": [
    {"Effect": "Allow", "Principal": {"AWS": ["*"]},
     "Action": ["s3:GetBucketLocation", "s3:ListBucket"],
     "Resource": ["arn:aws:s3:::%[1]s"]},
    {"Effect": "Allow", "Principal": {"AWS": ["*"]},
     "Action": ["s3:GetObject"],
     "Resource": ["arn:aws:s3:::%[1]s/*"]}
  ]
}`
	return fmt.Sprintf(tmpl, bucket)
}

// BucketPolicyJSON returns the bucket policy as pretty-printed JSON. Empty
// string if no policy is attached.
func (c *Client) BucketPolicyJSON(ctx context.Context, bucket string) (string, error) {
	raw, err := c.S3.GetBucketPolicy(ctx, bucket)
	if err != nil {
		// MinIO returns "no such bucket policy" — surface as empty, not error
		return "", nil
	}
	if raw == "" {
		return "", nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw, nil // already not JSON, return as-is
	}
	pretty, _ := json.MarshalIndent(v, "", "  ")
	return string(pretty), nil
}
