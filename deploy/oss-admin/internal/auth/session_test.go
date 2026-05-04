package auth

import (
	"net/http/httptest"
	"testing"
)

func TestSessionRoundTrip(t *testing.T) {
	m := NewManager([]byte("01234567890123456789012345678901"))

	rr := httptest.NewRecorder()
	if err := m.Issue(rr, "grom"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	resp := rr.Result()
	defer resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set")
	}

	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	sess, err := m.FromRequest(req)
	if err != nil {
		t.Fatalf("from request: %v", err)
	}
	if sess.Username != "grom" {
		t.Errorf("got %q, want grom", sess.Username)
	}
}

func TestTamperedSessionRejected(t *testing.T) {
	m := NewManager([]byte("01234567890123456789012345678901"))
	rr := httptest.NewRecorder()
	if err := m.Issue(rr, "grom"); err != nil {
		t.Fatal(err)
	}
	c := rr.Result().Cookies()[0]
	// flip one byte in payload
	if len(c.Value) > 5 {
		b := []byte(c.Value)
		if b[5] == 'a' {
			b[5] = 'b'
		} else {
			b[5] = 'a'
		}
		c.Value = string(b)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(c)
	if _, err := m.FromRequest(req); err == nil {
		t.Fatal("expected tampered session to be rejected")
	}
}
