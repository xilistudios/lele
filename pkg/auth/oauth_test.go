package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func makeJWTForClaims(t *testing.T, claims map[string]interface{}) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".sig"
}

func TestBuildAuthorizeURL(t *testing.T) {
	cfg := OAuthProviderConfig{
		Issuer:     "https://auth.example.com",
		ClientID:   "test-client-id",
		Scopes:     "openid profile",
		Originator: "codex_cli_rs",
		Port:       1455,
		// Provider params are now declared explicitly on the config instead of
		// being sniffed from the issuer URL. This hand-built config must list
		// them itself (the presets do this for the real providers).
		ExtraAuthParams: map[string]string{
			"id_token_add_organizations": "true",
			"codex_cli_simplified_flow":  "true",
		},
	}
	pkce := PKCECodes{
		CodeVerifier:  "test-verifier",
		CodeChallenge: "test-challenge",
	}

	u := BuildAuthorizeURL(cfg, pkce, "test-state", "http://localhost:1455/auth/callback")

	if !strings.HasPrefix(u, "https://auth.example.com/oauth/authorize?") {
		t.Errorf("URL does not start with expected prefix: %s", u)
	}
	if !strings.Contains(u, "client_id=test-client-id") {
		t.Error("URL missing client_id")
	}
	if !strings.Contains(u, "code_challenge=test-challenge") {
		t.Error("URL missing code_challenge")
	}
	if !strings.Contains(u, "code_challenge_method=S256") {
		t.Error("URL missing code_challenge_method")
	}
	if !strings.Contains(u, "state=test-state") {
		t.Error("URL missing state")
	}
	if !strings.Contains(u, "response_type=code") {
		t.Error("URL missing response_type")
	}
	if !strings.Contains(u, "id_token_add_organizations=true") {
		t.Error("URL missing id_token_add_organizations")
	}
	if !strings.Contains(u, "codex_cli_simplified_flow=true") {
		t.Error("URL missing codex_cli_simplified_flow")
	}
	if !strings.Contains(u, "originator=codex_cli_rs") {
		t.Error("URL missing originator")
	}
}

func TestBuildAuthorizeURLOpenAIExtras(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	pkce := PKCECodes{CodeVerifier: "test-verifier", CodeChallenge: "test-challenge"}

	u := BuildAuthorizeURL(cfg, pkce, "test-state", "http://localhost:1455/auth/callback")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("url.Parse() error: %v", err)
	}
	q := parsed.Query()

	if q.Get("id_token_add_organizations") != "true" {
		t.Errorf("id_token_add_organizations = %q, want true", q.Get("id_token_add_organizations"))
	}
	if q.Get("codex_cli_simplified_flow") != "true" {
		t.Errorf("codex_cli_simplified_flow = %q, want true", q.Get("codex_cli_simplified_flow"))
	}
	if q.Get("originator") != "codex_cli_rs" {
		t.Errorf("originator = %q, want codex_cli_rs", q.Get("originator"))
	}
}

func TestParseTokenResponse(t *testing.T) {
	resp := map[string]interface{}{
		"access_token":  "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      "test-id-token",
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}

	if cred.AccessToken != "test-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "test-access-token")
	}
	if cred.RefreshToken != "test-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", cred.RefreshToken, "test-refresh-token")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
	if cred.AuthMethod != "oauth" {
		t.Errorf("AuthMethod = %q, want %q", cred.AuthMethod, "oauth")
	}
	if cred.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should not be zero")
	}
}

func TestParseTokenResponseExtractsAccountIDFromIDToken(t *testing.T) {
	idToken := makeJWTForClaims(t, map[string]interface{}{"chatgpt_account_id": "acc-id-from-id-token"})
	resp := map[string]interface{}{
		"access_token":  "opaque-access-token",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      idToken,
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}
	if cred.AccountID != "acc-id-from-id-token" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-id-from-id-token")
	}
}

func TestExtractAccountIDFromOrganizationsFallback(t *testing.T) {
	token := makeJWTForClaims(t, map[string]interface{}{
		"organizations": []interface{}{
			map[string]interface{}{"id": "org_from_orgs"},
		},
	})

	if got := extractAccountID(token); got != "org_from_orgs" {
		t.Errorf("extractAccountID() = %q, want %q", got, "org_from_orgs")
	}
}

func TestParseTokenResponseNoAccessToken(t *testing.T) {
	body := []byte(`{"refresh_token": "test"}`)
	_, err := parseTokenResponse(body, "openai")
	if err == nil {
		t.Error("expected error for missing access_token")
	}
}

func TestParseTokenResponseAccountIDFromIDToken(t *testing.T) {
	idToken := makeJWTWithAccountID("acc-from-id")
	resp := map[string]interface{}{
		"access_token":  "not-a-jwt",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      idToken,
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}

	if cred.AccountID != "acc-from-id" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-from-id")
	}
}

func makeJWTWithAccountID(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return header + "." + payload + ".sig"
}

func TestExchangeCodeForTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"expires_in":    3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
		Scopes:   "openid",
		Port:     1455,
	}

	cred, err := exchangeCodeForTokens(cfg, "test-code", "test-verifier", "http://localhost:1455/auth/callback")
	if err != nil {
		t.Fatalf("exchangeCodeForTokens() error: %v", err)
	}

	if cred.AccessToken != "mock-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "mock-access-token")
	}
}

func TestRefreshAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"access_token":  "refreshed-access-token",
			"refresh_token": "refreshed-refresh-token",
			"expires_in":    3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
	}

	cred := &AuthCredential{
		AccessToken:  "old-token",
		RefreshToken: "old-refresh-token",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}

	if refreshed.AccessToken != "refreshed-access-token" {
		t.Errorf("AccessToken = %q, want %q", refreshed.AccessToken, "refreshed-access-token")
	}
	if refreshed.RefreshToken != "refreshed-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", refreshed.RefreshToken, "refreshed-refresh-token")
	}
}

func TestRefreshAccessTokenNoRefreshToken(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	cred := &AuthCredential{
		AccessToken: "old-token",
		Provider:    "openai",
		AuthMethod:  "oauth",
	}

	_, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Error("expected error for missing refresh token")
	}
}

func TestRefreshAccessTokenPreservesRefreshAndAccountID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"access_token": "new-access-token-only",
			"expires_in":   3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "test-client"}
	cred := &AuthCredential{
		AccessToken:  "old-access",
		RefreshToken: "existing-refresh",
		AccountID:    "acc_existing",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}
	if refreshed.RefreshToken != "existing-refresh" {
		t.Errorf("RefreshToken = %q, want %q", refreshed.RefreshToken, "existing-refresh")
	}
	if refreshed.AccountID != "acc_existing" {
		t.Errorf("AccountID = %q, want %q", refreshed.AccountID, "acc_existing")
	}
}

func TestOpenAIOAuthConfig(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	if cfg.Issuer != "https://auth.openai.com" {
		t.Errorf("Issuer = %q, want %q", cfg.Issuer, "https://auth.openai.com")
	}
	if cfg.ClientID == "" {
		t.Error("ClientID is empty")
	}
	if cfg.Port != 1455 {
		t.Errorf("Port = %d, want 1455", cfg.Port)
	}
}

func TestParseDeviceCodeResponseIntervalAsNumber(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":5}`)

	resp, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}

	if resp.DeviceAuthID != "abc" {
		t.Errorf("DeviceAuthID = %q, want %q", resp.DeviceAuthID, "abc")
	}
	if resp.UserCode != "DEF-1234" {
		t.Errorf("UserCode = %q, want %q", resp.UserCode, "DEF-1234")
	}
	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want %d", resp.Interval, 5)
	}
}

func TestParseDeviceCodeResponseIntervalAsString(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":"5"}`)

	resp, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}

	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want %d", resp.Interval, 5)
	}
}

func TestParseDeviceCodeResponseInvalidInterval(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":"abc"}`)

	if _, err := parseDeviceCodeResponse(body); err == nil {
		t.Fatal("expected error for invalid interval")
	}
}

