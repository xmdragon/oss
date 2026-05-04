package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	sessionCookieName = "oss_admin_session"
	sessionDuration   = 8 * time.Hour
)

type Session struct {
	Username string    `json:"u"`
	IssuedAt time.Time `json:"iat"`
	Expires  time.Time `json:"exp"`
}

// Manager mints and verifies signed session cookies. Stateless — no DB.
type Manager struct {
	secret []byte
}

func NewManager(secret []byte) *Manager { return &Manager{secret: secret} }

// Issue writes a signed cookie. Caller is responsible for any TLS/SameSite
// adjustments via the cookie returned values — defaults are tuned for prod.
func (m *Manager) Issue(w http.ResponseWriter, username string) error {
	now := time.Now().UTC()
	s := Session{Username: username, IssuedAt: now, Expires: now.Add(sessionDuration)}
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tok := encodeSigned(m.secret, payload)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    tok,
		Path:     "/",
		Expires:  s.Expires,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	return nil
}

// Clear deletes the session cookie.
func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}

// FromRequest returns the session if the cookie is present, signed, and
// unexpired. Any failure → (nil, error).
func (m *Manager) FromRequest(r *http.Request) (*Session, error) {
	ck, err := r.Cookie(sessionCookieName)
	if err != nil {
		return nil, err
	}
	payload, err := decodeSigned(m.secret, ck.Value)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(payload, &s); err != nil {
		return nil, err
	}
	if time.Now().UTC().After(s.Expires) {
		return nil, errors.New("session expired")
	}
	return &s, nil
}

// encodeSigned returns base64url(payload) + "." + base64url(HMAC-SHA256).
func encodeSigned(key, payload []byte) string {
	enc := base64.RawURLEncoding
	body := enc.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	sig := enc.EncodeToString(mac.Sum(nil))
	return body + "." + sig
}

func decodeSigned(key []byte, tok string) ([]byte, error) {
	idx := strings.IndexByte(tok, '.')
	if idx <= 0 || idx == len(tok)-1 {
		return nil, errors.New("malformed token")
	}
	body, sig := tok[:idx], tok[idx+1:]
	enc := base64.RawURLEncoding
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	wantSig := mac.Sum(nil)
	gotSig, err := enc.DecodeString(sig)
	if err != nil {
		return nil, err
	}
	if !hmac.Equal(wantSig, gotSig) {
		return nil, errors.New("bad signature")
	}
	return enc.DecodeString(body)
}
