package channels

import (
	"bytes"
	"log"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The WebUI renews its access token through /auth/refresh in the background.
// That endpoint used to share the pairing bucket — 5 requests per minute per
// IP — so ordinary use (several tabs, a few sessions, a page left open) could
// exhaust it, and the client treated the resulting 429 as a dead credential
// and signed the user out. These tests pin the structural fix: renewal has its
// own budget, and a rejection tells the client how long to wait.

func postAuth(t *testing.T, url, body string) *http.Response {
	t.Helper()

	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestRefreshEndpointHasItsOwnBudget(t *testing.T) {
	ts := newNativeTestServer(t)

	// Ten renewals is more than the pairing bucket (5/min) allows but well
	// within renewal's own budget, and each is rejected by the handler with 400
	// because the token is fake. Anything answering 429 here means the two
	// endpoints are sharing a bucket again.
	for i := 0; i < 10; i++ {
		resp := postAuth(t, ts.server.URL+"/api/v1/auth/refresh", `{"refresh_token":"not-a-real-token"}`)
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("refresh was rate limited after %d attempts: renewal shares the pairing bucket", i+1)
		}
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("attempt %d: status = %d, want 400 (handler reached and rejected the token)", i+1, resp.StatusCode)
		}
	}
}

func TestRefreshAndPairLimitsAreIndependent(t *testing.T) {
	ts := newNativeTestServer(t)

	// Drain the pairing bucket completely.
	var pairLimited bool
	for i := 0; i < 12; i++ {
		resp := postAuth(t, ts.server.URL+"/api/v1/auth/pair", `{"pin":"000000"}`)
		if resp.StatusCode == http.StatusTooManyRequests {
			pairLimited = true
			break
		}
	}
	if !pairLimited {
		t.Fatal("pairing never rate limited over 12 attempts; the pair bucket is not enforcing its limit")
	}

	// A signed-in browser renewing right now must be unaffected: this is the
	// exact interleaving that used to log users out.
	for i := 0; i < 5; i++ {
		resp := postAuth(t, ts.server.URL+"/api/v1/auth/refresh", `{"refresh_token":"not-a-real-token"}`)
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("refresh rate limited while pairing was drained (attempt %d): buckets are not independent", i+1)
		}
	}
}

func TestRateLimitRejectionAdvertisesRetryAfter(t *testing.T) {
	ts := newNativeTestServer(t)

	// The renewal limiter is configured at 20/min; walk past it until the
	// limiter answers.
	var limited *http.Response
	for i := 0; i < 40; i++ {
		resp := postAuth(t, ts.server.URL+"/api/v1/auth/refresh", `{"refresh_token":"not-a-real-token"}`)
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = resp
			break
		}
	}
	if limited == nil {
		t.Fatal("refresh never rate limited over 40 attempts; the renewal limiter is not wired to the route")
	}

	raw := limited.Header.Get("Retry-After")
	if raw == "" {
		t.Fatal("429 has no Retry-After header; clients cannot back off correctly")
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("Retry-After %q is not an integer number of seconds: %v", raw, err)
	}
	if seconds < 1 || seconds > 60 {
		t.Fatalf("Retry-After = %d, want 1..60 (the window is one minute)", seconds)
	}
}

// captureLogs redirects the standard logger for the duration of fn.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	fn()
	return buf.String()
}

func TestAuthRejectionIsLogged(t *testing.T) {
	ts := newNativeTestServer(t)

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer deliberately-invalid")

	out := captureLogs(t, func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", resp.StatusCode)
		}
	})

	if !strings.Contains(out, "auth rejected") {
		t.Fatalf("rejected request left no trace in the log, got: %q", out)
	}
	if !strings.Contains(out, "auth_invalid_token") {
		t.Fatalf("log line does not name the rejection code, got: %q", out)
	}
}

func TestRateLimitRejectionIsLogged(t *testing.T) {
	ts := newNativeTestServer(t)

	out := captureLogs(t, func() {
		for i := 0; i < 40; i++ {
			resp := postAuth(t, ts.server.URL+"/api/v1/auth/refresh", `{"refresh_token":"not-a-real-token"}`)
			if resp.StatusCode == http.StatusTooManyRequests {
				return
			}
		}
		t.Error("never rate limited, nothing to assert about the log")
	})

	if !strings.Contains(out, "rate limit exceeded") {
		t.Fatalf("rate limit rejection left no trace in the log, got: %q", out)
	}
}

func TestRateLimitSamplerDoesNotFloodTheLog(t *testing.T) {
	ts := newNativeTestServer(t)

	// The limiter can reject far more often than once a minute (the renewal
	// bucket is 20/min and several endpoints share a source IP), so the log
	// line has to be sampled or a stuck client would write the log full.
	out := captureLogs(t, func() {
		for i := 0; i < 200; i++ {
			resp := postAuth(t, ts.server.URL+"/api/v1/auth/refresh", `{"refresh_token":"not-a-real-token"}`)
			resp.Body.Close()
		}
	})

	lines := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "rate limit exceeded") {
			lines++
		}
	}
	if lines > 10 {
		t.Fatalf("wrote %d rate-limit log lines for 200 rejections; sampling is not working", lines)
	}
}

func TestLimiterTimeUntilResetCountsDown(t *testing.T) {
	rl := newRateLimiter(1, 200*time.Millisecond)
	defer rl.Stop()

	if !rl.allow("client") {
		t.Fatal("first request should be allowed")
	}
	if rl.allow("client") {
		t.Fatal("second request should be limited")
	}

	full := rl.timeUntilReset("client")
	if full <= 0 || full > 200*time.Millisecond {
		t.Fatalf("timeUntilReset = %v, want (0, 200ms]", full)
	}

	time.Sleep(220 * time.Millisecond)
	if remaining := rl.timeUntilReset("client"); remaining > 0 {
		t.Fatalf("timeUntilReset = %v after the window elapsed, want <= 0", remaining)
	}
	if !rl.allow("client") {
		t.Fatal("request after the window should be allowed again")
	}
}

func TestPINEndpointRateLimitIsEnforced(t *testing.T) {
	ts := newNativeTestServer(t)

	// The PIN limiter existed, was stopped on shutdown and was wired to nothing:
	// a declared control that guarded no route. PIN generation evicts the oldest
	// pending PIN once the cap is reached, so unlimited generation from one
	// client could keep replacing the PIN another device is about to redeem.
	limited := false
	for i := 0; i < 15; i++ {
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/pin?device_name=probe", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("GET /auth/pin never rate limited over 15 requests; the pin limiter is still not wired")
	}
}