// TestPresetExplicitFields pins the newly-added explicit metadata of both
// presets. Each expected value is the literal equivalent of what the legacy
// code derived from Issuer or hardcoded outright, so any change here is a
// change in the wire behavior of a live flow and must be intentional.
func TestPresetExplicitFields(t *testing.T) {
	tests := []struct {
		name            string
		cfg             OAuthProviderConfig
		wantName        string
		wantAuthorize   string
		wantDeviceCode  string
		wantDeviceToken string
		wantVerify      string
		wantLegacyCode  string
		wantLegacyToken string
		wantExtra       map[string]string
		wantRefresh     string
	}{
		{
			name:            "openai",
			cfg:             OpenAIOAuthConfig(),
			wantName:        "openai",
			wantAuthorize:   "https://auth.openai.com/oauth/authorize",
			wantDeviceCode:  "https://auth.openai.com/api/accounts/deviceauth/usercode",
			wantDeviceToken: "https://auth.openai.com/api/accounts/deviceauth/token",
			wantVerify:      "https://auth.openai.com/codex/device",
			wantLegacyCode:  "https://auth.openai.com/api/accounts/deviceauth/usercode",
			wantLegacyToken: "https://auth.openai.com/api/accounts/deviceauth/token",
			wantExtra: map[string]string{
				"id_token_add_organizations": "true",
				"codex_cli_simplified_flow":  "true",
			},
			wantRefresh: "openid profile email",
		},
		{
			name:          "google-antigravity",
			cfg:           GoogleAntigravityOAuthConfig(),
			wantName:      "google-antigravity",
			wantAuthorize: "https://accounts.google.com/o/oauth2/v2/auth",
			// Google has no device flow: the device fields stay at their zero value.
			wantExtra: map[string]string{
				"access_type": "offline",
				"prompt":      "consent",
			},
			// No RefreshScopes override: google-antigravity refreshes with
			// cfg.Scopes (the scopes it was originally granted with).
			wantRefresh: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cfg.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", tc.cfg.Name, tc.wantName)
			}
			if tc.cfg.AuthorizeURL != tc.wantAuthorize {
				t.Errorf("AuthorizeURL = %q, want %q", tc.cfg.AuthorizeURL, tc.wantAuthorize)
			}
			if tc.cfg.DeviceCodeURL != tc.wantDeviceCode {
				t.Errorf("DeviceCodeURL = %q, want %q", tc.cfg.DeviceCodeURL, tc.wantDeviceCode)
			}
			if tc.cfg.DeviceTokenURL != tc.wantDeviceToken {
				t.Errorf("DeviceTokenURL = %q, want %q", tc.cfg.DeviceTokenURL, tc.wantDeviceToken)
			}
			if tc.cfg.DeviceVerifyURL != tc.wantVerify {
				t.Errorf("DeviceVerifyURL = %q, want %q", tc.cfg.DeviceVerifyURL, tc.wantVerify)
			}
			if tc.cfg.LegacyDeviceCodeURL != tc.wantLegacyCode {
				t.Errorf("LegacyDeviceCodeURL = %q, want %q", tc.cfg.LegacyDeviceCodeURL, tc.wantLegacyCode)
			}
			if tc.cfg.LegacyDeviceTokenURL != tc.wantLegacyToken {
				t.Errorf("LegacyDeviceTokenURL = %q, want %q", tc.cfg.LegacyDeviceTokenURL, tc.wantLegacyToken)
			}
			if tc.cfg.CallbackPath != "/auth/callback" {
				t.Errorf("CallbackPath = %q, want %q", tc.cfg.CallbackPath, "/auth/callback")
			}
			if !reflect.DeepEqual(tc.cfg.ExtraAuthParams, tc.wantExtra) {
				t.Errorf("ExtraAuthParams = %v, want %v", tc.cfg.ExtraAuthParams, tc.wantExtra)
			}
			if tc.cfg.RefreshScopes != tc.wantRefresh {
				t.Errorf("RefreshScopes = %q, want %q", tc.cfg.RefreshScopes, tc.wantRefresh)
			}
			if tc.cfg.TokenRequestStyle != TokenRequestFormURLEncoded {
				t.Errorf("TokenRequestStyle = %d, want %d", tc.cfg.TokenRequestStyle, TokenRequestFormURLEncoded)
			}
			if tc.cfg.AuthHeaderStyle != TokenAuthBody {
				t.Errorf("AuthHeaderStyle = %d, want %d", tc.cfg.AuthHeaderStyle, TokenAuthBody)
			}
			// Fields with no preset value yet must stay at their zero value so the
			// consumers' documented fallbacks (RedirectURI from Port+CallbackPath,
			// nil HTTPClient => http.DefaultClient) remain in charge.
			if tc.cfg.RedirectURI != "" {
				t.Errorf("RedirectURI = %q, want empty", tc.cfg.RedirectURI)
			}
			if tc.cfg.HTTPClient != nil {
				t.Errorf("HTTPClient = %p, want nil", tc.cfg.HTTPClient)
			}
		})
	}
}

// TestPresetLegacyFieldsPreserved guards the pre-existing fields of both presets:
// appending new fields must not disturb what already worked.
func TestPresetLegacyFieldsPreserved(t *testing.T) {
	openai := OpenAIOAuthConfig()
	openaiChecks := []struct {
		label string
		got   string
		want  string
	}{
		{"Issuer", openai.Issuer, "https://auth.openai.com"},
		{"ClientID", openai.ClientID, "app_EMoamEEZ73f0CkXaXp7hrann"},
		{"Scopes", openai.Scopes, "openid profile email offline_access"},
		{"Originator", openai.Originator, "codex_cli_rs"},
		{"TokenURL (empty, derived from Issuer)", openai.TokenURL, ""},
		{"ClientSecret (public client)", openai.ClientSecret, ""},
	}
	for _, c := range openaiChecks {
		if c.got != c.want {
			t.Errorf("openai %s = %q, want %q", c.label, c.got, c.want)
		}
	}
	if openai.Port != 1455 {
		t.Errorf("openai Port = %d, want 1455", openai.Port)
	}

	antigravity := GoogleAntigravityOAuthConfig()
	antigravityChecks := []struct {
		label string
		got   string
		want  string
	}{
		{"Issuer", antigravity.Issuer, "https://accounts.google.com/o/oauth2/v2"},
		{"TokenURL", antigravity.TokenURL, "https://oauth2.googleapis.com/token"},
		{"ClientID (base64-decoded)", antigravity.ClientID, "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"},
		{"ClientSecret (base64-decoded)", antigravity.ClientSecret, "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"},
	}
	for _, c := range antigravityChecks {
		if c.got != c.want {
			t.Errorf("google-antigravity %s = %q, want %q", c.label, c.got, c.want)
		}
	}
	if antigravity.Port != 51121 {
		t.Errorf("google-antigravity Port = %d, want 51121", antigravity.Port)
	}
	for _, scope := range []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/userinfo.profile",
		"https://www.googleapis.com/auth/cclog",
		"https://www.googleapis.com/auth/experimentsandconfigs",
	} {
		if !strings.Contains(antigravity.Scopes, scope) {
			t.Errorf("google-antigravity Scopes is missing %q (got %q)", scope, antigravity.Scopes)
		}
	}
}

func TestTokenRequestStyleZeroValues(t *testing.T) {
	// Documents that the zero value of both enums is the current (legacy) behavior.
	if TokenRequestFormURLEncoded != 0 {
		t.Errorf("TokenRequestFormURLEncoded = %d, want 0", TokenRequestFormURLEncoded)
	}
	if TokenAuthBody != 0 {
		t.Errorf("TokenAuthBody = %d, want 0", TokenAuthBody)
	}
	// A config that only sets the legacy fields must therefore keep using
	// form-urlencoded bodies and client credentials in the body.
	var legacy OAuthProviderConfig
	if legacy.TokenRequestStyle != TokenRequestFormURLEncoded {
		t.Errorf("zero-value TokenRequestStyle = %d, want form-urlencoded", legacy.TokenRequestStyle)
	}
	if legacy.AuthHeaderStyle != TokenAuthBody {
		t.Errorf("zero-value AuthHeaderStyle = %d, want body auth", legacy.AuthHeaderStyle)
	}
}

