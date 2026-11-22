package auth

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
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

func TestParseOAuthScopes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"only separators", "  ", nil},
		{"single", "a", []string{"a"}},
		{"mixed whitespace", "a  b\tc", []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOAuthScopes(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseOAuthScopes(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseTokenResponseParsesScope(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "scope present",
			body: `{"access_token":"at","expires_in":3600,"scope":"user:profile user:inference"}`,
			want: []string{"user:profile", "user:inference"},
		},
		{
			name: "scope absent",
			body: `{"access_token":"at","expires_in":3600}`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cred, err := parseTokenResponse([]byte(tt.body), "anthropic")
			if err != nil {
				t.Fatalf("parseTokenResponse() error: %v", err)
			}
			if !reflect.DeepEqual(cred.Scopes, tt.want) {
				t.Errorf("Scopes = %#v, want %#v", cred.Scopes, tt.want)
			}
		})
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

func TestPresetExplicitFields(t *testing.T) {
	tests := []struct {
		name                    string
		cfg                     OAuthProviderConfig
		wantName                string
		wantAuthorize           string
		wantDeviceCode          string
		wantDeviceToken         string
		wantVerify              string
		wantLegacyCode          string
		wantLegacyToken         string
		wantExtra               map[string]string
		wantRefresh             string
		wantTokenURL            string
		wantClientID            string
		wantScopes              string
		wantCallback            string
		wantStyle               TokenRequestStyle
		wantHasClient           bool
		wantRequireRefreshToken bool
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
			wantExtra: map[string]string{
				"access_type": "offline",
				"prompt":      "consent",
			},
			wantRefresh: "",
		},
		{
			name:          "anthropic",
			cfg:           ClaudeOAuthConfig(),
			wantName:      "anthropic",
			wantAuthorize: "https://claude.com/cai/oauth/authorize",
			wantTokenURL:  "https://platform.claude.com/v1/oauth/token",
			wantClientID:  "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
			wantScopes:    "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload",
			wantExtra: map[string]string{
				"code": "true",
			},
			wantRefresh:             "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload",
			wantCallback:            "/callback",
			wantStyle:               TokenRequestJSON,
			wantHasClient:           true,
			wantRequireRefreshToken: true,
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
			if tc.cfg.CallbackPath != wantCallbackPath(tc.wantCallback) {
				t.Errorf("CallbackPath = %q, want %q", tc.cfg.CallbackPath, wantCallbackPath(tc.wantCallback))
			}
			if !reflect.DeepEqual(tc.cfg.ExtraAuthParams, tc.wantExtra) {
				t.Errorf("ExtraAuthParams = %v, want %v", tc.cfg.ExtraAuthParams, tc.wantExtra)
			}
			if tc.cfg.RefreshScopes != tc.wantRefresh {
				t.Errorf("RefreshScopes = %q, want %q", tc.cfg.RefreshScopes, tc.wantRefresh)
			}
			if tc.cfg.TokenRequestStyle != tc.wantStyle {
				t.Errorf("TokenRequestStyle = %d, want %d", tc.cfg.TokenRequestStyle, tc.wantStyle)
			}
			if tc.cfg.AuthHeaderStyle != TokenAuthBody {
				t.Errorf("AuthHeaderStyle = %d, want %d", tc.cfg.AuthHeaderStyle, TokenAuthBody)
			}
			if tc.cfg.RedirectURI != "" {
				t.Errorf("RedirectURI = %q, want empty", tc.cfg.RedirectURI)
			}
			if (tc.cfg.HTTPClient != nil) != tc.wantHasClient {
				t.Errorf("HTTPClient = %p, want nil=%v", tc.cfg.HTTPClient, !tc.wantHasClient)
			}
			if tc.wantTokenURL != "" && tc.cfg.TokenURL != tc.wantTokenURL {
				t.Errorf("TokenURL = %q, want %q", tc.cfg.TokenURL, tc.wantTokenURL)
			}
			if tc.wantClientID != "" && tc.cfg.ClientID != tc.wantClientID {
				t.Errorf("ClientID = %q, want %q", tc.cfg.ClientID, tc.wantClientID)
			}
			if tc.wantScopes != "" && tc.cfg.Scopes != tc.wantScopes {
				t.Errorf("Scopes = %q, want %q", tc.cfg.Scopes, tc.wantScopes)
			}
			if tc.cfg.RequireRefreshToken != tc.wantRequireRefreshToken {
				t.Errorf("RequireRefreshToken = %v, want %v", tc.cfg.RequireRefreshToken, tc.wantRequireRefreshToken)
			}
		})
	}
}

