package auth

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// geminiClientID / geminiClientSecret are the literals Google's official Gemini
// CLI publishes. They are spelled out here (instead of reusing the constructor's
// decodeBase64 output) so the test catches a corrupted/rotated base64 blob: it
// proves the decode, not just that the field was assigned.
const (
	geminiClientID     = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"
	geminiClientSecret = "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"
)

// geminiScopes is the exact, ordered, single-space-joined scope list from
// .lele/specs/jcode-gemini.md §1 (jcode `GEMINI_SCOPES`): cloud-platform plus
// the two userinfo scopes. Order matters — it is a single OAuth parameter.
const geminiScopes = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"

// TestGeminiOAuthConfigPreset pins every field of the Gemini preset against the
// wire contract in .lele/tasks/oauth-providers-f2-tasks.md §3. Each assertion
// names its evidence so a failure points straight at the drifted field.
func TestGeminiOAuthConfigPreset(t *testing.T) {
	cfg := GeminiOAuthConfig()

	// Identity: the canonical key that a later task registers, and the label the
	// RequireRefreshToken gate prints.
	if cfg.Name != "gemini" {
		t.Errorf("Name = %q, want %q", cfg.Name, "gemini")
	}
	if cfg.DisplayName != "Gemini" {
		t.Errorf("DisplayName = %q, want %q", cfg.DisplayName, "Gemini")
	}

	// Endpoints: verbatim from jcode gemini.rs (spec §1).
	if cfg.AuthorizeURL != "https://accounts.google.com/o/oauth2/v2/auth" {
		t.Errorf("AuthorizeURL = %q, want the accounts.google.com authorize endpoint", cfg.AuthorizeURL)
	}
	if cfg.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Errorf("TokenURL = %q, want %q", cfg.TokenURL, "https://oauth2.googleapis.com/token")
	}
	if cfg.Issuer != "https://accounts.google.com/o/oauth2/v2" {
		t.Errorf("Issuer = %q, want %q", cfg.Issuer, "https://accounts.google.com/o/oauth2/v2")
	}

	// Credentials: decoded values must equal the published literals, which also
	// proves the base64 blobs round-trip.
	if cfg.ClientID != geminiClientID {
		t.Errorf("ClientID = %q, want the public Gemini CLI client id %q", cfg.ClientID, geminiClientID)
	}
	if cfg.ClientSecret != geminiClientSecret {
		t.Errorf("ClientSecret = %q, want the public Gemini CLI desktop-app secret %q", cfg.ClientSecret, geminiClientSecret)
	}

	// Scopes: one ordered, space-joined parameter (spec §1).
	if cfg.Scopes != geminiScopes {
		t.Errorf("Scopes = %q, want %q", cfg.Scopes, geminiScopes)
	}
	if cfg.RefreshScopes != "" {
		t.Errorf("RefreshScopes = %q, want empty (refresh omits scope)", cfg.RefreshScopes)
	}
	if !cfg.OmitRefreshScope {
		t.Error("OmitRefreshScope = false, want true (RFC 6749 §6: refresh sends no scope)")
	}

	// Redirects: ephemeral loopback + hosted paste redirect.
	if cfg.Port != 0 {
		t.Errorf("Port = %d, want 0 (ephemeral loopback, jcode bind_callback_listener(0))", cfg.Port)
	}
	if cfg.CallbackPath != "/oauth2callback" {
		t.Errorf("CallbackPath = %q, want %q", cfg.CallbackPath, "/oauth2callback")
	}
	if cfg.RedirectURI != "" {
		t.Errorf("RedirectURI = %q, want empty (derived from the bound port)", cfg.RedirectURI)
	}
	if cfg.ManualRedirectURI != "https://codeassist.google.com/authcode" {
		t.Errorf("ManualRedirectURI = %q, want the hosted paste redirect", cfg.ManualRedirectURI)
	}

	// ExtraAuthParams: exactly these two entries; both are what make Google
	// return a refresh_token (charter R2).
	if want := map[string]string{"access_type": "offline", "prompt": "consent"}; !reflect.DeepEqual(cfg.ExtraAuthParams, want) {
		t.Errorf("ExtraAuthParams = %v, want %v", cfg.ExtraAuthParams, want)
	}

	// Must-stay-zero fields. A non-zero value here would silently change the
	// wire (originator param), the state semantics, or the flow selection.
	if cfg.Originator != "" {
		t.Errorf("Originator = %q, want empty (jcode's Gemini builder has no originator)", cfg.Originator)
	}
	if cfg.StateIsVerifier {
		t.Error("StateIsVerifier = true, want false (jcode Gemini uses a random state, not the PKCE verifier)")
	}
	if cfg.RequireInferenceScope {
		t.Error("RequireInferenceScope = true, want false (jcode never validates Gemini scopes)")
	}
	if cfg.HTTPClient != nil {
		t.Errorf("HTTPClient = %v, want nil (nil => http.DefaultClient, like jcode's shared_http_client)", cfg.HTTPClient)
	}

	// Device flow: Gemini has none.
	if cfg.DeviceCodeURL != "" {
		t.Errorf("DeviceCodeURL = %q, want empty", cfg.DeviceCodeURL)
	}
	if cfg.DeviceTokenURL != "" {
		t.Errorf("DeviceTokenURL = %q, want empty", cfg.DeviceTokenURL)
	}
	if cfg.DeviceVerifyURL != "" {
		t.Errorf("DeviceVerifyURL = %q, want empty", cfg.DeviceVerifyURL)
	}
	if cfg.LegacyDeviceCodeURL != "" {
		t.Errorf("LegacyDeviceCodeURL = %q, want empty", cfg.LegacyDeviceCodeURL)
	}
	if cfg.LegacyDeviceTokenURL != "" {
		t.Errorf("LegacyDeviceTokenURL = %q, want empty", cfg.LegacyDeviceTokenURL)
	}

	// Gate + wire encoding.
	if !cfg.RequireRefreshToken {
		t.Error("RequireRefreshToken = false, want true (jcode's Gemini exchange treats a missing refresh_token as a hard error)")
	}
	if cfg.TokenRequestStyle != TokenRequestFormURLEncoded {
		t.Errorf("TokenRequestStyle = %d, want TokenRequestFormURLEncoded (%d)", cfg.TokenRequestStyle, TokenRequestFormURLEncoded)
	}
	if cfg.AuthHeaderStyle != TokenAuthBody {
		t.Errorf("AuthHeaderStyle = %d, want TokenAuthBody (%d)", cfg.AuthHeaderStyle, TokenAuthBody)
	}
}

