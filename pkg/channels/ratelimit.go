package channels

import (
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

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
	if rl == nil {
		return
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	select {
	case <-rl.stopCh:
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
		if len(rl.entries) >= maxRateLimitEntries {
			rl.evictOldest(1)
		}
		rl.entries[key] = &rateLimitEntry{count: 1, windowStart: now}
		return true
	}

	entry.count++
	return entry.count <= rl.rate
}

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

func extractHost(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

func (rl *rateLimiter) sample(key string) bool {
	if rl == nil {
		return true
	}
	return rl.allow(key)
}

func (rl *rateLimiter) timeUntilReset(key string) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.entries[key]
	if !exists {
		return rl.window
	}
	remaining := rl.window - time.Since(entry.windowStart)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (n *NativeChannel) rateLimitMiddleware(limiter *rateLimiter, next http.Handler) http.Handler {
	if limiter == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractHost(r.RemoteAddr)
		if !limiter.allow(key) {
			retryIn := limiter.timeUntilReset(key)
			if retryIn < time.Second {
				retryIn = time.Second
			}
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryIn.Seconds()))))
			if n.authLogLimiter.sample(key + "|ratelimit") {
				log.Printf("WARNING: rate limit exceeded for %s on %s %s", key, r.Method, r.URL.Path)
			}
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded, try again later", "rate_limit_exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