func wantCallbackPath(perCase string) string {
	if perCase == "" {
		return "/auth/callback"
	}
	return perCase
}

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
	if TokenRequestFormURLEncoded != 0 {
		t.Errorf("TokenRequestFormURLEncoded = %d, want 0", TokenRequestFormURLEncoded)
	}
	if TokenAuthBody != 0 {
		t.Errorf("TokenAuthBody = %d, want 0", TokenAuthBody)
	}
	var legacy OAuthProviderConfig
	if legacy.TokenRequestStyle != TokenRequestFormURLEncoded {
		t.Errorf("zero-value TokenRequestStyle = %d, want form-urlencoded", legacy.TokenRequestStyle)
	}
	if legacy.AuthHeaderStyle != TokenAuthBody {
		t.Errorf("zero-value AuthHeaderStyle = %d, want body auth", legacy.AuthHeaderStyle)
	}
}

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

func TestBuildAuthorizeURLLegacyFallback(t *testing.T) {
	pkce := PKCECodes{CodeChallenge: "CHALLENGE"}

	tests := []struct {
		name     string
		issuer   string
		endpoint string
	}{
		{
			name:     "google",
			issuer:   "https://accounts.google.com/o/oauth2/v2",
			endpoint: "https://accounts.google.com/o/oauth2/v2/auth",
		},
		{
			name:     "openai",
			issuer:   "https://auth.openai.com",
			endpoint: "https://auth.openai.com/oauth/authorize",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := OAuthProviderConfig{Issuer: tc.issuer, ClientID: "cid", Scopes: "s"}
			got := buildAuthorizeURL(cfg, pkce, "STATE", "http://localhost:1/auth/callback")

			parsed, err := url.Parse(got)
			if err != nil {
				t.Fatalf("parsing authorize URL %q: %v", got, err)
			}
			if base := parsed.Scheme + "://" + parsed.Host + parsed.Path; base != tc.endpoint {
				t.Errorf("derived endpoint = %q, want %q", base, tc.endpoint)
			}

			want := map[string]string{
				"client_id":             "cid",
				"code_challenge":        "CHALLENGE",
				"code_challenge_method": "S256",
				"redirect_uri":          "http://localhost:1/auth/callback",
				"response_type":         "code",
				"scope":                 "s",
				"state":                 "STATE",
			}
			q := parsed.Query()
			for k, v := range want {
				if q.Get(k) != v {
					t.Errorf("query param %q = %q, want %q", k, q.Get(k), v)
				}
			}
			for k := range q {
				if _, ok := want[k]; !ok {
					t.Errorf("unexpected query param %q = %q", k, q.Get(k))
				}
			}
		})
	}
}

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

			if refreshed.RefreshToken != "rt" {
				t.Errorf("refreshed.RefreshToken = %q, want %q (preserved)", refreshed.RefreshToken, "rt")
			}
			if refreshed.AccessToken != "at2" {
				t.Errorf("refreshed.AccessToken = %q, want %q", refreshed.AccessToken, "at2")
			}
		})
	}
}

