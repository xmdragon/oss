package handlers

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/xmdragon/oss/deploy/oss-admin/internal/minioadm"
)

func (s *Server) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := reqCtx(r)
	defer cancel()

	buckets, err := s.MC.ListBuckets(ctx)
	healthy := s.MC.Healthy(ctx) == nil
	var totalSize, objCount uint64
	for _, b := range buckets {
		totalSize += b.Size
		objCount += b.ObjectCount
	}
	if err != nil && len(buckets) == 0 {
		// Don't fail the whole page — show what we have.
		buckets = nil
	}

	hours := s.Sampler.HourlyDeltas()
	chart := renderChart(hours)
	latest := s.Sampler.Latest()

	kind, msg := consumeFlash(w, r)
	s.render(w, r, "dashboard", View{
		Title:     "概览",
		Flash:     msg,
		FlashKind: kind,
		Data: map[string]any{
			"TotalSize":    totalSize,
			"ObjectCount":  objCount,
			"BucketCount":  len(buckets),
			"Healthy":      healthy,
			"ChartSVG":     chart,
			"LatestSample": latest.Time,
			"SampleCount":  len(hours),
			"Endpoint":     s.MC.Endpoint,
		},
	})
}

// renderChart returns an SVG bar chart of net bytes-in per hour. Empty if not
// enough data. We render server-side so there's no JS chart-library dependency.
func renderChart(hours []minioadm.HourBucket) template.HTML {
	if len(hours) < 2 {
		return ""
	}
	const (
		width    = 720
		height   = 180
		padLeft  = 50
		padRight = 10
		padTop   = 10
		padBot   = 28
	)
	plotW := width - padLeft - padRight
	plotH := height - padTop - padBot
	barW := float64(plotW) / float64(len(hours))

	// Scale: max absolute value of BytesIn — bars go up (positive) or down (negative).
	var maxAbs int64 = 1
	for _, h := range hours {
		v := h.BytesIn
		if v < 0 {
			v = -v
		}
		if v > maxAbs {
			maxAbs = v
		}
	}
	zeroY := float64(padTop) + float64(plotH)/2

	var b strings.Builder
	fmt.Fprintf(&b, `<svg viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="24h upload chart">`, width, height)
	// Axes
	fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#475569" stroke-dasharray="2,2"/>`,
		padLeft, zeroY, width-padRight, zeroY)
	// Bars
	for i, h := range hours {
		x := float64(padLeft) + float64(i)*barW + 1
		ratio := float64(h.BytesIn) / float64(maxAbs)
		barH := ratio * float64(plotH) / 2
		y := zeroY - barH
		if barH < 0 {
			// negative — bar drops below zeroY
			y = zeroY
			barH = -barH
		}
		fill := "#38bdf8"
		if h.BytesIn < 0 {
			fill = "#ef4444"
		}
		fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s" opacity="0.85"><title>%s\nΔ %s, Δ %d 对象</title></rect>`,
			x, y, barW-2, barH, fill,
			h.Hour.Local().Format("01-02 15:04"),
			fmtSignedSize(h.BytesIn), h.NetObjs)
	}
	// X labels: first, middle, last
	labels := []int{0, len(hours) / 2, len(hours) - 1}
	for _, i := range labels {
		x := float64(padLeft) + float64(i)*barW + barW/2
		fmt.Fprintf(&b, `<text x="%.1f" y="%d" fill="#94a3b8" font-size="10" text-anchor="middle">%s</text>`,
			x, height-10, hours[i].Hour.Local().Format("15:04"))
	}
	// Y labels (max + / -)
	fmt.Fprintf(&b, `<text x="%d" y="%.1f" fill="#94a3b8" font-size="10" text-anchor="end">+%s</text>`,
		padLeft-4, float64(padTop)+10, fmtSize(maxAbs))
	fmt.Fprintf(&b, `<text x="%d" y="%.1f" fill="#94a3b8" font-size="10" text-anchor="end">-%s</text>`,
		padLeft-4, zeroY+(zeroY-float64(padTop))-2, fmtSize(maxAbs))
	fmt.Fprintf(&b, `<text x="%d" y="%.1f" fill="#94a3b8" font-size="10" text-anchor="end">0</text>`,
		padLeft-4, zeroY+3)
	b.WriteString(`</svg>`)
	return template.HTML(b.String())
}

func fmtSize(b int64) string {
	if b < 0 {
		b = -b
	}
	const k = 1024.0
	switch {
	case b >= int64(k*k*k):
		return fmt.Sprintf("%.1fGB", float64(b)/(k*k*k))
	case b >= int64(k*k):
		return fmt.Sprintf("%.1fMB", float64(b)/(k*k))
	case b >= int64(k):
		return fmt.Sprintf("%.1fKB", float64(b)/k)
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func fmtSignedSize(b int64) string {
	if b >= 0 {
		return "+" + fmtSize(b)
	}
	return "-" + fmtSize(-b)
}
