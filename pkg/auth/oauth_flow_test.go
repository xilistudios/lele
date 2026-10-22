package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- helpers -----------------------------------------------------------------
//
// The flow tests are hermetic: every token endpoint is an httptest.Server and
// every "callback" is a plain http.Get against the flow's own loopback
// listener. No test opens a browser or reaches the network.

// flowTokenRecorder is the hermetic token endpoint for the flow tests. It
// records every request (hit count, raw body, parsed form) and answers 200
// with a fixed token payload. Fields are read only after the request that
// wrote them completed (the tests poll on the flow's terminal state first),
// mirroring the recorder pattern used by pasteTokenRecorder.
type flowTokenRecorder struct {
	srv      *httptest.Server
	mu       sync.Mutex
	hitCount int
	lastBody []byte
	form     url.Values
}

// newFlowTokenRecorder answers with the given JSON token body (defaults to a
// generic OpenAI-style token) and registers server cleanup.
func newFlowTokenRecorder(t *testing.T, responseBody string) *flowTokenRecorder {
	t.Helper()
	if responseBody == "" {
		responseBody = `{"access_token":"flow-at","refresh_token":"flow-rt","expires_in":3600}`
	}
	rec := &flowTokenRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = r.ParseForm()
		rec.mu.Lock()
		rec.hitCount++
		rec.lastBody = raw
		rec.form = r.Form
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, responseBody)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// hits returns how many token requests the recorder has served.
func (rec *flowTokenRecorder) hits() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.hitCount
}

// rawBody returns the raw body of the last recorded request.
func (rec *flowTokenRecorder) rawBody() []byte {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]byte(nil), rec.lastBody...)
}

// sent decodes the last recorded request into a flat map of the FIRST value of
// every field, whatever the wire encoding was: Claude preset requests are
// JSON, Gemini/OpenAI preset requests are form-urlencoded. This keeps the
// assertions encoding-agnostic without re-implementing the parsers.
func (rec *flowTokenRecorder) sent(t *testing.T) map[string]string {
	t.Helper()
	raw := rec.rawBody()

	var m map[string]string
	if err := json.Unmarshal(raw, &m); err == nil {
		return m
	}

	values, err := url.ParseQuery(string(raw))
	if err != nil {
		t.Fatalf("decoding token request body %q: %v", string(raw), err)
	}
	m = make(map[string]string, len(values))
	for k, vs := range values {
		if len(vs) > 0 {
			m[k] = vs[0]
		}
	}
	return m
}

// hasField reports whether the last recorded request carried the field at all
// (the Gemini contract is "no state field on the wire", not "empty value").
func (rec *flowTokenRecorder) hasField(field string) bool {
	raw := rec.rawBody()

	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err == nil {
		_, ok := m[field]
		return ok
	}

	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return false
	}
	_, ok := values[field]
	return ok
}

// claudeFlowConfig returns the Claude preset pointed at the recorder. The
// presets copy by value, so mutating the copy is safe.
func (rec *flowTokenRecorder) claudeFlowConfig() OAuthProviderConfig {
	cfg := ClaudeOAuthConfig()
	cfg.TokenURL = rec.srv.URL
	cfg.HTTPClient = rec.srv.Client()
	return cfg
}

// geminiFlowConfig returns the Gemini preset pointed at the recorder.
func (rec *flowTokenRecorder) geminiFlowConfig() OAuthProviderConfig {
	cfg := GeminiOAuthConfig()
	cfg.TokenURL = rec.srv.URL
	cfg.HTTPClient = rec.srv.Client()
	return cfg
}

// claudeFlowResponseBody includes the inference scope, because the Claude
// preset gates the exchange on it (RequireInferenceScope) plus a refresh token
// and expiry (RequireRefreshToken).
const claudeFlowResponseBody = `{"access_token":"flow-at","refresh_token":"flow-rt","expires_in":3600,"scope":"user:profile user:inference"}`

// geminiFlowResponseBody mirrors the Claude one: the Gemini preset requires a
// refresh token and expiry (RequireRefreshToken).
const geminiFlowResponseBody = `{"access_token":"flow-at-gemini","refresh_token":"flow-rt-gemini","expires_in":3600}`

// waitTerminal polls the flow until it is terminal or the deadline passes and
// returns whether it became terminal in time.
func waitTerminal(t *testing.T, f *ProviderFlow, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(withSlackBudget(within))
	for time.Now().Before(deadline) {
		if f.Done() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return f.Done()
}

// withSlackBudget stretches a test budget a little so a loaded CI box does not
// produce flakes on "becomes terminal within ~2s" style assertions.
func withSlackBudget(d time.Duration) time.Duration { return d + 2*time.Second }

// requireStatusError asserts the flow is terminal with an error matching want.
func requireStatusError(t *testing.T, f *ProviderFlow, want error) {
	t.Helper()
	status, err := f.Status()
	if status != FlowStatusError {
		t.Fatalf("Status() = %q, %v; want %q with error %v", status, err, FlowStatusError, want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("Status() error = %v, want errors.Is(%v)", err, want)
	}
	if f.Credential() != nil {
		t.Errorf("Credential() = %#v, want nil on error", f.Credential())
	}
}

// cancelFlow is the cleanup that guarantees no listener/goroutine outlives a
// failing test.
func cancelFlow(t *testing.T, f *ProviderFlow) {
	t.Helper()
	if err := f.Cancel(); err != nil {
		t.Errorf("Cancel() cleanup error: %v", err)
	}
}

// callbackPort extracts the loopback port from a redirect_uri of the shape
// http://localhost:<port><path>.
func callbackPort(t *testing.T, redirectURI string) int {
	t.Helper()
	u, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parsing redirect URI %q: %v", redirectURI, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("redirect URI %q carries no port: %v", redirectURI, err)
	}
	return port
}

// requirePortFree asserts that the flow's loopback port can be bound again,
// i.e. that no listener leaked. The bounded retry exists because a loopback
// listener shared with the Go http transport of OTHER tests in this package
// (the client side of httptest servers also binds 127.0.0.1:*) can leave the
// kernel holding the port in TIME_WAIT for a short while. Re-binding the SAME
// port is still proof that the flow's own listener is gone: a leaked listener
// would fail forever, not for a second.
func requirePortFree(t *testing.T, port int, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: listener leaked, cannot re-bind 127.0.0.1:%d: %v", what, port, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// flowStateFromURL extracts the state query parameter of an authorize URL.
func flowStateFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing authorize URL %q: %v", rawURL, err)
	}
	return u.Query().Get("state")
}

