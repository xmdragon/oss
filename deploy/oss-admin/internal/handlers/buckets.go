package handlers

import (
	"net/http"
)

func (s *Server) GetBuckets(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	buckets, err := s.MC.ListBuckets(ctx)
	if err != nil {
		http.Error(w, "list buckets: "+err.Error(), http.StatusBadGateway)
		return
	}
	kind, msg := consumeFlash(w, r)
	s.render(w, r, "buckets", View{
		Title:     "Buckets",
		Flash:     msg,
		FlashKind: kind,
		Data:      map[string]any{"Buckets": buckets},
	})
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
