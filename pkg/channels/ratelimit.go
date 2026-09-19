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

// sample reports whether a log line for key should be written now. It is a
// rate limiter used as a sampler, so nil means "no sampling configured" and the
// caller should log: failing open is correct here, unlike in allow, where a
// missing limiter would silently leave an endpoint unguarded.
func (rl *rateLimiter) sample(key string) bool {
	if rl == nil {
		return true
	}
	return rl.allow(key)
}

// timeUntilReset reports how long a caller keyed by `key` should wait before
// its window rolls over, which is what Retry-After is supposed to mean. It is
// advisory: it reads the entry in a separate lock acquisition from allow, so a
// racing request can only shorten the real wait, never extend it. A key with no
// entry reports the full window.
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
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := extractHost(r.RemoteAddr)
		if !limiter.allow(key) {
			retryIn := limiter.timeUntilReset(key)
			// The window can roll over between allow() and timeUntilReset(),
			// which would advertise a zero-second wait and send an eager client
			// straight back into another request. One second is the floor.
			if retryIn < time.Second {
				retryIn = time.Second
			}
			// Without this header a client cannot tell "back off for a while"
			// from "you are blocked", and has to guess. Guessing is what let a
			// browser treat a 429 as a dead session and log itself out.
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryIn.Seconds()))))
			// Rejections used to be silent, so "it keeps logging me out" left
			// nothing to confirm or refute. Sampled: a client hammering a
			// limited endpoint must not be able to write the log full.
			if n.authLogLimiter.sample(key + "|ratelimit") {
				log.Printf("WARNING: rate limit exceeded for %s on %s %s", key, r.Method, r.URL.Path)
			}
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded, try again later", "rate_limit_exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}
