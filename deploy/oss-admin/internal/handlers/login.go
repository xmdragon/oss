package handlers

import (
	"net/http"

	"github.com/xmdragon/oss/deploy/oss-admin/internal/audit"
	"github.com/xmdragon/oss/deploy/oss-admin/internal/auth"
)

func (s *Server) GetLogin(w http.ResponseWriter, r *http.Request) {
	// If already logged in, bounce to dashboard.
	if _, err := s.Sessions.FromRequest(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	kind, msg := consumeFlash(w, r)
	s.render(w, r, "login", View{
		Title:     "登录",
		Flash:     msg,
		FlashKind: kind,
		Data: map[string]any{
			"Next":        safeRedirect(r.URL.Query().Get("next")),
			"TOTPEnabled": s.Cfg.AdminTOTPSecret != "",
		},
	})
}

func (s *Server) PostLogin(w http.ResponseWriter, r *http.Request) {
	if !auth.VerifyCSRF(r) {
		http.Error(w, "csrf token mismatch", http.StatusForbidden)
		return
	}
	ip := clientIP(r)
	if ok, retry := s.Limiter.Allow(ip); !ok {
		s.Audit.Write(audit.Entry{Actor: "?", IP: ip, Action: "login", Result: "denied", Note: "rate limited"})
		setFlash(w, "err", "登录尝试过于频繁，请 "+retry.Truncate(1e9).String()+" 后再试")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	user := r.FormValue("username")
	pass := r.FormValue("password")
	totp := r.FormValue("totp")
	next := safeRedirect(r.FormValue("next"))

	if user != s.Cfg.AdminUsername {
		s.Audit.Write(audit.Entry{Actor: user, IP: ip, Action: "login", Result: "denied", Note: "unknown user"})
		setFlash(w, "err", "用户名或密码错误")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	ok, err := auth.VerifyPassword(pass, s.Cfg.AdminPasswordHash)
	if err != nil || !ok {
		s.Audit.Write(audit.Entry{Actor: user, IP: ip, Action: "login", Result: "denied", Note: "bad password"})
		setFlash(w, "err", "用户名或密码错误")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Cfg.AdminTOTPSecret != "" {
		if !auth.VerifyTOTP(totp, s.Cfg.AdminTOTPSecret) {
			s.Audit.Write(audit.Entry{Actor: user, IP: ip, Action: "login", Result: "denied", Note: "bad totp"})
			setFlash(w, "err", "TOTP 验证失败")
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
	}

	s.Limiter.Reset(ip)
	if err := s.Sessions.Issue(w, user); err != nil {
		http.Error(w, "issue session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Audit.Write(audit.Entry{Actor: user, IP: ip, Action: "login", Result: "ok"})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) PostLogout(w http.ResponseWriter, r *http.Request) {
	if !auth.VerifyCSRF(r) {
		http.Error(w, "csrf token mismatch", http.StatusForbidden)
		return
	}
	if sess, err := s.Sessions.FromRequest(r); err == nil {
		s.Audit.Write(audit.Entry{Actor: sess.Username, IP: clientIP(r), Action: "logout", Result: "ok"})
	}
	s.Sessions.Clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func clientIP(r *http.Request) string {
	if h := r.Header.Get("X-Real-IP"); h != "" {
		return h
	}
	if h := r.Header.Get("X-Forwarded-For"); h != "" {
		return h
	}
	return r.RemoteAddr
}