func TestRefreshAccessTokenRetriesWithoutScopeOnInvalidScope(t *testing.T) {
	var hits int
	var wireBodies []string
	var parsedBodies []map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading request body: %v", err)
		}
		wireBodies = append(wireBodies, string(raw))

		var fields map[string]string
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Errorf("decoding request body %q: %v", string(raw), err)
		}
		parsedBodies = append(parsedBodies, fields)

		if _, ok := fields["scope"]; ok {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error":"invalid_scope","error_description":"requested scope is invalid"}`)
			return
		}
		io.WriteString(w, `{"access_token":"fresh","refresh_token":"rot","expires_in":3600}`)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Name:              "anthropic",
		ClientID:          "cid",
		Scopes:            "user:profile user:inference",
		TokenRequestStyle: TokenRequestJSON,
		TokenURL:          server.URL,
		HTTPClient:        server.Client(),
	}
	cred := &AuthCredential{
		AccessToken:  "old",
		RefreshToken: "old-refresh",
		Provider:     "anthropic",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}

	if hits != 2 {
		t.Fatalf("token endpoint hits = %d, want exactly 2 (one scoped attempt + one retry)", hits)
	}
	t.Logf("request 1 wire body: %s", wireBodies[0])
	t.Logf("request 2 wire body: %s", wireBodies[1])

	if _, ok := parsedBodies[0]["scope"]; !ok {
		t.Errorf("request 1 body = %s, want it to carry the scope", wireBodies[0])
	} else if got := parsedBodies[0]["scope"]; got != "user:profile user:inference" {
		t.Errorf("request 1 scope = %q, want %q", got, "user:profile user:inference")
	}
	if _, ok := parsedBodies[1]["scope"]; ok {
		t.Errorf("request 2 body = %s, want the scope field deleted", wireBodies[1])
	}

	if refreshed.AccessToken != "fresh" {
		t.Errorf("AccessToken = %q, want %q", refreshed.AccessToken, "fresh")
	}
	if refreshed.RefreshToken != "rot" {
		t.Errorf("RefreshToken = %q, want the ROTATED %q (not the old one)", refreshed.RefreshToken, "rot")
	}
}

func TestRefreshAccessTokenRetryFailureKeepsBothDiagnostics(t *testing.T) {
	const (
		scopedBody   = `{"error":"invalid_scope","error_description":"scoped attempt rejected"}`
		fallbackBody = `{"error":"invalid_request","error_description":"fallback attempt rejected"}`
	)

	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		var fields map[string]string
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Errorf("decoding request body %q: %v", string(raw), err)
		}
		w.WriteHeader(http.StatusBadRequest)
		if _, ok := fields["scope"]; ok {
			io.WriteString(w, scopedBody)
			return
		}
		io.WriteString(w, fallbackBody)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Name:              "anthropic",
		ClientID:          "cid",
		Scopes:            "user:profile user:inference",
		TokenRequestStyle: TokenRequestJSON,
		TokenURL:          server.URL,
		HTTPClient:        server.Client(),
	}
	cred := &AuthCredential{RefreshToken: "old-refresh", Provider: "anthropic", AuthMethod: "oauth"}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Fatal("RefreshAccessToken() error = nil, want the double-failure error")
	}
	if refreshed != nil {
		t.Errorf("credential = %#v, want nil when both attempts failed", refreshed)
	}
	if hits != 2 {
		t.Errorf("token endpoint hits = %d, want exactly 2 (scoped attempt + unscoped fallback)", hits)
	}

	for _, want := range []string{
		"token refresh failed",
		"fallback without scope failed",
		"scoped refresh error",
		"scoped attempt rejected",
		"fallback attempt rejected",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

func TestRefreshAccessTokenNonInvalidScopeErrorDoesNotRetry(t *testing.T) {
	var hits int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		var fields map[string]string
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Errorf("decoding request body %q: %v", string(raw), err)
		}
		if _, ok := fields["scope"]; !ok {
			t.Errorf("request %d body = %s, want the scope to be present", hits, string(raw))
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Name:              "anthropic",
		ClientID:          "cid",
		Scopes:            "user:profile user:inference",
		TokenRequestStyle: TokenRequestJSON,
		TokenURL:          server.URL,
		HTTPClient:        server.Client(),
	}
	cred := &AuthCredential{RefreshToken: "old-refresh", Provider: "anthropic", AuthMethod: "oauth"}

	_, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Fatal("RefreshAccessToken() error = nil, want the invalid_grant failure")
	}
	if hits != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1 (no retry for invalid_grant)", hits)
	}
	if !strings.Contains(err.Error(), "token refresh failed") || !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %q, want it to carry the first attempt's body", err)
	}
}