// --- 1. StartProviderFlow: immediate return, correct authorize URL ----------

func TestStartProviderFlowLoopbackBuildsAuthorizeURL(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig() // Port 0 => ephemeral loopback, CallbackPath /oauth2callback

	start := time.Now()
	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("StartProviderFlow took %s, want well under 1s", elapsed)
	}

	if status, serr := f.Status(); status != FlowStatusPending || serr != nil {
		t.Errorf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusPending)
	}
	if f.Done() {
		t.Error("Done() = true on a fresh flow, want false")
	}
	if f.Credential() != nil {
		t.Error("Credential() = non-nil on a fresh flow, want nil")
	}
	if f.Mode() != FlowModeLoopback {
		t.Errorf("Mode() = %v, want FlowModeLoopback", f.Mode())
	}

	redirectURI := f.RedirectURI()
	if wantPrefix := "http://localhost:"; !strings.HasPrefix(redirectURI, wantPrefix) {
		t.Fatalf("RedirectURI() = %q, want it to start with %q", redirectURI, wantPrefix)
	}
	if !strings.HasSuffix(redirectURI, "/oauth2callback") {
		t.Errorf("RedirectURI() = %q, want the /oauth2callback callback path", redirectURI)
	}
	port := callbackPort(t, redirectURI)

	u, err := url.Parse(f.URL())
	if err != nil {
		t.Fatalf("URL() is not parseable: %v", err)
	}
	q := u.Query()

	// The PKCE challenge and method must be on the URL, and the state must be
	// the CSRF-style random state (Gemini), not the verifier.
	if q.Get("code_challenge") == "" {
		t.Error("authorize URL carries no code_challenge")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if state := q.Get("state"); state == "" || state == cfg.ClientID {
		t.Error("authorize URL carries no state")
	}
	if q.Get("client_id") != cfg.ClientID {
		t.Errorf("client_id = %q, want %q", q.Get("client_id"), cfg.ClientID)
	}

	// The port advertised on the URL must be the port the listener actually
	// bound, and the redirect_uri must be byte-identical to RedirectURI().
	if q.Get("redirect_uri") != redirectURI {
		t.Errorf("URL() redirect_uri = %q, want RedirectURI() %q", q.Get("redirect_uri"), redirectURI)
	}
	if got := callbackPort(t, q.Get("redirect_uri")); got != port {
		t.Errorf("URL() port = %d, want the bound port %d", got, port)
	}

	// The bound port must really be the loopback listener.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("dialing the bound loopback port %d: %v", port, err)
	}
	_ = conn.Close()
}

// --- 2. loopback callback completes the flow ---------------------------------

func TestProviderFlowCallbackCompletes(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	state := flowStateFromURL(t, f.URL())
	resp, err := http.Get(f.RedirectURI() + "?code=CB-CODE&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", resp.StatusCode)
	}

	if !waitTerminal(t, f, 2*time.Second) {
		t.Fatal("flow did not become terminal within ~2s of the callback")
	}
	if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
		t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
	}
	cred := f.Credential()
	if cred == nil {
		t.Fatal("Credential() = nil after done")
	}
	if cred.AccessToken != "flow-at" {
		t.Errorf("Credential().AccessToken = %q, want %q", cred.AccessToken, "flow-at")
	}

	sent := rec.sent(t)
	if sent["code"] != "CB-CODE" {
		t.Errorf("exchange body[code] = %q, want CB-CODE", sent["code"])
	}
	if sent["redirect_uri"] != f.RedirectURI() {
		t.Errorf("exchange body[redirect_uri] = %q, want %q", sent["redirect_uri"], f.RedirectURI())
	}
}

// --- 3. a wrong state on the callback terminates with no exchange ------------

func TestProviderFlowStateMismatchRejects(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	resp, err := http.Get(f.RedirectURI() + "?code=CB-CODE&state=TOTALLY-WRONG")
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("callback status = %d, want 400", resp.StatusCode)
	}

	if !waitTerminal(t, f, 2*time.Second) {
		t.Fatal("flow did not become terminal within ~2s of the mismatched callback")
	}
	requireStatusError(t, f, ErrFlowStateMismatch)
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (a state mismatch must not exchange)", got)
	}
}

// --- 4. StateIsVerifier wiring ----------------------------------------------

// TestProviderFlowClaudeStateIsVerifier pins the Claude (StateIsVerifier)
// wiring end to end: the authorize URL's state is the verifier the challenge
// was derived from (so a callback carrying that state succeeds), and the
// exchange request carries a `state` field equal to it.
func TestProviderFlowClaudeStateIsVerifier(t *testing.T) {
	rec := newFlowTokenRecorder(t, claudeFlowResponseBody)
	cfg := rec.claudeFlowConfig() // Port 0 => ephemeral, CallbackPath /callback

	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	state := flowStateFromURL(t, f.URL())
	if state == "" {
		t.Fatal("authorize URL carries no state")
	}
	// Sanity: the state must be the 64-byte base64url verifier GeneratePKCE
	// produces (86 chars, no padding), i.e. it CAN be the PKCE verifier.
	if len(state) != 86 || strings.ContainsAny(state, "=/+") {
		t.Errorf("authorize URL state has the wrong shape for a PKCE verifier: %q", state)
	}

	resp, err := http.Get(f.RedirectURI() + "?code=CLAUDE-CODE&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", resp.StatusCode)
	}
	if !waitTerminal(t, f, 2*time.Second) {
		t.Fatal("flow did not become terminal within ~2s of the callback")
	}
	if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
		t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
	}

	sent := rec.sent(t)
	if sent["state"] != state {
		t.Errorf("exchange body[state] = %q, want the verifier %q", sent["state"], state)
	}
	if sent["code"] != "CLAUDE-CODE" {
		t.Errorf("exchange body[code] = %q, want CLAUDE-CODE", sent["code"])
	}
	if sent["redirect_uri"] != f.RedirectURI() {
		t.Errorf("exchange body[redirect_uri] = %q, want %q", sent["redirect_uri"], f.RedirectURI())
	}
}

