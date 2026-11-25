package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/security"
	"github.com/xilistudios/lele/pkg/skills"
)

type rateLimitTestServer struct {
	channel  *NativeChannel
	server   *httptest.Server
	bus      *bus.MessageBus
	token    string
	clientID string
}

func withRateLimitDisabled(cfg *config.Config) *config.Config {
	cfg.Channels.Native.RateLimit = config.NativeRateLimitConfig{Enabled: false}
	return cfg
}

func newRateLimitTestServer(t *testing.T, cfg *config.Config) *rateLimitTestServer {
	t.Helper()

	cfg.Channels.Native.LeleDir = t.TempDir()

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	loop := newNativeTestAgentLoop(cfg)
	approvalMgr := NewApprovalManager()

	native, err := NewNativeChannel(cfg, msgBus, loop, approvalMgr)
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancel()
		native.Stop(ctx)
	})

	if err := native.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	mux := http.NewServeMux()
	native.RegisterRoutes(mux)
	handler := security.Middleware()(mux)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	pending, err := native.auth.GeneratePIN("RLTestDevice")
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}
	client, token, _, err := native.auth.PairWithPIN(pending.PIN, "RLTestDevice")
	if err != nil {
		t.Fatalf("PairWithPIN() error = %v", err)
	}

	return &rateLimitTestServer{
		channel:  native,
		server:   server,
		bus:      msgBus,
		token:    token,
		clientID: client.ClientID,
	}
}

func postPair(t *testing.T, baseURL, pin string) *http.Response {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/v1/auth/pair", "application/json",
		strings.NewReader(`{"pin":"`+pin+`"}`))
	if err != nil {
		t.Fatalf("POST /auth/pair: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func postRefresh(t *testing.T, baseURL, token string) *http.Response {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/v1/auth/refresh", "application/json",
		strings.NewReader(`{"refresh_token":"`+token+`"}`))
	if err != nil {
		t.Fatalf("POST /auth/refresh: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestRateLimitExplicitlyDisabledServesNo429(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0

	ts := newRateLimitTestServer(t, withRateLimitDisabled(cfg))

	for i := 0; i < 20; i++ {
		resp := postPair(t, ts.server.URL, "000000")
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("pair got 429 on attempt %d with rate limiting disabled", i+1)
		}
	}

	for i := 0; i < 40; i++ {
		resp := postRefresh(t, ts.server.URL, "not-a-real-token")
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("refresh got 429 on attempt %d with rate limiting disabled", i+1)
		}
	}
}

func TestRateLimitDisabledLeavesLimitersNil(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0

	ts := newRateLimitTestServer(t, withRateLimitDisabled(cfg))

	ch := ts.channel
	if ch.pinLimiter != nil {
		t.Error("pinLimiter: want nil (rate limiting disabled), got non-nil")
	}
	if ch.pairLimiter != nil {
		t.Error("pairLimiter: want nil (rate limiting disabled), got non-nil")
	}
	if ch.refreshLimiter != nil {
		t.Error("refreshLimiter: want nil (rate limiting disabled), got non-nil")
	}
	if ch.apiLimiter != nil {
		t.Error("apiLimiter: want nil (rate limiting disabled), got non-nil")
	}
	if ch.wsMessageLimiter != nil {
		t.Error("wsMessageLimiter: want nil (rate limiting disabled), got non-nil")
	}
	if ch.authLogLimiter == nil {
		t.Error("authLogLimiter: want non-nil (always active), got nil")
	}
}

func TestRateLimitEnabledRespectsConfiguredLimits(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.RateLimit = config.NativeRateLimitConfig{
		Enabled:       true,
		PairPerMinute: 2,
	}

	ts := newRateLimitTestServer(t, cfg)

	for i := 0; i < 2; i++ {
		resp := postPair(t, ts.server.URL, "000000")
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("pair got 429 on attempt %d; rate limit hit too early (want 2 free)", i+1)
		}
	}

	resp := postPair(t, ts.server.URL, "000000")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("pair attempt 3: status = %d, want 429", resp.StatusCode)
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("429 response has no Retry-After header")
	}
	if _, err := strconv.Atoi(retryAfter); err != nil {
		t.Fatalf("Retry-After %q is not an integer: %v", retryAfter, err)
	}
}