func TestRefreshAccessTokenInvalidScopeWithoutScopeSentNoRetry(t *testing.T) {
	var hits int
	var wireBodies []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		raw, _ := io.ReadAll(r.Body)
		wireBodies = append(wireBodies, string(raw))
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"invalid_scope","error_description":"requested scope is invalid"}`)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Name:              "anthropic",
		ClientID:          "cid",
		Scopes:            "user:profile user:inference",
		OmitRefreshScope:  true,
		TokenRequestStyle: TokenRequestJSON,
		TokenURL:          server.URL,
		HTTPClient:        server.Client(),
	}
	cred := &AuthCredential{RefreshToken: "old-refresh", Provider: "anthropic", AuthMethod: "oauth"}

	_, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Fatal("RefreshAccessToken() error = nil, want the invalid_scope failure")
	}
	if hits != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1 (no scope was sent, nothing to retry)", hits)
	}
	if n := len(wireBodies); n > 0 && strings.Contains(wireBodies[0], "scope") {
		t.Errorf("request body = %s, want no scope field", wireBodies[0])
	}
	if !strings.Contains(err.Error(), "token refresh failed") || !strings.Contains(err.Error(), "invalid_scope") {
		t.Errorf("error = %q, want the first attempt's invalid_scope body", err)
	}
}

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

	if got, ok := params["client_secret"]; !ok || len(got) != 1 || got[0] != "s3cr3t" {
		t.Errorf("params was mutated: client_secret = %v", params["client_secret"])
	}
	if len(params) != 3 {
		t.Errorf("params was mutated: %v", params)
	}
}

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

	claude := ClaudeOAuthConfig()
	if claude.OmitRefreshScope {
		t.Errorf("ClaudeOAuthConfig().OmitRefreshScope = true, want false")
	}
	if want := "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"; claude.RefreshScopes != want {
		t.Errorf("ClaudeOAuthConfig().RefreshScopes = %q, want %q", claude.RefreshScopes, want)
	}
}

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

	if len(params["grant_type"]) != 2 {
		t.Errorf("params were mutated: grant_type = %v, want [refresh_token authorization_code]", params["grant_type"])
	}
}

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

type deviceFlowServer struct {
	*httptest.Server

	deviceStatus int
	deviceBody   string
	pollStatuses []int
	tokenStatus  int
	tokenBody    string

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

func (s *deviceFlowServer) client() (*http.Client, *countingTransport) {
	counter := &countingTransport{inner: s.Client().Transport}
	return &http.Client{Transport: counter}, counter
}

func (s *deviceFlowServer) openAIConfig(client *http.Client) OAuthProviderConfig {
	cfg := OpenAIOAuthConfig()
	cfg.LegacyDeviceCodeURL = s.URL + "/api/accounts/deviceauth/usercode"
	cfg.LegacyDeviceTokenURL = s.URL + "/api/accounts/deviceauth/token"
	cfg.DeviceVerifyURL = s.URL + "/codex/device"
	cfg.TokenURL = s.URL + "/oauth/token"
	cfg.HTTPClient = client
	return cfg
}

func (s *deviceFlowServer) snapshot() (deviceHits int, deviceVerb, deviceCT, deviceReqBody string,
	pollHits int, pollVerb, pollCT, pollReqBody string,
	tokenHits int, tokenVerb, tokenCT string, tokenForm url.Values) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deviceHits, s.deviceVerb, s.deviceCT, s.deviceReqBody,
		s.pollHits, s.pollVerb, s.pollCT, s.pollReqBody,
		s.tokenHits, s.tokenVerb, s.tokenCT, s.tokenForm
}

func captureDeviceFlowOut(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := &bytes.Buffer{}
	previous := loginDeviceCodeOut
	loginDeviceCodeOut = buf
	t.Cleanup(func() { loginDeviceCodeOut = previous })
	return buf
}

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

	if deviceHits != 1 {
		t.Errorf("usercode endpoint hits = %d, want 1", deviceHits)
	}
	if deviceVerb != http.MethodPost {
		t.Errorf("usercode method = %q, want POST", deviceVerb)
	}
	if deviceCT != "application/json" {
		t.Errorf("usercode Content-Type = %q, want exactly application/json", deviceCT)
	}
	const wantDeviceBody = `{"client_id":"app_EMoamEEZ73f0CkXaXp7hrann"}`
	if deviceReqBody != wantDeviceBody {
		t.Errorf("usercode body = %q, want %q", deviceReqBody, wantDeviceBody)
	}

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
		"redirect_uri":  "https://auth.openai.com/deviceauth/callback",
	} {
		if got := tokenForm.Get(field); got != want {
			t.Errorf("token form %s = %q, want %q", field, got, want)
		}
	}

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
	if got := counter.count(); got != 4 {
		t.Errorf("cfg.HTTPClient saw %d requests, want 4 (1 usercode + 2 polls + 1 exchange)", got)
	}
}

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

func TestClaudeExchangeSendsStateAndCodeVerifier(t *testing.T) {
	var mu sync.Mutex
	var gotContentType string
	var gotBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "reading body", http.StatusInternalServerError)
			return
		}
		var fields map[string]string
		if err := json.Unmarshal(raw, &fields); err != nil {
			http.Error(w, "body is not JSON", http.StatusBadRequest)
			return
		}
		mu.Lock()
		gotContentType = r.Header.Get("Content-Type")
		gotBody = fields
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "at",
			"refresh_token": "rt",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	const callbackURL = "https://platform.claude.com/oauth/code/callback"
	cfg := OAuthProviderConfig{
		TokenRequestStyle: TokenRequestJSON,
		TokenURL:          server.URL,
		HTTPClient:        server.Client(),
		ClientID:          "cid",
		Name:              "anthropic",
	}

	cred, err := exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         "C",
		CodeVerifier: "V",
		RedirectURI:  callbackURL,
		State:        "V",
	})
	if err != nil {
		t.Fatalf("exchangeCodeForTokensWithParams() error: %v", err)
	}
	if cred.AccessToken != "at" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "at")
	}

	mu.Lock()
	defer mu.Unlock()

	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	want := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "C",
		"code_verifier": "V",
		"redirect_uri":  callbackURL,
		"client_id":     "cid",
		"state":         "V",
	}
	if !reflect.DeepEqual(gotBody, want) {
		t.Errorf("json body = %v, want %v", gotBody, want)
	}
}

func TestExchangeCodeForTokensOmitsStateByDefault(t *testing.T) {
	var mu sync.Mutex
	var gotBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "reading body", http.StatusInternalServerError)
			return
		}
		mu.Lock()
		gotBody = string(raw)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"at","expires_in":3600}`))
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		TokenURL:   server.URL,
		HTTPClient: server.Client(),
		ClientID:   "cid",
		Name:       "openai",
	}
	if _, err := exchangeCodeForTokens(cfg, "C", "V", "http://localhost:1455/auth/callback"); err != nil {
		t.Fatalf("exchangeCodeForTokens() error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	values, err := url.ParseQuery(gotBody)
	if err != nil {
		t.Fatalf("form body is not a valid query: %v", err)
	}
	if got := values.Get("state"); got != "" {
		t.Errorf("state = %q, want it omitted (OpenAI parity)", got)
	}
	if strings.Contains(gotBody, "state") {
		t.Errorf("form body mentions state: %q", gotBody)
	}
	want := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {"C"},
		"redirect_uri":  {"http://localhost:1455/auth/callback"},
		"client_id":     {"cid"},
		"code_verifier": {"V"},
	}.Encode()
	if gotBody != want {
		t.Errorf("form body = %q, want %q", gotBody, want)
	}
}

