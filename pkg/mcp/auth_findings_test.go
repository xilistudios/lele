package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	"github.com/xilistudios/lele/pkg/mcp/authtest"
)

type silentTCPServer struct {
	ln      net.Listener
	dropped chan struct{}
	once    sync.Once

	mu    sync.Mutex
	conns []net.Conn
}

func startSilentTCPServer(t *testing.T) *silentTCPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &silentTCPServer{ln: ln, dropped: make(chan struct{})}
	t.Cleanup(func() {
		_ = ln.Close()
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, c := range s.conns {
			_ = c.Close()
		}
	})
	go s.accept()
	return s
}

func (s *silentTCPServer) url() string { return "http://" + s.ln.Addr().String() + "/mcp" }

func (s *silentTCPServer) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
		go s.drain(c)
	}
}

func (s *silentTCPServer) drain(c net.Conn) {
	buf := make([]byte, 512)
	for {
		if _, err := c.Read(buf); err != nil {
			s.once.Do(func() { close(s.dropped) })
			return
		}
	}
}

func TestStartAuthPreFlowIsBoundedByTheBudget(t *testing.T) {
	srv := startSilentTCPServer(t)
	store := NewAuthStore(t.TempDir())

	const budget = 250 * time.Millisecond
	start := time.Now()
	done := make(chan struct{})
	var (
		flow *AuthFlow
		err  error
	)
	go func() {
		defer close(done)
		target := ServerConfig{URL: srv.url()}
		flow, err = StartAuth(context.Background(), "silent", target, store, AuthOptions{Timeout: budget})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StartAuth never returned: the pre-flow phase is not bounded by AuthOptions.Timeout")
	}
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("StartAuth against a server that never answers returned no error")
	}
	if !errors.Is(err, ErrAuthTimedOut) {
		t.Fatalf("StartAuth = %v, want %v (the budget expired) — not a probe report, the server never answered", err, ErrAuthTimedOut)
	}
	if flow != nil {
		t.Fatalf("StartAuth returned a flow (%v) together with an error", flow)
	}
	if elapsed > 4*budget {
		t.Fatalf("StartAuth took %v for a %v budget", elapsed, budget)
	}
	if msg := err.Error(); strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "/mcp") {
		t.Fatalf("StartAuth error %q leaks the server URL", msg)
	}
	select {
	case <-srv.dropped:
	case <-time.After(2 * time.Second):
		t.Fatal("the timed-out probe left its connection open: the request is still in flight")
	}
}

func TestStartAuthBudgetCoversTheHealthyPathToo(t *testing.T) {
	f := newAuthFixture(t)
	start := time.Now()
	flow, err := StartAuth(context.Background(), "srv", f.serverConfig(), f.store, AuthOptions{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()
	if flow.URL() == "" {
		t.Fatal("the flow reported no authorization URL")
	}
	if state, ferr := flow.Status(); state != authStatusPending || ferr != nil {
		t.Fatalf("Status = (%q, %v), want pending", state, ferr)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("StartAuth against a live server took %v", elapsed)
	}
}

func TestStartAuthBudgetReleasesTheListenerWhenRegistrationStalls(t *testing.T) {
	as := authtest.New()
	release := make(chan struct{})
	asrv := httptest.NewServer(stallingRegisterHandler{
		next:    as.Handler(),
		stall:   5 * time.Second,
		release: release,
	})
	t.Cleanup(asrv.Close)
	t.Cleanup(func() { close(release) })
	as.SetIssuer(asrv.URL)
	store := NewAuthStore(t.TempDir())
	srv := ServerConfig{URL: asrv.URL + authtest.DefaultResourcePath, Auth: &AuthSpec{Type: typeAuthOAuth}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	const budget = 300 * time.Millisecond
	start := time.Now()
	flow, err := StartAuth(context.Background(), "srv", srv, store, AuthOptions{Port: port, Timeout: budget})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("StartAuth succeeded although the registration never answered (flow %v)", flow)
	}
	if !errors.Is(err, ErrAuthTimedOut) {
		t.Fatalf("StartAuth = %v, want %v: the budget, not the server, is what ended the phase", err, ErrAuthTimedOut)
	}
	if elapsed > 4*budget {
		t.Fatalf("StartAuth took %v for a %v budget (the stalled registration was not bounded)", elapsed, budget)
	}
	if msg := err.Error(); strings.Contains(msg, "127.0.0.1") {
		t.Fatalf("StartAuth error %q leaks the server URL", msg)
	}
	again, lerr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if lerr != nil {
		t.Fatalf("the loopback listener was leaked by the failed pre-flow phase: %v", lerr)
	}
	_ = again.Close()
	if store.Authenticated("srv", srv.URL) {
		t.Fatal("a timed-out pre-flow phase stored a credential")
	}
}

type stallingRegisterHandler struct {
	next    http.Handler
	stall   time.Duration
	release <-chan struct{}
}

func (h stallingRegisterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/register" {
		select {
		case <-time.After(h.stall):
		case <-h.release:
		case <-r.Context().Done():
			return
		}
	}
	h.next.ServeHTTP(w, r)
}

func TestStartAuthReleasesTheListenerWhenThePreFlowPhaseFails(t *testing.T) {
	f := newAuthFixture(t)
	f.as.FailRegistrationOnce("invalid_client_metadata")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	flow, err := StartAuth(context.Background(), "srv", f.serverConfig(), f.store, AuthOptions{Port: port, Timeout: 30 * time.Second})
	if err == nil {
		t.Fatalf("StartAuth succeeded although the registration was rejected (flow %v)", flow)
	}
	if !errors.Is(err, errAuthRegistrationDone) {
		t.Fatalf("StartAuth = %v, want %v", err, errAuthRegistrationDone)
	}
	if msg := err.Error(); strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "invalid_client_metadata") {
		t.Fatalf("StartAuth error %q echoes server-controlled text", msg)
	}
	again, lerr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if lerr != nil {
		t.Fatalf("the loopback listener was leaked by the failed pre-flow phase: %v", lerr)
	}
	_ = again.Close()
}

