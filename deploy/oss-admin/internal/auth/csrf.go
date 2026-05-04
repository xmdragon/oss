package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

const (
	csrfCookieName = "oss_admin_csrf"
	csrfHeader     = "X-CSRF-Token"
	csrfFormField  = "_csrf"
)

// EnsureCSRFCookie reads the CSRF cookie, minting a fresh token if absent.
// Returns the current token so handlers can embed it in forms.
func EnsureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if ck, err := r.Cookie(csrfCookieName); err == nil && len(ck.Value) >= 32 {
		return ck.Value
	}
	tok := newCSRFToken()
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    tok,
		Path:     "/",
		HttpOnly: false, // must be readable by htmx so it can echo into header
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	return tok
}

// VerifyCSRF returns true when the cookie token equals the token submitted via
// header OR form field. Double-submit pattern.
func VerifyCSRF(r *http.Request) bool {
	ck, err := r.Cookie(csrfCookieName)
	if err != nil || ck.Value == "" {
		return false
	}
	got := r.Header.Get(csrfHeader)
	if got == "" {
		got = r.FormValue(csrfFormField)
	}
	if got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(ck.Value), []byte(got)) == 1
}

// CSRFFieldName / CSRFHeaderName are exported so templates can reference them
// without hard-coding strings.
func CSRFFieldName() string  { return csrfFormField }
func CSRFHeaderName() string { return csrfHeader }

func newCSRFToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
