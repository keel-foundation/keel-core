package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string][]time.Time
	limit   int
	window  time.Duration
	now     func() time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &rateLimiter{
		buckets: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
		now:     time.Now,
	}
}

func (l *rateLimiter) allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	now := l.now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.buckets[key]
	kept := bucket[:0]
	for _, at := range bucket {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= l.limit {
		l.buckets[key] = kept
		return false
	}
	l.buckets[key] = append(kept, now)
	return true
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
			if first != "" {
				return first
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