func TestRateLimitEnabledDefaultsMatchLegacyRates(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.RateLimit = config.NativeRateLimitConfig{
		Enabled: true,
	}

	ts := newRateLimitTestServer(t, cfg)

	for i := 0; i < 5; i++ {
		resp := postPair(t, ts.server.URL, "000000")
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("pair got 429 on attempt %d; expected 5 free requests at legacy rate", i+1)
		}
	}

	resp := postPair(t, ts.server.URL, "000000")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("pair attempt 6: status = %d, want 429 (legacy 5/min rate not enforced)", resp.StatusCode)
	}
}

func TestRateLimitEnabledConstructsEveryBucket(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.RateLimit = config.NativeRateLimitConfig{
		Enabled: true,
	}

	ts := newRateLimitTestServer(t, cfg)

	ch := ts.channel
	if ch.pinLimiter == nil {
		t.Error("pinLimiter: want non-nil when enabled, got nil")
	}
	if ch.pairLimiter == nil {
		t.Error("pairLimiter: want non-nil when enabled, got nil")
	}
	if ch.refreshLimiter == nil {
		t.Error("refreshLimiter: want non-nil when enabled, got nil")
	}
	if ch.apiLimiter == nil {
		t.Error("apiLimiter: want non-nil when enabled, got nil")
	}
	if ch.wsMessageLimiter == nil {
		t.Error("wsMessageLimiter: want non-nil when enabled, got nil")
	}
	if ch.authLogLimiter == nil {
		t.Error("authLogLimiter: want non-nil, got nil")
	}
}

func TestRateLimitNegativeRateFallsBackToDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.RateLimit = config.NativeRateLimitConfig{
		Enabled:       true,
		PairPerMinute: -5,
	}

	ts := newRateLimitTestServer(t, cfg)

	for i := 0; i < 5; i++ {
		resp := postPair(t, ts.server.URL, "000000")
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("pair got 429 on attempt %d; negative rate -5 fell through to default 5", i+1)
		}
	}

	resp := postPair(t, ts.server.URL, "000000")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("pair attempt 6: status = %d, want 429 (default 5/min rate not applied)", resp.StatusCode)
	}
}

func TestRateLimitDisabledStopIsSafe(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.LeleDir = t.TempDir()
	withRateLimitDisabled(cfg)

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	loop := newNativeTestAgentLoop(cfg)
	approvalMgr := NewApprovalManager()

	native, err := NewNativeChannel(cfg, msgBus, loop, approvalMgr)
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	if native.pinLimiter != nil || native.pairLimiter != nil ||
		native.refreshLimiter != nil || native.apiLimiter != nil ||
		native.wsMessageLimiter != nil {
		t.Fatal("expected nil traffic limiters with rate limiting explicitly disabled")
	}

	ctx := t.Context()

	native.Stop(ctx)

	native.Stop(ctx)
}

func TestWSMessageRateLimitSkippedWhenDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0

	ts := newRateLimitTestServer(t, withRateLimitDisabled(cfg))

	if ts.channel.wsMessageLimiter != nil {
		t.Fatal("wsMessageLimiter must be nil when rate limiting is disabled")
	}

	ts.channel.auth.TrackSessionKey(ts.clientID, "test-session")

	client := &WSClient{
		ID:         "ws-rl-test",
		SessionKey: "test-session",
		ClientInfo: &ClientInfo{ClientID: ts.clientID},
		SendChan:   make(chan []byte, 16),
		done:       make(chan struct{}),
	}

	payload, err := json.Marshal(WSMessagePayload{Content: "hello", SessionKey: "test-session"})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		ts.channel.handleWSClientMessage(client, payload, "evt-ws-rl")
	}()

	drainCtx, drainCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer drainCancel()
	inbound, ok := ts.bus.ConsumeInbound(drainCtx)
	if !ok {
		t.Fatal("expected inbound message from handleWSClientMessage")
	}
	if inbound.ChatID != "test-session" {
		t.Errorf("inbound chat_id = %q, want %q", inbound.ChatID, "test-session")
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleWSClientMessage did not return (possible nil dereference?)")
	}

	select {
	case raw := <-client.SendChan:
		var ack WSMessage
		if err := json.Unmarshal(raw, &ack); err != nil {
			t.Fatalf("Unmarshal(ack) error = %v", err)
		}
		if ack.Event != "message.ack" {
			t.Fatalf("ack event = %q, want message.ack", ack.Event)
		}
	default:
		t.Fatal("expected message.ack on the client's SendChan")
	}
}

func TestAuthLogSamplerStaysActiveWhenRateLimitDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0

	ts := newRateLimitTestServer(t, withRateLimitDisabled(cfg))

	if ts.channel.authLogLimiter == nil {
		t.Fatal("authLogLimiter must be non-nil even when rate limiting is disabled")
	}

	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	for i := 0; i < 200; i++ {
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/agents", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer deliberately-invalid-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET /agents: %v", err)
		}
		resp.Body.Close()
	}

	lines := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "auth rejected") {
			lines++
		}
	}
	if lines > 10 {
		t.Fatalf("wrote %d auth-rejection log lines for 200 rejections with rate limiting OFF; sampler not working", lines)
	}
	if lines == 0 {
		t.Fatal("wrote 0 auth-rejection log lines; sampler is not active")
	}
}

func TestRateLimitDisabledConstructorUsesNilNotNoop(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.LeleDir = t.TempDir()
	withRateLimitDisabled(cfg)

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	loop := newNativeTestAgentLoop(cfg)
	approvalMgr := NewApprovalManager()

	native, err := NewNativeChannel(cfg, msgBus, loop, approvalMgr)
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	lims := []*rateLimiter{
		native.pinLimiter,
		native.pairLimiter,
		native.refreshLimiter,
		native.apiLimiter,
		native.wsMessageLimiter,
	}
	for i, lim := range lims {
		if lim != nil {
			t.Errorf("traffic limiter[%d] is non-nil; constructor should use nil when rate limiting is off", i)
		}
	}
}

func TestNewNativeChannelStopWithNilLimiters(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.LeleDir = t.TempDir()
	withRateLimitDisabled(cfg)

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	loop := newNativeTestAgentLoop(cfg)
	approvalMgr := NewApprovalManager()

	native, err := NewNativeChannel(cfg, msgBus, loop, approvalMgr)
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		native.Stop(ctx)
	}()
	go func() {
		defer wg.Done()
		native.Stop(ctx)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent Stop() calls did not return in 5s")
	}
}

func TestRateLimitTestHelperUsesProductionConstructor(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0

	ts := newRateLimitTestServer(t, cfg)

	if ts.channel.skillsLoader == nil {
		t.Fatal("skillsLoader is nil; the test helper must use NewNativeChannel, not a bare literal")
	}
	if ts.channel.auth == nil {
		t.Fatal("auth is nil; the test helper must use NewNativeChannel, not a bare literal")
	}
}

func TestRateLimitTestHelperAuthLogLimiterNotNil(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0

	ts := newRateLimitTestServer(t, cfg)

	if ts.channel.authLogLimiter == nil {
		t.Fatal("authLogLimiter is nil; the constructor must always create it")
	}
}