// TestProviderFlowGeminiStateIsRandomAndNotEchoed is the mirror: Gemini's
// state is a random CSRF token (not the verifier) and the exchange must NOT
// carry a `state` field at all.
func TestProviderFlowGeminiStateIsRandomAndNotEchoed(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	state := flowStateFromURL(t, f.URL())
	if state == "" {
		t.Fatal("authorize URL carries no state")
	}
	// A random state is hex of 32 bytes (oauth.go generateState), never the
	// 86-char base64url shape of a PKCE verifier.
	if len(state) != 64 {
		t.Errorf("authorize URL state length = %d, want 64 (hex of 32 random bytes)", len(state))
	}

	resp, err := http.Get(f.RedirectURI() + "?code=GEM-CODE&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", resp.StatusCode)
	}
	if !waitTerminal(t, f, 2*time.Second) {
		t.Fatal("flow did not become terminal within ~2s of the callback")
	}

	if sent := rec.sent(t); sent["code"] != "GEM-CODE" {
		t.Errorf("exchange body[code] = %q, want GEM-CODE", sent["code"])
	}
	if rec.hasField("state") {
		t.Errorf("exchange body carried a state field (%q), want none for Gemini (spec §2)", rec.sent(t)["state"])
	}
}

// --- 5. manual mode ----------------------------------------------------------

func TestProviderFlowManualModeNoListener(t *testing.T) {
	rec := newFlowTokenRecorder(t, claudeFlowResponseBody)
	cfg := rec.claudeFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	if f.Mode() != FlowModeManual {
		t.Errorf("Mode() = %v, want FlowModeManual", f.Mode())
	}
	if want := "https://platform.claude.com/oauth/code/callback"; f.RedirectURI() != want {
		t.Errorf("RedirectURI() = %q, want %q", f.RedirectURI(), want)
	}
	// No port was bound: the redirect URI carries no host/port of ours, and a
	// second manual flow starts fine even with the first one still pending.
	if strings.Contains(f.RedirectURI(), "localhost") || strings.Contains(f.RedirectURI(), "127.0.0.1") {
		t.Errorf("RedirectURI() = %q, want the hosted redirect (no loopback listener)", f.RedirectURI())
	}
	f2, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("second StartProviderFlow(Mode: FlowModeManual) error: %v", err)
	}
	defer cancelFlow(t, f2)
	if f2.RedirectURI() != f.RedirectURI() {
		t.Errorf("second flow RedirectURI() = %q, want %q", f2.RedirectURI(), f.RedirectURI())
	}

	// The manual flow completes only through Submit. A paste whose state does
	// not belong to THIS flow is rejected and — because the attempt is
	// validated before the single exchange is claimed — leaves the flow
	// pending, so the user can paste again without restarting the login.
	if err := f.Submit("CODE#VERIFIER-PLACEHOLDER"); !errors.Is(err, ErrFlowStateMismatch) {
		t.Fatalf("Submit(bogus state) error = %v, want ErrFlowStateMismatch", err)
	}
	if f.Done() {
		t.Fatal("Done() = true after a rejected paste, want false (still pending)")
	}
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0", got)
	}

	// The same flow then completes with the real verifier.
	if err := f.Submit("CODE#" + f.pkce.CodeVerifier); err != nil {
		t.Fatalf("Submit(real verifier) error: %v", err)
	}
	if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
		t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
	}
}

func TestProviderFlowManualRequiresPresetRedirect(t *testing.T) {
	// openai (and google-antigravity) have no hosted redirect: the manual
	// flow is unavailable and nothing is bound.
	for _, cfg := range []OAuthProviderConfig{OpenAIOAuthConfig(), GoogleAntigravityOAuthConfig()} {
		f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
		if !errors.Is(err, ErrFlowNoManualRedirect) {
			t.Errorf("StartProviderFlow(%q, Mode: FlowModeManual) error = %v, want ErrFlowNoManualRedirect", cfg.Name, err)
		}
		if f != nil {
			t.Errorf("StartProviderFlow(%q) returned a non-nil flow on error", cfg.Name)
		}
	}
}

func TestProviderFlowManualGeminiRedirect(t *testing.T) {
	rec := newFlowTokenRecorder(t, geminiFlowResponseBody)
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	if want := "https://codeassist.google.com/authcode"; f.RedirectURI() != want {
		t.Errorf("RedirectURI() = %q, want %q", f.RedirectURI(), want)
	}
	u, err := url.Parse(f.URL())
	if err != nil {
		t.Fatalf("URL() is not parseable: %v", err)
	}
	if got := u.Query().Get("redirect_uri"); got != "https://codeassist.google.com/authcode" {
		t.Errorf("authorize URL redirect_uri = %q, want %q", got, "https://codeassist.google.com/authcode")
	}
	if got := u.Query().Get("code_challenge_method"); got != "S256" {
		t.Errorf("authorize URL code_challenge_method = %q, want S256", got)
	}
}

// --- 6. Submit: the paste grammar -------------------------------------------