// TestBuildAuthorizeURLPresetGolden pins the exact authorize URL produced for
// each shipped preset. The expected strings were captured verbatim from the
// pre-refactor implementation at c993e50e (same PKCE/state/redirect inputs), so
// they are the parity contract for F0-T3: any drift in the explicit
// AuthorizeURL/ExtraAuthParams/Originator wiring fails here.
func TestBuildAuthorizeURLPresetGolden(t *testing.T) {
	pkce := PKCECodes{CodeVerifier: "v", CodeChallenge: "CHALLENGE"}
	const state = "STATE"

	tests := []struct {
		name string
		cfg  OAuthProviderConfig
		want string
	}{
		{
			name: "openai",
			cfg:  OpenAIOAuthConfig(),
			want: "https://auth.openai.com/oauth/authorize?client_id=app_EMoamEEZ73f0CkXaXp7hrann&code_challenge=CHALLENGE&code_challenge_method=S256&codex_cli_simplified_flow=true&id_token_add_organizations=true&originator=codex_cli_rs&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&response_type=code&scope=openid+profile+email+offline_access&state=STATE",
		},
		{
			name: "google-antigravity",
			cfg:  GoogleAntigravityOAuthConfig(),
			want: "https://accounts.google.com/o/oauth2/v2/auth?access_type=offline&client_id=1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com&code_challenge=CHALLENGE&code_challenge_method=S256&prompt=consent&redirect_uri=http%3A%2F%2Flocalhost%3A51121%2Fauth%2Fcallback&response_type=code&scope=https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fcloud-platform+https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fuserinfo.email+https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fuserinfo.profile+https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fcclog+https%3A%2F%2Fwww.googleapis.com%2Fauth%2Fexperimentsandconfigs&state=STATE",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			redirectURI := fmt.Sprintf("http://localhost:%d/auth/callback", tc.cfg.Port)
			got := buildAuthorizeURL(tc.cfg, pkce, state, redirectURI)
			if got != tc.want {
				t.Errorf("buildAuthorizeURL mismatch\ngot:  %s\nwant: %s", got, tc.want)
			}
		})
	}
}

// TestBuildAuthorizeURLUsesExplicitAuthorizeURL verifies that a non-empty
// AuthorizeURL is used verbatim instead of being derived from the issuer, and
// that the query is exactly the base parameter set when no extra params or
// originator are configured.
func TestBuildAuthorizeURLUsesExplicitAuthorizeURL(t *testing.T) {
	cfg := OAuthProviderConfig{
		Issuer:       "https://example.com/whatever",
		AuthorizeURL: "https://example.com/custom/authorize",
		ClientID:     "cid",
		Scopes:       "s1 s2",
	}
	pkce := PKCECodes{CodeChallenge: "CHALLENGE"}

	want := "https://example.com/custom/authorize?client_id=cid&code_challenge=CHALLENGE&code_challenge_method=S256&redirect_uri=http%3A%2F%2Flocalhost%3A1%2Fauth%2Fcallback&response_type=code&scope=s1+s2&state=STATE"
	got := buildAuthorizeURL(cfg, pkce, "STATE", "http://localhost:1/auth/callback")
	if got != want {
		t.Errorf("buildAuthorizeURL mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// TestBuildAuthorizeURLLegacyFallback covers hand-built configs that leave
// AuthorizeURL empty: the endpoint must be derived from the issuer exactly like
// the pre-refactor code (Google => /auth, everything else => /oauth/authorize).
func TestBuildAuthorizeURLLegacyFallback(t *testing.T) {
	pkce := PKCECodes{CodeChallenge: "CHALLENGE"}

	tests := []struct {
		name   string
		issuer string
		prefix string
	}{
		{
			name:   "google",
			issuer: "https://accounts.google.com/o/oauth2/v2",
			prefix: "https://accounts.google.com/o/oauth2/v2/auth?",
		},
		{
			name:   "openai",
			issuer: "https://auth.openai.com",
			prefix: "https://auth.openai.com/oauth/authorize?",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := OAuthProviderConfig{Issuer: tc.issuer, ClientID: "cid", Scopes: "s"}
			got := buildAuthorizeURL(cfg, pkce, "STATE", "http://localhost:1/auth/callback")
			if !strings.HasPrefix(got, tc.prefix) {
				t.Errorf("buildAuthorizeURL = %q, want prefix %q", got, tc.prefix)
			}
		})
	}
}

// TestBuildAuthorizeURLExtraParamsAndOriginator verifies that ExtraAuthParams
// are applied verbatim and that a non-empty Originator is added, while an empty
// Originator adds nothing.
func TestBuildAuthorizeURLExtraParamsAndOriginator(t *testing.T) {
	cfg := OAuthProviderConfig{
		AuthorizeURL: "https://example.com/authorize",
		ExtraAuthParams: map[string]string{
			"prompt":      "login",
			"access_type": "offline",
		},
		Originator: "my-originator",
	}
	pkce := PKCECodes{CodeChallenge: "CHALLENGE"}

	got := buildAuthorizeURL(cfg, pkce, "STATE", "http://localhost:1/auth/callback")
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parsing authorize URL: %v", err)
	}
	q := parsed.Query()
	for k, want := range map[string]string{
		"prompt":      "login",
		"access_type": "offline",
		"originator":  "my-originator",
	} {
		if q.Get(k) != want {
			t.Errorf("query param %q = %q, want %q", k, q.Get(k), want)
		}
	}

	// Empty originator must not inject an `originator` param (no more "lele" default).
	cfgNoOrigin := OAuthProviderConfig{AuthorizeURL: "https://example.com/authorize"}
	got = buildAuthorizeURL(cfgNoOrigin, pkce, "STATE", "http://localhost:1/auth/callback")
	parsed, err = url.Parse(got)
	if err != nil {
		t.Fatalf("parsing authorize URL: %v", err)
	}
	if _, ok := parsed.Query()["originator"]; ok {
		t.Errorf("unexpected originator param in %q", got)
	}
}

// TestCallbackPathAndRedirectOverride covers the pure helpers that LoginBrowser
// uses to derive the loopback callback path and the redirect_uri, including the
// explicit overrides.
func TestCallbackPathAndRedirectOverride(t *testing.T) {
	tests := []struct {
		name         string
		cfg          OAuthProviderConfig
		wantPath     string
		wantRedirect string
	}{
		{
			name:         "defaults",
			cfg:          OAuthProviderConfig{Port: 1234},
			wantPath:     "/auth/callback",
			wantRedirect: "http://localhost:1234/auth/callback",
		},
		{
			name:         "custom callback path",
			cfg:          OAuthProviderConfig{CallbackPath: "/cb", Port: 80},
			wantPath:     "/cb",
			wantRedirect: "http://localhost:80/cb",
		},
		{
			name:         "redirect override wins",
			cfg:          OAuthProviderConfig{CallbackPath: "/cb", Port: 80, RedirectURI: "https://app.example/return"},
			wantPath:     "/cb",
			wantRedirect: "https://app.example/return",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := oauthCallbackPath(tc.cfg); got != tc.wantPath {
				t.Errorf("oauthCallbackPath = %q, want %q", got, tc.wantPath)
			}
			if got := oauthRedirectURI(tc.cfg); got != tc.wantRedirect {
				t.Errorf("oauthRedirectURI = %q, want %q", got, tc.wantRedirect)
			}
		})
	}
}

