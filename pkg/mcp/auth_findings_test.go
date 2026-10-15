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

// This file holds the regression tests of the auth-engine findings whose fix
// is NOT a change of the flow's contract: the StartAuth budget (F1),
// CheckAuth's Authenticated in the header/challenge branches (F3) and the
// user's denial code surviving the error classifier (F5). The store's copy
// semantics (F4) live next to their subject, in auth_store_test.go.

// ---------------------------------------------------------------------------
// F1 — the pre-flow phase is bounded by the documented budget
// ---------------------------------------------------------------------------

// silentTCPServer accepts TCP connections and NEVER answers: it reads whatever
// the client sends and writes nothing back, which is the one failure mode the
// caller's context cannot save it from (mcp-go builds an http.Client with no
// Timeout of its own, so an unbounded context waits forever).
type silentTCPServer struct {
	ln net.Listener
	// dropped is closed by the first connection the CLIENT tears down. It is
	// the liveness proof: after StartAuth returns, the probe's request must be
	// gone, not left in flight against a hanging server.
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

// url returns the MCP endpoint the flow must dial.
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

// drain reads and discards the client's request bytes. It blocks until the
// client closes the connection (or an error tears it down), which is what
// signals that the request was abandoned rather than left pending.
func (s *silentTCPServer) drain(c net.Conn) {
	buf := make([]byte, 512)
	for {
		if _, err := c.Read(buf); err != nil {
			s.once.Do(func() { close(s.dropped) })
			return
		}
	}
}

// TestStartAuthPreFlowIsBoundedByTheBudget is the finding-F1 guard: the
// documented "Timeout bounds the whole flow" is only true if the PROBE (and
// the OAuth dial, discovery, DCR and the authorization URL after it) run on the
// budget. They used to run on the caller's raw context — which all three
// production callers pass as context.Background() — so a server that accepted
// the TCP connection and never answered blocked StartAuth forever: the CLI
// never printed the authorization URL, the TUI stayed in mcpAuthStarting, and
// the gateway held the per-(agent, server) flow lock for good.
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
		// A hard watchdog: the point of the finding is that this call never
		// returns, so a blocking implementation must fail the test instead of
		// hanging the package's whole test run.
		t.Fatal("StartAuth never returned: the pre-flow phase is not bounded by AuthOptions.Timeout")
	}
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("StartAuth against a server that never answers returned no error")
	}
	// The budget is the documented outcome, and it keeps the sentinel the CLI
	// already reports as "authorization timed out": the pre-flow phase and the
	// flow goroutine share one budget, so both report the same thing.
	if !errors.Is(err, ErrAuthTimedOut) {
		t.Fatalf("StartAuth = %v, want %v (the budget expired) — not a probe report, the server never answered", err, ErrAuthTimedOut)
	}
	if flow != nil {
		t.Fatalf("StartAuth returned a flow (%v) together with an error", flow)
	}
	// ~budget, not budget*N: the pre-flow phase may not spend a fresh budget of
	// its own on top of the flow's.
	if elapsed > 4*budget {
		t.Fatalf("StartAuth took %v for a %v budget", elapsed, budget)
	}
	// Value-free: the report must not carry the URL (a URL can carry a token
	// in its query string) nor the endpoint.
	if msg := err.Error(); strings.Contains(msg, "127.0.0.1") || strings.Contains(msg, "/mcp") {
		t.Fatalf("StartAuth error %q leaks the server URL", msg)
	}
	// No listener: the probe runs BEFORE the loopback bind, so a failed probe
	// can never leave a port bound. (Nothing was bound here because the
	// failure is in the probe; this asserts the observable half.)
	// The request itself must be torn down, not left in flight.
	select {
	case <-srv.dropped:
	case <-time.After(2 * time.Second):
		t.Fatal("the timed-out probe left its connection open: the request is still in flight")
	}
}

// TestStartAuthBudgetCoversTheHealthyPathToo proves the budget does not break
// the ordinary case: a server that answers keeps flowing, and the flow it
// returns is the one the caller expects (URL, pending status, cancellable).
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
	// The pre-flow phase spends part of the budget; the flow still gets what is
	// left, which for a generous budget is "everything that matters".
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("StartAuth against a live server took %v", elapsed)
	}
}