func TestProviderFlowSubmitBareCode(t *testing.T) {
	rec := newFlowTokenRecorder(t, claudeFlowResponseBody)
	cfg := rec.claudeFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	// Correction V8: a bare code with no state is accepted and the verifier is
	// substituted as the exchange `state`.
	if err := f.Submit("BARE-CODE"); err != nil {
		t.Fatalf("Submit(bare code) error: %v", err)
	}
	if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
		t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
	}
	sent := rec.sent(t)
	if sent["code"] != "BARE-CODE" {
		t.Errorf("exchange body[code] = %q, want BARE-CODE", sent["code"])
	}
	if sent["state"] == "" {
		t.Error("exchange body[state] is empty, want the verifier substituted")
	}
	if sent["state"] != sent["code_verifier"] {
		t.Errorf("exchange body[state] = %q, want it equal to code_verifier %q", sent["state"], sent["code_verifier"])
	}
	if sent["redirect_uri"] != f.RedirectURI() {
		t.Errorf("exchange body[redirect_uri] = %q, want %q", sent["redirect_uri"], f.RedirectURI())
	}
}

func TestProviderFlowSubmitCallbackURL(t *testing.T) {
	rec := newFlowTokenRecorder(t, claudeFlowResponseBody)
	cfg := rec.claudeFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	verifier := flowStateFromURL(t, f.URL())
	pasted := f.RedirectURI() + "?code=PASTED-CODE&state=" + url.QueryEscape(verifier)
	if err := f.Submit(pasted); err != nil {
		t.Fatalf("Submit(callback URL) error: %v", err)
	}
	if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
		t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
	}
	sent := rec.sent(t)
	if sent["code"] != "PASTED-CODE" {
		t.Errorf("exchange body[code] = %q, want PASTED-CODE", sent["code"])
	}
	if sent["state"] != verifier {
		t.Errorf("exchange body[state] = %q, want the verifier %q", sent["state"], verifier)
	}
}

func TestProviderFlowSubmitClaudeStateMismatch(t *testing.T) {
	rec := newFlowTokenRecorder(t, claudeFlowResponseBody)
	cfg := rec.claudeFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	err = f.Submit("CODE#SOME-OTHER-VERIFIER")
	if !errors.Is(err, ErrFlowStateMismatch) {
		t.Fatalf("Submit(code#stale state) error = %v, want ErrFlowStateMismatch", err)
	}
	// A rejected paste must NOT consume the single exchange attempt: the flow
	// stays pending so a TUI paste box can simply ask again (only a claimed
	// attempt — an actual token request — may turn the flow terminal).
	if f.Done() {
		t.Fatalf("Done() = true after a rejected paste, want false (flow must stay pending)")
	}
	if status, serr := f.Status(); status != FlowStatusPending || serr != nil {
		t.Fatalf("Status() = (%q, %v), want (%q, nil) after a rejected paste", status, serr, FlowStatusPending)
	}
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (a stale state must not exchange)", got)
	}

	// ...and the same flow then accepts the CORRECT paste, echoing the verifier.
	if err := f.Submit("PASTED-CODE#" + f.pkce.CodeVerifier); err != nil {
		t.Fatalf("Submit(correct paste after a rejected one) error: %v", err)
	}
	if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
		t.Fatalf("Status() after the retry = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
	}
	if got := rec.hits(); got != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (the retry is the one shot)", got)
	}
	if sent := rec.sent(t); sent["code"] != "PASTED-CODE" || sent["state"] != f.pkce.CodeVerifier {
		t.Errorf("exchange body = code %q state %q, want PASTED-CODE / %q", sent["code"], sent["state"], f.pkce.CodeVerifier)
	}
}

