package auth

import (
	"net/http"
)

// RequireSession redirects unauthenticated GETs to /login and rejects
// non-GET requests with 401. Authenticated requests get the username pinned
// to the X-Audit-Actor header (read-only contract for the audit logger).
func (m *Manager) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := m.FromRequest(r)
		if err != nil {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/login?next="+r.URL.RequestURI(), http.StatusSeeOther)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Header.Set("X-Audit-Actor", s.Username)
		next.ServeHTTP(w, r)
	})
}

// RequireCSRF rejects non-GET/HEAD requests whose CSRF token doesn't validate.
// Place INSIDE RequireSession so anonymous probes still get 401, not 403.
func RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !VerifyCSRF(r) {
			http.Error(w, "csrf token mismatch", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
