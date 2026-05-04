package handlers

import (
	"net/http"
	"strconv"

	"github.com/xmdragon/oss/deploy/oss-admin/internal/minioadm"
)

func (s *Server) PostLifecycleAdd(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	bucket := r.PathValue("name")

	days, err := strconv.Atoi(r.FormValue("expiry_days"))
	if err != nil || days < 1 {
		setFlash(w, "err", "过期天数必须 ≥ 1")
		http.Redirect(w, r, "/buckets/"+bucket, http.StatusSeeOther)
		return
	}
	rule := minioadm.Rule{
		Status:     r.FormValue("status"),
		Prefix:     r.FormValue("prefix"),
		ExpiryDays: days,
	}
	if err := s.MC.AddRule(ctx, bucket, rule); err != nil {
		s.Audit.FromRequest(r, "lifecycle.add", bucket, "error", err.Error())
		setFlash(w, "err", "添加失败: "+err.Error())
	} else {
		s.Audit.FromRequest(r, "lifecycle.add", bucket, "ok",
			"days="+strconv.Itoa(days)+" prefix="+rule.Prefix)
		setFlash(w, "ok", "规则已添加")
	}
	http.Redirect(w, r, "/buckets/"+bucket, http.StatusSeeOther)
}

// PostLifecycleDelete reads the rule ID from the form body, not the URL path,
// so IDs containing `/`, empty IDs (S3 lifecycle allows it), or any other
// reserved-character payload generated outside our UI route correctly.
func (s *Server) PostLifecycleDelete(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()
	bucket := r.PathValue("name")
	id := r.FormValue("id")
	if err := s.MC.DeleteRule(ctx, bucket, id); err != nil {
		s.Audit.FromRequest(r, "lifecycle.delete", bucket+"/"+id, "error", err.Error())
		setFlash(w, "err", "删除失败: "+err.Error())
	} else {
		s.Audit.FromRequest(r, "lifecycle.delete", bucket+"/"+id, "ok", "")
		setFlash(w, "ok", "规则已删除")
	}
	http.Redirect(w, r, "/buckets/"+bucket, http.StatusSeeOther)
}