// TestRefreshScope covers the pure scope-selection helper used by
// RefreshAccessToken: an explicit RefreshScopes wins, otherwise the originally
// granted Scopes are reused, and with both empty the scope parameter is omitted.
// OmitRefreshScope wins over both and always means "omit the scope parameter";
// this precedence is what TestRefreshScopeOmissionWinsOverScopes would assert,
// so it lives in the table below instead of a duplicated test function.
func TestRefreshScope(t *testing.T) {
	tests := []struct {
		name string
		cfg  OAuthProviderConfig
		want string
	}{
		{
			name: "RefreshScopes wins over Scopes",
			cfg:  OAuthProviderConfig{RefreshScopes: "a b", Scopes: "c d"},
			want: "a b",
		},
		{
			name: "falls back to the granted Scopes",
			cfg:  OAuthProviderConfig{Scopes: "c d"},
			want: "c d",
		},
		{
			name: "both empty means omit the scope parameter",
			cfg:  OAuthProviderConfig{},
			want: "",
		},
		{
			name: "OmitRefreshScope wins over RefreshScopes and omits the parameter",
			cfg:  OAuthProviderConfig{OmitRefreshScope: true, RefreshScopes: "a b"},
			want: "",
		},
		{
			name: "OmitRefreshScope wins over Scopes and omits the parameter",
			cfg:  OAuthProviderConfig{OmitRefreshScope: true, Scopes: "s1 s2"},
			want: "",
		},
		{
			name: "OmitRefreshScope with both scopes empty omits the parameter",
			cfg:  OAuthProviderConfig{OmitRefreshScope: true},
			want: "",
		},
		{
			name: "OmitRefreshScope false keeps the historical Scopes fallback",
			cfg:  OAuthProviderConfig{OmitRefreshScope: false, Scopes: "c d"},
			want: "c d",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := refreshScope(tc.cfg); got != tc.want {
				t.Errorf("refreshScope() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestExchangeCodeForTokensProviderName pins the provider identity stored on the
// credential: it is taken verbatim from cfg.Name (with the legacy "openai"
// default for hand-built configs) and is never sniffed from TokenURL.
//
// The old code set provider = "google-antigravity" whenever TokenURL contained
// the substring "googleapis.com", so every case below with that substring in the
// URL would have been labelled google-antigravity before this change. The URLs
// point at the local httptest server (which happens to expose that substring as
// part of its path), so the test stays offline.
func TestExchangeCodeForTokensProviderName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "at",
			"refresh_token": "rt",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	tests := []struct {
		name     string
		cfg      OAuthProviderConfig
		wantProv string
	}{
		{
			name:     "Name is used as the provider identity",
			cfg:      OAuthProviderConfig{Issuer: server.URL, ClientID: "cid", Name: "claude"},
			wantProv: "claude",
		},
		{
			name:     "empty Name keeps the legacy openai default",
			cfg:      OAuthProviderConfig{Issuer: server.URL, ClientID: "cid"},
			wantProv: "openai",
		},
		{
			name:     "non-googleapis TokenURL does not affect the identity",
			cfg:      OAuthProviderConfig{Issuer: server.URL, ClientID: "cid", Name: "claude", TokenURL: server.URL + "/oauth/token"},
			wantProv: "claude",
		},
		{
			name:     "googleapis substring in TokenURL does not affect the identity",
			cfg:      OAuthProviderConfig{Issuer: server.URL, ClientID: "cid", Name: "claude", TokenURL: server.URL + "/googleapis.com/token"},
			wantProv: "claude",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cred, err := exchangeCodeForTokens(tc.cfg, "code", "verifier", "http://localhost/cb")
			if err != nil {
				t.Fatalf("exchangeCodeForTokens() error: %v", err)
			}
			if cred.Provider != tc.wantProv {
				t.Errorf("cred.Provider = %q, want %q", cred.Provider, tc.wantProv)
			}
			if cred.AuthMethod != "oauth" {
				t.Errorf("cred.AuthMethod = %q, want %q", cred.AuthMethod, "oauth")
			}
			if cred.AccessToken != "at" {
				t.Errorf("cred.AccessToken = %q, want %q", cred.AccessToken, "at")
			}
		})
	}
}

// TestRefreshAccessTokenScopeOnWire asserts what RefreshAccessToken actually puts
// on the wire: the scope is resolved from the config (RefreshScopes, else the
// originally granted Scopes) and omitted entirely when neither is set, instead of
// the pre-existing hardcoded "openid profile email".
func TestRefreshAccessTokenScopeOnWire(t *testing.T) {
	tests := []struct {
		name      string
		cfg       func(srvURL string) OAuthProviderConfig
		wantScope string
		wantNoKey bool
	}{
		{
			name: "RefreshScopes is sent (openai parity)",
			cfg: func(srvURL string) OAuthProviderConfig {
				return OAuthProviderConfig{
					Issuer:        srvURL,
					ClientID:      "cid",
					Name:          "openai",
					RefreshScopes: "openid profile email",
					Scopes:        "openid profile email offline_access",
				}
			},
			wantScope: "openid profile email",
		},
		{
			name: "falls back to the granted Scopes (google-antigravity case)",
			cfg: func(srvURL string) OAuthProviderConfig {
				return OAuthProviderConfig{
					Issuer:   srvURL,
					ClientID: "cid",
					Name:     "x",
					Scopes:   "cloud-platform userinfo.email",
				}
			},
			wantScope: "cloud-platform userinfo.email",
		},
		{
			name: "no scopes at all omits the scope parameter",
			cfg: func(srvURL string) OAuthProviderConfig {
				return OAuthProviderConfig{Issuer: srvURL, ClientID: "cid"}
			},
			wantNoKey: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotForm url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				gotForm = r.PostForm
				json.NewEncoder(w).Encode(map[string]interface{}{
					"access_token": "at2",
					"expires_in":   3600,
				})
			}))
			defer server.Close()

			cred := &AuthCredential{RefreshToken: "rt", Provider: "p"}
			refreshed, err := RefreshAccessToken(cred, tc.cfg(server.URL))
			if err != nil {
				t.Fatalf("RefreshAccessToken() error: %v", err)
			}

			if gotForm.Get("grant_type") != "refresh_token" {
				t.Errorf("grant_type = %q, want %q", gotForm.Get("grant_type"), "refresh_token")
			}
			if gotForm.Get("refresh_token") != "rt" {
				t.Errorf("refresh_token = %q, want %q", gotForm.Get("refresh_token"), "rt")
			}
			if gotForm.Get("client_id") != "cid" {
				t.Errorf("client_id = %q, want %q", gotForm.Get("client_id"), "cid")
			}

			if tc.wantNoKey {
				if _, ok := gotForm["scope"]; ok {
					t.Errorf("scope parameter was sent (%q), want it omitted", gotForm.Get("scope"))
				}
			} else if got := gotForm.Get("scope"); got != tc.wantScope {
				t.Errorf("scope on the wire = %q, want %q", got, tc.wantScope)
			}

			// The refresh response carries no refresh_token: the credential's
			// existing one must survive the refresh.
			if refreshed.RefreshToken != "rt" {
				t.Errorf("refreshed.RefreshToken = %q, want %q (preserved)", refreshed.RefreshToken, "rt")
			}
			if refreshed.AccessToken != "at2" {
				t.Errorf("refreshed.AccessToken = %q, want %q", refreshed.AccessToken, "at2")
			}
		})
	}
}

