// Package audit appends one JSON line per write operation to a configured log
// file. Reads are not audited; only state-changing actions (lifecycle CRUD, AK
// CRUD, login success/failure).
package audit

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logger struct {
	mu sync.Mutex
	f  *os.File
}

type Entry struct {
	Time   string `json:"ts"`
	Actor  string `json:"actor"`
	IP     string `json:"ip"`
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
	Result string `json:"result"` // "ok" | "denied" | "error"
	Note   string `json:"note,omitempty"`
}

func New(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return nil, err
	}
	return &Logger{f: f}, nil
}

func (l *Logger) Close() error { return l.f.Close() }

func (l *Logger) Write(e Entry) {
	if e.Time == "" {
		e.Time = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := json.Marshal(e)
	if err != nil {
		log.Printf("audit marshal: %v", err)
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.f.Write(append(b, '\n')); err != nil {
		log.Printf("audit write: %v", err)
	}
}

// FromRequest is a convenience for handlers — pulls actor + IP out of the
// request and writes one entry. Actor is read from header X-Audit-Actor which
// auth middleware sets to the logged-in username.
func (l *Logger) FromRequest(r *http.Request, action, target, result, note string) {
	l.Write(Entry{
		Actor:  r.Header.Get("X-Audit-Actor"),
		IP:     clientIP(r),
		Action: action,
		Target: target,
		Result: result,
		Note:   note,
	})
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
