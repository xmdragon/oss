// Package config loads runtime configuration from /opt/oss/.env (shared with
// minio.service) and /opt/oss/admin.env (oss-admin specific). Both files use
// KEY=VALUE syntax with optional double-quoted values.
package config

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	// from /opt/oss/.env
	MinIORootUser     string
	MinIORootPassword string
	BucketName        string
	PublicHost        string
	AdminHost         string
	LifecycleDays     int

	// from /opt/oss/admin.env
	AdminUsername     string // default "grom"
	AdminPasswordHash string // argon2id encoded form ($argon2id$...)
	AdminTOTPSecret   string // base32; empty = TOTP disabled
	SessionSecret     []byte // 32 bytes, hex-decoded from SESSION_SECRET
	ListenAddr        string // default 127.0.0.1:9002
	AuditLogPath      string // default /var/log/oss-admin/audit.log
}

// Load reads both env files. Missing admin.env is not an error here — the
// `setup` subcommand creates it; runtime serve will refuse to start without it.
func Load(ossEnvPath, adminEnvPath string) (*Config, error) {
	ossEnv, err := readEnvFile(ossEnvPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", ossEnvPath, err)
	}
	adminEnv, _ := readEnvFile(adminEnvPath) // optional for setup mode

	c := &Config{
		MinIORootUser:     ossEnv["MINIO_ROOT_USER"],
		MinIORootPassword: ossEnv["MINIO_ROOT_PASSWORD"],
		BucketName:        ossEnv["BUCKET_NAME"],
		PublicHost:        ossEnv["PUBLIC_HOST"],
		AdminHost:         ossEnv["ADMIN_HOST"],
		AdminUsername:     adminEnv["ADMIN_USERNAME"],
		AdminPasswordHash: adminEnv["ADMIN_PASSWORD_HASH"],
		AdminTOTPSecret:   adminEnv["ADMIN_TOTP_SECRET"],
		ListenAddr:        adminEnv["LISTEN_ADDR"],
		AuditLogPath:      adminEnv["AUDIT_LOG_PATH"],
	}
	if days, ok := ossEnv["LIFECYCLE_EXPIRE_DAYS"]; ok && days != "" {
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err == nil {
			c.LifecycleDays = n
		}
	}
	if s := adminEnv["SESSION_SECRET"]; s != "" {
		b, err := hex.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("SESSION_SECRET not hex: %w", err)
		}
		c.SessionSecret = b
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:9002"
	}
	if c.AuditLogPath == "" {
		c.AuditLogPath = "/var/log/oss-admin/audit.log"
	}
	return c, nil
}

// ValidateForServe checks the fields needed by the HTTP server.
// `setup`/`setpw` skip this since they're allowed to bootstrap a missing admin.env.
func (c *Config) ValidateForServe() error {
	miss := func(k string) error { return fmt.Errorf("missing %s", k) }
	switch {
	case c.MinIORootUser == "":
		return miss("MINIO_ROOT_USER")
	case c.MinIORootPassword == "":
		return miss("MINIO_ROOT_PASSWORD")
	case c.BucketName == "":
		return miss("BUCKET_NAME")
	case c.AdminUsername == "":
		return miss("ADMIN_USERNAME (run: oss-admin setup)")
	case c.AdminPasswordHash == "":
		return miss("ADMIN_PASSWORD_HASH (run: oss-admin setup)")
	case len(c.SessionSecret) < 32:
		return fmt.Errorf("SESSION_SECRET too short (need 32 bytes hex, got %d)", len(c.SessionSecret))
	}
	return nil
}

// readEnvFile is a minimal KEY=VALUE parser that mirrors what bash `source` and
// systemd `EnvironmentFile=` will accept for the keys we read. It deliberately
// does NOT do shell expansion — values are returned literal.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		// strip wrapping double quotes (matches systemd / bash source semantics)
		if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out, sc.Err()
}