// TestTokenRequestEncoding exercises tokenRequest in isolation (no sockets) for
// the four combinations of TokenRequestStyle × AuthHeaderStyle, and pins that
// the input params map is never mutated.
func TestTokenRequestEncoding(t *testing.T) {
	params := url.Values{
		"a":             {"1"},
		"client_secret": {"s3cr3t"},
		"client_id":     {"cid"},
	}
	basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("cid:s3cr3t"))

	read := func(t *testing.T, r io.Reader) string {
		t.Helper()
		b, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("reading body: %v", err)
		}
		return string(b)
	}

	t.Run("form + body", func(t *testing.T) {
		body, contentType, authorization := tokenRequest(OAuthProviderConfig{}, params)
		if contentType != "application/x-www-form-urlencoded" {
			t.Errorf("contentType = %q, want application/x-www-form-urlencoded", contentType)
		}
		if authorization != "" {
			t.Errorf("authorization = %q, want empty", authorization)
		}
		if got := read(t, body); got != params.Encode() {
			t.Errorf("body = %q, want %q", got, params.Encode())
		}
	})

	t.Run("form + basic", func(t *testing.T) {
		cfg := OAuthProviderConfig{
			AuthHeaderStyle: TokenAuthBasic,
			ClientID:        "cid",
			ClientSecret:    "s3cr3t",
		}
		body, contentType, authorization := tokenRequest(cfg, params)
		if contentType != "application/x-www-form-urlencoded" {
			t.Errorf("contentType = %q, want application/x-www-form-urlencoded", contentType)
		}
		if authorization != basicAuth {
			t.Errorf("authorization = %q, want %q", authorization, basicAuth)
		}
		got := read(t, body)
		if strings.Contains(got, "client_secret") {
			t.Errorf("body still carries client_secret: %q", got)
		}
		want := url.Values{"a": {"1"}, "client_id": {"cid"}}.Encode()
		if got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
	})

	t.Run("json + body", func(t *testing.T) {
		cfg := OAuthProviderConfig{TokenRequestStyle: TokenRequestJSON}
		body, contentType, authorization := tokenRequest(cfg, params)
		if contentType != "application/json" {
			t.Errorf("contentType = %q, want application/json", contentType)
		}
		if authorization != "" {
			t.Errorf("authorization = %q, want empty", authorization)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(read(t, body)), &got); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		want := map[string]string{"a": "1", "client_secret": "s3cr3t", "client_id": "cid"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("json body = %v, want %v", got, want)
		}
	})

	t.Run("json + basic", func(t *testing.T) {
		cfg := OAuthProviderConfig{
			TokenRequestStyle: TokenRequestJSON,
			AuthHeaderStyle:   TokenAuthBasic,
			ClientID:          "cid",
			ClientSecret:      "s3cr3t",
		}
		body, contentType, authorization := tokenRequest(cfg, params)
		if contentType != "application/json" {
			t.Errorf("contentType = %q, want application/json", contentType)
		}
		if authorization != basicAuth {
			t.Errorf("authorization = %q, want %q", authorization, basicAuth)
		}
		var got map[string]string
		if err := json.Unmarshal([]byte(read(t, body)), &got); err != nil {
			t.Fatalf("body is not valid JSON: %v", err)
		}
		want := map[string]string{"a": "1", "client_id": "cid"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("json body = %v, want %v", got, want)
		}
	})

	// None of the four calls above may have mutated the caller's map.
	if got, ok := params["client_secret"]; !ok || len(got) != 1 || got[0] != "s3cr3t" {
		t.Errorf("params was mutated: client_secret = %v", params["client_secret"])
	}
	if len(params) != 3 {
		t.Errorf("params was mutated: %v", params)
	}
}

// TestDoTokenRequestWire checks the four style combinations on the wire (not
// only in the helper): method, path, Content-Type, Authorization and raw body.
func TestDoTokenRequestWire(t *testing.T) {
	type capture struct {
		method        string
		path          string
		contentType   string
		authorization string
		body          string
	}

	var mu sync.Mutex
	var captured capture

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		captured = capture{
			method:        r.Method,
			path:          r.URL.Path,
			contentType:   r.Header.Get("Content-Type"),
			authorization: r.Header.Get("Authorization"),
			body:          string(b),
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"at","expires_in":3600}`))
	}))
	defer server.Close()

	endpoint := server.URL + "/oauth/token"
	basicAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("cid:s3cr3t"))
	params := url.Values{"a": {"1"}, "client_secret": {"s3cr3t"}, "client_id": {"cid"}}

	tests := []struct {
		name            string
		cfg             OAuthProviderConfig
		wantContentType string
		wantAuth        string
		wantBody        func(t *testing.T, body string)
	}{
		{
			name:            "form + body",
			cfg:             OAuthProviderConfig{},
			wantContentType: "application/x-www-form-urlencoded",
			wantAuth:        "",
			wantBody: func(t *testing.T, body string) {
				if body != params.Encode() {
					t.Errorf("body = %q, want %q", body, params.Encode())
				}
			},
		},
		{
			name:            "form + basic",
			cfg:             OAuthProviderConfig{AuthHeaderStyle: TokenAuthBasic, ClientID: "cid", ClientSecret: "s3cr3t"},
			wantContentType: "application/x-www-form-urlencoded",
			wantAuth:        basicAuth,
			wantBody: func(t *testing.T, body string) {
				if strings.Contains(body, "client_secret") {
					t.Errorf("body should not carry client_secret: %q", body)
				}
			},
		},
		{
			name:            "json + body",
			cfg:             OAuthProviderConfig{TokenRequestStyle: TokenRequestJSON},
			wantContentType: "application/json",
			wantAuth:        "",
			wantBody: func(t *testing.T, body string) {
				var m map[string]string
				if err := json.Unmarshal([]byte(body), &m); err != nil {
					t.Fatalf("body is not valid JSON: %v", err)
				}
				if m["client_secret"] != "s3cr3t" {
					t.Errorf("json body missing client_secret: %v", m)
				}
			},
		},
		{
			name:            "json + basic",
			cfg:             OAuthProviderConfig{TokenRequestStyle: TokenRequestJSON, AuthHeaderStyle: TokenAuthBasic, ClientID: "cid", ClientSecret: "s3cr3t"},
			wantContentType: "application/json",
			wantAuth:        basicAuth,
			wantBody: func(t *testing.T, body string) {
				var m map[string]string
				if err := json.Unmarshal([]byte(body), &m); err != nil {
					t.Fatalf("body is not valid JSON: %v", err)
				}
				if _, ok := m["client_secret"]; ok {
					t.Errorf("json body should not carry client_secret: %v", m)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			captured = capture{}
			mu.Unlock()

			resp, err := doTokenRequest(tc.cfg, endpoint, params)
			if err != nil {
				t.Fatalf("doTokenRequest() error: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}

			mu.Lock()
			got := captured
			mu.Unlock()

			if got.method != http.MethodPost {
				t.Errorf("method = %q, want POST", got.method)
			}
			if got.path != "/oauth/token" {
				t.Errorf("path = %q, want /oauth/token", got.path)
			}
			if got.contentType != tc.wantContentType {
				t.Errorf("Content-Type = %q, want %q", got.contentType, tc.wantContentType)
			}
			if got.authorization != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", got.authorization, tc.wantAuth)
			}
			tc.wantBody(t, got.body)
		})
	}
}

// stubTransport is an http.RoundTripper that records the requests it sees and
// answers with a canned token response, without touching the network.
type stubTransport struct {
	mu    sync.Mutex
	calls int
	reqs  []*http.Request
}

func (s *stubTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	s.mu.Lock()
	s.calls++
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"access_token":"at"}`)),
		Request:    req,
	}, nil
}

// TestOAuthHTTPClientUsesConfig proves the token-endpoint call goes through
// cfg.HTTPClient: the issuer points at 127.0.0.1:1 (an impossible connection),
// yet exchangeCodeForTokens succeeds because the stub transport answers instead.
func TestOAuthHTTPClientUsesConfig(t *testing.T) {
	stub := &stubTransport{}
	cfg := OAuthProviderConfig{
		Issuer:     "http://127.0.0.1:1",
		ClientID:   "cid",
		HTTPClient: &http.Client{Transport: stub},
	}

	cred, err := exchangeCodeForTokens(cfg, "code", "verifier", "http://localhost/cb")
	if err != nil {
		t.Fatalf("exchangeCodeForTokens() error: %v", err)
	}
	if cred.AccessToken != "at" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "at")
	}

	stub.mu.Lock()
	calls := stub.calls
	var path string
	if len(stub.reqs) > 0 {
		path = stub.reqs[0].URL.Path
	}
	stub.mu.Unlock()

	if calls == 0 {
		t.Error("stub transport received no request: cfg.HTTPClient was ignored")
	}
	if path != "/oauth/token" {
		t.Errorf("requested path = %q, want /oauth/token", path)
	}

	if oauthHTTPClient(OAuthProviderConfig{}) != http.DefaultClient {
		t.Error("oauthHTTPClient with nil HTTPClient should return http.DefaultClient")
	}
	if oauthHTTPClient(cfg) != cfg.HTTPClient {
		t.Error("oauthHTTPClient should return cfg.HTTPClient when set")
	}
}

