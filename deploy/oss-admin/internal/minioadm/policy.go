package minioadm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Permission encodes the access level for a scoped policy. The string values
// are stable identifiers used in policy names and the audit log.
type Permission string

const (
	PermWrite     Permission = "w"  // PutObject only
	PermRead      Permission = "r"  // GetObject + ListBucket
	PermReadWrite Permission = "rw" // both
)

// IsValid returns true for the three supported levels.
func (p Permission) IsValid() bool {
	switch p {
	case PermWrite, PermRead, PermReadWrite:
		return true
	}
	return false
}

func (p Permission) human() string {
	switch p {
	case PermWrite:
		return "write-only"
	case PermRead:
		return "read-only"
	case PermReadWrite:
		return "read+write"
	}
	return string(p)
}

// scopedPolicyNameRE allows the same characters MinIO accepts for policy
// names. Bucket names already match a stricter regex, so the only thing left
// to guard is unexpected input slipping past the bucket validator.
var scopedPolicyNameRE = regexp.MustCompile(`^[a-zA-Z0-9_.\-]+$`)

// ScopedPolicyName returns the deterministic name used for an
// (bucket, permission) tuple. Stable so repeated calls don't pile up policies.
func ScopedPolicyName(bucket string, p Permission) string {
	return fmt.Sprintf("scoped-%s-%s", p, bucket)
}

// EnsureScopedPolicy idempotently creates a canned policy granting `perm` on
// `bucket` and returns its name. If the policy already exists with matching
// content it is reused; if its content differs it is overwritten so the UI
// stays the source of truth.
func (c *Client) EnsureScopedPolicy(ctx context.Context, bucket string, perm Permission) (string, error) {
	if !bucketNameRE.MatchString(bucket) {
		return "", fmt.Errorf("invalid bucket %q", bucket)
	}
	if !perm.IsValid() {
		return "", fmt.Errorf("invalid permission %q", perm)
	}
	name := ScopedPolicyName(bucket, perm)
	if !scopedPolicyNameRE.MatchString(name) {
		return "", fmt.Errorf("computed policy name %q is unsafe", name)
	}
	doc := buildScopedPolicy(bucket, perm)
	if err := c.Admin.AddCannedPolicy(ctx, name, []byte(doc)); err != nil {
		return "", fmt.Errorf("AddCannedPolicy %s: %w", name, err)
	}
	return name, nil
}

// buildScopedPolicy returns a properly-escaped policy JSON document granting
// `perm` on `bucket`. encoding/json takes care of escaping bucket name into
// the resource ARN, so callers don't need to pre-validate (we do anyway).
func buildScopedPolicy(bucket string, perm Permission) string {
	type stmt struct {
		Effect   string   `json:"Effect"`
		Action   []string `json:"Action"`
		Resource []string `json:"Resource"`
	}
	type doc struct {
		Version   string `json:"Version"`
		Statement []stmt `json:"Statement"`
	}

	bucketARN := "arn:aws:s3:::" + bucket
	objectARN := bucketARN + "/*"

	var stmts []stmt
	if perm == PermWrite || perm == PermReadWrite {
		stmts = append(stmts, stmt{
			Effect:   "Allow",
			Action:   []string{"s3:PutObject"},
			Resource: []string{objectARN},
		})
	}
	if perm == PermRead || perm == PermReadWrite {
		stmts = append(stmts, stmt{
			Effect:   "Allow",
			Action:   []string{"s3:GetBucketLocation", "s3:ListBucket"},
			Resource: []string{bucketARN},
		})
		stmts = append(stmts, stmt{
			Effect:   "Allow",
			Action:   []string{"s3:GetObject"},
			Resource: []string{objectARN},
		})
	}
	b, _ := json.MarshalIndent(doc{Version: "2012-10-17", Statement: stmts}, "", "  ")
	// Trim trailing newline json.MarshalIndent doesn't emit, but be defensive.
	return strings.TrimSpace(string(b))
}
