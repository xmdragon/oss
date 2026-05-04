// Package handlers implements all HTTP routes for oss-admin.
package handlers

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/xmdragon/oss/deploy/oss-admin/internal/audit"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/auth"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/config"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/minioadm"
)

// Server bundles every dependency the handlers need. One instance, registered
// on a single mux.
type Server struct {
	Cfg      *config.Config
	Tmpl     *template.Template
	MC       *minioadm.Client
	Sampler  *minioadm.Sampler
	Sessions *auth.Manager
	Limiter  *auth.LoginLimiter
	Audit    *audit.Logger
}

// View is the base data context every page receives.
type View struct {
	Title     string
	Username  string
	Flash     string
	FlashKind string // "ok" | "err"
	CSRFToken string
	Data      any
}

// render writes a template by name and logs render errors. Always call this
// instead of t.ExecuteTemplate so we get consistent error handling.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, v View) {
	if v.Username == "" {
		if sess, err := s.Sessions.FromRequest(r); err == nil {
			v.Username = sess.Username
		}
	}
	if v.CSRFToken == "" {
		v.CSRFToken = auth.EnsureCSRFCookie(w, r)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.Tmpl.ExecuteTemplate(w, name, v); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// flash stores a one-shot message in a short-lived cookie consumed by the next
// render. Simpler than a server-side session store.
const flashCookieName = "oss_admin_flash"

type flashPayload struct {
	Kind, Msg string
}

func setFlash(w http.ResponseWriter, kind, msg string) {
	v := url.QueryEscape(kind + "|" + msg)
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookieName,
		Value:    v,
		Path:     "/",
		MaxAge:   60,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

func consumeFlash(w http.ResponseWriter, r *http.Request) (kind, msg string) {
	ck, err := r.Cookie(flashCookieName)
	if err != nil {
		return "", ""
	}
	v, err := url.QueryUnescape(ck.Value)
	if err != nil || v == "" {
		return "", ""
	}
	for i := 0; i < len(v); i++ {
		if v[i] == '|' {
			kind = v[:i]
			msg = v[i+1:]
			break
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     flashCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	return
}

// reqCtx returns a 5-second context for short MinIO calls so a stalled cluster
// doesn't hang user requests indefinitely.
func reqCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 5*time.Second)
}

// safeRedirect ensures the `next` URL is local (no protocol-relative or
// absolute) so /login?next=... can't be turned into an open redirect.
func safeRedirect(next string) string {
	if next == "" || len(next) > 256 {
		return "/"
	}
	if next[0] != '/' || (len(next) > 1 && next[1] == '/') {
		return "/"
	}
	return next
}
