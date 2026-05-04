// Package web ships the HTML templates and static assets compiled into the
// binary via go:embed. Handlers ParseFS at startup once.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"time"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

func ParseTemplates() (*template.Template, error) {
	funcs := template.FuncMap{
		"humanSize": humanSize,
		"humanTime": humanTime,
		"yesNo": func(b bool) string {
			if b {
				return "是"
			}
			return "否"
		},
	}
	return template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
}

// StaticHandler serves embedded files at /static/.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

func humanSize(n any) string {
	var b float64
	switch v := n.(type) {
	case uint64:
		b = float64(v)
	case int64:
		b = float64(v)
	case int:
		b = float64(v)
	default:
		return ""
	}
	const k = 1024.0
	switch {
	case b >= k*k*k*k:
		return fmt.Sprintf("%.1f TB", b/(k*k*k*k))
	case b >= k*k*k:
		return fmt.Sprintf("%.1f GB", b/(k*k*k))
	case b >= k*k:
		return fmt.Sprintf("%.1f MB", b/(k*k))
	case b >= k:
		return fmt.Sprintf("%.1f KB", b/k)
	default:
		return fmt.Sprintf("%.0f B", b)
	}
}

func humanTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