// TestFormBodyParityWithPostForm pins the zero-style wire parity: the request
// built by doTokenRequest must be indistinguishable from one produced by
// http.PostForm (method, Content-Type, Content-Length and body).
func TestFormBodyParityWithPostForm(t *testing.T) {
	type capture struct {
		method        string
		contentType   string
		contentLength int64
		body          string
	}

	var mu sync.Mutex
	var captures []capture

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		captures = append(captures, capture{
			method:        r.Method,
			contentType:   r.Header.Get("Content-Type"),
			contentLength: r.ContentLength,
			body:          string(b),
		})
		mu.Unlock()
		w.Write([]byte(`{}`))
	}))
	defer server.Close()

	params := url.Values{
		"grant_type": {"authorization_code"},
		"code":       {"c0de"},
		"client_id":  {"cid"},
	}
	endpoint := server.URL + "/oauth/token"

	resp, err := doTokenRequest(OAuthProviderConfig{}, endpoint, params)
	if err != nil {
		t.Fatalf("doTokenRequest() error: %v", err)
	}
	resp.Body.Close()

	resp2, err := http.PostForm(endpoint, params)
	if err != nil {
		t.Fatalf("http.PostForm() error: %v", err)
	}
	resp2.Body.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(captures) != 2 {
		t.Fatalf("captured %d requests, want 2", len(captures))
	}
	got, want := captures[0], captures[1]
	if got.method != want.method {
		t.Errorf("method: doTokenRequest %q != http.PostForm %q", got.method, want.method)
	}
	if got.contentType != want.contentType {
		t.Errorf("Content-Type: %q != %q", got.contentType, want.contentType)
	}
	if got.contentLength != want.contentLength {
		t.Errorf("Content-Length: %d != %d (body lengths %d vs %d)",
			got.contentLength, want.contentLength, len(got.body), len(want.body))
	}
	if got.body != want.body {
		t.Errorf("body: %q != %q", got.body, want.body)
	}
}

// TestRefreshAccessTokenOmitsScopeForGooglePreset is the wire-level falsifier for
// the google-antigravity refresh: unlike the historical hardcoded
// scope="openid profile email" (which openai must keep), the Google preset must
// send no scope key at all on the refresh_token request (RFC 6749 §6; jcode
// omits it too). The assertion parses the real POST body with url.ParseQuery
// instead of substring-matching it.
func TestRefreshAccessTokenOmitsScopeForGooglePreset(t *testing.T) {
	tests := []struct {
		name      string
		cfg       func(srvURL string) OAuthProviderConfig
		wantScope string
		wantNoKey bool
	}{
		{
			name: "google-antigravity preset omits the scope parameter",
			cfg: func(srvURL string) OAuthProviderConfig {
				cfg := GoogleAntigravityOAuthConfig()
				cfg.TokenURL = srvURL
				return cfg
			},
			wantNoKey: true,
		},
		{
			name: "openai preset still sends openid profile email (c993e50e parity)",
			cfg: func(srvURL string) OAuthProviderConfig {
				cfg := OpenAIOAuthConfig()
				cfg.Issuer = srvURL
				cfg.TokenURL = srvURL
				return cfg
			},
			wantScope: "openid profile email",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var rawBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				rawBody = string(b)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"access_token": "new",
					"expires_in":   3600,
				})
			}))
			defer server.Close()

			cfg := tc.cfg(server.URL)
			cfg.HTTPClient = server.Client()

			cred := &AuthCredential{RefreshToken: "rt", Provider: "p"}
			refreshed, err := RefreshAccessToken(cred, cfg)
			if err != nil {
				t.Fatalf("RefreshAccessToken() error: %v", err)
			}
			if refreshed.AccessToken != "new" {
				t.Errorf("AccessToken = %q, want %q", refreshed.AccessToken, "new")
			}

			form, err := url.ParseQuery(rawBody)
			if err != nil {
				t.Fatalf("url.ParseQuery(%q) error: %v", rawBody, err)
			}
			if form.Get("grant_type") != "refresh_token" {
				t.Errorf("grant_type = %q, want %q", form.Get("grant_type"), "refresh_token")
			}

			_, hasScope := form["scope"]
			if tc.wantNoKey {
				if hasScope {
					t.Errorf("scope parameter was sent (%q), want it omitted", form.Get("scope"))
				}
			} else if !hasScope {
				t.Errorf("scope parameter was omitted, want %q", tc.wantScope)
			} else if got := form.Get("scope"); got != tc.wantScope {
				t.Errorf("scope on the wire = %q, want %q", got, tc.wantScope)
			}
		})
	}
}

// TestPresetsRefreshScopePolicy pins the refresh-scope policy encoded in both
// presets: google-antigravity omits the parameter, while openai keeps the
// historical "openid profile email" (byte-for-byte what c993e50e hardcoded).
func TestPresetsRefreshScopePolicy(t *testing.T) {
	google := GoogleAntigravityOAuthConfig()
	if !google.OmitRefreshScope {
		t.Errorf("GoogleAntigravityOAuthConfig().OmitRefreshScope = false, want true")
	}
	if google.RefreshScopes != "" {
		t.Errorf("GoogleAntigravityOAuthConfig().RefreshScopes = %q, want empty", google.RefreshScopes)
	}

	openai := OpenAIOAuthConfig()
	if openai.OmitRefreshScope {
		t.Errorf("OpenAIOAuthConfig().OmitRefreshScope = true, want false")
	}
	if openai.RefreshScopes != "openid profile email" {
		t.Errorf("OpenAIOAuthConfig().RefreshScopes = %q, want %q", openai.RefreshScopes, "openid profile email")
	}
}

// TestTokenRequestJSONMultiValueKeepsFirst pins the documented semantics of the
// JSON encoder for a key with several values: a flat JSON object cannot express
// a list, so the FIRST value wins (see tokenRequest's doc comment). Every other
// test uses single-valued params, so changing "first" to "last" in the encoder
// would otherwise go unnoticed. No preset uses TokenRequestJSON today, which is
// exactly why this arbitrary rule must stay pinned by a test.
func TestTokenRequestJSONMultiValueKeepsFirst(t *testing.T) {
	params := url.Values{
		"grant_type": {"refresh_token", "authorization_code"},
		"scope":      {"a", "b"},
	}

	body, contentType, authorization := tokenRequest(
		OAuthProviderConfig{TokenRequestStyle: TokenRequestJSON}, params,
	)
	if contentType != "application/json" {
		t.Errorf("contentType = %q, want application/json", contentType)
	}
	if authorization != "" {
		t.Errorf("authorization = %q, want empty", authorization)
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("body is not a flat JSON object: %v (body=%q)", err, string(raw))
	}
	if got["grant_type"] != "refresh_token" {
		t.Errorf("grant_type = %q, want the first value %q", got["grant_type"], "refresh_token")
	}
	if got["scope"] != "a" {
		t.Errorf("scope = %q, want the first value %q", got["scope"], "a")
	}

	// The caller's params map is cloned by the encoder and must stay intact.
	if len(params["grant_type"]) != 2 {
		t.Errorf("params were mutated: grant_type = %v, want [refresh_token authorization_code]", params["grant_type"])
	}
}

// ---------------------------------------------------------------------------
// Proprietary OpenAI device flow (LoginDeviceCode / pollDeviceCode)
//
// These tests exercise the flow end to end without touching the network: every
// endpoint is served by an httptest.Server and the only client handed to the
// flow wraps that server's own transport.
// ---------------------------------------------------------------------------

// countingTransport wraps a RoundTripper and counts the requests that pass
// through it. It proves the proprietary device flow goes through cfg.HTTPClient
// instead of http.DefaultClient: a request that bypassed cfg.HTTPClient would
// not be counted (and would leave the process, since the presets point at the
// real OpenAI issuer).
type countingTransport struct {
	inner http.RoundTripper
	mu    sync.Mutex
	calls int
}

func (c *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.inner.RoundTrip(req)
}

