package channels

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// maxRateLimitEntries caps the number of distinct keys tracked by the rate
// limiter. When the limit is reached, the oldest (by window start time)
// entries are evicted before inserting a new one. This prevents unbounded
// memory growth from an attacker cycling many source IPs.
const maxRateLimitEntries = 10000

type rateLimiter struct {
	mu              sync.Mutex
	entries         map[string]*rateLimitEntry
	rate            int
	window          time.Duration
	cleanupInterval time.Duration
	stopCh          chan struct{}
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
		stopCh:          make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) cleanup() {
	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for key, entry := range rl.entries {
				if now.Sub(entry.windowStart) > rl.window {
					delete(rl.entries, key)
				}
			}
			rl.mu.Unlock()
		case <-rl.stopCh:
			return
		}
	}
}

func (rl *rateLimiter) Stop() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	select {
	case <-rl.stopCh:
		// Already closed
	default:
		close(rl.stopCh)
	}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.entries[key]
	if !exists || now.Sub(entry.windowStart) > rl.window {
		// Evict oldest entries if we've hit the cap.
		if len(rl.entries) >= maxRateLimitEntries {
			rl.evictOldest(1)
		}
		rl.entries[key] = &rateLimitEntry{count: 1, windowStart: now}
		return true
	}

	entry.count++
	return entry.count <= rl.rate
}

// evictOldest removes the n oldest entries (by windowStart) to make room.
// Caller must hold rl.mu.
func (rl *rateLimiter) evictOldest(n int) {
	for i := 0; i < n; i++ {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for key, entry := range rl.entries {
			if first || entry.windowStart.Before(oldestTime) {
				oldestKey = key
				oldestTime = entry.windowStart
				first = false
			}
		}
		if oldestKey != "" {
			delete(rl.entries, oldestKey)
		}
	}
}

// extractHost extracts the host (IP only, no port) from r.RemoteAddr.
// For IPv6 addresses like "[::1]:1234", it returns "::1".
// Falls back to the raw RemoteAddr if parsing fails.
func extractHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func (n *NativeChannel) rateLimitMiddleware(limiter *rateLimiter, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractHost(r.RemoteAddr)
		if !limiter.allow(key) {
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded, try again later", "rate_limit_exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
