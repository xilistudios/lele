package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// claudeInferenceScopeWant is the exact gate message for
// action="token exchange" and scopes=["org:create_api_key"]. It is spelled out
// literally (not reused from the implementation) so that the wording — which is
// user-facing and points at the recovery command — cannot drift silently.
const claudeInferenceScopeWant = "Claude OAuth token exchange returned a token without the required user:inference scope (scopes: org:create_api_key). Re-run `lele auth login --provider claude` so lele opens the Claude.ai OAuth flow, or import/use a fresh Claude Code login."

func TestClaudeScopesHaveInference(t *testing.T) {
	tests := []struct {
		name   string
		scopes []string
		want   bool
	}{
		// The allowlist has SIX entries (jcode `claude_scopes_have_inference`),
		// not just `user:inference`: any of them grants inference access.
		{"user:inference", []string{"user:inference"}, true},
		{"user:ccr_inference", []string{"user:ccr_inference"}, true},
		{"user:voice", []string{"user:voice"}, true},
		{"org:service_key_inference", []string{"org:service_key_inference"}, true},
		{"workspace:developer", []string{"workspace:developer"}, true},
		{"workspace:inference", []string{"workspace:inference"}, true},
		{"allowlisted among others", []string{"org:create_api_key", "user:voice", "user:profile"}, true},

		{"org:create_api_key only", []string{"org:create_api_key"}, false},
		{"user:profile only", []string{"user:profile"}, false},
		{"near miss suffix", []string{"user:inference:read"}, false},
		{"near miss prefix", []string{"inference"}, false},
		{"nil", nil, false},
		{"empty", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeScopesHaveInference(tt.scopes); got != tt.want {
				t.Errorf("claudeScopesHaveInference(%#v) = %v, want %v", tt.scopes, got, tt.want)
			}
		})
	}
}

func TestEnsureClaudeInferenceScope(t *testing.T) {
	t.Run("nil scopes are accepted", func(t *testing.T) {
		if err := ensureClaudeInferenceScope(nil, "token exchange"); err != nil {
			t.Fatalf("ensureClaudeInferenceScope(nil) = %v, want nil", err)
		}
	})

	t.Run("empty scopes are accepted", func(t *testing.T) {
		if err := ensureClaudeInferenceScope([]string{}, "token exchange"); err != nil {
			t.Fatalf("ensureClaudeInferenceScope([]) = %v, want nil", err)
		}
	})

	t.Run("stale scopes are rejected", func(t *testing.T) {
		err := ensureClaudeInferenceScope([]string{"org:create_api_key"}, "token exchange")
		if err == nil {
			t.Fatal("ensureClaudeInferenceScope([org:create_api_key]) = nil, want error")
		}
		if got := err.Error(); got != claudeInferenceScopeWant {
			t.Errorf("error text mismatch\n got: %q\nwant: %q", got, claudeInferenceScopeWant)
		}
	})

	t.Run("inference scope is accepted", func(t *testing.T) {
		if err := ensureClaudeInferenceScope([]string{"org:create_api_key", "workspace:inference"}, "token exchange"); err != nil {
			t.Fatalf("ensureClaudeInferenceScope() = %v, want nil", err)
		}
	})
}