func TestCheckAuthAuthenticatedFollowsTheStoreInEveryBranch(t *testing.T) {
	const url = "https://example.test/mcp"
	headers := ServerConfig{URL: url, Headers: map[string]string{"Authorization": "Bearer api-key"}}
	oauth := ServerConfig{URL: url, Auth: &AuthSpec{Type: typeAuthOAuth}}

	usable := &transport.Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}
	stale := &transport.Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Hour)}

	tests := []struct {
		name       string
		srv        ServerConfig
		challenged bool
		token      *transport.Token
		want       AuthStatus
	}{
		{
			name:       "header carrier with a recorded 401 and a token",
			srv:        headers,
			challenged: true,
			token:      usable,
			want:       AuthStatus{Required: true, Authenticated: true, Method: "headers"},
		},
		{
			name:       "header carrier with a recorded 401 and no token",
			srv:        headers,
			challenged: true,
			want:       AuthStatus{Required: true, Authenticated: false, Method: "headers"},
		},
		{
			name:       "bare recorded 401 with a token",
			srv:        ServerConfig{URL: url},
			challenged: true,
			token:      usable,
			want:       AuthStatus{Required: true, Authenticated: true, Method: ""},
		},
		{
			name: "fresh header row",
			srv:  headers,
			want: AuthStatus{Required: false, Authenticated: false, Method: "headers"},
		},
		{
			name:  "oauth block with a token",
			srv:   oauth,
			token: usable,
			want:  AuthStatus{Required: true, Authenticated: true, Method: "oauth"},
		},
		{
			name:       "header carrier with an unusable token",
			srv:        headers,
			challenged: true,
			token:      stale,
			want:       AuthStatus{Required: true, Authenticated: false, Method: "headers"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewAuthStore(t.TempDir())
			rec := AuthRecord{URL: url, AuthRequired: tc.challenged}
			if tc.token != nil {
				rec.AuthRequired = true
				rec.Token = tc.token
			}
			if tc.challenged || tc.token != nil {
				if err := store.Save("srv", rec); err != nil {
					t.Fatalf("seed store: %v", err)
				}
			}
			if got := CheckAuth(store, "srv", tc.srv); got != tc.want {
				t.Fatalf("CheckAuth = %+v, want %+v", got, tc.want)
			}
		})
	}

	store := NewAuthStore(t.TempDir())
	if err := store.Save("srv", AuthRecord{
		URL:          url,
		AuthRequired: true,
		Token: &transport.Token{
			AccessToken:  "expired",
			RefreshToken: "refresh",
			TokenType:    "Bearer",
			ExpiresAt:    time.Now().Add(-time.Hour),
		},
	}); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if got := CheckAuth(store, "srv", headers); got != (AuthStatus{Required: true, Authenticated: true, Method: "headers"}) {
		t.Fatalf("CheckAuth with a refreshable token = %+v, want authenticated (the transport refreshes it)", got)
	}
}