func TestBindCallbackListenerEphemeralPort(t *testing.T) {
	cfg := OAuthProviderConfig{Port: 0, CallbackPath: "/callback"}

	ln, err := bindCallbackListener(cfg)
	if err != nil {
		t.Fatalf("bindCallbackListener() error: %v", err)
	}
	defer ln.Close()

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is %T, want *net.TCPAddr", ln.Addr())
	}
	if tcpAddr.Port <= 0 {
		t.Errorf("bound port = %d, want an OS-assigned port > 0", tcpAddr.Port)
	}
	if got := ln.Addr().String(); !strings.HasPrefix(got, "127.0.0.1:") {
		t.Errorf("bind address = %q, want the 127.0.0.1 loopback prefix", got)
	}
	want := fmt.Sprintf("http://localhost:%d/callback", tcpAddr.Port)
	if got := redirectURIForListener(cfg, ln); got != want {
		t.Errorf("redirectURIForListener = %q, want %q", got, want)
	}
}

func TestRedirectURIForListener(t *testing.T) {
	cfg := OAuthProviderConfig{Port: 0, CallbackPath: "/callback"}

	ln, err := bindCallbackListener(cfg)
	if err != nil {
		t.Fatalf("bindCallbackListener() error: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	cfg.Port = port

	want := fmt.Sprintf("http://localhost:%d/callback", port)
	if got := redirectURIForListener(cfg, ln); got != want {
		t.Errorf("redirectURIForListener = %q, want %q", got, want)
	}
	if got := oauthRedirectURI(cfg); got != want {
		t.Errorf("oauthRedirectURI = %q, want %q (must equal the bound-port derivation)", got, want)
	}

	cfg.RedirectURI = "http://127.0.0.1:9/x"
	if got := redirectURIForListener(cfg, ln); got != cfg.RedirectURI {
		t.Errorf("redirectURIForListener with override = %q, want %q", got, cfg.RedirectURI)
	}
	if got := oauthRedirectURI(cfg); got != cfg.RedirectURI {
		t.Errorf("oauthRedirectURI with override = %q, want %q", got, cfg.RedirectURI)
	}
}

func TestLooksLikeCloudflareChallenge(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "cf-hit-challenge marker", body: `<html><body>Error code: CF-HIT-CHALLENGE</body></html>`, want: true},
		{name: "cf-challenge marker", body: `<html><body>Error 1020: cf-challenge</body></html>`, want: true},
		{name: "cloudflare mention", body: `<!DOCTYPE html><title>Attention Required! | Cloudflare</title>`, want: true},
		{name: "just a moment", body: `<html><head><title>Just a moment...</title></head></html>`, want: true},
		{name: "challenge platform script", body: `<script src="/cdn-cgi/challenge-platform/h/b/orchestrate/chl_page/v1"></script>`, want: true},
		{name: "markers are matched case-insensitively", body: "JUST A MOMENT", want: true},
		{name: "regular oauth error document", body: `{"error":"invalid_grant","error_description":"bad code"}`, want: false},
		{name: "empty body", body: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeCloudflareChallenge(tc.body); got != tc.want {
				t.Errorf("looksLikeCloudflareChallenge(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestExchangeCloudflare403Message(t *testing.T) {
	challenge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "<html><head><title>Just a moment...</title></head><body>Just a moment...</body></html>")
	}))
	defer challenge.Close()

	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	defer plain.Close()

	newCfg := func(endpoint string) OAuthProviderConfig {
		return OAuthProviderConfig{
			TokenURL: endpoint,
			ClientID: "cid",
			Name:     "anthropic",
		}
	}
	params := exchangeParams{
		Code:         "C",
		CodeVerifier: "V",
		RedirectURI:  "https://platform.claude.com/oauth/code/callback",
		State:        "V",
	}

	t.Run("cloudflare challenge 403", func(t *testing.T) {
		_, err := exchangeCodeForTokensWithParams(newCfg(challenge.URL), params)
		if err == nil {
			t.Fatal("exchangeCodeForTokensWithParams() = nil error, want a Cloudflare error")
		}
		if !strings.Contains(err.Error(), "Cloudflare") {
			t.Errorf("error = %q, want it to mention Cloudflare", err.Error())
		}
		if !strings.Contains(err.Error(), "lele auth login --provider claude --paste-code") {
			t.Errorf("error = %q, want it to mention the paste-code remediation", err.Error())
		}
		if strings.Contains(err.Error(), "token exchange failed") {
			t.Errorf("error = %q, want the Cloudflare branch to win over the generic status error", err.Error())
		}
	})

	t.Run("plain 403 keeps the generic message", func(t *testing.T) {
		_, err := exchangeCodeForTokensWithParams(newCfg(plain.URL), params)
		if err == nil {
			t.Fatal("exchangeCodeForTokensWithParams() = nil error, want an error")
		}
		if !strings.Contains(err.Error(), "token exchange failed") {
			t.Errorf("error = %q, want the generic status error", err.Error())
		}
		if strings.Contains(err.Error(), "Cloudflare") {
			t.Errorf("error = %q, want no Cloudflare claim for a plain 403", err.Error())
		}
	})
}

func cloudflareChallengeServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, "<html><head><title>Just a moment...</title></head><body>Just a moment...</body></html>")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestExchangeCloudflare403MessageUsesDisplayName(t *testing.T) {
	cfg := OAuthProviderConfig{
		Name:        "gemini",
		DisplayName: "Gemini",
		ClientID:    "cid",
		TokenURL:    cloudflareChallengeServer(t).URL,
	}

	cred, err := exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         "C",
		CodeVerifier: "V",
		RedirectURI:  "https://codeassist.google.com/authcode",
		State:        "V",
	})
	if err == nil {
		t.Fatal("exchangeCodeForTokensWithParams() = nil error, want a Cloudflare error")
	}
	if cred != nil {
		t.Errorf("credential = %#v, want nil when the exchange is blocked", cred)
	}

	msg := err.Error()
	if !strings.Contains(msg, "Gemini") {
		t.Errorf("error = %q, want it to name the provider from DisplayName %q", msg, cfg.DisplayName)
	}
	if !strings.Contains(msg, "`lele auth login --provider gemini --paste-code`") {
		t.Errorf("error = %q, want the recovery command derived from the lower-case label", msg)
	}
	if strings.Contains(msg, "--provider claude") {
		t.Errorf("error = %q, must not keep the hardcoded Claude flag", msg)
	}
	if strings.Contains(msg, "claude") {
		t.Errorf("error = %q, must not mention claude for a Gemini config", msg)
	}
	if strings.Contains(msg, "Anthropic") {
		t.Errorf("error = %q, must not mention Anthropic for a Gemini config", msg)
	}
}