func TestBareChannelWithExplicitLimitersStillWorks(t *testing.T) {
	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0

	auth, err := NewAuthManager(&cfg.Channels.Native, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager() error = %v", err)
	}

	ch := &NativeChannel{
		base:             NewBaseChannel(ChannelName, cfg.Channels.Native, msgBus, []string{}),
		cfg:              &cfg.Channels.Native,
		auth:             auth,
		bus:              msgBus,
		agentLoop:        newNativeTestAgentLoop(cfg),
		wsClients:        make(map[string]*WSClient),
		pinLimiter:       newRateLimiter(10, time.Minute),
		pairLimiter:      newRateLimiter(5, time.Minute),
		refreshLimiter:   newRateLimiter(20, time.Minute),
		apiLimiter:       newRateLimiter(120, time.Minute),
		wsMessageLimiter: newRateLimiter(30, time.Minute),
		authLogLimiter:   newRateLimiter(6, time.Minute),
		skillsLoader:     &skills.SkillsLoader{},
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	t.Cleanup(func() { ch.Stop(ctx) })

	mux := http.NewServeMux()
	ch.RegisterRoutes(mux)
	handler := security.Middleware()(mux)
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	pending, err := auth.GeneratePIN("BareTestDevice")
	if err != nil {
		t.Fatalf("GeneratePIN() error = %v", err)
	}
	_, token, _, err := auth.PairWithPIN(pending.PIN, "BareTestDevice")
	if err != nil {
		t.Fatalf("PairWithPIN() error = %v", err)
	}
	_ = token

	limited := false
	for i := 0; i < 40; i++ {
		resp := postRefresh(t, server.URL, "not-a-real-token")
		if resp.StatusCode == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("refresh never rate limited over 40 attempts with explicit limiters; nil-gate broke the literal path")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"", false},
		{"localhost", true},
		{"127.0.0.1", true},
		{"::1", true},
		{"[::1]", true},
		{"0.0.0.0", false},
		{"::", false},
		{"192.168.0.171", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := IsLoopbackHost(tt.host); got != tt.want {
				t.Errorf("IsLoopbackHost(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestConfiguredRateIsEnforcedPerBucket(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.RateLimit = config.NativeRateLimitConfig{
		Enabled:             true,
		PinPerMinute:        2,
		PairPerMinute:       1,
		RefreshPerMinute:    3,
		APIPerMinute:        4,
		WSMessagesPerMinute: 5,
	}

	ts := newRateLimitTestServer(t, cfg)

	type bucket struct {
		name       string
		send       func(t *testing.T) *http.Response
		wantRate   int
		bucketName string
	}

	buckets := []bucket{
		{
			name: "pin (PinPerMinute=2)",
			send: func(t *testing.T) *http.Response {
				req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/pin", nil)
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				req.Header.Set("Authorization", "Bearer "+ts.token)
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("GET /auth/pin: %v", err)
				}
				t.Cleanup(func() { resp.Body.Close() })
				return resp
			},
			wantRate:   2,
			bucketName: "pinLimiter",
		},
		{
			name: "pair (PairPerMinute=1)",
			send: func(t *testing.T) *http.Response {
				return postPair(t, ts.server.URL, "000000")
			},
			wantRate:   1,
			bucketName: "pairLimiter",
		},
		{
			name: "refresh (RefreshPerMinute=3)",
			send: func(t *testing.T) *http.Response {
				return postRefresh(t, ts.server.URL, "not-a-real-token")
			},
			wantRate:   3,
			bucketName: "refreshLimiter",
		},
		{
			name: "api (APIPerMinute=4)",
			send: func(t *testing.T) *http.Response {
				req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/auth/status", nil)
				if err != nil {
					t.Fatalf("NewRequest: %v", err)
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatalf("GET /auth/status: %v", err)
				}
				t.Cleanup(func() { resp.Body.Close() })
				return resp
			},
			wantRate:   4,
			bucketName: "apiLimiter",
		},
	}

	for _, b := range buckets {
		t.Run(b.name, func(t *testing.T) {
			for i := 0; i < b.wantRate; i++ {
				resp := b.send(t)
				if resp.StatusCode == http.StatusTooManyRequests {
					t.Fatalf("%s: request %d got 429; expected %d free requests",
						b.bucketName, i+1, b.wantRate)
				}
			}
			resp := b.send(t)
			if resp.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("%s: request %d: status = %d, want 429 (rate %d not enforced)",
					b.bucketName, b.wantRate+1, resp.StatusCode, b.wantRate)
			}
		})
	}

	t.Run("ws_message (WSMessagesPerMinute=5)", func(t *testing.T) {
		ts.channel.auth.TrackSessionKey(ts.clientID, "test-session")

		client := &WSClient{
			ID:         "ws-bucket-test",
			SessionKey: "test-session",
			ClientInfo: &ClientInfo{ClientID: ts.clientID},
			SendChan:   make(chan []byte, 16),
			done:       make(chan struct{}),
		}
		payload, err := json.Marshal(WSMessagePayload{Content: "hello", SessionKey: "test-session"})
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		allowed := 0
		firstErrIdx := -1
		for i := 0; i < 6; i++ {
			done := make(chan struct{})
			go func(idx int) {
				defer close(done)
				ts.channel.handleWSClientMessage(client, payload, "evt-ws-"+strconv.Itoa(idx))
			}(i)

			if i < 5 {
				dCtx, dCancel := context.WithTimeout(context.Background(), 2*time.Second)
				inbound, ok := ts.bus.ConsumeInbound(dCtx)
				if ok {
					_ = inbound
				}
				dCancel()
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("handleWSClientMessage did not return for request %d", i+1)
			}

			select {
			case raw := <-client.SendChan:
				var msg WSMessage
				if err := json.Unmarshal(raw, &msg); err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				if msg.Event == "error" {
					var errData map[string]string
					json.Unmarshal(msg.Data, &errData)
					if errData["code"] != "rate_limit_exceeded" {
						t.Fatalf("ws: request %d error code = %q, want rate_limit_exceeded", i+1, errData["code"])
					}
					if firstErrIdx == -1 {
						firstErrIdx = i
					}
				} else {
					allowed++
				}
			default:
				t.Fatal("expected message on SendChan")
			}
		}
		if allowed != 5 {
			t.Fatalf("ws: allowed %d requests, want exactly 5 (WSMessagesPerMinute=5)", allowed)
		}
		if firstErrIdx != 5 {
			t.Fatalf("ws: first rate-limit error at index %d, want 5 (WSMessagesPerMinute=5)", firstErrIdx)
		}
	})
}

func TestRateLimitEnabledByDefaultConstructsGenerousBuckets(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Host = "127.0.0.1"
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.LeleDir = t.TempDir()

	if !cfg.Channels.Native.RateLimit.Enabled {
		t.Fatal("DefaultConfig() disables native rate limiting; the secure default is on")
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()

	native, err := NewNativeChannel(cfg, msgBus, newNativeTestAgentLoop(cfg), NewApprovalManager())
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}

	limiters := []struct {
		name  string
		limit *rateLimiter
		rate  int
	}{
		{name: "pin", limit: native.pinLimiter, rate: config.DefaultNativeRateLimitPinPerMinute},
		{name: "pair", limit: native.pairLimiter, rate: config.DefaultNativeRateLimitPairPerMinute},
		{name: "refresh", limit: native.refreshLimiter, rate: config.DefaultNativeRateLimitRefreshPerMinute},
		{name: "api", limit: native.apiLimiter, rate: 600},
		{name: "ws", limit: native.wsMessageLimiter, rate: 600},
	}
	for _, limiter := range limiters {
		if limiter.limit == nil {
			t.Errorf("%s limiter is nil; the default config must enable every bucket", limiter.name)
			continue
		}
		if limiter.limit.rate != limiter.rate {
			t.Errorf("%s limiter rate = %d, want %d", limiter.name, limiter.limit.rate, limiter.rate)
		}
	}
}
