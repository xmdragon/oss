package handlers

import (
	"net/http"

	"github.com/xmdragon/oss/deploy/oss-admin/internal/auth"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/web"
)

// Router builds the full http.Handler stack: static assets, login, then a
// session-protected sub-tree with CSRF on writes.
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// Static + healthz: no auth required.
	mux.Handle("GET /static/", http.StripPrefix("/static/", web.StaticHandler()))
	mux.HandleFunc("GET /healthz", s.GetHealthz)

	// Login: no session required, but CSRF on POST.
	mux.HandleFunc("GET /login", s.GetLogin)
	mux.HandleFunc("POST /login", s.PostLogin)

	// Everything else requires a session.
	protected := http.NewServeMux()
	protected.HandleFunc("GET /{$}", s.GetDashboard)
	protected.HandleFunc("GET /buckets", s.GetBuckets)
	protected.HandleFunc("POST /buckets", s.PostBucketCreate)
	protected.HandleFunc("GET /buckets/{name}", s.GetBucketDetail)
	protected.HandleFunc("POST /buckets/{name}/lifecycle", s.PostLifecycleAdd)
	protected.HandleFunc("POST /buckets/{name}/lifecycle/delete", s.PostLifecycleDelete)
	protected.HandleFunc("GET /access-keys", s.GetAccessKeys)
	protected.HandleFunc("POST /access-keys", s.PostAccessKeyCreate)
	protected.HandleFunc("POST /access-keys/{ak}/{action}", s.PostAccessKeyAction)
	protected.HandleFunc("POST /logout", s.PostLogout)

	mux.Handle("/", s.Sessions.RequireSession(auth.RequireCSRF(protected)))

	return securityHeaders(mux)
}

// securityHeaders adds baseline response headers. Caddy already adds HSTS at
// the edge; what we add here are the headers Caddy doesn't touch.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}
