package auth

import (
	"testing"
	"time"
)

func TestLimiterGC(t *testing.T) {
	l := NewLoginLimiter(5, time.Minute)

	// Three IPs touch the limiter.
	for _, ip := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		if ok, _ := l.Allow(ip); !ok {
			t.Fatalf("first allow for %s should succeed", ip)
		}
	}
	if got := l.Size(); got != 3 {
		t.Fatalf("size after fill: got %d, want 3", got)
	}

	// gcOnce with `now` past the window must purge them all.
	if removed := l.gcOnce(time.Now().Add(2 * time.Minute)); removed != 3 {
		t.Fatalf("removed: got %d, want 3", removed)
	}
	if got := l.Size(); got != 0 {
		t.Fatalf("size after gc: got %d, want 0", got)
	}

	// gcOnce on empty map is a no-op.
	if removed := l.gcOnce(time.Now()); removed != 0 {
		t.Fatalf("empty gc removed %d", removed)
	}
}

func TestLimiterCapsAttempts(t *testing.T) {
	l := NewLoginLimiter(3, time.Minute)
	const ip = "9.9.9.9"
	for i := 0; i < 3; i++ {
		ok, _ := l.Allow(ip)
		if !ok {
			t.Fatalf("attempt %d should pass", i+1)
		}
	}
	ok, retry := l.Allow(ip)
	if ok {
		t.Fatal("4th attempt should be blocked")
	}
	if retry <= 0 {
		t.Fatalf("retryAfter should be > 0, got %v", retry)
	}
	l.Reset(ip)
	if ok, _ := l.Allow(ip); !ok {
		t.Fatal("after Reset, allow should pass")
	}
}
