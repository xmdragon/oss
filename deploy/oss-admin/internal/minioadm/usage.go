package minioadm

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/minio/madmin-go/v3"
)

// Sample is one snapshot of cluster-wide usage. Sampler keeps a 24h ring.
type Sample struct {
	Time        time.Time
	TotalSize   uint64
	ObjectCount uint64
}

// HourBucket is what the dashboard chart consumes — one bar per hour.
type HourBucket struct {
	Hour      time.Time // bucket start, UTC, truncated to the hour
	BytesIn   int64     // delta size vs previous hour (negative = lifecycle deletes won)
	NetObjs   int64     // delta object count
}

// Sampler periodically polls AccountInfo and stores samples in memory.
// Restart loses history — acceptable for a dashboard.
type Sampler struct {
	c        *Client
	interval time.Duration
	window   time.Duration

	mu      sync.RWMutex
	samples []Sample
}

func NewSampler(c *Client, interval, window time.Duration) *Sampler {
	return &Sampler{c: c, interval: interval, window: window}
}

// Run blocks until ctx is cancelled. Intended to be called in its own goroutine.
func (s *Sampler) Run(ctx context.Context) {
	// Take one immediate sample so the dashboard isn't empty on first hit.
	s.takeSample(ctx)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.takeSample(ctx)
		}
	}
}

func (s *Sampler) takeSample(ctx context.Context) {
	info, err := s.c.Admin.AccountInfo(ctx, madmin.AccountOpts{})
	if err != nil {
		return // silent — sampler is best-effort, errors visible in logs would be too noisy
	}
	var size, objs uint64
	for _, b := range info.Buckets {
		size += b.Size
		objs += b.Objects
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.samples = append(s.samples, Sample{Time: now, TotalSize: size, ObjectCount: objs})
	// Drop samples older than window.
	cutoff := now.Add(-s.window)
	i := 0
	for ; i < len(s.samples); i++ {
		if !s.samples[i].Time.Before(cutoff) {
			break
		}
	}
	if i > 0 {
		s.samples = append(s.samples[:0], s.samples[i:]...)
	}
}

// Latest returns the most recent sample, or zero if none yet.
func (s *Sampler) Latest() Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.samples) == 0 {
		return Sample{}
	}
	return s.samples[len(s.samples)-1]
}

// HourlyDeltas bins samples into hour-aligned buckets and emits the delta in
// (bytes, objects) per hour over the configured window. Hours with no sample
// are still emitted with zero deltas so the chart has a continuous x-axis.
func (s *Sampler) HourlyDeltas() []HourBucket {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.samples) < 2 {
		return nil
	}
	// Pick first sample within each hour bucket (earliest), and last sample
	// of the bucket. Delta = last(this_hour) - last(prev_hour).
	type hourPick struct{ first, last Sample }
	picks := make(map[time.Time]*hourPick)
	for _, sm := range s.samples {
		h := sm.Time.Truncate(time.Hour)
		p := picks[h]
		if p == nil {
			picks[h] = &hourPick{first: sm, last: sm}
			continue
		}
		if sm.Time.Before(p.first.Time) {
			p.first = sm
		}
		if sm.Time.After(p.last.Time) {
			p.last = sm
		}
	}
	hours := make([]time.Time, 0, len(picks))
	for h := range picks {
		hours = append(hours, h)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i].Before(hours[j]) })

	out := make([]HourBucket, 0, len(hours))
	var prev *Sample
	for _, h := range hours {
		p := picks[h]
		if prev == nil {
			prev = &p.last
			out = append(out, HourBucket{Hour: h})
			continue
		}
		out = append(out, HourBucket{
			Hour:    h,
			BytesIn: int64(p.last.TotalSize) - int64(prev.TotalSize),
			NetObjs: int64(p.last.ObjectCount) - int64(prev.ObjectCount),
		})
		last := p.last
		prev = &last
	}
	return out
}
