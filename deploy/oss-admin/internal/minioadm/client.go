// Package minioadm wraps minio-go (S3 ops) and madmin-go (admin API) into the
// narrow interface used by oss-admin. We intentionally do NOT shell out to mc
// — direct API calls are faster and don't depend on a binary on PATH.
package minioadm

import (
	"context"
	"fmt"
	"net/url"

	"github.com/minio/madmin-go/v3"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	// PolicyDesktopAK identifies access keys created for the desktop client.
	// Mirrors what init-bucket.sh attaches.
	PolicyDesktopAK = "put-only"
)

type Client struct {
	S3     *minio.Client
	Admin  *madmin.AdminClient
	Bucket string
	// Endpoint is the host:port we connect to (always 127.0.0.1:9000 in
	// production — both clients are loopback-only).
	Endpoint string
}

// New wires both SDK clients against the local MinIO instance.
func New(endpoint, accessKey, secretKey, bucket string) (*Client, error) {
	if endpoint == "" {
		endpoint = "127.0.0.1:9000"
	}
	s3, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false, // loopback HTTP — TLS is terminated at Caddy
	})
	if err != nil {
		return nil, fmt.Errorf("minio.New: %w", err)
	}
	admin, err := madmin.New(endpoint, accessKey, secretKey, false)
	if err != nil {
		return nil, fmt.Errorf("madmin.New: %w", err)
	}
	return &Client{S3: s3, Admin: admin, Bucket: bucket, Endpoint: endpoint}, nil
}

// Healthy returns nil if the MinIO admin endpoint responds. Used by /healthz.
func (c *Client) Healthy(ctx context.Context) error {
	_, err := c.Admin.ServerInfo(ctx)
	return err
}

// PublicURL builds a viewer-side URL for an object. Matches the pattern docs
// promise to the desktop client (anonymous GET on PUBLIC_HOST).
func PublicURL(publicHost, bucket, key string) string {
	u := &url.URL{Scheme: "https", Host: publicHost, Path: "/" + bucket + "/" + key}
	return u.String()
}
