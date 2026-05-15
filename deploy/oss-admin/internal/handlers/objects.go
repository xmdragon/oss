package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xmdragon/oss/deploy/oss-admin/internal/minioadm"
)

// objectsCtx grants 30s for listings — large prefixes on a slow disk are
// noticeably slower than the 5s reqCtx default, but anything beyond 30s should
// be paginated harder, not waited out.
func objectsCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 30*time.Second)
}

// bulkCtx is wider still: scan + delete in one click can do up to ScanLimit
// objects (10k). 60s is a generous bound that still trips before any user
// reasonably abandons the tab.
func bulkCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 60*time.Second)
}

const presignExpire = 5 * time.Minute

// GetObjects renders the object browser for a bucket. Query params:
//
//	prefix  filter
//	cursor  startAfter token (from prior page's NextCursor)
//	size    page size (one of minioadm.AllowedPageSizes; default 100)
func (s *Server) GetObjects(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := objectsCtx(r)
	defer cancel()

	bucket := r.PathValue("name")
	prefix := r.URL.Query().Get("prefix")
	cursor := r.URL.Query().Get("cursor")
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	size = minioadm.NormalizePageSize(size)

	// We list recursively (delimiter="") so the UI shows the full key path —
	// "directory" style browsing was rejected during scoping to keep one
	// flat, searchable table.
	page, err := s.MC.ListObjectsPage(ctx, bucket, prefix, "", cursor, size)
	if err != nil {
		http.Error(w, "list objects: "+err.Error(), http.StatusBadGateway)
		return
	}

	kind, msg := consumeFlash(w, r)
	s.render(w, r, "objects", View{
		Title:     "Objects: " + bucket,
		Flash:     msg,
		FlashKind: kind,
		Data: map[string]any{
			"Bucket":     bucket,
			"Prefix":     prefix,
			"Cursor":     cursor,
			"PageSize":   size,
			"PageSizes":  minioadm.AllowedPageSizes,
			"Page":       page,
			"HasPresign": s.MC.Presign != nil,
		},
	})
}

// GetObjectDetail shows metadata + a presigned URL for one object. The key is
// in ?key=... not the path, so it can contain '/' freely.
func (s *Server) GetObjectDetail(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()

	bucket := r.PathValue("name")
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}

	info, err := s.MC.StatObject(ctx, bucket, key)
	if err != nil {
		http.Error(w, "stat: "+err.Error(), http.StatusBadGateway)
		return
	}

	var presignedURL, presignErr string
	if s.MC.Presign != nil {
		u, err := s.MC.PresignedGetURL(ctx, bucket, key, presignExpire)
		if err != nil {
			presignErr = err.Error()
		} else {
			presignedURL = u
		}
	} else {
		presignErr = "PUBLIC_HOST 未配置，无法生成可访问的 presigned URL"
	}

	// Build a "back to listing" URL that preserves prefix/cursor/size if the
	// user came from there (referer is good enough — no need to plumb state).
	backURL := "/buckets/" + bucket + "/objects"
	if ref := r.Referer(); strings.Contains(ref, "/buckets/"+bucket+"/objects") {
		backURL = ref
	}

	kind, msg := consumeFlash(w, r)
	s.render(w, r, "object_detail", View{
		Title:     "Object: " + key,
		Flash:     msg,
		FlashKind: kind,
		Data: map[string]any{
			"Bucket":         bucket,
			"Key":            key,
			"Info":           info,
			"PresignedURL":   presignedURL,
			"PresignError":   presignErr,
			"PresignMinutes": int(presignExpire / time.Minute),
			"BackURL":        backURL,
		},
	})
}

// PostObjectDelete removes a single object. Key in form body to allow any
// characters (CSRF-protected by the middleware).
func (s *Server) PostObjectDelete(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()

	bucket := r.PathValue("name")
	key := r.FormValue("key")
	if key == "" {
		setFlash(w, "err", "缺少对象 key")
		http.Redirect(w, r, "/buckets/"+bucket+"/objects", http.StatusSeeOther)
		return
	}

	if err := s.MC.RemoveObject(ctx, bucket, key); err != nil {
		s.Audit.FromRequest(r, "object.delete", bucket+"/"+key, "error", err.Error())
		setFlash(w, "err", "删除失败: "+err.Error())
	} else {
		s.Audit.FromRequest(r, "object.delete", bucket+"/"+key, "ok", "")
		setFlash(w, "ok", "对象已删除: "+key)
	}
	// Return to the listing page (with whatever prefix/cursor was in the form).
	next := r.FormValue("return_to")
	if next == "" {
		next = "/buckets/" + bucket + "/objects"
	}
	http.Redirect(w, r, safeRedirect(next), http.StatusSeeOther)
}