func TestProviderFlowSubmitGeminiRequiresState(t *testing.T) {
	rec := newFlowTokenRecorder(t, geminiFlowResponseBody)
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	// A callback-shaped paste carrying a WRONG state must be rejected.
	wrong := f.RedirectURI() + "?code=GEM-CODE&state=WRONG"
	if err := f.Submit(wrong); !errors.Is(err, ErrFlowStateMismatch) {
		t.Fatalf("Submit(callback with wrong state) error = %v, want ErrFlowStateMismatch", err)
	}
	if f.Done() {
		t.Fatalf("Done() = true after a rejected paste, want false (flow must stay pending)")
	}
	if got := rec.hits(); got != 0 {
		t.Fatalf("token endpoint hits = %d, want 0 (a stale state must not exchange)", got)
	}

	// A fresh flow: a bare code needs no state and is used verbatim, with NO
	// `state` field on the exchange.
	f2, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("second StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f2)
	if err := f2.Submit("GEM-BARE-CODE"); err != nil {
		t.Fatalf("Submit(bare code) error: %v", err)
	}
	if status, serr := f2.Status(); status != FlowStatusDone || serr != nil {
		t.Fatalf("second flow Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
	}
	if sent := rec.sent(t); sent["code"] != "GEM-BARE-CODE" {
		t.Errorf("exchange body[code] = %q, want GEM-BARE-CODE", sent["code"])
	}
	if rec.hasField("state") {
		t.Errorf("exchange body carried a state field, want none for Gemini")
	}
}

func TestProviderFlowSubmitGeminiTreatsCodeHashStateAsBareCode(t *testing.T) {
	rec := newFlowTokenRecorder(t, geminiFlowResponseBody)
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	// The documented jcode quirk: `CODE#STATE` is not callback-shaped for the
	// random-state branch, so the WHOLE line is the code. Do not "fix" this.
	if err := f.Submit("abc#def"); err != nil {
		t.Fatalf("Submit(abc#def) error: %v", err)
	}
	if sent := rec.sent(t); sent["code"] != "abc#def" {
		t.Errorf("exchange body[code] = %q, want the verbatim paste %q", sent["code"], "abc#def")
	}
	if rec.hasField("state") {
		t.Errorf("exchange body carried a state field, want none for Gemini")
	}
}

func TestProviderFlowSubmitEmpty(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	for _, blank := range []string{"", "   ", "\t\n "} {
		if err := f.Submit(blank); !errors.Is(err, ErrFlowNoCode) {
			t.Errorf("Submit(%q) error = %v, want ErrFlowNoCode", blank, err)
		}
	}
	// A blank paste must not consume the one-shot nor terminate the flow: the
	// user can simply paste again.
	if f.Done() {
		t.Error("Done() = true after blank Submits, want false (still pending)")
	}
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0", got)
	}

	// Once the flow is terminal, EVERY paste — blank included — reports
	// ErrFlowClosed (terminal check before the blank check): a caller that
	// keeps its paste box open on ErrFlowNoCode must not wait for a flow that
	// already ended (e.g. the callback won a Submit-vs-callback race).
	if err := f.Submit("REAL-CODE"); err != nil {
		t.Fatalf("Submit(real code) error: %v", err)
	}
	if !f.Done() {
		t.Fatal("Done() = false after a successful Submit, want true")
	}
	for _, blank := range []string{"", "   "} {
		if err := f.Submit(blank); !errors.Is(err, ErrFlowClosed) {
			t.Errorf("Submit(%q) on a terminal flow error = %v, want ErrFlowClosed (not ErrFlowNoCode)", blank, err)
		}
	}
}

// --- 6b. callback `error=` sanitization --------------------------------------

// TestSanitizeCallbackError pins the sanitizer that keeps a crafted local
// redirect (a query parameter under the attacker's control) from injecting
// control bytes — ESC sequences that would repaint/clear the TUI's alternate
// screen, newlines that forge messages — or unbounded junk into the terminal
// error string handleCallback produces.
func TestSanitizeCallbackError(t *testing.T) {
	t.Run("strips control bytes", func(t *testing.T) {
		got := sanitizeCallbackError("denied\x1b[2J\x1b[1;31mboom\r\nnext\tline\x00")
		if strings.ContainsAny(got, "\x1b\r\n\t\x00") {
			t.Errorf("sanitizeCallbackError left control bytes: %q", got)
		}
		if !strings.Contains(got, "denied") || !strings.Contains(got, "boom") {
			t.Errorf("sanitizeCallbackError(%q) = %q, want the printable text kept", "denied\\x1b[2J...", got)
		}
	})
	t.Run("caps the length", func(t *testing.T) {
		got := sanitizeCallbackError(strings.Repeat("x", 500))
		if len(got) > callbackErrorMaxLen {
			t.Errorf("len(sanitizeCallbackError(500 x's)) = %d, want <= %d", len(got), callbackErrorMaxLen)
		}
	})
	t.Run("keeps multi-byte runes whole", func(t *testing.T) {
		got := sanitizeCallbackError(strings.Repeat("é", 300))
		if got != strings.Repeat("é", callbackErrorMaxLen) {
			t.Errorf("sanitizeCallbackError over multibyte input = %q (runes=%d), want %d whole runes", got, len([]rune(got)), callbackErrorMaxLen)
		}
	})
	t.Run("empty and clean input pass through", func(t *testing.T) {
		if got := sanitizeCallbackError(""); got != "" {
			t.Errorf(`sanitizeCallbackError("") = %q, want ""`, got)
		}
		if got := sanitizeCallbackError("access_denied"); got != "access_denied" {
			t.Errorf("sanitizeCallbackError(clean) = %q, want it unchanged", got)
		}
	})
}

// --- 7. one-shot exchange ----------------------------------------------------

func TestProviderFlowSubmitIsOneShot(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	if err := f.Submit("FIRST-CODE"); err != nil {
		t.Fatalf("first Submit error: %v", err)
	}
	if err := f.Submit("SECOND-CODE"); !errors.Is(err, ErrFlowClosed) {
		t.Fatalf("second Submit error = %v, want ErrFlowClosed", err)
	}
	if got := rec.hits(); got != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1", got)
	}
	if sent := rec.sent(t); sent["code"] != "FIRST-CODE" {
		t.Errorf("exchange body[code] = %q, want FIRST-CODE", sent["code"])
	}
}

// --- 8. Cancel: idempotent, releases the port -------------------------------

func TestProviderFlowCancelReleasesPort(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	port := callbackPort(t, f.RedirectURI())

	if err := f.Cancel(); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
	// Second Cancel (and any after) must not panic nor return an error.
	if err := f.Cancel(); err != nil {
		t.Fatalf("second Cancel() error: %v", err)
	}

	requireStatusError(t, f, ErrFlowCanceled)
	if !f.Done() {
		t.Error("Done() = false after Cancel, want true")
	}
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (canceled flows never exchange)", got)
	}

	// The port must be free again: re-binding the very same port must succeed.
	// The listener was never used for a connection here, so this normally
	// succeeds instantly; the bounded retry exists because a loopback listener
	// shared with the Go http transport of OTHER tests in this package (the
	// client side of httptest servers also binds 127.0.0.1:*) can leave the
	// kernel holding the port in TIME_WAIT for a short while. A fresh bind of
	// the SAME port is still proof that the flow's own listener is gone — a
	// leaked listener would fail forever, not for a second.
	requirePortFree(t, port, "after Cancel")

	// Submit after Cancel is locked out too (ErrFlowClosed from the one-shot
	// guard: the flow is terminal).
	if err := f.Submit("LATE-CODE"); !errors.Is(err, ErrFlowClosed) {
		t.Errorf("Submit after Cancel error = %v, want ErrFlowClosed", err)
	}
}