// TestGeminiAuthorizeURLGolden pins the exact authorize URL byte-for-byte. The
// query order is whatever url.Values.Encode() emits (alphabetical by key), and
// the string below was written from .lele/tasks/oauth-providers-f2-tasks.md §3
// rather than copied from the implementation.
//
// access_type=offline + prompt=consent are the two parameters that make Google
// hand back a refresh_token (and force a fresh consent screen even for an
// already-approved account). Without them the login "succeeds" with an access
// token that expires in an hour and can never be refreshed — the credential is
// useless. See charter R2.
func TestGeminiAuthorizeURLGolden(t *testing.T) {
	want := "https://accounts.google.com/o/oauth2/v2/auth?access_type=offline&client_id=681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com&code_challenge=CHALLENGE&code_challenge_method=S256&prompt=consent&redirect_uri=http%3A%2F%2Flocalhost%3A54321%2Foauth2callback&response_type=code&scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fcloud-platform+https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fuserinfo.email+https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fuserinfo.profile&state=STATE"

	got := buildAuthorizeURL(GeminiOAuthConfig(), PKCECodes{CodeChallenge: "CHALLENGE"}, "STATE", "http://localhost:54321/oauth2callback")
	if got != want {
		t.Errorf("buildAuthorizeURL mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// --- F2-T03: random-state paste flow (jcode's manual/no-browser flow) ----------
//
// These tests cover the StateIsVerifier=false half of LoginPasteCode: the pasted
// callback URL/query must carry the random CSRF state back, a bare code is taken
// verbatim, and the exchange never sends a `state` field. Every rule is a direct
// port of jcode's `resolve_callback_or_manual_code` / `looks_like_callback_input`
// / `parse_callback_input_with_state` (.lele/specs/jcode-gemini.md §4).

// geminiManualRedirectForTest is the hosted paste redirect of the Gemini preset;
// the paste flow must print it in the authorize URL and echo it on the exchange.
const geminiManualRedirectForTest = "https://codeassist.google.com/authcode"

// TestLooksLikeCallbackInput pins the jcode predicate that decides whether a
// pasted line is a callback (parse + verify the state) or a bare code (used
// verbatim). The table is the spec §4 list, including the two cases that must
// stay FALSE: a plain code and the OpenCode-style `code#state` paste.
func TestLooksLikeCallbackInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"full https url", "https://x/?code=a&state=b", true},
		{"http url", "http://x", true},
		{"bare query", "?code=a", true},
		{"contains code= mid string", "abc&code=x", true},
		{"contains state= mid string", "state=x", true},

		{"bare code", "abc", false},
		{"code#state paste is not callback shaped", "abc#state", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeCallbackInput(tt.raw); got != tt.want {
				t.Errorf("looksLikeCallbackInput(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

// TestParseCallbackInputWithState pins the state requirement that guards the
// random-state paste: a callback without a state cannot be verified, so it is an
// actionable error instead of a silent code exchange. The parser itself is the
// shared Claude parser (delegation, not a re-implementation).
func TestParseCallbackInputWithState(t *testing.T) {
	t.Run("query string with code and state", func(t *testing.T) {
		code, state, err := parseCallbackInputWithState("?code=a&state=b")
		if err != nil {
			t.Fatalf("parseCallbackInputWithState() error: %v", err)
		}
		if code != "a" || state != "b" {
			t.Errorf("= (%q, %q), want (%q, %q)", code, state, "a", "b")
		}
	})

	t.Run("full callback URL with code and state", func(t *testing.T) {
		code, state, err := parseCallbackInputWithState(geminiManualRedirectForTest + "?code=a&state=b")
		if err != nil {
			t.Fatalf("parseCallbackInputWithState() error: %v", err)
		}
		if code != "a" || state != "b" {
			t.Errorf("= (%q, %q), want (%q, %q)", code, state, "a", "b")
		}
	})

	const wantErr = "Please paste the full callback URL or query string so lele can verify the login state."

	t.Run("bare code has no state", func(t *testing.T) {
		code, state, err := parseCallbackInputWithState("a")
		if err == nil {
			t.Fatalf("parseCallbackInputWithState(%q) = (%q, %q), want error %q", "a", code, state, wantErr)
		}
		if err.Error() != wantErr {
			t.Errorf("error = %q, want %q", err.Error(), wantErr)
		}
	})

	t.Run("code without state parameter", func(t *testing.T) {
		code, state, err := parseCallbackInputWithState("?code=a")
		if err == nil {
			t.Fatalf("parseCallbackInputWithState(%q) = (%q, %q), want error %q", "?code=a", code, state, wantErr)
		}
		if err.Error() != wantErr {
			t.Errorf("error = %q, want %q", err.Error(), wantErr)
		}
	})

	t.Run("blank state parameter", func(t *testing.T) {
		if _, _, err := parseCallbackInputWithState("?code=a&state="); err == nil || err.Error() != wantErr {
			t.Errorf("parseCallbackInputWithState(%q) error = %v, want %q", "?code=a&state=", err, wantErr)
		}
	})
}

// geminiConfig points the Gemini preset at the shared paste token recorder. The
// preset uses form-encoded token requests with the client credentials in the
// body, so the recorded body has to be decoded as url.Values.
func (rec *pasteTokenRecorder) geminiConfig() OAuthProviderConfig {
	cfg := GeminiOAuthConfig()
	cfg.TokenURL = rec.srv.URL
	cfg.HTTPClient = rec.srv.Client()
	return cfg
}

// sentForm decodes the recorded token request as a form body (the Gemini preset
// uses TokenRequestFormURLEncoded, unlike Claude's TokenRequestJSON).
func (rec *pasteTokenRecorder) sentForm(t *testing.T) url.Values {
	t.Helper()

	values, err := url.ParseQuery(string(rec.body))
	if err != nil {
		t.Fatalf("decoding token request body %q: %v", string(rec.body), err)
	}
	return values
}

// TestLoginPasteCodeGeminiVerifiesRandomState is the mandatory M3 target: the
// pasted callback URL carries the RANDOM state the flow printed, the exchange
// echoes the code/verifier/manual redirect, and it must NOT carry a `state`
// field (spec §2: the Google token endpoint never receives one).
//
// Forcing `state := pkce.CodeVerifier` inside loginPasteCodeWithPKCEAndState
// turns this test red: the pasted real state no longer matches.
func TestLoginPasteCodeGeminiVerifiesRandomState(t *testing.T) {
	const knownState = "KNOWN-STATE"

	rec := newPasteTokenRecorder(t)
	cfg := rec.geminiConfig()

	var (
		cred *AuthCredential
		err  error
	)
	out := captureStdout(t, func() {
		cred, err = loginPasteCodeWithPKCEAndState(
			cfg,
			PKCECodes{CodeVerifier: "VERIFIER", CodeChallenge: "CH"},
			knownState,
			strings.NewReader(geminiManualRedirectForTest+"?code=CODE&state="+knownState),
		)
	})
	if err != nil {
		t.Fatalf("loginPasteCodeWithPKCEAndState() error: %v", err)
	}
	if rec.hits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", rec.hits)
	}
	if cred == nil || cred.AccessToken != "at" {
		t.Fatalf("credential = %#v, want the access token %q", cred, "at")
	}

	sent := rec.sentForm(t)
	for k, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "CODE",
		"code_verifier": "VERIFIER",
		"redirect_uri":  geminiManualRedirectForTest,
		"client_id":     geminiClientID,
		"client_secret": geminiClientSecret,
	} {
		if got := sent.Get(k); got != want {
			t.Errorf("exchange body[%q] = %q, want %q", k, got, want)
		}
	}
	if _, ok := sent["state"]; ok {
		t.Errorf("exchange body[state] = %q, want the field absent (spec §2: Google must not receive a state)", sent.Get("state"))
	}

	// The authorize URL must advertise the random state so the browser sends it
	// back to the hosted redirect.
	if !strings.Contains(out, "state="+knownState) {
		t.Errorf("stdout does not carry state=%s:\n%s", knownState, out)
	}
	if want := "redirect_uri=" + url.QueryEscape(geminiManualRedirectForTest); !strings.Contains(out, want) {
		t.Errorf("stdout does not advertise the manual redirect (%q):\n%s", want, out)
	}
}

// TestLoginPasteCodeGeminiAcceptsBareCode covers the manual-paste half of
// jcode's resolve_callback_or_manual_code: a line that does not look like a
// callback IS the authorization code, used verbatim, and the exchange still
// carries no state.
func TestLoginPasteCodeGeminiAcceptsBareCode(t *testing.T) {
	rec := newPasteTokenRecorder(t)
	cfg := rec.geminiConfig()

	var (
		cred *AuthCredential
		err  error
	)
	_ = captureStdout(t, func() {
		cred, err = loginPasteCodeWithPKCEAndState(cfg, PKCECodes{CodeVerifier: "VERIFIER", CodeChallenge: "CH"}, "KNOWN-STATE", strings.NewReader("CODE"))
	})
	if err != nil {
		t.Fatalf("loginPasteCodeWithPKCEAndState() error: %v", err)
	}
	if cred == nil || cred.AccessToken != "at" {
		t.Fatalf("credential = %#v, want the access token %q", cred, "at")
	}
	if rec.hits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", rec.hits)
	}

	sent := rec.sentForm(t)
	if sent.Get("code") != "CODE" {
		t.Errorf("exchange body[code] = %q, want %q", sent.Get("code"), "CODE")
	}
	if sent.Get("redirect_uri") != geminiManualRedirectForTest {
		t.Errorf("exchange body[redirect_uri] = %q, want %q", sent.Get("redirect_uri"), geminiManualRedirectForTest)
	}
	if _, ok := sent["state"]; ok {
		t.Errorf("exchange body[state] = %q, want the field absent", sent.Get("state"))
	}
}

// TestLoginPasteCodeGeminiRejectsMissingState covers the "please paste the full
// callback URL" contract: a callback-shaped paste without the state cannot be
// verified, so no token request may be issued.
func TestLoginPasteCodeGeminiRejectsMissingState(t *testing.T) {
	rec := newPasteTokenRecorder(t)
	cfg := rec.geminiConfig()

	var err error
	_ = captureStdout(t, func() {
		_, err = loginPasteCodeWithPKCEAndState(cfg, PKCECodes{CodeVerifier: "VERIFIER", CodeChallenge: "CH"}, "KNOWN-STATE", strings.NewReader(geminiManualRedirectForTest+"?code=CODE"))
	})

	const want = "Please paste the full callback URL or query string so lele can verify the login state."
	if err == nil {
		t.Fatalf("loginPasteCodeWithPKCEAndState() error = nil, want %q", want)
	}
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if rec.hits != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (the state could not be verified)", rec.hits)
	}
}

// TestLoginPasteCodeGeminiRejectsStateMismatch covers the stale-paste guard for
// random-state providers: a well-formed callback whose state is not the one this
// flow generated must fail without issuing a request.
func TestLoginPasteCodeGeminiRejectsStateMismatch(t *testing.T) {
	rec := newPasteTokenRecorder(t)
	cfg := rec.geminiConfig()

	var err error
	_ = captureStdout(t, func() {
		_, err = loginPasteCodeWithPKCEAndState(cfg, PKCECodes{CodeVerifier: "VERIFIER", CodeChallenge: "CH"}, "KNOWN-STATE", strings.NewReader(geminiManualRedirectForTest+"?code=CODE&state=WRONG"))
	})

	const want = "OAuth state mismatch. Start login again and use the latest callback URL."
	if err == nil {
		t.Fatalf("loginPasteCodeWithPKCEAndState() error = nil, want %q", want)
	}
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if rec.hits != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (no request for a stale state)", rec.hits)
	}
}

