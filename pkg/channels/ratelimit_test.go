package channels

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestExtractHost_IPv4WithPort(t *testing.T) {
	got := extractHost("192.168.0.50:41872")
	want := "192.168.0.50"
	if got != want {
		t.Errorf("extractHost(%q) = %q, want %q", "192.168.0.50:41872", got, want)
	}
}

func TestExtractHost_IPv6WithPort(t *testing.T) {
	got := extractHost("[::1]:1234")
	want := "::1"
	if got != want {
		t.Errorf("extractHost(%q) = %q, want %q", "[::1]:1234", got, want)
	}
}

func TestExtractHost_FallbackNoPort(t *testing.T) {
	got := extractHost("192.168.0.50")
	want := "192.168.0.50"
	if got != want {
		t.Errorf("extractHost(%q) = %q, want %q", "192.168.0.50", got, want)
	}
}

func TestRateLimiter_SameHostDifferentPortsShareBucket(t *testing.T) {
	// FIX-3: the middleware keys on host only (no port). Two requests from
	// the same IP but different ports share the same bucket.
	rl := newRateLimiter(2, time.Minute)
	defer rl.Stop()

	// Simulate what the middleware does: extractHost strips the port.
	key1 := extractHost("10.0.0.1:1000")
	key2 := extractHost("10.0.0.1:2000")

	if !rl.allow(key1) {
		t.Fatal("first request should be allowed")
	}
	if !rl.allow(key1) {
		t.Fatal("second request should be allowed")
	}
	// Third request from different port, same host — must be blocked.
	if rl.allow(key2) {
		t.Fatal("third request from same host different port should be blocked")
	}
}

func TestRateLimiter_DifferentHostsIsolation(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	defer rl.Stop()

	keyA := extractHost("10.0.0.1:1000")
	keyB := extractHost("10.0.0.2:1000")

	// Host A: use up quota.
	rl.allow(keyA)
	rl.allow(keyA)
	if rl.allow(keyA) {
		t.Fatal("host A should be rate-limited")
	}
	// Host B: separate bucket, should be fine.
	if !rl.allow(keyB) {
		t.Fatal("host B should be allowed (separate bucket)")
	}
}

func TestRateLimiter_EntryCapEviction(t *testing.T) {
	rl := newRateLimiter(100, time.Minute)
	defer rl.Stop()

	rl.mu.Lock()
	for i := 0; i < 100; i++ {
		rl.entries[fmt.Sprintf("10.0.0.%d", i+1)] = &rateLimitEntry{
			count:       1,
			windowStart: time.Now().Add(-time.Duration(100-i) * time.Second),
		}
	}
	rl.evictOldest(10)
	if len(rl.entries) != 90 {
		t.Errorf("after evicting 10, len = %d, want 90", len(rl.entries))
	}
	rl.mu.Unlock()
}

func TestRateLimiter_EntryCapViaAllow(t *testing.T) {
	rl := newRateLimiter(100, time.Minute)
	defer rl.Stop()

	// Fill to maxRateLimitEntries.
	rl.mu.Lock()
	for i := 0; i < maxRateLimitEntries; i++ {
		rl.entries[fmt.Sprintf("10.0.%d.%d", i/256, i%256)] = &rateLimitEntry{
			count:       1,
			windowStart: time.Now().Add(-time.Duration(i) * time.Second),
		}
	}
	rl.mu.Unlock()

	// One more entry should trigger eviction.
	rl.allow("192.168.1.1")

	rl.mu.Lock()
	size := len(rl.entries)
	rl.mu.Unlock()

	if size > maxRateLimitEntries {
		t.Errorf("entries size = %d, want <= %d", size, maxRateLimitEntries)
	}
}

func TestRateLimiter_WindowReset(t *testing.T) {
	rl := newRateLimiter(1, 50*time.Millisecond)
	defer rl.Stop()

	key := extractHost("10.0.0.1:1000")
	if !rl.allow(key) {
		t.Fatal("first request allowed")
	}
	if rl.allow(key) {
		t.Fatal("should be rate limited")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.allow(key) {
		t.Fatal("should be allowed after window reset")
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := newRateLimiter(100, time.Minute)
	defer rl.Stop()

	var wg sync.WaitGroup
	// 50 goroutines, 5 unique hosts.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// ExtractHost strips the port; all goroutines with same n%5
			// produce the same host key.
			remoteAddr := fmt.Sprintf("10.0.0.%d:%d", n%5+1, n)
			key := extractHost(remoteAddr)
			rl.allow(key)
		}(i)
	}
	wg.Wait()

	// 5 unique hosts (10.0.0.1 through 10.0.0.5).
	rl.mu.Lock()
	size := len(rl.entries)
	rl.mu.Unlock()
	if size != 5 {
		t.Errorf("entries = %d, want 5", size)
	}
}

func TestRateLimiter_IPv6BucketSharing(t *testing.T) {
	// FIX-3: IPv6 addresses like [::1]:1234 must be keyed on "::1".
	rl := newRateLimiter(1, time.Minute)
	defer rl.Stop()

	key := extractHost("[::1]:1234")
	if key != "::1" {
		t.Fatalf("extractHost IPv6 = %q, want ::1", key)
	}

	if !rl.allow(key) {
		t.Fatal("first request allowed")
	}
	if rl.allow(key) {
		t.Fatal("should be rate limited (same IPv6 host)")
	}
	// Different IPv6 host.
	key2 := extractHost("[::2]:5678")
	if !rl.allow(key2) {
		t.Fatal("different IPv6 host should be allowed")
	}
}