// TestProviderFlowTerminalTransitionReleasesPort pins the release of the
// callback listener on EVERY terminal path, not only on Cancel. Before, Cancel
// returned early once the flow was terminal and was the only caller of
// server.Shutdown, so a SUCCESSFUL login (or a terminal error) left the port
// bound for the lifetime of the process — and openai pins that port (1455), so
// the NEXT login could not bind.
//
// Which sub-test kills which mutant:
//   - "success via callback" and "terminal error via callback" and
//     "success via Submit" each drop the release of their own succeed/fail
//     path (dropping `f.release()` from succeed turns the first red; from fail
//     the second; the third covers the Submit-side succeed path). Because the
//     success page is flushed BEFORE the exchange (see handleCallback), the
//     terminal transition now lands asynchronously right after http.Get
//     returns, so those sub-tests wait for the terminal state before asserting
//     the port is free — the release must hold whether it is observed before
//     or after the transition.
//   - "Cancel after terminal" kills the mutant that restores Cancel's early
//     `if f.Done() { return }` before release() (previously SURVIVING: none of
//     the other sub-tests call Cancel after a terminal transition).
func TestProviderFlowTerminalTransitionReleasesPort(t *testing.T) {
	t.Run("success via callback", func(t *testing.T) {
		rec := newFlowTokenRecorder(t, geminiFlowResponseBody)
		cfg := rec.geminiFlowConfig()
		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		port := callbackPort(t, f.RedirectURI())
		state := flowStateFromURL(t, f.URL())

		resp, err := http.Get(f.RedirectURI() + "?code=OK&state=" + url.QueryEscape(state))
		if err != nil {
			t.Fatalf("GET callback: %v", err)
		}
		_ = resp.Body.Close()
		// The handler flushes the page before the synchronous exchange, so
		// http.Get can return while the flow is still exchanging: the terminal
		// transition is asynchronous from the caller's point of view.
		if !waitTerminal(t, f, 2*time.Second) {
			t.Fatal("flow did not become terminal within ~2s of the callback")
		}
		if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
			t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
		}
		// No Cancel() call here on purpose: the release must not depend on it.
		requirePortFree(t, port, "after a successful callback")
	})

	t.Run("terminal error via callback", func(t *testing.T) {
		rec := newFlowTokenRecorder(t, "")
		cfg := rec.geminiFlowConfig()
		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		port := callbackPort(t, f.RedirectURI())

		resp, err := http.Get(f.RedirectURI() + "?code=OK&state=WRONG")
		if err != nil {
			t.Fatalf("GET callback: %v", err)
		}
		_ = resp.Body.Close()
		requireStatusError(t, f, ErrFlowStateMismatch)
		requirePortFree(t, port, "after a terminal callback error")
	})

	t.Run("success via Submit", func(t *testing.T) {
		rec := newFlowTokenRecorder(t, geminiFlowResponseBody)
		cfg := rec.geminiFlowConfig()
		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		port := callbackPort(t, f.RedirectURI())
		if err := f.Submit("PASTED"); err != nil {
			t.Fatalf("Submit() error: %v", err)
		}
		requirePortFree(t, port, "after a successful Submit")
	})

	// Kills the mutant that restores Cancel's early return
	// (`if f.Done() { … return }` before f.release()): a flow that already
	// finished through the callback still gets its port released by a late
	// Cancel, and — the property the doc promises — the credential survives it.
	t.Run("Cancel after terminal", func(t *testing.T) {
		rec := newFlowTokenRecorder(t, geminiFlowResponseBody)
		cfg := rec.geminiFlowConfig()
		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		port := callbackPort(t, f.RedirectURI())
		state := flowStateFromURL(t, f.URL())

		resp, err := http.Get(f.RedirectURI() + "?code=OK&state=" + url.QueryEscape(state))
		if err != nil {
			t.Fatalf("GET callback: %v", err)
		}
		_ = resp.Body.Close()
		if !waitTerminal(t, f, 2*time.Second) {
			t.Fatal("flow did not become terminal within ~2s of the callback")
		}
		if err := f.Cancel(); err != nil {
			t.Fatalf("Cancel() after terminal error: %v", err)
		}
		// The credential obtained before the Cancel must still be there…
		if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
			t.Fatalf("Status() after late Cancel = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
		}
		if cred := f.Credential(); cred == nil || cred.AccessToken != "flow-at-gemini" {
			t.Fatalf("Credential() after late Cancel = %#v, want the one from the callback", cred)
		}
		// …and the port must be free even if an earlier terminal transition
		// somehow skipped the release.
		requirePortFree(t, port, "after Cancel following a terminal transition")
	})
}

// --- 9. timeout --------------------------------------------------------------

func TestProviderFlowTimeout(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	if !waitTerminal(t, f, time.Second) {
		t.Fatal("flow did not become terminal within ~1s of the 50ms budget")
	}
	requireStatusError(t, f, ErrFlowTimeout)
}

// --- 10. mutation pins: credential fidelity, one-shot races, timeout release -

