package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateBucket struct {
	timestamps []time.Time
}

type rateLimiter struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	buckets   map[string]*rateBucket
	lastSweep time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		limit:     limit,
		window:    window,
		buckets:   make(map[string]*rateBucket),
		lastSweep: time.Now().UTC(),
	}
}

// Allow records a hit for key and returns false if the sliding window limit is exceeded.
func (rl *rateLimiter) Allow(key string) bool {
	if rl == nil || rl.limit <= 0 {
		return true
	}
	now := time.Now().UTC()
	cutoff := now.Add(-rl.window)

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if now.Sub(rl.lastSweep) > rl.window {
		for k, b := range rl.buckets {
			kept := pruneBefore(b.timestamps, cutoff)
			if len(kept) == 0 {
				delete(rl.buckets, k)
			} else {
				b.timestamps = kept
			}
		}
		rl.lastSweep = now
	}

	b := rl.buckets[key]
	if b == nil {
		b = &rateBucket{}
		rl.buckets[key] = b
	}
	b.timestamps = pruneBefore(b.timestamps, cutoff)
	if len(b.timestamps) >= rl.limit {
		return false
	}
	b.timestamps = append(b.timestamps, now)
	return true
}

func pruneBefore(ts []time.Time, cutoff time.Time) []time.Time {
	idx := 0
	for idx < len(ts) && ts[idx].Before(cutoff) {
		idx++
	}
	if idx == 0 {
		return ts
	}
	if idx >= len(ts) {
		return nil
	}
	out := make([]time.Time, len(ts)-idx)
	copy(out, ts[idx:])
	return out
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
			first := strings.TrimSpace(strings.Split(xff, ",")[0])
			if ip := net.ParseIP(first); ip != nil {
				return ip.String()
			}
		}
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			if ip := net.ParseIP(xri); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	if strings.TrimSpace(r.RemoteAddr) != "" {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return "unknown"
}