// TestLoginPasteCodeGeminiTreatsCodeHashStateAsBareCode pins jcode's quirk, on
// purpose: `looks_like_callback_input("CODE#STATE")` is false, so the hash form
// never reaches the `code#state` splitter and the whole line is sent as the
// code. Claude accepts that paste form (StateIsVerifier branch); Gemini does
// not, and Google would presumably reject `CODE#STATE` as an authorization code.
// The faithful behaviour is pinned here rather than "fixed", because silently
// inventing a different rule would diverge from the reference implementation
// (gemini.rs `resolve_callback_or_manual_code`, spec §4).
func TestLoginPasteCodeGeminiTreatsCodeHashStateAsBareCode(t *testing.T) {
	rec := newPasteTokenRecorder(t)
	cfg := rec.geminiConfig()

	var (
		cred *AuthCredential
		err  error
	)
	_ = captureStdout(t, func() {
		cred, err = loginPasteCodeWithPKCEAndState(cfg, PKCECodes{CodeVerifier: "VERIFIER", CodeChallenge: "CH"}, "KNOWN-STATE", strings.NewReader("CODE#STATE"))
	})
	if err != nil {
		t.Fatalf("loginPasteCodeWithPKCEAndState() error: %v", err)
	}
	if cred == nil || cred.AccessToken != "at" {
		t.Fatalf("credential = %#v, want the access token %q", cred, "at")
	}
	if rec.hits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", rec.hits)
	}

	sent := rec.sentForm(t)
	if want := "CODE#STATE"; sent.Get("code") != want {
		t.Errorf("exchange body[code] = %q, want the verbatim %q (jcode does not split the hash on this branch)", sent.Get("code"), want)
	}
	if _, ok := sent["state"]; ok {
		t.Errorf("exchange body[state] = %q, want the field absent", sent.Get("state"))
	}
}