// newFlowErrorRecorder is newFlowTokenRecorder with an arbitrary status/body
// (used for token-endpoint failures like 400 invalid_grant).
func newFlowErrorRecorder(t *testing.T, status int, body string) *flowTokenRecorder {
	t.Helper()
	rec := &flowTokenRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = r.ParseForm()
		rec.mu.Lock()
		rec.hitCount++
		rec.lastBody = raw
		rec.form = r.Form
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// newFlowSlowRecorder answers with the given JSON body after the delay, so a
// test can overlap a second claim attempt with an in-flight exchange.
func newFlowSlowRecorder(t *testing.T, body string, delay time.Duration) *flowTokenRecorder {
	t.Helper()
	if body == "" {
		body = `{"access_token":"flow-at","refresh_token":"flow-rt","expires_in":3600}`
	}
	rec := &flowTokenRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = r.ParseForm()
		rec.mu.Lock()
		rec.hitCount++
		rec.lastBody = raw
		rec.form = r.Form
		rec.mu.Unlock()
		time.Sleep(delay)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// TestProviderFlowCredentialFidelityAfterSubmit pins what the flow publishes
// after a successful Submit: Credential() must carry the token the endpoint
// actually returned (AccessToken "flow-at", NOT an empty placeholder), and the
// exchange-error branch must leave Status()==error with Credential()==nil and
// the loopback port released — no matter how the exchange is reached.
//
// Mutants it kills:
//   - succeed(&AuthCredential{}) before the exchange (empty placeholder) —
//     caught by "success carries the real token";
//   - the same placeholder submitted through the callback — caught by
//     "success via callback carries the real token";
//   - the exchange-error branch not recording a terminal error / leaking the
//     port — caught by the three "exchange error" sub-cases (loopback
//     callback, loopback Submit, manual Submit).
func TestProviderFlowCredentialFidelityAfterSubmit(t *testing.T) {
	t.Run("success carries the real token", func(t *testing.T) {
		rec := newFlowTokenRecorder(t, claudeFlowResponseBody)
		cfg := rec.claudeFlowConfig()

		f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)

		if err := f.Submit("GOOD-CODE"); err != nil {
			t.Fatalf("Submit() error: %v", err)
		}
		cred := f.Credential()
		if cred == nil {
			t.Fatal("Credential() = nil after a successful Submit, want the exchanged credential")
		}
		if cred.AccessToken != "flow-at" {
			t.Errorf("Credential().AccessToken = %q, want %q (NOT an empty placeholder)", cred.AccessToken, "flow-at")
		}
		if cred.RefreshToken != "flow-rt" {
			t.Errorf("Credential().RefreshToken = %q, want %q", cred.RefreshToken, "flow-rt")
		}
	})

	t.Run("success via callback carries the real token", func(t *testing.T) {
		rec := newFlowTokenRecorder(t, geminiFlowResponseBody)
		cfg := rec.geminiFlowConfig()

		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		state := flowStateFromURL(t, f.URL())

		resp, err := http.Get(f.RedirectURI() + "?code=CB&state=" + url.QueryEscape(state))
		if err != nil {
			t.Fatalf("GET callback: %v", err)
		}
		_ = resp.Body.Close()
		if !waitTerminal(t, f, 2*time.Second) {
			t.Fatal("flow did not become terminal within ~2s of the callback")
		}
		cred := f.Credential()
		if cred == nil {
			t.Fatal("Credential() = nil after a successful callback, want the exchanged credential")
		}
		if cred.AccessToken != "flow-at-gemini" {
			t.Errorf("Credential().AccessToken = %q, want %q (NOT an empty placeholder)", cred.AccessToken, "flow-at-gemini")
		}
	})

	t.Run("exchange error via callback", func(t *testing.T) {
		rec := newFlowErrorRecorder(t, http.StatusBadRequest, `{"error":"invalid_grant"}`)
		cfg := rec.geminiFlowConfig()
		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		port := callbackPort(t, f.RedirectURI())
		state := flowStateFromURL(t, f.URL())

		resp, err := http.Get(f.RedirectURI() + "?code=BAD&state=" + url.QueryEscape(state))
		if err != nil {
			t.Fatalf("GET callback: %v", err)
		}
		_ = resp.Body.Close()
		if !waitTerminal(t, f, 2*time.Second) {
			t.Fatal("flow did not become terminal within ~2s of the failing callback")
		}
		if status, serr := f.Status(); status != FlowStatusError || serr == nil {
			t.Fatalf("Status() = (%q, %v), want (%q, <non-nil exchange error>)", status, serr, FlowStatusError)
		}
		if f.Credential() != nil {
			t.Errorf("Credential() = %#v, want nil after an exchange error", f.Credential())
		}
		if got := rec.hits(); got != 1 {
			t.Errorf("token endpoint hits = %d, want exactly 1", got)
		}
		requirePortFree(t, port, "after an exchange error via callback")
	})

	t.Run("exchange error via loopback Submit", func(t *testing.T) {
		rec := newFlowErrorRecorder(t, http.StatusBadRequest, `{"error":"invalid_grant"}`)
		cfg := rec.geminiFlowConfig()
		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		port := callbackPort(t, f.RedirectURI())

		err = f.Submit("BAD-PASTE")
		if err == nil {
			t.Fatal("Submit() error = nil, want the exchange error")
		}
		if status, serr := f.Status(); status != FlowStatusError || serr == nil {
			t.Fatalf("Status() = (%q, %v), want (%q, <non-nil exchange error>)", status, serr, FlowStatusError)
		}
		if f.Credential() != nil {
			t.Errorf("Credential() = %#v, want nil after an exchange error", f.Credential())
		}
		requirePortFree(t, port, "after an exchange error via Submit")
	})

	t.Run("exchange error via manual Submit", func(t *testing.T) {
		rec := newFlowErrorRecorder(t, http.StatusBadRequest, `{"error":"invalid_grant"}`)
		cfg := rec.geminiFlowConfig() // manual mode: no listener, hosted redirect
		f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)

		err = f.Submit("BAD-PASTE")
		if err == nil {
			t.Fatal("Submit() error = nil, want the exchange error")
		}
		if status, serr := f.Status(); status != FlowStatusError || serr == nil {
			t.Fatalf("Status() = (%q, %v), want (%q, <non-nil exchange error>)", status, serr, FlowStatusError)
		}
		if f.Credential() != nil {
			t.Errorf("Credential() = %#v, want nil after an exchange error", f.Credential())
		}
		if got := rec.hits(); got != 1 {
			t.Errorf("token endpoint hits = %d, want exactly 1", got)
		}
	})
}

