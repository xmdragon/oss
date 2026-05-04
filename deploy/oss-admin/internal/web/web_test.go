package web

import (
	"bytes"
	"testing"
	"time"
)

// TestParseTemplates ensures every embedded template parses and that each
// page's named entry can render against a minimal data context. This catches
// most "{{.Field}} doesn't exist" template errors at build/test time rather
// than first hit in production.
func TestParseTemplates(t *testing.T) {
	tmpl, err := ParseTemplates()
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	type baseView struct {
		Title     string
		Username  string
		Flash     string
		FlashKind string
		CSRFToken string
		Data      any
	}

	cases := []struct {
		name string
		data baseView
	}{
		{"login", baseView{Title: "Login", Data: map[string]any{"Next": "/", "TOTPEnabled": false}}},
		{"dashboard", baseView{Title: "Dashboard", Username: "grom", Data: map[string]any{
			"TotalSize":    uint64(0),
			"ObjectCount":  uint64(0),
			"BucketCount":  0,
			"Healthy":      true,
			"ChartSVG":     "",
			"LatestSample": time.Time{},
			"SampleCount":  0,
			"Endpoint":     "127.0.0.1:9000",
		}}},
		{"buckets", baseView{Title: "Buckets", Username: "grom", Data: map[string]any{"Buckets": []any{}}}},
		{"bucket_detail", baseView{Title: "Bucket: x", Username: "grom", Data: map[string]any{
			"Bucket": "x", "Rules": []any{}, "PolicyJSON": "", "SuggestedDays": 7,
		}}},
		{"accesskeys", baseView{Title: "AKs", Username: "grom", Data: map[string]any{
			"Keys": []any{}, "NewAK": map[string]string(nil),
		}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, c.name, c.data); err != nil {
				t.Fatalf("execute %s: %v", c.name, err)
			}
			if buf.Len() == 0 {
				t.Fatalf("%s rendered empty", c.name)
			}
		})
	}
}
