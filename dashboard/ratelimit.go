package dashboard

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	loginMaxAttempts = 10
	loginWindow      = time.Minute
)

type ipBucket struct {
	count   int
	resetAt time.Time
}

// ipRateLimiter is a simple fixed-window rate limiter keyed by client IP.
type ipRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*ipBucket
}

func newIPRateLimiter() *ipRateLimiter {
	rl := &ipRateLimiter{buckets: make(map[string]*ipBucket)}
	go rl.cleanupLoop()
	return rl
}

// Allow reports whether the given IP may proceed, consuming one token.
func (rl *ipRateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok || now.After(b.resetAt) {
		rl.buckets[ip] = &ipBucket{count: 1, resetAt: now.Add(loginWindow)}
		return true
	}
	if b.count >= loginMaxAttempts {
		return false
	}
	b.count++
	return true
}

func (rl *ipRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, b := range rl.buckets {
			if now.After(b.resetAt) {
				delete(rl.buckets, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// realIP extracts the client IP, respecting X-Real-IP and X-Forwarded-For
// headers set by trusted reverse proxies.
func realIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if idx := strings.Index(fwd, ","); idx != -1 {
			return strings.TrimSpace(fwd[:idx])
		}
		return strings.TrimSpace(fwd)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