// TestProviderFlowOneShotUnderConcurrency pins the exact property the doc
// claims: the authorization code is exchanged AT MOST ONCE even when two
// valid claims race inside the same flow (two callbacks, or a callback and a
// Submit), with a deliberately slow token endpoint to keep the first exchange
// in flight while the second claim attempt happens.
//
// Mutants it kills: dropping `if !f.claimExchange() { return }` in
// handleCallback (either of the two concurrent claims would then exchange) —
// caught by both sub-tests.
func TestProviderFlowOneShotUnderConcurrency(t *testing.T) {
	t.Run("two concurrent callbacks", func(t *testing.T) {
		rec := newFlowSlowRecorder(t, geminiFlowResponseBody, 300*time.Millisecond)
		cfg := rec.geminiFlowConfig()
		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		state := flowStateFromURL(t, f.URL())
		target := f.RedirectURI() + "?code=RACY-CODE&state=" + url.QueryEscape(state)

		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				resp, err := http.Get(target)
				if err == nil {
					_ = resp.Body.Close()
				}
			}()
		}
		wg.Wait() // both handlers returned (the page is flushed before the exchange)
		if !waitTerminal(t, f, 2*time.Second) {
			t.Fatal("flow did not become terminal within ~2s of the racing callbacks")
		}
		if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
			t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
		}
		// Let any (wrongly) concurrent second exchange finish before counting.
		time.Sleep(400 * time.Millisecond)
		if got := rec.hits(); got != 1 {
			t.Errorf("token endpoint hits = %d, want EXACTLY ONE (one-shot exchange)", got)
		}
	})

	t.Run("callback racing Submit", func(t *testing.T) {
		rec := newFlowSlowRecorder(t, geminiFlowResponseBody, 300*time.Millisecond)
		cfg := rec.geminiFlowConfig()
		f, err := StartProviderFlow(nil, cfg, FlowOptions{})
		if err != nil {
			t.Fatalf("StartProviderFlow() error: %v", err)
		}
		defer cancelFlow(t, f)
		state := flowStateFromURL(t, f.URL())

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp, err := http.Get(f.RedirectURI() + "?code=RACY-CODE&state=" + url.QueryEscape(state))
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		go func() {
			defer wg.Done()
			_ = f.Submit("PASTED-CODE") // either wins; the loser returns ErrFlowClosed
		}()
		wg.Wait()
		if !waitTerminal(t, f, 2*time.Second) {
			t.Fatal("flow did not become terminal within ~2s of the race")
		}
		if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
			t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
		}
		time.Sleep(400 * time.Millisecond)
		if got := rec.hits(); got != 1 {
			t.Errorf("token endpoint hits = %d, want EXACTLY ONE (one-shot exchange)", got)
		}
	})
}

// TestProviderFlowTimeoutReleasesPortWithoutCancel pins that the timeout path
// itself releases the loopback port: the test never calls Cancel before
// asserting, so a mutant that drops the release from the timeout branch (or
// from fail) leaks the listener and turns requirePortFree red.
func TestProviderFlowTimeoutReleasesPortWithoutCancel(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)
	port := callbackPort(t, f.RedirectURI())

	if !waitTerminal(t, f, time.Second) {
		t.Fatal("flow did not become terminal within ~1s of the 50ms budget")
	}
	requireStatusError(t, f, ErrFlowTimeout)
	requirePortFree(t, port, "after a timeout without Cancel")
}

// --- extra invariants: cancellation races, context tolerance -----------------

// TestProviderFlowCallbackAfterCancelIgnored pins that a callback racing (or
// following) the cancellation is answered but never changes the terminal
// state, and never reaches the token endpoint.
func TestProviderFlowCallbackAfterCancelIgnored(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)
	state := flowStateFromURL(t, f.URL())

	if err := f.Cancel(); err != nil {
		t.Fatalf("Cancel() error: %v", err)
	}
	resp, err := http.Get(f.RedirectURI() + "?code=LATE-CODE&state=" + url.QueryEscape(state))
	if err != nil {
		// The listener is gone: a refused connection is also a fine outcome.
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("callback status after Cancel = %d, want 400", resp.StatusCode)
	}
	requireStatusError(t, f, ErrFlowCanceled)
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0", got)
	}
}

// TestProviderFlowStartWithCancelledContext pins the ignored-ctx contract: an
// already-cancelled context neither panics nor aborts the flow — StartProviderFlow
// deliberately IGNORES ctx (nothing in setup is cancellable; the lifecycle is
// owned by Cancel), so the flow it returns is a normal pending flow.
func TestProviderFlowStartWithCancelledContext(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	defer cancel()

	f, err := StartProviderFlow(ctx, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow(cancelled ctx) error: %v", err)
	}
	defer cancelFlow(t, f)
	if f.Done() {
		t.Error("Done() = true for a flow started with an already-cancelled ctx, want false")
	}
}

// TestProviderFlowCallbackLoserGetsAConflictBody pins the response of a
// callback that lost the one-shot exchange race. It must not look like a
// success AND must not leave the browser on an empty 200: the winner is still
// exchanging, so the loser gets a 4xx with a fixed body that carries no code,
// state or token. Mutant it kills: returning from the `!f.claimExchange()`
// branch without writing anything.
func TestProviderFlowCallbackLoserGetsAConflictBody(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()
	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)
	state := flowStateFromURL(t, f.URL())

	// Simulate the winner: the exchange slot is taken, the flow is still
	// pending, so a callback arriving now can only be the loser.
	if !f.claimExchange() {
		t.Fatal("claimExchange() = false on a fresh flow")
	}

	resp, err := http.Get(f.RedirectURI() + "?code=LOSER-CODE&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if resp.StatusCode == http.StatusOK {
		t.Errorf("loser status = 200, want a conflict (the browser must not see success)")
	}
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("loser status = %d, want %d", resp.StatusCode, http.StatusConflict)
	}
	if len(body) == 0 {
		t.Error("loser response has an empty body (the browser would sit on a blank page)")
	}
	for _, leak := range []string{"LOSER-CODE", state, "Authentication successful"} {
		if leak != "" && bytes.Contains(body, []byte(leak)) {
			t.Errorf("loser body leaks %q: %s", leak, body)
		}
	}
	// The flow must still be pending: refusing the loser does not decide it.
	if status, _ := f.Status(); status != FlowStatusPending {
		t.Errorf("status = %q, want %q", status, FlowStatusPending)
	}
}
