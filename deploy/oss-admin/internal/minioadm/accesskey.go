package minioadm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/minio/madmin-go/v3"
)

type AccessKey struct {
	AccessKey string
	Policies  []string // attached policy names
	Enabled   bool
	IsDesktop bool // true when PolicyDesktopAK is in Policies
}

// NewAccessKey is what we hand back to the UI right after creating an AK —
// SecretKey is shown ONCE.
type NewAccessKey struct {
	AccessKey string
	SecretKey string
}

// ListAccessKeys returns all MinIO users (built-in IDP) with their attached
// policies. Filtering "desktop" AKs is a UI concern via IsDesktop.
func (c *Client) ListAccessKeys(ctx context.Context) ([]AccessKey, error) {
	users, err := c.Admin.ListUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListUsers: %w", err)
	}
	out := make([]AccessKey, 0, len(users))
	for ak, info := range users {
		policies := splitPolicies(info.PolicyName)
		out = append(out, AccessKey{
			AccessKey: ak,
			Policies:  policies,
			Enabled:   info.Status == madmin.AccountEnabled,
			IsDesktop: containsPolicy(policies, PolicyDesktopAK),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccessKey < out[j].AccessKey })
	return out, nil
}

// CreateAK creates a new MinIO user and attaches the named policy. SecretKey
// is generated server-side (48 hex chars) if not provided. The returned
// NewAccessKey.SecretKey is the only time the plaintext is exposed.
//
// Partial-failure cleanup: if AttachPolicy fails after AddUser, the user is
// removed so we never leave an AK with no policy attached.
func (c *Client) CreateAK(ctx context.Context, accessKey, secretKey, policy string) (NewAccessKey, error) {
	if accessKey == "" {
		return NewAccessKey{}, fmt.Errorf("access key required")
	}
	if policy == "" {
		return NewAccessKey{}, fmt.Errorf("policy required")
	}
	if secretKey == "" {
		secretKey = randomHex(24) // matches openssl rand -hex 24 in install.sh
	}
	if err := c.Admin.AddUser(ctx, accessKey, secretKey); err != nil {
		return NewAccessKey{}, fmt.Errorf("AddUser: %w", err)
	}
	if _, err := c.Admin.AttachPolicy(ctx, madmin.PolicyAssociationReq{
		Policies: []string{policy},
		User:     accessKey,
	}); err != nil {
		// Best effort cleanup so we don't leave a user with no policy.
		_ = c.Admin.RemoveUser(ctx, accessKey)
		return NewAccessKey{}, fmt.Errorf("AttachPolicy: %w", err)
	}
	return NewAccessKey{AccessKey: accessKey, SecretKey: secretKey}, nil
}

// CreateDesktopAK is a convenience wrapper that always attaches PolicyDesktopAK.
// Kept so the rotate flow stays callable with one fewer argument.
func (c *Client) CreateDesktopAK(ctx context.Context, accessKey, secretKey string) (NewAccessKey, error) {
	return c.CreateAK(ctx, accessKey, secretKey, PolicyDesktopAK)
}

// ListPolicies returns the canned policy names known to MinIO. Used by the AK
// create form to populate the policy dropdown.
func (c *Client) ListPolicies(ctx context.Context) ([]string, error) {
	policies, err := c.Admin.ListCannedPolicies(ctx)
	if err != nil {
		return nil, fmt.Errorf("ListCannedPolicies: %w", err)
	}
	out := make([]string, 0, len(policies))
	for name := range policies {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// SetEnabled flips a user's enable/disable flag.
func (c *Client) SetEnabled(ctx context.Context, accessKey string, enabled bool) error {
	status := madmin.AccountDisabled
	if enabled {
		status = madmin.AccountEnabled
	}
	return c.Admin.SetUserStatus(ctx, accessKey, status)
}

// DeleteAK removes a user entirely. Caller is responsible for confirming.
func (c *Client) DeleteAK(ctx context.Context, accessKey string) error {
	return c.Admin.RemoveUser(ctx, accessKey)
}

// RotateDesktopAK creates a new desktop AK and disables the old one. We do NOT
// delete the old one — handler/operator must confirm clients have rolled over
// before purging. Mirrors the rotate-ak.sh policy.
func (c *Client) RotateDesktopAK(ctx context.Context, oldAK, newAK string) (NewAccessKey, error) {
	created, err := c.CreateAK(ctx, newAK, "", PolicyDesktopAK)
	if err != nil {
		return NewAccessKey{}, err
	}
	if err := c.SetEnabled(ctx, oldAK, false); err != nil {
		return created, fmt.Errorf("created %s but failed to disable %s: %w", newAK, oldAK, err)
	}
	return created, nil
}

func splitPolicies(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containsPolicy(list []string, want string) bool {
	for _, p := range list {
		if p == want {
			return true
		}
	}
	return false
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failing is catastrophic; better to crash than to emit a
		// predictable secret.
		panic(fmt.Sprintf("crypto/rand: %v", err))
	}
	return hex.EncodeToString(b)
}
