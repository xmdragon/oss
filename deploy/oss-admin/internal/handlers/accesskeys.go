package handlers

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/xmdragon/oss/deploy/oss-admin/internal/minioadm"
)

func (s *Server) GetAccessKeys(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	keys, err := s.MC.ListAccessKeys(ctx)
	if err != nil {
		http.Error(w, "list access keys: "+err.Error(), http.StatusBadGateway)
		return
	}
	policies, perr := s.MC.ListPolicies(ctx)
	if perr != nil {
		// Non-fatal: if policy listing fails, the create form falls back to
		// just the desktop default. Surface as a flash for visibility.
		setFlash(w, "err", "policy 列表加载失败: "+perr.Error())
		policies = []string{minioadm.PolicyDesktopAK}
	}
	buckets, berr := s.MC.ListBuckets(ctx)
	if berr != nil {
		// Same handling as policies — degrade gracefully.
		setFlash(w, "err", "bucket 列表加载失败: "+berr.Error())
	}
	bucketNames := make([]string, 0, len(buckets))
	for _, b := range buckets {
		bucketNames = append(bucketNames, b.Name)
	}

	// New AK passed via query param after creation/rotation. We use a one-shot
	// cookie because query string would leak the secret into Caddy access logs.
	var newAK map[string]string
	if ck, err := r.Cookie("oss_admin_newak"); err == nil {
		if v, err := url.QueryUnescape(ck.Value); err == nil {
			_ = json.Unmarshal([]byte(v), &newAK)
		}
		http.SetCookie(w, &http.Cookie{
			Name: "oss_admin_newak", Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
		})
	}

	kind, msg := consumeFlash(w, r)
	s.render(w, r, "accesskeys", View{
		Title:     "Access Keys",
		Flash:     msg,
		FlashKind: kind,
		Data: map[string]any{
			"Keys":          keys,
			"NewAK":         newAK,
			"Policies":      policies,
			"DefaultPolicy": minioadm.PolicyDesktopAK,
			"Buckets":       bucketNames,
		},
	})
}

// PostAccessKeyCreate has two modes selected via the `mode` form value:
//
//   - "scoped" (default): user picks a bucket + permission, we generate (or
//     reuse) a canned policy named scoped-<perm>-<bucket> and attach it.
//   - "existing": user picks one of MinIO's existing canned policies. This is
//     the escape hatch for cluster-wide / admin AKs.
func (s *Server) PostAccessKeyCreate(w http.ResponseWriter, r *http.Request) {
	// Scoped mode does an extra AddCannedPolicy round-trip; bump the budget.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ak := r.FormValue("access_key")
	sk := r.FormValue("secret_key")
	mode := r.FormValue("mode")
	if mode == "" {
		mode = "scoped"
	}
	if ak == "" {
		setFlash(w, "err", "AccessKey 不能为空")
		http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
		return
	}

	var policy string
	var err error
	switch mode {
	case "scoped":
		bucket := r.FormValue("bucket")
		perm := minioadm.Permission(r.FormValue("perm"))
		if bucket == "" {
			setFlash(w, "err", "请选择 Bucket")
			http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
			return
		}
		if !perm.IsValid() {
			setFlash(w, "err", "权限值不合法（应为 w / r / rw）")
			http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
			return
		}
		policy, err = s.MC.EnsureScopedPolicy(ctx, bucket, perm)
		if err != nil {
			s.Audit.FromRequest(r, "policy.ensure", bucket+"/"+string(perm), "error", err.Error())
			setFlash(w, "err", "policy 生成失败: "+err.Error())
			http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
			return
		}
		s.Audit.FromRequest(r, "policy.ensure", policy, "ok", "")
	case "existing":
		policy = r.FormValue("policy")
		if policy == "" {
			setFlash(w, "err", "请选择 Policy")
			http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
			return
		}
	default:
		setFlash(w, "err", "未知 mode: "+mode)
		http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
		return
	}

	created, err := s.MC.CreateAK(ctx, ak, sk, policy)
	if err != nil {
		s.Audit.FromRequest(r, "accesskey.create", ak, "error", err.Error())
		setFlash(w, "err", "创建失败: "+err.Error())
		http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
		return
	}
	s.Audit.FromRequest(r, "accesskey.create", ak, "ok", "policy="+policy)
	stashNewAK(w, created.AccessKey, created.SecretKey)
	setFlash(w, "ok", "新 Access Key 已创建（policy="+policy+"）——SecretKey 仅显示一次，立刻保存。")
	http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
}

func (s *Server) PostAccessKeyAction(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	ak := r.PathValue("ak")
	action := r.PathValue("action")
	switch action {
	case "enable":
		err := s.MC.SetEnabled(ctx, ak, true)
		s.flashAction(w, r, "accesskey.enable", ak, err, "已启用 "+ak)
	case "disable":
		err := s.MC.SetEnabled(ctx, ak, false)
		s.flashAction(w, r, "accesskey.disable", ak, err, "已停用 "+ak)
	case "delete":
		err := s.MC.DeleteAK(ctx, ak)
		s.flashAction(w, r, "accesskey.delete", ak, err, "已删除 "+ak)
	case "rotate":
		newAK := ak + "-r" + shortTS()
		created, err := s.MC.RotateDesktopAK(ctx, ak, newAK)
		if err != nil {
			s.Audit.FromRequest(r, "accesskey.rotate", ak, "error", err.Error())
			setFlash(w, "err", "轮换失败: "+err.Error())
		} else {
			s.Audit.FromRequest(r, "accesskey.rotate", ak+"→"+newAK, "ok", "")
			stashNewAK(w, created.AccessKey, created.SecretKey)
			setFlash(w, "ok", "已生成新 AK 并停用旧 AK——记得发版并最终删除旧 AK")
		}
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/access-keys", http.StatusSeeOther)
}

func (s *Server) flashAction(w http.ResponseWriter, r *http.Request, action, target string, err error, okMsg string) {
	if err != nil {
		s.Audit.FromRequest(r, action, target, "error", err.Error())
		setFlash(w, "err", action+" 失败: "+err.Error())
		return
	}
	s.Audit.FromRequest(r, action, target, "ok", "")
	setFlash(w, "ok", okMsg)
}

// stashNewAK puts the freshly minted (AK, SK) into a one-shot cookie. We
// deliberately avoid query strings so the secret never reaches Caddy logs.
func stashNewAK(w http.ResponseWriter, ak, sk string) {
	b, _ := json.Marshal(map[string]string{"AccessKey": ak, "SecretKey": sk})
	http.SetCookie(w, &http.Cookie{
		Name:     "oss_admin_newak",
		Value:    url.QueryEscape(string(b)),
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// shortTS returns 6 random hex chars used as an AK suffix on rotation.
// Collision risk is negligible for human-paced rotations and keeps names short.
func shortTS() string {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "000000"
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 6)
	for i, v := range b {
		out[2*i] = hex[v>>4]
		out[2*i+1] = hex[v&0x0f]
	}
	return string(out)
}