// claudeScopedTokenServer answers every token request with a 200 carrying the
// given `scope` value (the field is omitted entirely when scope == "").
func claudeScopedTokenServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("reading request body: %v", err)
		}
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestClaudeExchangeRejectsMissingInferenceScope is mandatory mutation #3: with
// RequireInferenceScope set, a 200 whose scope list has no inference scope must
// fail the exchange and return no credential at all.
func TestClaudeExchangeRejectsMissingInferenceScope(t *testing.T) {
	srv := claudeScopedTokenServer(t, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"scope":"org:create_api_key user:profile"}`)

	// Built by hand on purpose: the flag stays off here so this test isolates the
	// inference gate (see TestClaudeExchangeRequiresRefreshTokenAndExpiry for the
	// complete-credential gate).
	cfg := OAuthProviderConfig{
		Name:                  "anthropic",
		ClientID:              "cid",
		RequireInferenceScope: true,
		TokenRequestStyle:     TokenRequestJSON,
		TokenURL:              srv.URL,
		HTTPClient:            srv.Client(),
	}

	cred, err := exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://platform.claude.com/oauth/code/callback",
	})
	if err == nil {
		t.Fatal("exchangeCodeForTokensWithParams() = nil error, want inference-scope error")
	}
	if !strings.Contains(err.Error(), "user:inference") {
		t.Errorf("error %q does not mention user:inference", err.Error())
	}
	if cred != nil {
		t.Errorf("credential = %#v, want nil when the gate rejects the token", cred)
	}
}

// TestClaudeExchangeAcceptsEmptyScope pins the "no scope reported" case: an
// unscoped token response is not a gate failure.
func TestClaudeExchangeAcceptsEmptyScope(t *testing.T) {
	srv := claudeScopedTokenServer(t, `{"access_token":"at","refresh_token":"rt","expires_in":3600}`)

	cfg := OAuthProviderConfig{
		Name:                  "anthropic",
		ClientID:              "cid",
		RequireInferenceScope: true,
		TokenRequestStyle:     TokenRequestJSON,
		TokenURL:              srv.URL,
		HTTPClient:            srv.Client(),
	}

	cred, err := exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://platform.claude.com/oauth/code/callback",
	})
	if err != nil {
		t.Fatalf("exchangeCodeForTokensWithParams() error: %v", err)
	}
	if cred == nil || cred.AccessToken != "at" {
		t.Fatalf("credential = %#v, want access token %q", cred, "at")
	}
	if cred.Scopes != nil {
		t.Errorf("Scopes = %#v, want nil for a response without scope", cred.Scopes)
	}
}

// TestClaudeExchangeRequiresRefreshTokenAndExpiry is the V7 guard, and the
// mandatory mutation target for it: with RequireRefreshToken set, a 200 that
// carries an access token but no refresh_token (or no expires_in) must fail the
// exchange with the actionable error and return NO credential. Without the gate
// the exchange "succeeds", prints Login successful! and stores a credential
// that can never be refreshed (and whose zero expiry makes NeedsRefresh() true
// forever) — so removing the check must turn this test red.
func TestClaudeExchangeRequiresRefreshTokenAndExpiry(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "missing refresh_token",
			body:    `{"access_token":"at","expires_in":3600,"scope":"user:inference"}`,
			wantErr: "Claude OAuth token exchange returned no refresh token; the credential could not be refreshed. Re-run: lele auth login --provider claude",
		},
		{
			name:    "missing expires_in",
			body:    `{"access_token":"at","refresh_token":"rt","scope":"user:inference"}`,
			wantErr: "Claude OAuth token exchange returned no expires_in; a credential without an expiry would refresh on every use. Re-run: lele auth login --provider claude",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := claudeScopedTokenServer(t, tt.body)

			cfg := OAuthProviderConfig{
				Name: "anthropic",
				// The inference gate stays on so the response is otherwise valid
				// for the Claude flow: the new gate is the only reason to fail.
				ClientID:              "cid",
				RequireInferenceScope: true,
				RequireRefreshToken:   true,
				TokenRequestStyle:     TokenRequestJSON,
				TokenURL:              srv.URL,
				HTTPClient:            srv.Client(),
			}

			cred, err := exchangeCodeForTokensWithParams(cfg, exchangeParams{
				Code:         "code",
				CodeVerifier: "verifier",
				RedirectURI:  "https://platform.claude.com/oauth/code/callback",
			})
			if err == nil {
				t.Fatal("exchangeCodeForTokensWithParams() = nil error, want the incomplete-credential error")
			}
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
			if cred != nil {
				t.Errorf("credential = %#v, want nil when the exchange is rejected", cred)
			}
		})
	}
}

// TestRequireRefreshTokenMessageUsesDisplayName pins the D6 provider-aware
// wording: reusing the RequireRefreshToken gate for a second provider (Gemini)
// must name that provider in both the message body and the recovery command,
// instead of hardcoding "Claude". The label is OAuthProviderConfig.DisplayName;
// an empty value still falls back to "Claude", so the historical message
// (asserted byte-for-byte by TestClaudeExchangeRequiresRefreshTokenAndExpiry)
// is preserved. The mandatory mutation for this test is to hardcode
// label := "Claude" inside requireRefreshTokenAndExpiry.
func TestRequireRefreshTokenMessageUsesDisplayName(t *testing.T) {
	// 200 with an access token but no refresh_token (and a valid expires_in, so
	// the refresh-token branch is the only reason to fail).
	srv := claudeScopedTokenServer(t, `{"access_token":"a","expires_in":3600,"token_type":"Bearer"}`)

	// Hand-built on purpose: this task adds no GeminiOAuthConfig(), and the point
	// is that the message follows whatever DisplayName the config carries.
	cfg := OAuthProviderConfig{
		Name:                "gemini",
		DisplayName:         "Gemini",
		RequireRefreshToken: true,
		TokenURL:            srv.URL,
		HTTPClient:          srv.Client(),
		TokenRequestStyle:   TokenRequestFormURLEncoded,
	}

	cred, err := exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         "code",
		CodeVerifier: "verifier",
		RedirectURI:  "https://codeassist.google.com/authcode",
	})
	if err == nil {
		t.Fatal("exchangeCodeForTokensWithParams() = nil error, want the incomplete-credential error")
	}
	msg := err.Error()
	want := "Gemini OAuth token exchange returned no refresh token"
	if !strings.Contains(msg, want) {
		t.Errorf("error = %q, want it to contain %q", msg, want)
	}
	if strings.Contains(msg, "Claude") {
		t.Errorf("error = %q, must not name Claude for a config whose DisplayName is %q", msg, cfg.DisplayName)
	}
	if cred != nil {
		t.Errorf("credential = %#v, want nil when the exchange is rejected", cred)
	}
}

// TestClaudeRefreshRejectsMissingInferenceScope is the V2 guard: when the gate
// rejects a refreshed token, RefreshAccessToken must still hand the caller the
// credential the endpoint just issued, so the ROTATED refresh token can be
// persisted before the failure is surfaced. The old refresh token is already
// consumed at that point; dropping the new one bricks the account.
func TestClaudeRefreshRejectsMissingInferenceScope(t *testing.T) {
	var hits int
	var wireBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		wireBody = raw
		io.WriteString(w, `{"access_token":"fresh","refresh_token":"rotated-refresh","expires_in":3600,"scope":"org:create_api_key user:profile"}`)
	}))
	defer srv.Close()

	cfg := OAuthProviderConfig{
		Name:                  "anthropic",
		ClientID:              "cid",
		Scopes:                "user:profile user:inference",
		RequireInferenceScope: true,
		TokenRequestStyle:     TokenRequestJSON,
		TokenURL:              srv.URL,
		HTTPClient:            srv.Client(),
	}
	cred := &AuthCredential{
		AccessToken:  "old",
		RefreshToken: "old-refresh",
		Provider:     "anthropic",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Fatal("RefreshAccessToken() = nil error, want inference-scope error")
	}
	if hits != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (the gate is not a retry trigger)", hits)
	}

	// The credential travels with the error (correction V2).
	if refreshed == nil {
		t.Fatal("RefreshAccessToken() returned a nil credential; the rotated refresh token would be lost")
	}
	if refreshed.RefreshToken != "rotated-refresh" {
		t.Errorf("refreshed.RefreshToken = %q, want the rotated value %q", refreshed.RefreshToken, "rotated-refresh")
	}

	var scopeErr *inferenceScopeError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("error type = %T, want *inferenceScopeError", err)
	}
	if scopeErr.Action != "token refresh" {
		t.Errorf("scopeErr.Action = %q, want %q", scopeErr.Action, "token refresh")
	}
	if scopeErr.Credential == nil || scopeErr.Credential.RefreshToken != "rotated-refresh" {
		t.Errorf("scopeErr.Credential = %#v, want the credential carrying the rotated refresh token", scopeErr.Credential)
	}
	if !strings.Contains(err.Error(), "user:inference") {
		t.Errorf("error %q does not mention user:inference", err.Error())
	}

	// The refresh request itself is unchanged: same wire as every other
	// provider, just JSON-encoded.
	var sent map[string]string
	if err := json.Unmarshal(wireBody, &sent); err != nil {
		t.Fatalf("decoding request body %q: %v", string(wireBody), err)
	}
	if sent["grant_type"] != "refresh_token" || sent["refresh_token"] != "old-refresh" {
		t.Errorf("refresh request body = %v, want grant_type=refresh_token with the old refresh token", sent)
	}
}

// claudeAuthorizeURLGolden is the byte-for-byte authorize URL the Claude preset
// must produce (plan F1-T05). It is spelled out literally so any drift in the
// endpoint host, the scope set/order, the extra `code=true` param or the state
// wiring fails loudly.
const claudeAuthorizeURLGolden = "https://claude.com/cai/oauth/authorize?client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e&code=true&code_challenge=CHALLENGE&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A54321%2Fcallback&response_type=code&scope=org%3Acreate_api_key+user%3Aprofile+user%3Ainference+user%3Asessions%3Aclaude_code+user%3Amcp_servers+user%3Afile_upload&state=verifier"

// TestClaudeOAuthConfigPreset pins every field of the Claude preset verbatim.
// The preset is a fixed contract with a third-party service: changing any value
// here changes what the user logs into or how the token is presented, so this
// test is intentionally exhaustive rather than behavioural.
func TestClaudeOAuthConfigPreset(t *testing.T) {
	cfg := ClaudeOAuthConfig()

	if cfg.Name != "anthropic" {
		t.Errorf("Name = %q, want %q", cfg.Name, "anthropic")
	}
	if cfg.ClientID != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
		t.Errorf("ClientID = %q, want the public Claude Code client id", cfg.ClientID)
	}
	if cfg.ClientSecret != "" {
		t.Errorf("ClientSecret = %q, want empty (public client)", cfg.ClientSecret)
	}
	if cfg.Issuer != "" {
		t.Errorf("Issuer = %q, want empty (all endpoints are explicit)", cfg.Issuer)
	}
	if cfg.AuthorizeURL != "https://claude.com/cai/oauth/authorize" {
		t.Errorf("AuthorizeURL = %q, want the claude.com/cai surface", cfg.AuthorizeURL)
	}
	if cfg.TokenURL != "https://platform.claude.com/v1/oauth/token" {
		t.Errorf("TokenURL = %q, want %q", cfg.TokenURL, "https://platform.claude.com/v1/oauth/token")
	}
	if want := "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"; cfg.Scopes != want {
		t.Errorf("Scopes = %q, want %q", cfg.Scopes, want)
	}
	if want := "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"; cfg.RefreshScopes != want {
		t.Errorf("RefreshScopes = %q, want %q", cfg.RefreshScopes, want)
	}
	if cfg.OmitRefreshScope {
		t.Error("OmitRefreshScope = true, want false (refresh sends RefreshScopes)")
	}
	if cfg.CallbackPath != "/callback" {
		t.Errorf("CallbackPath = %q, want %q", cfg.CallbackPath, "/callback")
	}
	if cfg.Port != 0 {
		t.Errorf("Port = %d, want 0 (ephemeral loopback)", cfg.Port)
	}
	if cfg.RedirectURI != "" {
		t.Errorf("RedirectURI = %q, want empty (derived from the bound port)", cfg.RedirectURI)
	}
	if cfg.ManualRedirectURI != "https://platform.claude.com/oauth/code/callback" {
		t.Errorf("ManualRedirectURI = %q, want the hosted paste redirect", cfg.ManualRedirectURI)
	}
	if !reflect.DeepEqual(cfg.ExtraAuthParams, map[string]string{"code": "true"}) {
		t.Errorf("ExtraAuthParams = %v, want map[code:true]", cfg.ExtraAuthParams)
	}
	if cfg.Originator != "" {
		t.Errorf("Originator = %q, want empty", cfg.Originator)
	}
	if cfg.TokenRequestStyle != TokenRequestJSON {
		t.Errorf("TokenRequestStyle = %d, want TokenRequestJSON (%d)", cfg.TokenRequestStyle, TokenRequestJSON)
	}
	if cfg.AuthHeaderStyle != TokenAuthBody {
		t.Errorf("AuthHeaderStyle = %d, want TokenAuthBody (%d)", cfg.AuthHeaderStyle, TokenAuthBody)
	}
	if !cfg.StateIsVerifier {
		t.Error("StateIsVerifier = false, want true (jcode binds state to the PKCE verifier)")
	}
	if !cfg.RequireInferenceScope {
		t.Error("RequireInferenceScope = false, want true")
	}
	if !cfg.RequireRefreshToken {
		t.Error("RequireRefreshToken = false, want true (V7: a Claude credential without a refresh token and expiry is unusable)")
	}
	if cfg.HTTPClient == nil {
		t.Fatal("HTTPClient = nil, want a client carrying jcode's 15s token timeout")
	}
	if cfg.HTTPClient.Timeout != 15*time.Second {
		t.Errorf("HTTPClient.Timeout = %s, want %s", cfg.HTTPClient.Timeout, 15*time.Second)
	}

	// Explicit regressions, one per jcode trap. These duplicate the checks above
	// on purpose: they are the failures a reviewer looks for first.
	t.Run("authorize host is claude.com not platform", func(t *testing.T) {
		u, err := url.Parse(cfg.AuthorizeURL)
		if err != nil {
			t.Fatalf("AuthorizeURL %q is not a URL: %v", cfg.AuthorizeURL, err)
		}
		if u.Hostname() != "claude.com" {
			t.Errorf("authorize host = %q, want claude.com (platform/console mint tokens the inference API rejects)", u.Hostname())
		}
		if strings.Contains(cfg.AuthorizeURL, "platform.claude.com") || strings.Contains(cfg.AuthorizeURL, "console.anthropic.com") {
			t.Errorf("AuthorizeURL %q points at the console/platform surface", cfg.AuthorizeURL)
		}
	})

	t.Run("refresh scopes exclude org:create_api_key", func(t *testing.T) {
		for _, scope := range strings.Fields(cfg.RefreshScopes) {
			if scope == "org:create_api_key" {
				t.Errorf("RefreshScopes = %q must not carry the API-key-minting scope", cfg.RefreshScopes)
			}
		}
	})

	t.Run("code=true extra param", func(t *testing.T) {
		if cfg.ExtraAuthParams["code"] != "true" {
			t.Errorf("ExtraAuthParams[code] = %q, want %q", cfg.ExtraAuthParams["code"], "true")
		}
	})
}

// TestClaudeAuthorizeURLGolden pins the full authorize URL. Changing
// AuthorizeURL to the platform/console surface is the most expensive jcode trap
// (tokens that refresh but are rejected for inference), and this test is its
// primary detector.
func TestClaudeAuthorizeURLGolden(t *testing.T) {
	got := buildAuthorizeURL(
		ClaudeOAuthConfig(),
		PKCECodes{CodeChallenge: "CHALLENGE"},
		"verifier",
		"http://localhost:54321/callback",
	)
	if got != claudeAuthorizeURLGolden {
		t.Errorf("buildAuthorizeURL mismatch\ngot:  %s\nwant: %s", got, claudeAuthorizeURLGolden)
	}
}

// TestParseAuthorizeCodeInput covers the jcode precedence (D2): URL with code=,
// bare query string, plain code, code#state, whitespace, and the two error
// shapes.
func TestParseAuthorizeCodeInput(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantCode  string
		wantState string
		wantErr   string
	}{
		{name: "bare code", raw: "abc", wantCode: "abc"},
		{name: "query string", raw: "code=abc&state=xyz", wantCode: "abc", wantState: "xyz"},
		{name: "full callback URL", raw: "https://platform.claude.com/oauth/code/callback?code=abc&state=xyz", wantCode: "abc", wantState: "xyz"},
		{name: "hash separated", raw: "abc#xyz", wantCode: "abc", wantState: "xyz"},
		{name: "surrounding whitespace", raw: "  abc  ", wantCode: "abc"},
		{name: "no code= is treated as a bare code", raw: "xyz", wantCode: "xyz"},
		{name: "code= without a code", raw: "?code=", wantErr: "no authorization code found in the URL"},
		{name: "blank input", raw: "   ", wantErr: "no authorization code provided"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, state, err := parseAuthorizeCodeInput(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseAuthorizeCodeInput(%q) = (%q, %q), want error %q", tt.raw, code, state, tt.wantErr)
				}
				if err.Error() != tt.wantErr {
					t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAuthorizeCodeInput(%q) unexpected error: %v", tt.raw, err)
			}
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if state != tt.wantState {
				t.Errorf("state = %q, want %q", state, tt.wantState)
			}
		})
	}

	// A "#" inside the code value wins over a state from the query string, and
	// only the FIRST "#" splits (jcode splitn(2, '#')). When the hash is outside
	// the value it is consumed by the URL/query parsing (jcode behaves the same
	// way), so the precedence case has to percent-encode it.
	t.Run("hash inside the code overrides query state", func(t *testing.T) {
		code, state, err := parseAuthorizeCodeInput("code=abc%23fromhash&state=fromquery")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code != "abc" || state != "fromhash" {
			t.Errorf("= (%q, %q), want (%q, %q)", code, state, "abc", "fromhash")
		}
	})

	t.Run("trailing hash is consumed by the URL parse", func(t *testing.T) {
		code, state, err := parseAuthorizeCodeInput("code=abc&state=fromquery#fragment")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code != "abc" || state != "fromquery" {
			t.Errorf("= (%q, %q), want (%q, %q)", code, state, "abc", "fromquery")
		}
	})

	t.Run("only the first hash splits", func(t *testing.T) {
		code, state, err := parseAuthorizeCodeInput("abc#state#extra")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code != "abc" || state != "state#extra" {
			t.Errorf("= (%q, %q), want (%q, %q)", code, state, "abc", "state#extra")
		}
	})
}

// --- F1-T06: paste-code login and browser-to-paste fallback --------------------

// claudeManualRedirectForTest is the hosted redirect the Claude paste flow must
// advertise and echo on the exchange.
const claudeManualRedirectForTest = "https://platform.claude.com/oauth/code/callback"

// claudeAuthorizeHostForTest is the authorize surface of the Claude preset; the
// console/platform host mints tokens the inference API rejects.
const claudeAuthorizeHostForTest = "https://claude.com/cai/oauth/authorize"

// captureStdout swaps os.Stdout for a pipe, runs fn and returns everything it
// printed. The original descriptor is restored with t.Cleanup so a failing
// assertion can never leave the process writing into a closed pipe. The pipe is
// drained on a separate goroutine (the standard os.Pipe capture idiom) so the
// test cannot deadlock on a full pipe buffer.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = original })

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, reader)
		done <- buf.String()
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Errorf("closing stdout pipe: %v", err)
	}
	out := <-done
	if err := reader.Close(); err != nil {
		t.Errorf("closing stdout reader: %v", err)
	}
	return out
}

// pasteTokenRecorder is the httptest token endpoint used by the paste-flow
// tests: it records the raw body and hit count of every request and answers 200
// with an access token plus an inference scope (the Claude preset gates the
// exchange on it). The fields are read only after the request has completed,
// mirroring the recorder pattern already used in TestClaudeRefreshRejects…
type pasteTokenRecorder struct {
	srv  *httptest.Server
	body []byte
	hits int
}

func newPasteTokenRecorder(t *testing.T) *pasteTokenRecorder {
	t.Helper()

	rec := &pasteTokenRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.hits++
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		rec.body = raw
		io.WriteString(w, `{"access_token":"at","refresh_token":"rt","expires_in":3600,"scope":"user:profile user:inference"}`)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// claudeConfig returns the Claude preset pointed at the recorder endpoint.
func (rec *pasteTokenRecorder) claudeConfig() OAuthProviderConfig {
	cfg := ClaudeOAuthConfig()
	cfg.TokenURL = rec.srv.URL
	cfg.HTTPClient = rec.srv.Client()
	return cfg
}

// sent decodes the recorded JSON body into a flat map (the Claude preset uses
// TokenRequestJSON, so every request field is a JSON string).
func (rec *pasteTokenRecorder) sent(t *testing.T) map[string]string {
	t.Helper()

	var m map[string]string
	if err := json.Unmarshal(rec.body, &m); err != nil {
		t.Fatalf("decoding token request body %q: %v", string(rec.body), err)
	}
	return m
}

// TestLoginPasteCodeExchangesWithManualRedirect pins the whole paste wire: the
// authorize URL must carry the hosted manual redirect and the PKCE verifier as
// state, and the exchange must echo state, code_verifier and redirect_uri.
// Dropping the State from the exchange params (or sending the parsed state
// instead of the verifier) turns this test red — mandatory mutation for F1-T06.
func TestLoginPasteCodeExchangesWithManualRedirect(t *testing.T) {
	rec := newPasteTokenRecorder(t)
	cfg := rec.claudeConfig()

	pkce := PKCECodes{CodeVerifier: "VERIFIER", CodeChallenge: "CH"}

	var (
		cred *AuthCredential
		err  error
	)
	out := captureStdout(t, func() {
		cred, err = loginPasteCodeWithPKCE(cfg, pkce, strings.NewReader("CODE#VERIFIER"))
	})
	if err != nil {
		t.Fatalf("loginPasteCodeWithPKCE() error: %v", err)
	}
	if rec.hits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", rec.hits)
	}
	if cred == nil || cred.AccessToken != "at" {
		t.Fatalf("credential = %#v, want the access token %q", cred, "at")
	}

	for k, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "CODE",
		"code_verifier": "VERIFIER",
		"redirect_uri":  claudeManualRedirectForTest,
		"state":         "VERIFIER",
		"client_id":     cfg.ClientID,
	} {
		if got := rec.sent(t)[k]; got != want {
			t.Errorf("exchange body[%q] = %q, want %q", k, got, want)
		}
	}

	// The printed URL must be the claude.com authorize surface carrying the
	// hosted manual redirect and the PKCE verifier as state: the paste flow never
	// falls back to the loopback redirect.
	if !strings.Contains(out, claudeAuthorizeHostForTest) {
		t.Errorf("stdout does not contain the authorize URL:\n%s", out)
	}
	if want := "redirect_uri=" + url.QueryEscape(claudeManualRedirectForTest); !strings.Contains(out, want) {
		t.Errorf("stdout does not advertise the manual redirect (%q):\n%s", want, out)
	}
	if !strings.Contains(out, "state=VERIFIER") {
		t.Errorf("stdout does not carry state=VERIFIER:\n%s", out)
	}
}

// TestLoginPasteCodeStateMismatch pins the stale-code guard: a non-empty pasted
// state that differs from the verifier this flow generated must fail without
// issuing any request.
func TestLoginPasteCodeStateMismatch(t *testing.T) {
	rec := newPasteTokenRecorder(t)
	cfg := rec.claudeConfig()

	var (
		cred *AuthCredential
		err  error
	)
	_ = captureStdout(t, func() {
		cred, err = loginPasteCodeWithPKCE(cfg, PKCECodes{CodeVerifier: "VERIFIER", CodeChallenge: "CH"}, strings.NewReader("CODE#WRONG"))
	})

	const want = "OAuth state mismatch. Start login again and use the latest callback/code."
	if err == nil {
		t.Fatalf("loginPasteCodeWithPKCE() error = nil, want %q", want)
	}
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if cred != nil {
		t.Errorf("credential = %#v, want nil", cred)
	}
	if rec.hits != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (no request for a stale state)", rec.hits)
	}
}

// TestLoginPasteCodeAcceptsBareCode covers correction V8: a bare code without a
// state is valid and the PKCE verifier is substituted as the exchange state.
func TestLoginPasteCodeAcceptsBareCode(t *testing.T) {
	rec := newPasteTokenRecorder(t)
	cfg := rec.claudeConfig()

	var (
		cred *AuthCredential
		err  error
	)
	_ = captureStdout(t, func() {
		cred, err = LoginPasteCode(cfg, strings.NewReader("CODE"))
	})
	if err != nil {
		t.Fatalf("LoginPasteCode() error: %v", err)
	}
	if cred == nil || cred.AccessToken != "at" {
		t.Fatalf("credential = %#v, want the access token %q", cred, "at")
	}

	sent := rec.sent(t)
	if sent["code"] != "CODE" {
		t.Errorf("exchange body[code] = %q, want %q", sent["code"], "CODE")
	}
	if sent["redirect_uri"] != claudeManualRedirectForTest {
		t.Errorf("exchange body[redirect_uri] = %q, want %q", sent["redirect_uri"], claudeManualRedirectForTest)
	}
	if sent["state"] == "" {
		t.Error("exchange body[state] is empty, want the PKCE verifier substituted for the missing state")
	}
	if sent["state"] != sent["code_verifier"] {
		t.Errorf("exchange body[state] = %q, want it equal to code_verifier %q", sent["state"], sent["code_verifier"])
	}
}

// TestLoginPasteCodeRequiresManualRedirect pins the guard that keeps the
// paste-code flow off providers without a hosted redirect.
func TestLoginPasteCodeRequiresManualRedirect(t *testing.T) {
	cfg := ClaudeOAuthConfig()
	cfg.ManualRedirectURI = ""

	cred, err := LoginPasteCode(cfg, strings.NewReader("CODE"))
	if err == nil {
		t.Fatal("LoginPasteCode() error = nil, want the missing-ManualRedirectURI error")
	}
	const want = `oauth: provider "anthropic" has no ManualRedirectURI configured for the paste-code flow`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if cred != nil {
		t.Errorf("credential = %#v, want nil", cred)
	}
}

// TestLoginBrowserFallsBackToPasteOnBindFailure exercises the degradation path:
// with the loopback port occupied, loginBrowser must announce the failure and
// finish the login through the manual paste flow.
//
// The fallback calls LoginPasteCode, which mints a fresh PKCE the test cannot
// predict, so the pasted value is a bare code: an absent pasted state is
// substituted by the freshly generated verifier (jcode behavior), which keeps
// this test about the degradation, not about state validation (that is covered
// by TestLoginPasteCodeExchangesWithManualRedirect and
// TestLoginPasteCodeStateMismatch).
func TestLoginBrowserFallsBackToPasteOnBindFailure(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupying a port: %v", err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	rec := newPasteTokenRecorder(t)
	cfg := rec.claudeConfig()
	cfg.Port = port

	var (
		cred *AuthCredential
		lerr error
	)
	out := captureStdout(t, func() {
		cred, lerr = loginBrowser(cfg, true, strings.NewReader("CODE"))
	})
	if lerr != nil {
		t.Fatalf("loginBrowser(fallbackToPaste=true) error: %v", lerr)
	}
	if !strings.Contains(out, "Falling back to manual code paste") {
		t.Errorf("stdout does not announce the paste fallback:\n%s", out)
	}
	if cred == nil || cred.AccessToken != "at" {
		t.Fatalf("credential = %#v, want the access token %q", cred, "at")
	}
	if rec.hits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", rec.hits)
	}
	if sent := rec.sent(t); sent["redirect_uri"] != claudeManualRedirectForTest {
		t.Errorf("exchange body[redirect_uri] = %q, want the manual redirect %q", sent["redirect_uri"], claudeManualRedirectForTest)
	}
}

// TestLoginBrowserWithoutFallbackKeepsBindError guards the historical behavior:
// the exported LoginBrowser (fallbackToPaste=false) must return the original
// bind error untouched.
func TestLoginBrowserWithoutFallbackKeepsBindError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupying a port: %v", err)
	}
	defer occupied.Close()
	port := occupied.Addr().(*net.TCPAddr).Port

	cfg := ClaudeOAuthConfig()
	cfg.Port = port

	cred, err := loginBrowser(cfg, false, nil)
	if err == nil {
		t.Fatal("loginBrowser(fallbackToPaste=false) error = nil, want the bind error")
	}
	if cred != nil {
		t.Errorf("credential = %#v, want nil", cred)
	}
	if want := fmt.Sprintf("starting callback server on port %d", port); !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
}

// TestLoginBrowserHeadlessHintIsProviderSpecific pins D9: the hint printed by
// loginBrowser must name the provider that actually owns the alternative flow.
// The previous `case cfg.ManualRedirectURI != ""` arm advertised
// `--provider claude --paste-code` for ANY preset with a hosted redirect, so the
// Gemini preset would have been told to run the Claude command.
//
// All four providers that can reach this switch are covered: openai (device
// code), anthropic and gemini (hosted paste redirect), and google-antigravity,
// whose silence is a free regression canary — it has no ManualRedirectURI and no
// alternative flow, and used to print a misleading openai line.
func TestLoginBrowserHeadlessHintIsProviderSpecific(t *testing.T) {
	tests := []struct {
		name string
		cfg  OAuthProviderConfig
		want string
	}{
		{
			name: "openai keeps the device-code line",
			cfg:  OpenAIOAuthConfig(),
			want: "If you're running in a headless environment, use: lele auth login --provider openai --device-code\n",
		},
		{
			name: "claude keeps the paste-code line byte-identical",
			cfg:  ClaudeOAuthConfig(),
			want: "If you're running in a headless environment, use: lele auth login --provider claude --paste-code\n",
		},
		{
			name: "gemini names itself, not claude",
			cfg:  GeminiOAuthConfig(),
			want: "If you're running in a headless environment, use: lele auth login --provider gemini --paste-code\n",
		},
		{
			name: "google-antigravity stays silent",
			cfg:  GoogleAntigravityOAuthConfig(),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, func() { printHeadlessHint(tt.cfg) })
			if out != tt.want {
				t.Errorf("headless hint for %q = %q, want %q", tt.cfg.Name, out, tt.want)
			}
		})
	}
}