// GetBulkDelete renders the "delete by age" form. On the first hit it's just
// the input form; with valid `days` it also runs a dry-run scan and shows a
// preview with a "确认执行" button that POSTs the same cutoff back.
func (s *Server) GetBulkDelete(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("name")
	q := r.URL.Query()
	daysStr := q.Get("days")
	prefix := q.Get("prefix")

	view := map[string]any{
		"Bucket":    bucket,
		"DaysInput": daysStr,
		"Prefix":    prefix,
		"ScanLimit": minioadm.ScanLimit,
	}

	if daysStr != "" {
		days, err := strconv.Atoi(daysStr)
		if err != nil || days < 1 {
			view["FormError"] = "天数必须 ≥ 1"
			s.render(w, r, "bulk_delete", View{Title: "批量删除: " + bucket, Data: view})
			return
		}

		ctx, cancel := bulkCtx(r)
		defer cancel()
		cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
		scan, err := s.MC.ScanByAge(ctx, bucket, prefix, cutoff)
		if err != nil {
			view["FormError"] = "扫描失败: " + err.Error()
			s.render(w, r, "bulk_delete", View{Title: "批量删除: " + bucket, Data: view})
			return
		}
		view["Scan"] = scan
		view["CutoffUnix"] = cutoff.Unix() // exact timestamp passed to confirm form
		view["Days"] = days
	}

	kind, msg := consumeFlash(w, r)
	s.render(w, r, "bulk_delete", View{
		Title:     "批量删除: " + bucket,
		Flash:     msg,
		FlashKind: kind,
		Data:      view,
	})
}

// PostBulkDelete actually performs the delete. The form must carry the exact
// cutoff_unix from the preview (so what the user saw is what gets deleted),
// plus a `confirm=yes` flag to make accidental form submissions a no-op.
func (s *Server) PostBulkDelete(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("name")
	prefix := r.FormValue("prefix")
	cutoffUnixStr := r.FormValue("cutoff_unix")
	confirm := r.FormValue("confirm")

	if confirm != "yes" {
		setFlash(w, "err", "未确认执行")
		http.Redirect(w, r, "/buckets/"+bucket+"/objects/bulk-delete", http.StatusSeeOther)
		return
	}
	cutoffUnix, err := strconv.ParseInt(cutoffUnixStr, 10, 64)
	if err != nil {
		setFlash(w, "err", "cutoff 参数错误")
		http.Redirect(w, r, "/buckets/"+bucket+"/objects/bulk-delete", http.StatusSeeOther)
		return
	}
	cutoff := time.Unix(cutoffUnix, 0)

	ctx, cancel := bulkCtx(r)
	defer cancel()

	// Rescan with the same cutoff. Objects uploaded between preview and
	// confirm are still subject to LastModified < cutoff, so the set is at
	// most equal — the user sees an accurate preview.
	scan, err := s.MC.ScanByAge(ctx, bucket, prefix, cutoff)
	if err != nil {
		s.Audit.FromRequest(r, "object.bulk_delete", bucket+"/"+prefix, "error", "scan: "+err.Error())
		setFlash(w, "err", "重新扫描失败: "+err.Error())
		http.Redirect(w, r, "/buckets/"+bucket+"/objects/bulk-delete", http.StatusSeeOther)
		return
	}
	if scan.Count == 0 {
		setFlash(w, "ok", "没有匹配的对象，未执行删除")
		http.Redirect(w, r, "/buckets/"+bucket+"/objects/bulk-delete", http.StatusSeeOther)
		return
	}

	keys := make([]string, len(scan.Objects))
	for i, o := range scan.Objects {
		keys[i] = o.Key
	}
	result := s.MC.BulkRemove(ctx, bucket, keys)

	note := "cutoff=" + cutoff.UTC().Format(time.RFC3339) +
		" prefix=" + prefix +
		" requested=" + strconv.Itoa(result.Requested) +
		" removed=" + strconv.Itoa(result.Removed) +
		" errors=" + strconv.Itoa(len(result.Errors))
	if scan.Truncated {
		note += " truncated=true"
	}
	resultLabel := "ok"
	if len(result.Errors) > 0 {
		resultLabel = "error"
	}
	s.Audit.FromRequest(r, "object.bulk_delete", bucket+"/"+prefix, resultLabel, note)

	if len(result.Errors) > 0 {
		setFlash(w, "err", "已删除 "+strconv.Itoa(result.Removed)+
			" / "+strconv.Itoa(result.Requested)+" 个对象，"+
			strconv.Itoa(len(result.Errors))+" 个失败（详见 audit.log）")
	} else {
		extra := ""
		if scan.Truncated {
			extra = "（已达单次上限 " + strconv.Itoa(minioadm.ScanLimit) + "，可再次执行清理剩余）"
		}
		setFlash(w, "ok", "已删除 "+strconv.Itoa(result.Removed)+" 个对象"+extra)
	}
	http.Redirect(w, r, "/buckets/"+bucket+"/objects/bulk-delete", http.StatusSeeOther)
}
