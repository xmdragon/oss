package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) GetBuckets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	buckets, err := s.MC.ListBuckets(ctx)
	if err != nil {
		http.Error(w, "list buckets: "+err.Error(), http.StatusBadGateway)
		return
	}
	suggested := s.Cfg.LifecycleDays
	if suggested == 0 {
		suggested = 7
	}
	kind, msg := consumeFlash(w, r)
	s.render(w, r, "buckets", View{
		Title:     "Buckets",
		Flash:     msg,
		FlashKind: kind,
		Data: map[string]any{
			"Buckets":        buckets,
			"SuggestedDays":  suggested,
		},
	})
}

// PostBucketCreate creates a bucket. When `apply_defaults=on` the bucket gets
// the same shape init-bucket.sh produces for `products`: anonymous download
// policy + a single expiration lifecycle rule of N days.
func (s *Server) PostBucketCreate(w http.ResponseWriter, r *http.Request) {
	// Bucket creation can be slow on a busy MinIO (lock dance + policy + lifecycle)
	// — use a 15s budget instead of the 5s default.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	name := strings.TrimSpace(r.FormValue("name"))
	applyDefaults := r.FormValue("apply_defaults") == "on"

	days := s.Cfg.LifecycleDays
	if v := r.FormValue("lifecycle_days"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			days = n
		}
	}

	if err := s.MC.CreateBucket(ctx, name, applyDefaults, days); err != nil {
		s.Audit.FromRequest(r, "bucket.create", name, "error", err.Error())
		setFlash(w, "err", "创建失败: "+err.Error())
		http.Redirect(w, r, "/buckets", http.StatusSeeOther)
		return
	}
	note := "empty"
	if applyDefaults {
		note = fmt.Sprintf("anon-download + lifecycle %dd", days)
	}
	s.Audit.FromRequest(r, "bucket.create", name, "ok", note)
	setFlash(w, "ok", "Bucket "+name+" 已创建（"+note+"）")
	http.Redirect(w, r, "/buckets/"+name, http.StatusSeeOther)
}

func (s *Server) GetBucketDetail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	name := r.PathValue("name")
	rules, err := s.MC.ListLifecycle(ctx, name)
	if err != nil {
		http.Error(w, "list lifecycle: "+err.Error(), http.StatusBadGateway)
		return
	}
	policy, _ := s.MC.BucketPolicyJSON(ctx, name)

	suggested := s.Cfg.LifecycleDays
	if suggested == 0 {
		suggested = 7
	}
	kind, msg := consumeFlash(w, r)
	s.render(w, r, "bucket_detail", View{
		Title:     "Bucket: " + name,
		Flash:     msg,
		FlashKind: kind,
		Data: map[string]any{
			"Bucket":        name,
			"Rules":         rules,
			"PolicyJSON":    policy,
			"SuggestedDays": suggested,
		},
	})
}