func TestExchangeCloudflare403MessageDefaultWordingUnchanged(t *testing.T) {
	want := "token exchange was blocked by Cloudflare before Anthropic returned OAuth tokens. Switch VPN exit IP or network, then retry with `lele auth login --provider claude --paste-code` and paste the callback URL"

	cfg := OAuthProviderConfig{
		Name:     "anthropic",
		ClientID: "cid",
		TokenURL: cloudflareChallengeServer(t).URL,
	}

	cred, err := exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         "C",
		CodeVerifier: "V",
		RedirectURI:  "https://platform.claude.com/oauth/code/callback",
		State:        "V",
	})
	if err == nil {
		t.Fatal("exchangeCodeForTokensWithParams() = nil error, want a Cloudflare error")
	}
	if cred != nil {
		t.Errorf("credential = %#v, want nil when the exchange is blocked", cred)
	}
	if err.Error() != want {
		t.Errorf("error = %q,\nwant %q", err.Error(), want)
	}
}

func TestOAuthProviderConfigDisplayName(t *testing.T) {
	tests := []struct {
		name         string
		displayName  string
		wantDisplay  string
		wantFallback string
	}{
		{name: "empty keeps the historical Claude wording", displayName: "", wantDisplay: "Claude", wantFallback: "Claude"},
		{name: "claude preset", displayName: "Claude", wantDisplay: "Claude", wantFallback: "Claude"},
		{name: "gemini preset", displayName: "Gemini", wantDisplay: "Gemini", wantFallback: "Gemini"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := OAuthProviderConfig{DisplayName: tt.displayName}
			if got := cfg.displayName(); got != tt.wantDisplay {
				t.Errorf("displayName() = %q, want %q", got, tt.wantDisplay)
			}
			if got := cfg.displayNameOr("Claude"); got != tt.wantFallback {
				t.Errorf("displayNameOr(%q) = %q, want %q", "Claude", got, tt.wantFallback)
			}
			if got := strings.ToLower(cfg.displayName()); got != strings.ToLower(tt.wantDisplay) {
				t.Errorf("lower-case label = %q, want %q", got, strings.ToLower(tt.wantDisplay))
			}
		})
	}

	if got := (OAuthProviderConfig{}).displayNameOr("Anthropic"); got != "Anthropic" {
		t.Errorf("displayNameOr(%q) = %q, want the historical product word", "Anthropic", got)
	}
}
