// Command oss-admin is the lightweight management console for the Ozon OSS
// MinIO deployment. It exposes a tiny web UI on 127.0.0.1 for lifecycle, AK
// and bucket management; Caddy terminates TLS and proxies in.
//
// Subcommands:
//
//	oss-admin              start HTTP server (alias of `serve`)
//	oss-admin serve
//	oss-admin setup        interactive bootstrap of /opt/oss/admin.env
//	oss-admin setpw        change admin password only
//	oss-admin version
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"github.com/xmdragon/oss/deploy/oss-admin/internal/audit"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/auth"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/config"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/handlers"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/minioadm"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/web"
)

var version = "dev"

const (
	defaultOSSEnv   = "/opt/oss/.env"
	defaultAdminEnv = "/opt/oss/admin.env"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		runServe()
	case "setup":
		runSetup(false)
	case "setpw":
		runSetup(true)
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		fmt.Println("usage: oss-admin [serve|setup|setpw|version]")
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", cmd)
		os.Exit(2)
	}
}

func runServe() {
	cfg, err := config.Load(defaultOSSEnv, defaultAdminEnv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := cfg.ValidateForServe(); err != nil {
		log.Fatalf("config invalid: %v", err)
	}

	mc, err := minioadm.New("127.0.0.1:9000", cfg.MinIORootUser, cfg.MinIORootPassword, cfg.BucketName)
	if err != nil {
		log.Fatalf("minio client: %v", err)
	}

	auditLog, err := audit.New(cfg.AuditLogPath)
	if err != nil {
		log.Fatalf("audit log: %v", err)
	}
	defer auditLog.Close()

	tmpl, err := web.ParseTemplates()
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	sampler := minioadm.NewSampler(mc, time.Minute, 24*time.Hour)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go sampler.Run(ctx)

	limiter := auth.NewLoginLimiter(5, 15*time.Minute)
	go limiter.RunGC(ctx, 5*time.Minute)

	srv := &handlers.Server{
		Cfg:      cfg,
		Tmpl:     tmpl,
		MC:       mc,
		Sampler:  sampler,
		Sessions: auth.NewManager(cfg.SessionSecret),
		Limiter:  limiter,
		Audit:    auditLog,
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("oss-admin %s listening on %s (bucket=%s)", version, cfg.ListenAddr, cfg.BucketName)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	log.Println("oss-admin stopped")
}

// runSetup writes /opt/oss/admin.env. With pwOnly=true, only the password line
// is updated and other values are preserved.
func runSetup(pwOnly bool) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		log.Fatal("setup must be run on an interactive terminal")
	}

	existing, _ := readKVFile(defaultAdminEnv)
	if pwOnly && len(existing) == 0 {
		log.Fatalf("%s does not exist yet — run `oss-admin setup` first", defaultAdminEnv)
	}

	username := existing["ADMIN_USERNAME"]
	if !pwOnly {
		username = prompt("管理员用户名 [grom]: ")
		if username == "" {
			username = "grom"
		}
	}

	pw := promptPassword("密码 (≥10 字符): ")
	if len(pw) < 10 {
		log.Fatal("密码太短")
	}
	pw2 := promptPassword("再输一次: ")
	if pw != pw2 {
		log.Fatal("两次输入不一致")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		log.Fatalf("hash: %v", err)
	}

	totpSecret := existing["ADMIN_TOTP_SECRET"]
	if !pwOnly {
		ans := prompt("启用 TOTP 二步验证？强烈推荐 [Y/n]: ")
		if ans == "" || strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes") {
			s, url, err := auth.GenerateTOTP("OSS Admin", username)
			if err != nil {
				log.Fatalf("totp: %v", err)
			}
			totpSecret = s
			fmt.Println("\n用任意 TOTP 应用扫描下面的二维码（或手动输入 secret）:")
			fmt.Println()
			qrterminal.GenerateHalfBlock(url, qrterminal.M, os.Stdout)
			fmt.Printf("\nSecret (Base32): %s\n", s)
			fmt.Printf("otpauth URL    : %s\n\n", url)
			fmt.Print("已扫描成功？回车继续 ")
			_ = prompt("")
		} else {
			totpSecret = ""
		}
	}

	sessionSecret := existing["SESSION_SECRET"]
	if !pwOnly || sessionSecret == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			log.Fatalf("rand: %v", err)
		}
		sessionSecret = hex.EncodeToString(b)
	}

	listenAddr := existing["LISTEN_ADDR"]
	if listenAddr == "" {
		listenAddr = "127.0.0.1:9002"
	}
	auditPath := existing["AUDIT_LOG_PATH"]
	if auditPath == "" {
		auditPath = "/var/log/oss-admin/audit.log"
	}

	out := strings.Builder{}
	out.WriteString("# /opt/oss/admin.env — 由 oss-admin setup 生成，chmod 600\n")
	out.WriteString("# 不要提交 git。SESSION_SECRET 改了会让所有人重登。\n")
	fmt.Fprintf(&out, "ADMIN_USERNAME=%s\n", username)
	fmt.Fprintf(&out, "ADMIN_PASSWORD_HASH=%s\n", hash)
	fmt.Fprintf(&out, "ADMIN_TOTP_SECRET=%s\n", totpSecret)
	fmt.Fprintf(&out, "SESSION_SECRET=%s\n", sessionSecret)
	fmt.Fprintf(&out, "LISTEN_ADDR=%s\n", listenAddr)
	fmt.Fprintf(&out, "AUDIT_LOG_PATH=%s\n", auditPath)

	if err := os.WriteFile(defaultAdminEnv, []byte(out.String()), 0o600); err != nil {
		log.Fatalf("write %s: %v", defaultAdminEnv, err)
	}
	// When run via sudo (root) on the VPS the file ends up root:root and the
	// oss-admin systemd user can't read it. Resolve oss-admin uid/gid via
	// /etc/passwd and chown accordingly. Skipped when not root.
	if os.Geteuid() == 0 {
		if err := chownToOSSAdmin(defaultAdminEnv); err != nil {
			fmt.Fprintf(os.Stderr, "warn: chown %s to oss-admin failed: %v\n", defaultAdminEnv, err)
		}
	}

	fmt.Printf("\n✓ %s 已写入。\n", defaultAdminEnv)
	if pwOnly {
		fmt.Println("  仅更新了密码哈希；其它字段保留。重启服务: sudo systemctl restart oss-admin")
	} else {
		fmt.Println("  下一步: sudo systemctl enable --now oss-admin")
	}
}