func TestFlowDenialKeepsTheOAuthErrorCode(t *testing.T) {
	const (
		descriptionCanary = "CANARY_ERROR_DESCRIPTION"
		stateCanary       = "CANARY_STATE"
	)
	f := newAuthFixture(t)
	ctx := context.Background()

	flow, err := StartAuth(ctx, "srv", f.serverConfig(), f.store, AuthOptions{})
	if err != nil {
		t.Fatalf("StartAuth: %v", err)
	}
	defer flow.Cancel()

	cb := flow.RedirectURI() + "?" + url.Values{
		"error":             {"access_denied"},
		"error_description": {descriptionCanary},
		"state":             {stateCanary},
	}.Encode()
	resp, err := f.asrv.Client().Get(cb)
	if err != nil {
		t.Fatalf("GET loopback callback: %v", err)
	}
	_ = resp.Body.Close()

	wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	derr := flow.Wait(wctx)
	if derr == nil {
		t.Fatal("the denied flow reported no error")
	}
	if !strings.Contains(derr.Error(), "access_denied") {
		t.Fatalf("flow error = %q, want it to carry the OAuth error code access_denied", derr.Error())
	}
	for _, leak := range []string{descriptionCanary, stateCanary, "http"} {
		if strings.Contains(derr.Error(), leak) {
			t.Fatalf("flow error %q leaked %q", derr.Error(), leak)
		}
	}
	if state, ferr := flow.Status(); state != authStatusError || ferr == nil || !strings.Contains(ferr.Error(), "access_denied") {
		t.Fatalf("Status = (%q, %v), want the denial with its code", state, ferr)
	}
	if !flow.Done() {
		t.Fatal("Done = false after a callback denial (the failure must be terminal)")
	}
	if f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("a denied flow stored a credential")
	}
}

func TestFlowDenialCodeSanitizationHolds(t *testing.T) {
	if got := valueFreeError(&oauthCodeError{code: "access_denied"}); got == nil || got.Error() != "authorization failed: access_denied" {
		t.Fatalf("valueFreeError(oauthCodeError) = %v, want the sanitized code", got)
	}
	if got := classifyAuthError(&oauthCodeError{code: "access_denied"}); got == nil || got.Error() != "authorization failed: access_denied" {
		t.Fatalf("classifyAuthError(oauthCodeError) = %v, want the sanitized code", got)
	}
	if got := classifyAuthError(errAuthRejected); !errors.Is(got, errAuthExchangeFailed) {
		t.Fatalf("classifyAuthError(errAuthRejected) = %v, want the fixed generic report", got)
	}
	for _, hostile := range []string{"<script>alert(1)</script>", "has space", strings.Repeat("x", 65), "bad\ncode", "token=abc&secret=def"} {
		lb, err := startLoopback("http://127.0.0.1:0/callback", nil)
		if err != nil {
			t.Fatalf("startLoopback: %v", err)
		}
		target := lb.RedirectURI() + "?" + url.Values{"error": {hostile}}.Encode()
		resp, err := http.Get(target)
		if err != nil {
			t.Fatalf("GET callback: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		_, _, aerr := lb.Await(context.Background())
		_ = lb.Close()
		if !errors.Is(aerr, errAuthRejected) {
			t.Errorf("hostile code %q: Await = %v, want %v", hostile, aerr, errAuthRejected)
		}
		if aerr != nil && strings.Contains(aerr.Error(), hostile) {
			t.Errorf("hostile code %q was echoed back", hostile)
		}
		if got := classifyAuthError(aerr); !errors.Is(got, errAuthExchangeFailed) {
			t.Errorf("hostile code %q: classifyAuthError = %v, want the fixed generic report", hostile, got)
		}
	}
	if got := valueFreeError(&oauthCodeError{code: strings.Repeat("a", 64)}); got == nil || len(got.Error()) > len("authorization failed: ")+64 {
		t.Fatalf("valueFreeError = %v, want the bounded code echoed", got)
	}
}
