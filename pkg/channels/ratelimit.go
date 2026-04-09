package channels

import (
	"net/http"
	"sync"
	"time"
)

type rateLimiter struct {
	mu              sync.Mutex
	entries         map[string]*rateLimitEntry
	rate            int
	window          time.Duration
	cleanupInterval time.Duration
}

type rateLimitEntry struct {
	count       int
	windowStart time.Time
}

func newRateLimiter(rate int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		entries:         make(map[string]*rateLimitEntry),
		rate:            rate,
		window:          window,
		cleanupInterval: 5 * time.Minute,
	}
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) cleanup() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for key, entry := range rl.entries {
			if now.Sub(entry.windowStart) > rl.window {
				delete(rl.entries, key)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.entries[key]
	if !exists || now.Sub(entry.windowStart) > rl.window {
		rl.entries[key] = &rateLimitEntry{count: 1, windowStart: now}
		return true
	}

	entry.count++
	return entry.count <= rl.rate
}

func (n *NativeChannel) rateLimitMiddleware(limiter *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.RemoteAddr
		if !limiter.allow(key) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded, try again later", "rate_limit_exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
