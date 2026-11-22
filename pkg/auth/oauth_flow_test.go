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

type flowTokenRecorder struct {
	srv      *httptest.Server
	mu       sync.Mutex
	hitCount int
	lastBody []byte
	form     url.Values
}

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

func (rec *flowTokenRecorder) hits() int {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.hitCount
}

func (rec *flowTokenRecorder) rawBody() []byte {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]byte(nil), rec.lastBody...)
}

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

func (rec *flowTokenRecorder) claudeFlowConfig() OAuthProviderConfig {
	cfg := ClaudeOAuthConfig()
	cfg.TokenURL = rec.srv.URL
	cfg.HTTPClient = rec.srv.Client()
	return cfg
}

func (rec *flowTokenRecorder) geminiFlowConfig() OAuthProviderConfig {
	cfg := GeminiOAuthConfig()
	cfg.TokenURL = rec.srv.URL
	cfg.HTTPClient = rec.srv.Client()
	return cfg
}

const claudeFlowResponseBody = `{"access_token":"flow-at","refresh_token":"flow-rt","expires_in":3600,"scope":"user:profile user:inference"}`

const geminiFlowResponseBody = `{"access_token":"flow-at-gemini","refresh_token":"flow-rt-gemini","expires_in":3600}`

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

func withSlackBudget(d time.Duration) time.Duration { return d + 2*time.Second }

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

func cancelFlow(t *testing.T, f *ProviderFlow) {
	t.Helper()
	if err := f.Cancel(); err != nil {
		t.Errorf("Cancel() cleanup error: %v", err)
	}
}

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

func flowStateFromURL(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parsing authorize URL %q: %v", rawURL, err)
	}
	return u.Query().Get("state")
}

func TestStartProviderFlowLoopbackBuildsAuthorizeURL(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()

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

	if q.Get("redirect_uri") != redirectURI {
		t.Errorf("URL() redirect_uri = %q, want RedirectURI() %q", q.Get("redirect_uri"), redirectURI)
	}
	if got := callbackPort(t, q.Get("redirect_uri")); got != port {
		t.Errorf("URL() port = %d, want the bound port %d", got, port)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("dialing the bound loopback port %d: %v", port, err)
	}
	_ = conn.Close()
}

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

func TestProviderFlowClaudeStateIsVerifier(t *testing.T) {
	rec := newFlowTokenRecorder(t, claudeFlowResponseBody)
	cfg := rec.claudeFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

	state := flowStateFromURL(t, f.URL())
	if state == "" {
		t.Fatal("authorize URL carries no state")
	}
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

	if err := f.Submit("CODE#VERIFIER-PLACEHOLDER"); !errors.Is(err, ErrFlowStateMismatch) {
		t.Fatalf("Submit(bogus state) error = %v, want ErrFlowStateMismatch", err)
	}
	if f.Done() {
		t.Fatal("Done() = true after a rejected paste, want false (still pending)")
	}
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0", got)
	}

	if err := f.Submit("CODE#" + f.pkce.CodeVerifier); err != nil {
		t.Fatalf("Submit(real verifier) error: %v", err)
	}
	if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
		t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
	}
}

func TestProviderFlowManualRequiresPresetRedirect(t *testing.T) {
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

func TestProviderFlowSubmitBareCode(t *testing.T) {
	rec := newFlowTokenRecorder(t, claudeFlowResponseBody)
	cfg := rec.claudeFlowConfig()

	f, err := StartProviderFlow(nil, cfg, FlowOptions{Mode: FlowModeManual})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)

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
	if f.Done() {
		t.Fatalf("Done() = true after a rejected paste, want false (flow must stay pending)")
	}
	if status, serr := f.Status(); status != FlowStatusPending || serr != nil {
		t.Fatalf("Status() = (%q, %v), want (%q, nil) after a rejected paste", status, serr, FlowStatusPending)
	}
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (a stale state must not exchange)", got)
	}

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
	if f.Done() {
		t.Error("Done() = true after blank Submits, want false (still pending)")
	}
	if got := rec.hits(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0", got)
	}

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

	requirePortFree(t, port, "after Cancel")

	if err := f.Submit("LATE-CODE"); !errors.Is(err, ErrFlowClosed) {
		t.Errorf("Submit after Cancel error = %v, want ErrFlowClosed", err)
	}
}

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
		if !waitTerminal(t, f, 2*time.Second) {
			t.Fatal("flow did not become terminal within ~2s of the callback")
		}
		if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
			t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
		}
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
		if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
			t.Fatalf("Status() after late Cancel = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
		}
		if cred := f.Credential(); cred == nil || cred.AccessToken != "flow-at-gemini" {
			t.Fatalf("Credential() after late Cancel = %#v, want the one from the callback", cred)
		}
		requirePortFree(t, port, "after Cancel following a terminal transition")
	})
}

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
		cfg := rec.geminiFlowConfig()
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
		wg.Wait()
		if !waitTerminal(t, f, 2*time.Second) {
			t.Fatal("flow did not become terminal within ~2s of the racing callbacks")
		}
		if status, serr := f.Status(); status != FlowStatusDone || serr != nil {
			t.Fatalf("Status() = (%q, %v), want (%q, nil)", status, serr, FlowStatusDone)
		}
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
			_ = f.Submit("PASTED-CODE")
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

func TestProviderFlowCallbackLoserGetsAConflictBody(t *testing.T) {
	rec := newFlowTokenRecorder(t, "")
	cfg := rec.geminiFlowConfig()
	f, err := StartProviderFlow(nil, cfg, FlowOptions{})
	if err != nil {
		t.Fatalf("StartProviderFlow() error: %v", err)
	}
	defer cancelFlow(t, f)
	state := flowStateFromURL(t, f.URL())

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
	if status, _ := f.Status(); status != FlowStatusPending {
		t.Errorf("status = %q, want %q", status, FlowStatusPending)
	}
}