func (c *countingTransport) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// deviceFlowServer is an httptest.Server serving the three endpoints the
// proprietary flow talks to, recording every request it receives.
type deviceFlowServer struct {
	*httptest.Server

	// Canned responses. Tests set these before calling the flow (no request is
	// in flight yet), so they need no locking.
	deviceStatus int    // usercode endpoint status (default 200)
	deviceBody   string // usercode endpoint body
	pollStatuses []int  // one status consumed per poll (default 200)
	tokenStatus  int    // token endpoint status (default 200)
	tokenBody    string // token endpoint body

	mu sync.Mutex

	deviceHits    int
	devicePath    string
	deviceVerb    string
	deviceCT      string
	deviceReqBody string

	pollHits    int
	pollPath    string
	pollVerb    string
	pollCT      string
	pollReqBody string

	tokenHits int
	tokenPath string
	tokenVerb string
	tokenCT   string
	tokenForm url.Values
}

// deviceFlowDeviceCodeBody is the usercode response: the shape OpenAI's real
// endpoint returns (device_auth_id + user_code + interval, the latter as a
// string in the wild). device_code is included on purpose so the "the device
// code is never printed" assertion is meaningful.
const deviceFlowDeviceCodeBody = `{"device_auth_id":"dc-1","device_code":"dc-1","user_code":"USER-1","expires_in":900,"interval":1}`

func newDeviceFlowServer(t *testing.T) *deviceFlowServer {
	t.Helper()

	s := &deviceFlowServer{
		deviceStatus: http.StatusOK,
		deviceBody:   deviceFlowDeviceCodeBody,
		tokenStatus:  http.StatusOK,
		tokenBody:    `{"access_token":"at-openai","expires_in":3600}`,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/accounts/deviceauth/usercode", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		s.mu.Lock()
		s.deviceHits++
		s.devicePath = r.URL.Path
		s.deviceVerb = r.Method
		s.deviceCT = r.Header.Get("Content-Type")
		s.deviceReqBody = string(body)
		status, payload := s.deviceStatus, s.deviceBody
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	})

	mux.HandleFunc("/api/accounts/deviceauth/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		s.mu.Lock()
		s.pollHits++
		s.pollPath = r.URL.Path
		s.pollVerb = r.Method
		s.pollCT = r.Header.Get("Content-Type")
		s.pollReqBody = string(body)
		status := http.StatusOK
		if len(s.pollStatuses) > 0 {
			status = s.pollStatuses[0]
			s.pollStatuses = s.pollStatuses[1:]
		}
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = io.WriteString(w, `{"authorization_code":"ac-1","code_challenge":"cc","code_verifier":"cv"}`)
		}
	})

	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))

		s.mu.Lock()
		s.tokenHits++
		s.tokenPath = r.URL.Path
		s.tokenVerb = r.Method
		s.tokenCT = r.Header.Get("Content-Type")
		s.tokenForm = form
		status, payload := s.tokenStatus, s.tokenBody
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	})

	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// client returns an *http.Client that reaches this server through a counting
// transport, plus the counter itself.
func (s *deviceFlowServer) client() (*http.Client, *countingTransport) {
	counter := &countingTransport{inner: s.Client().Transport}
	return &http.Client{Transport: counter}, counter
}

// openAIConfig is the real openai preset with every endpoint the proprietary
// flow can use pointed at this server, so nothing can escape to the network.
// Issuer is left untouched (https://auth.openai.com): it is still used for the
// deviceauth callback redirect and must not trigger any request.
func (s *deviceFlowServer) openAIConfig(client *http.Client) OAuthProviderConfig {
	cfg := OpenAIOAuthConfig()
	cfg.LegacyDeviceCodeURL = s.URL + "/api/accounts/deviceauth/usercode"
	cfg.LegacyDeviceTokenURL = s.URL + "/api/accounts/deviceauth/token"
	cfg.DeviceVerifyURL = s.URL + "/codex/device"
	cfg.TokenURL = s.URL + "/oauth/token"
	cfg.HTTPClient = client
	return cfg
}

// snapshot returns the recorded request data under the server lock.
func (s *deviceFlowServer) snapshot() (deviceHits int, deviceVerb, deviceCT, deviceReqBody string,
	pollHits int, pollVerb, pollCT, pollReqBody string,
	tokenHits int, tokenVerb, tokenCT string, tokenForm url.Values) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceHits, s.deviceVerb, s.deviceCT, s.deviceReqBody,
		s.pollHits, s.pollVerb, s.pollCT, s.pollReqBody,
		s.tokenHits, s.tokenVerb, s.tokenCT, s.tokenForm
}

// captureDeviceFlowOut swaps the printed-text seam for a buffer and restores it
// when the test finishes.
func captureDeviceFlowOut(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	previous := loginDeviceCodeOut
	loginDeviceCodeOut = buf
	t.Cleanup(func() { loginDeviceCodeOut = previous })
	return buf
}

// TestLoginDeviceCodeProprietaryRequestParity pins the wire format and the user
// facing text of the proprietary OpenAI device flow against the openai preset:
// JSON bodies, exact Content-Type, form-encoded exchange, the issuer-derived
// callback redirect, and byte-identical instructions.
func TestLoginDeviceCodeProprietaryRequestParity(t *testing.T) {
	s := newDeviceFlowServer(t)
	client, counter := s.client()
	out := captureDeviceFlowOut(t)

	cfg := s.openAIConfig(client)

	cred, err := LoginDeviceCode(cfg)
	if err != nil {
		t.Fatalf("LoginDeviceCode() error: %v", err)
	}

	deviceHits, deviceVerb, deviceCT, deviceReqBody,
		pollHits, pollVerb, pollCT, pollReqBody,
		tokenHits, tokenVerb, tokenCT, tokenForm := s.snapshot()

	// --- request 1: device code (proprietary usercode endpoint) ------------
	if deviceHits != 1 {
		t.Errorf("usercode endpoint hits = %d, want 1", deviceHits)
	}
	if deviceVerb != http.MethodPost {
		t.Errorf("usercode method = %q, want POST", deviceVerb)
	}
	if deviceCT != "application/json" {
		t.Errorf("usercode Content-Type = %q, want exactly application/json", deviceCT)
	}
	// Built from json.Marshal of a single-entry map: key order is deterministic.
	const wantDeviceBody = `{"client_id":"app_EMoamEEZ73f0CkXaXp7hrann"}`
	if deviceReqBody != wantDeviceBody {
		t.Errorf("usercode body = %q, want %q", deviceReqBody, wantDeviceBody)
	}

	// --- request 2: poll -----------------------------------------------
	if pollHits != 1 {
		t.Errorf("poll endpoint hits = %d, want 1", pollHits)
	}
	if pollVerb != http.MethodPost {
		t.Errorf("poll method = %q, want POST", pollVerb)
	}
	if pollCT != "application/json" {
		t.Errorf("poll Content-Type = %q, want exactly application/json", pollCT)
	}
	const wantPollBody = `{"device_auth_id":"dc-1","user_code":"USER-1"}`
	if pollReqBody != wantPollBody {
		t.Errorf("poll body = %q, want %q", pollReqBody, wantPollBody)
	}

	// --- request 3: code exchange (form-encoded, TokenURL override) -------
	if tokenHits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", tokenHits)
	}
	if tokenVerb != http.MethodPost {
		t.Errorf("token method = %q, want POST", tokenVerb)
	}
	if !strings.HasPrefix(tokenCT, "application/x-www-form-urlencoded") {
		t.Errorf("token Content-Type = %q, want application/x-www-form-urlencoded", tokenCT)
	}
	for field, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "ac-1",
		"code_verifier": "cv",
		"client_id":     cfg.ClientID,
		// The proprietary flow keeps its issuer-derived server callback.
		"redirect_uri": "https://auth.openai.com/deviceauth/callback",
	} {
		if got := tokenForm.Get(field); got != want {
			t.Errorf("token form %s = %q, want %q", field, got, want)
		}
	}

	// --- printed instructions -------------------------------------------
	// The literal format string the flow used before the seam existed.
	wantText := fmt.Sprintf(
		"\nTo authenticate, open this URL in your browser:\n\n  %s\n\nThen enter this code: %s\n\nWaiting for authentication...\n",
		s.URL+"/codex/device", "USER-1",
	)
	if got := out.String(); got != wantText {
		t.Errorf("printed text mismatch\n got: %q\nwant: %q", got, wantText)
	}
	if strings.Contains(out.String(), "dc-1") {
		t.Errorf("printed text leaks the device auth id: %q", out.String())
	}

	// --- credential and client -----------------------------------------
	if cred.AccessToken != "at-openai" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "at-openai")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
	if got := counter.count(); got != 3 {
		t.Errorf("cfg.HTTPClient saw %d requests, want 3 (a package-level http.Post would use http.DefaultClient)", got)
	}
}

