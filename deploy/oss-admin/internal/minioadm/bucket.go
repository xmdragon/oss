package minioadm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/minio/madmin-go/v3"
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