// TestStartAuthBudgetReleasesTheListenerWhenRegistrationStalls covers the other
// half of F1: the budget must hold for EVERY pre-flow step, and the loopback
// listener that already exists by then must be released. Here the probe, the
// OAuth dial and discovery all answer promptly and the authorization server's
// dynamic client registration stalls; the port that was bound for the redirect
// must still be free when StartAuth gives up.
func TestStartAuthBudgetReleasesTheListenerWhenRegistrationStalls(t *testing.T) {
	as := authtest.New()
	// A registration that never answers within the flow's budget. The stall is
	// released explicitly at cleanup so the test never waits for it, whatever
	// the engine does.
	release := make(chan struct{})
	asrv := httptest.NewServer(stallingRegisterHandler{
		next:    as.Handler(),
		stall:   5 * time.Second,
		release: release,
	})
	t.Cleanup(asrv.Close)
	// Registered after asrv.Close, so it runs BEFORE it (t.Cleanup is LIFO):
	// the stalled handler returns and Close does not block on it.
	t.Cleanup(func() { close(release) })
	as.SetIssuer(asrv.URL)
	store := NewAuthStore(t.TempDir())
	srv := ServerConfig{URL: asrv.URL + authtest.DefaultResourcePath, Auth: &AuthSpec{Type: typeAuthOAuth}}

	// Pin the loopback port so the test can prove the listener is gone.
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
	// Far below the 5s stall: the stalled request was really cancelled at the
	// deadline instead of being waited out.
	if elapsed > 4*budget {
		t.Fatalf("StartAuth took %v for a %v budget (the stalled registration was not bounded)", elapsed, budget)
	}
	if msg := err.Error(); strings.Contains(msg, "127.0.0.1") {
		t.Fatalf("StartAuth error %q leaks the server URL", msg)
	}
	// The listener bound for the redirect is gone with the failed phase.
	again, lerr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if lerr != nil {
		t.Fatalf("the loopback listener was leaked by the failed pre-flow phase: %v", lerr)
	}
	_ = again.Close()
	// Nothing was persisted as a completed login.
	if store.Authenticated("srv", srv.URL) {
		t.Fatal("a timed-out pre-flow phase stored a credential")
	}
}

// stallingRegisterHandler stalls the authorization server's dynamic client
// registration until the flow's budget expires (which cancels the request
// context) or until the test releases it, so it exercises a pre-flow step that
// hangs AFTER the loopback listener exists.
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

// TestStartAuthReleasesTheListenerWhenThePreFlowPhaseFails is the F1 companion
// for the ordinary failure: a pre-flow step that fails for a SERVER reason
// (here a rejected dynamic client registration) returns its own value-free
// report and still releases the port the listener had bound.
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
	// A server-side failure keeps its own report: it is NOT a timeout, and the
	// registration sentinel is what a caller can act on.
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

// ---------------------------------------------------------------------------
// F3 — CheckAuth's Authenticated is a store fact, in every branch
// ---------------------------------------------------------------------------

