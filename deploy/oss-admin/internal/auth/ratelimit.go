package auth

import (
	"context"
	"sync"
	"time"
)

// LoginLimiter caps login attempts per IP. Single-process, in-memory.
//
// `ossmanage` is internet-facing, so the IP set is unbounded over time
// (background scanners, distributed brute force). RunGC periodically prunes
// expired buckets so memory stays proportional to recent activity rather than
// total IPs ever seen.
type LoginLimiter struct {
	mu      sync.Mutex
	attempt map[string]*bucket
	window  time.Duration
	max     int
}

type bucket struct {
	count   int
	resetAt time.Time
}

func NewLoginLimiter(max int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{
		attempt: make(map[string]*bucket),
		window:  window,
		max:     max,
	}
}

// Allow returns true if the IP may attempt a login. On false, RetryAfter is
// the duration until the bucket resets.
func (l *LoginLimiter) Allow(ip string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, has := l.attempt[ip]
	if !has || now.After(b.resetAt) {
		l.attempt[ip] = &bucket{count: 1, resetAt: now.Add(l.window)}
		return true, 0
	}
	if b.count >= l.max {
		return false, b.resetAt.Sub(now)
	}
	b.count++
	return true, 0
}

// Reset clears the IP's counter, e.g. after a successful login.
func (l *LoginLimiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempt, ip)
}

// RunGC sweeps expired buckets at `interval` until ctx is done. Intended to be
// called in its own goroutine.
func (l *LoginLimiter) RunGC(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			l.gcOnce(time.Now())
		}
	}
}

// gcOnce is split out for tests — pass an explicit `now` to avoid time skew.
func (l *LoginLimiter) gcOnce(now time.Time) (removed int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, b := range l.attempt {
		if now.After(b.resetAt) {
			delete(l.attempt, ip)
			removed++
		}
	}
	return
}

// Size returns the current number of tracked IPs (for tests / introspection).
func (l *LoginLimiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.attempt)
}