// TestLoginDeviceCodeUsesExplicitLegacyURLs proves the explicit Legacy* fields
// are honoured: the issuer is an RFC 2606 name that can never resolve, so a
// flow that ignored the fields would fail with a network error instead of
// completing.
func TestLoginDeviceCodeUsesExplicitLegacyURLs(t *testing.T) {
	s := newDeviceFlowServer(t)
	client, counter := s.client()
	out := captureDeviceFlowOut(t)

	cfg := s.openAIConfig(client)
	cfg.Issuer = "https://issuer.invalid"

	cred, err := LoginDeviceCode(cfg)
	if err != nil {
		t.Fatalf("LoginDeviceCode() error: %v", err)
	}

	deviceHits, _, _, _, pollHits, _, _, _, tokenHits, _, _, _ := s.snapshot()
	if deviceHits != 1 {
		t.Errorf("usercode endpoint hits = %d, want 1 (LegacyDeviceCodeURL ignored?)", deviceHits)
	}
	if pollHits != 1 {
		t.Errorf("poll endpoint hits = %d, want 1 (LegacyDeviceTokenURL ignored?)", pollHits)
	}
	if tokenHits != 1 {
		t.Errorf("token endpoint hits = %d, want 1 (TokenURL ignored?)", tokenHits)
	}
	if cred.AccessToken != "at-openai" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "at-openai")
	}
	if got := counter.count(); got != 3 {
		t.Errorf("cfg.HTTPClient saw %d requests, want 3", got)
	}
	if strings.Contains(out.String(), "issuer.invalid") {
		t.Errorf("printed text used the issuer instead of DeviceVerifyURL: %q", out.String())
	}
}

// TestLoginDeviceCodeFallsBackToIssuerDerivedURLs pins the legacy derivation:
// with the explicit fields empty the three endpoints must resolve exactly as
// they did before they existed.
func TestLoginDeviceCodeFallsBackToIssuerDerivedURLs(t *testing.T) {
	s := newDeviceFlowServer(t)
	client, counter := s.client()
	out := captureDeviceFlowOut(t)

	cfg := s.openAIConfig(client)
	cfg.Issuer = s.URL
	cfg.LegacyDeviceCodeURL = ""
	cfg.LegacyDeviceTokenURL = ""
	cfg.DeviceVerifyURL = ""

	cred, err := LoginDeviceCode(cfg)
	if err != nil {
		t.Fatalf("LoginDeviceCode() error: %v", err)
	}

	s.mu.Lock()
	deviceHits, devicePath := s.deviceHits, s.devicePath
	pollHits, pollPath := s.pollHits, s.pollPath
	tokenHits := s.tokenHits
	s.mu.Unlock()

	if deviceHits != 1 {
		t.Errorf("usercode endpoint hits = %d, want 1", deviceHits)
	}
	if devicePath != "/api/accounts/deviceauth/usercode" {
		t.Errorf("usercode path = %q, want /api/accounts/deviceauth/usercode", devicePath)
	}
	if pollHits != 1 {
		t.Errorf("poll endpoint hits = %d, want 1", pollHits)
	}
	if pollPath != "/api/accounts/deviceauth/token" {
		t.Errorf("poll path = %q, want /api/accounts/deviceauth/token", pollPath)
	}
	if tokenHits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", tokenHits)
	}
	if cred.AccessToken != "at-openai" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "at-openai")
	}
	if got := counter.count(); got != 3 {
		t.Errorf("cfg.HTTPClient saw %d requests, want 3", got)
	}
	if want := s.URL + "/codex/device"; !strings.Contains(out.String(), want) {
		t.Errorf("printed text = %q, want it to contain the derived verify URL %q", out.String(), want)
	}
}

// TestLoginDeviceCodePollKeepsPollingOnNon200 pins the "pending" semantics: a
// non-200 poll is not fatal, the flow polls again and succeeds.
func TestLoginDeviceCodePollKeepsPollingOnNon200(t *testing.T) {
	s := newDeviceFlowServer(t)
	client, counter := s.client()
	captureDeviceFlowOut(t)

	s.pollStatuses = []int{http.StatusNotFound, http.StatusOK}

	cfg := s.openAIConfig(client)

	cred, err := LoginDeviceCode(cfg)
	if err != nil {
		t.Fatalf("LoginDeviceCode() error: %v", err)
	}

	_, _, _, _, pollHits, _, _, _, tokenHits, _, _, _ := s.snapshot()
	if pollHits != 2 {
		t.Errorf("poll endpoint hits = %d, want 2 (a non-200 must be retried)", pollHits)
	}
	if tokenHits != 1 {
		t.Errorf("token endpoint hits = %d, want 1", tokenHits)
	}
	if cred.AccessToken != "at-openai" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "at-openai")
	}
	// 1 usercode + 2 polls + 1 exchange.
	if got := counter.count(); got != 4 {
		t.Errorf("cfg.HTTPClient saw %d requests, want 4 (1 usercode + 2 polls + 1 exchange)", got)
	}
}

// TestLoginDeviceCodeDeviceCodeRequestFailure pins the non-200 handling of the
// usercode request: it must fail before any poll and print nothing.
func TestLoginDeviceCodeDeviceCodeRequestFailure(t *testing.T) {
	s := newDeviceFlowServer(t)
	client, counter := s.client()
	out := captureDeviceFlowOut(t)

	s.deviceStatus = http.StatusInternalServerError
	s.deviceBody = `{"error":"boom"}`

	cfg := s.openAIConfig(client)

	cred, err := LoginDeviceCode(cfg)
	if err == nil {
		t.Fatalf("LoginDeviceCode() = %+v, want error", cred)
	}
	if !strings.Contains(err.Error(), "device code request failed") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "device code request failed")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to carry the server body", err.Error())
	}

	deviceHits, _, _, _, pollHits, _, _, _, _, _, _, _ := s.snapshot()
	if deviceHits != 1 {
		t.Errorf("usercode endpoint hits = %d, want 1", deviceHits)
	}
	if pollHits != 0 {
		t.Errorf("poll endpoint hits = %d, want 0 (the flow must abort first)", pollHits)
	}
	if out.Len() != 0 {
		t.Errorf("printed text = %q, want nothing", out.String())
	}
	if got := counter.count(); got != 1 {
		t.Errorf("cfg.HTTPClient saw %d requests, want 1", got)
	}
}

// TestLoginDeviceCodeDeviceCodeResponseParseFailure pins that an unusable 200
// body (here user_code is not a string) aborts before any poll.
func TestLoginDeviceCodeDeviceCodeResponseParseFailure(t *testing.T) {
	s := newDeviceFlowServer(t)
	client, counter := s.client()
	out := captureDeviceFlowOut(t)

	s.deviceBody = `{"user_code":42,"interval":1}`

	cfg := s.openAIConfig(client)

	cred, err := LoginDeviceCode(cfg)
	if err == nil {
		t.Fatalf("LoginDeviceCode() = %+v, want error", cred)
	}
	if !strings.Contains(err.Error(), "parsing device code response") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "parsing device code response")
	}

	deviceHits, _, _, _, pollHits, _, _, _, _, _, _, _ := s.snapshot()
	if deviceHits != 1 {
		t.Errorf("usercode endpoint hits = %d, want 1", deviceHits)
	}
	if pollHits != 0 {
		t.Errorf("poll endpoint hits = %d, want 0 (the flow must abort first)", pollHits)
	}
	if out.Len() != 0 {
		t.Errorf("printed text = %q, want nothing", out.String())
	}
	if got := counter.count(); got != 1 {
		t.Errorf("cfg.HTTPClient saw %d requests, want 1", got)
	}
}