// TestCheckAuthAuthenticatedFollowsTheStoreInEveryBranch is the finding-F3
// guard: Authenticated means "a usable credential for this name+url exists in
// the store, i.e. no sign-in is needed", and the ONE login can remedy several
// entry shapes — an "oauth" block, a header-carrying entry and a bare recorded
// 401 (buildAuthRuntime attaches OAuth for a recorded challenge whatever the
// carrier is). Reporting false for the last two kept the WebUI offering an
// Authenticate button ("Sign-in required") to a user who had already signed in.
//
// Required and Method are orthogonal and keep their own rules: headers alone
// still do NOT set Required (they are frequently non-credential), which is why
// the fresh header row below is pinned unchanged.
func TestCheckAuthAuthenticatedFollowsTheStoreInEveryBranch(t *testing.T) {
	const url = "https://example.test/mcp"
	headers := ServerConfig{URL: url, Headers: map[string]string{"Authorization": "Bearer api-key"}}
	oauth := ServerConfig{URL: url, Auth: &AuthSpec{Type: typeAuthOAuth}}

	usable := &transport.Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}
	// Expired AND without a refresh token: mcp-go cannot rescue it, so the
	// store's verdict is "no usable credential".
	stale := &transport.Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(-time.Hour)}

	tests := []struct {
		name       string
		srv        ServerConfig
		challenged bool
		token      *transport.Token
		want       AuthStatus
	}{
		{
			// (a) the reported bug: header carrier, recorded 401, token stored
			// by the completed login ⇒ the user is signed in.
			name:       "header carrier with a recorded 401 and a token",
			srv:        headers,
			challenged: true,
			token:      usable,
			want:       AuthStatus{Required: true, Authenticated: true, Method: "headers"},
		},
		{
			// (b) same entry, no token yet: the sign-in is still owed.
			name:       "header carrier with a recorded 401 and no token",
			srv:        headers,
			challenged: true,
			want:       AuthStatus{Required: true, Authenticated: false, Method: "headers"},
		},
		{
			// (c) no declared carrier at all, a recorded 401 and a token: the
			// zero-config login succeeded, so Method stays "" and there is
			// nothing left to do.
			name:       "bare recorded 401 with a token",
			srv:        ServerConfig{URL: url},
			challenged: true,
			token:      usable,
			want:       AuthStatus{Required: true, Authenticated: true, Method: ""},
		},
		{
			// (d) the pinned header row: no challenge, no token. Headers alone
			// are still not evidence of auth.
			name: "fresh header row",
			srv:  headers,
			want: AuthStatus{Required: false, Authenticated: false, Method: "headers"},
		},
		{
			// The oauth branch keeps its behaviour (it is the branch that
			// already consulted the store).
			name:  "oauth block with a token",
			srv:   oauth,
			token: usable,
			want:  AuthStatus{Required: true, Authenticated: true, Method: "oauth"},
		},
		{
			// A header carrier whose stored token is EXPIRED beyond repair is
			// not authenticated: the store answers, and its answer is no.
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

	// The expired-without-refresh case the "unusable token" row leans on: the
	// store's predicate is mcp-go's own refresh decision, so an expired token
	// with a refresh token DOES count (the transport refreshes it).
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

// ---------------------------------------------------------------------------
// F5 — a user denial keeps its sanitized OAuth error code
// ---------------------------------------------------------------------------

// TestFlowDenialKeepsTheOAuthErrorCode is the finding-F5 guard: the browser
// callback of a DENIED authorization (`?error=access_denied`) is the one
// callback failure a user causes themselves, and the code used to be dropped by
// the flow's classifier: the loopback built the sanitized
// "authorization failed: access_denied", run() then ran it through
// valueFreeError (which only recovers codes from mcp-go's OAuthError) and the
// user was told "authorization token exchange failed" instead — a message about
// a token exchange that never happened.
//
// The sanitization is unchanged and is asserted here too: only the code is
// echoed, never error_description (attacker-controlled prose), the state or a
// value from the request.
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
	// The denial is terminal (a callback-delivered failure is one-shot: the
	// code it would have exchanged is not available to the caller) and it
	// stores no credential.
	if !flow.Done() {
		t.Fatal("Done = false after a callback denial (the failure must be terminal)")
	}
	if f.store.Authenticated("srv", f.mcpURL) {
		t.Fatal("a denied flow stored a credential")
	}
}

// TestFlowDenialCodeSanitizationHolds proves the code is echoed only when it is
// a safe token: everything else still collapses to the fixed "authorization was
// rejected" report, so a hostile authorization server cannot push arbitrary
// bytes (a script tag, a body chunk, a URL) into the user-visible error.
func TestFlowDenialCodeSanitizationHolds(t *testing.T) {
	// The classifier is the exact step that used to drop the code, so it is
	// driven directly with both shapes: the sanitized code survives, the
	// rejected one does not.
	if got := valueFreeError(&oauthCodeError{code: "access_denied"}); got == nil || got.Error() != "authorization failed: access_denied" {
		t.Fatalf("valueFreeError(oauthCodeError) = %v, want the sanitized code", got)
	}
	if got := classifyAuthError(&oauthCodeError{code: "access_denied"}); got == nil || got.Error() != "authorization failed: access_denied" {
		t.Fatalf("classifyAuthError(oauthCodeError) = %v, want the sanitized code", got)
	}
	if got := classifyAuthError(errAuthRejected); !errors.Is(got, errAuthExchangeFailed) {
		t.Fatalf("classifyAuthError(errAuthRejected) = %v, want the fixed generic report", got)
	}
	// A hostile code never reaches the typed error: sanitizeOAuthToken refuses
	// it, so resultOf reports the fixed rejection and the classifier keeps the
	// fixed message.
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
	// A WELL-FORMED but hostile code is bounded by the sanitizer's own rule
	// (≤64 characters of [A-Za-z0-9_.-]), which is the documented, deliberate
	// trade-off — it comes from the `error` field of a response we provoked and
	// never carries a body, URL or value. Pinned so a widening of that rule is
	// a conscious change rather than a silent one.
	if got := valueFreeError(&oauthCodeError{code: strings.Repeat("a", 64)}); got == nil || len(got.Error()) > len("authorization failed: ")+64 {
		t.Fatalf("valueFreeError = %v, want the bounded code echoed", got)
	}
}