// chownToOSSAdmin sets owner to oss-admin:oss-admin without depending on cgo
// (we don't link user.Lookup which pulls libc). Falls back gracefully if the
// user doesn't exist yet (e.g. running on a dev machine).
func chownToOSSAdmin(path string) error {
	uid, gid, err := lookupOSSAdmin()
	if err != nil {
		return err
	}
	return os.Chown(path, uid, gid)
}

func lookupOSSAdmin() (uid, gid int, err error) {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 4 && fields[0] == "oss-admin" {
			fmt.Sscanf(fields[2], "%d", &uid)
			fmt.Sscanf(fields[3], "%d", &gid)
			return uid, gid, nil
		}
	}
	return 0, 0, fmt.Errorf("oss-admin user not found in /etc/passwd")
}

func prompt(msg string) string {
	fmt.Print(msg)
	var s string
	fmt.Scanln(&s)
	return strings.TrimSpace(s)
}

func promptPassword(msg string) string {
	fmt.Print(msg)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		log.Fatalf("read password: %v", err)
	}
	return string(b)
}

// readKVFile is a forgiving KEY=VALUE reader for the setpw flow — config.Load
// requires both files; here we only need admin.env, possibly absent.
func readKVFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string)
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		out[k] = v
	}
	return out, nil
}
