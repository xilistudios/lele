package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// OAuthProviderConfig describes every knob of a provider's OAuth 2.0 flow.
//
// The first group of fields (Issuer … Port) is the original, minimal shape: all
// endpoints and flows were derived from it, partly by sniffing URL substrings.
// The second group makes that derivation explicit so new providers can be added
// without heuristics. Every field in the second group has a zero value that
// reproduces the pre-existing behavior, so a preset that leaves them unset keeps
// behaving exactly as before.
//
// Adding ExtraAuthParams (a map) makes OAuthProviderConfig non-comparable: do
// not use it with ==, as a map key, or in a switch.
type OAuthProviderConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string // Required for Google OAuth (confidential client)
	TokenURL     string // Override token endpoint (Google uses a different URL than issuer)
	Scopes       string
	Originator   string
	Port         int

	Name                 string            // provider identity key, e.g. "openai", "google-antigravity"
	AuthorizeURL         string            // full authorize endpoint; empty => derive from Issuer (legacy)
	DeviceCodeURL        string            // RFC 8628 device_authorization endpoint
	DeviceTokenURL       string            // RFC 8628 token poll endpoint
	LegacyDeviceCodeURL  string            // OpenAI-proprietary usercode endpoint
	LegacyDeviceTokenURL string            // OpenAI-proprietary poll endpoint
	DeviceVerifyURL      string            // device flow display URL
	CallbackPath         string            // loopback callback path; empty => "/auth/callback"
	RedirectURI          string            // full redirect override; empty => http://localhost:{Port}{CallbackPath}
	ExtraAuthParams      map[string]string // extra authorize query params; the OpenAI `originator` param is NOT part of this map (it is derived from Originator)
	RefreshScopes        string            // refresh-request scope; empty => Scopes (the originally granted set); if both are empty the scope parameter is omitted; OmitRefreshScope takes precedence

	// OmitRefreshScope, when true, omits the scope parameter from refresh_token
	// requests entirely. Per RFC 6749 section 6 the server then applies the scope
	// originally granted. It takes precedence over RefreshScopes and Scopes.
	// Zero value (false) preserves the historical behaviour.
	OmitRefreshScope  bool
	TokenRequestStyle TokenRequestStyle // form (zero value) | json
	AuthHeaderStyle   TokenAuthStyle    // body (zero value) | basic
	HTTPClient        *http.Client      // optional; nil => http.DefaultClient
}

// TokenRequestStyle selects how token-endpoint requests are encoded.
type TokenRequestStyle int

const (
	// TokenRequestFormURLEncoded sends application/x-www-form-urlencoded (default).
	TokenRequestFormURLEncoded TokenRequestStyle = iota
	// TokenRequestJSON sends an application/json body.
	TokenRequestJSON
)

// TokenAuthStyle selects how client credentials are presented to the token endpoint.
type TokenAuthStyle int

const (
	// TokenAuthBody sends client_id/client_secret in the request body (default).
	TokenAuthBody TokenAuthStyle = iota
	// TokenAuthBasic sends HTTP Basic auth and omits client_secret from the body.
	TokenAuthBasic
)

func OpenAIOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Issuer:     "https://auth.openai.com",
		ClientID:   "app_EMoamEEZ73f0CkXaXp7hrann",
		Scopes:     "openid profile email offline_access",
		Originator: "codex_cli_rs",
		Port:       1455,

		Name:                 "openai",
		AuthorizeURL:         "https://auth.openai.com/oauth/authorize",
		DeviceCodeURL:        "https://auth.openai.com/api/accounts/deviceauth/usercode",
		DeviceTokenURL:       "https://auth.openai.com/api/accounts/deviceauth/token",
		DeviceVerifyURL:      "https://auth.openai.com/codex/device",
		LegacyDeviceCodeURL:  "https://auth.openai.com/api/accounts/deviceauth/usercode",
		LegacyDeviceTokenURL: "https://auth.openai.com/api/accounts/deviceauth/token",
		CallbackPath:         "/auth/callback",
		ExtraAuthParams: map[string]string{
			"id_token_add_organizations": "true",
			"codex_cli_simplified_flow":  "true",
		},
		RefreshScopes:     "openid profile email",
		TokenRequestStyle: TokenRequestFormURLEncoded,
		AuthHeaderStyle:   TokenAuthBody,
	}
}

// GoogleAntigravityOAuthConfig returns the OAuth configuration for Google Cloud Code Assist (Antigravity).
// Client credentials are the same ones used by OpenCode/pi-ai for Cloud Code Assist access.
func GoogleAntigravityOAuthConfig() OAuthProviderConfig {
	// These are the same client credentials used by the OpenCode antigravity plugin.
	clientID := decodeBase64(
		"MTA3MTAwNjA2MDU5MS10bWhzc2luMmgyMWxjcmUyMzV2dG9sb2poNGc0MDNlcC5hcHBzLmdvb2dsZXVzZXJjb250ZW50LmNvbQ==",
	)
	clientSecret := decodeBase64("R09DU1BYLUs1OEZXUjQ4NkxkTEoxbUxCOHNYQzR6NnFEQWY=")
	return OAuthProviderConfig{
		Issuer:       "https://accounts.google.com/o/oauth2/v2",
		TokenURL:     "https://oauth2.googleapis.com/token",
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Scopes:       "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs",
		Port:         51121,

		Name:         "google-antigravity",
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		CallbackPath: "/auth/callback",
		ExtraAuthParams: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
		// Google refreshes with no scope parameter: RFC 6749 §6 says the server
		// then applies the scope originally granted, and jcode (the porting
		// reference) omits `scope` on Google refresh too
		// (crates/jcode-base/src/auth/google_oauth.rs). RefreshScopes stays empty;
		// OmitRefreshScope makes the omission explicit instead of falling back to
		// the long Scopes list.
		OmitRefreshScope:  true,
		TokenRequestStyle: TokenRequestFormURLEncoded,
	}
}

func decodeBase64(s string) string {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return s
	}
	return string(data)
}

func generateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// oauthCallbackPath returns the loopback callback path for the browser flow.
// An explicit cfg.CallbackPath wins; otherwise the historical "/auth/callback"
// is used. It is a pure function so the derivation can be unit-tested without
// starting a listener.
func oauthCallbackPath(cfg OAuthProviderConfig) string {
	if cfg.CallbackPath != "" {
		return cfg.CallbackPath
	}
	return "/auth/callback"
}

// oauthRedirectURI returns the redirect_uri sent to the authorize endpoint.
// An explicit cfg.RedirectURI wins; otherwise it is derived from the loopback
// listener (http://localhost:{Port}{CallbackPath}). It is a pure function so
// the derivation can be unit-tested without starting a listener.
func oauthRedirectURI(cfg OAuthProviderConfig) string {
	if cfg.RedirectURI != "" {
		return cfg.RedirectURI
	}
	return fmt.Sprintf("http://localhost:%d%s", cfg.Port, oauthCallbackPath(cfg))
}

func LoginBrowser(cfg OAuthProviderConfig) (*AuthCredential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE: %w", err)
	}

	state, err := generateState()
	if err != nil {
		return nil, fmt.Errorf("generating state: %w", err)
	}

	redirectURI := oauthRedirectURI(cfg)

	authURL := buildAuthorizeURL(cfg, pkce, state, redirectURI)

	resultCh := make(chan callbackResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(oauthCallbackPath(cfg), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			resultCh <- callbackResult{err: fmt.Errorf("state mismatch")}
			http.Error(w, "State mismatch", http.StatusBadRequest)
			return
		}

		code := r.URL.Query().Get("code")
		if code == "" {
			errMsg := r.URL.Query().Get("error")
			resultCh <- callbackResult{err: fmt.Errorf("no code received: %s", errMsg)}
			http.Error(w, "No authorization code received", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html><body><h2>Authentication successful!</h2><p>You can close this window.</p></body></html>")
		resultCh <- callbackResult{code: code}
	})

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("starting callback server on port %d: %w", cfg.Port, err)
	}

	server := &http.Server{Handler: mux}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("OAuth callback server error: %v\n", err)
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			fmt.Printf("OAuth callback server shutdown error: %v\n", err)
		}
	}()

	fmt.Printf("Open this URL to authenticate:\n\n%s\n\n", authURL)

	if err := openBrowser(authURL); err != nil {
		fmt.Printf("Could not open browser automatically.\nPlease open this URL manually:\n\n%s\n\n", authURL)
	}

	fmt.Println("If you're running in a headless environment, use: lele auth login --provider openai --device-code")
	fmt.Println("Waiting for authentication in browser...")

	select {
	case result := <-resultCh:
		if result.err != nil {
			return nil, result.err
		}
		return exchangeCodeForTokens(cfg, result.code, pkce.CodeVerifier, redirectURI)
	case <-time.After(5 * time.Minute):
		return nil, fmt.Errorf("authentication timed out after 5 minutes")
	}
}

type callbackResult struct {
	code string
	err  error
}

type deviceCodeResponse struct {
	DeviceAuthID string
	UserCode     string
	Interval     int
}

func parseDeviceCodeResponse(body []byte) (deviceCodeResponse, error) {
	var raw struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}

	if err := json.Unmarshal(body, &raw); err != nil {
		return deviceCodeResponse{}, err
	}

	interval, err := parseFlexibleInt(raw.Interval)
	if err != nil {
		return deviceCodeResponse{}, err
	}

	return deviceCodeResponse{
		DeviceAuthID: raw.DeviceAuthID,
		UserCode:     raw.UserCode,
		Interval:     interval,
	}, nil
}

func parseFlexibleInt(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}

	var interval int
	if err := json.Unmarshal(raw, &interval); err == nil {
		return interval, nil
	}

	var intervalStr string
	if err := json.Unmarshal(raw, &intervalStr); err == nil {
		intervalStr = strings.TrimSpace(intervalStr)
		if intervalStr == "" {
			return 0, nil
		}
		return strconv.Atoi(intervalStr)
	}

	return 0, fmt.Errorf("invalid integer value: %s", string(raw))
}

// loginDeviceCodeOut is the writer the OpenAI-proprietary device flow prints
// its user instructions to. It is a package-level test seam: production always
// uses os.Stdout, but a test can swap it for a bytes.Buffer (and restore it with
// t.Cleanup) to assert the exact text shown to the user.
var loginDeviceCodeOut io.Writer = os.Stdout

// legacyDeviceEndpoints resolves the three endpoints of the OpenAI-proprietary
// device flow (the one used by `lele auth login --device-code` for the openai
// provider). Every explicit field wins; when it is empty the historical
// derivation from cfg.Issuer is used, so hand-built configs that predate the
// explicit fields keep sending requests to exactly the same URLs as before.
//
// The returned verifyURL is the URL printed for the user to open in a browser.
// The redirect sent on the code exchange that follows a successful poll is
// cfg.Issuer + "/deviceauth/callback" — an OpenAI server-side callback rather
// than a loopback redirect — so it is intentionally NOT part of this
// resolution.
func legacyDeviceEndpoints(cfg OAuthProviderConfig) (deviceCodeURL, deviceTokenURL, verifyURL string) {
	deviceCodeURL = cfg.LegacyDeviceCodeURL
	if deviceCodeURL == "" {
		deviceCodeURL = cfg.Issuer + "/api/accounts/deviceauth/usercode"
	}

	deviceTokenURL = cfg.LegacyDeviceTokenURL
	if deviceTokenURL == "" {
		deviceTokenURL = cfg.Issuer + "/api/accounts/deviceauth/token"
	}

	verifyURL = cfg.DeviceVerifyURL
	if verifyURL == "" {
		verifyURL = cfg.Issuer + "/codex/device"
	}

	return deviceCodeURL, deviceTokenURL, verifyURL
}

func LoginDeviceCode(cfg OAuthProviderConfig) (*AuthCredential, error) {
	deviceCodeURL, _, verifyURL := legacyDeviceEndpoints(cfg)

	reqBody, _ := json.Marshal(map[string]string{
		"client_id": cfg.ClientID,
	})

	// This proprietary API speaks JSON with client_id in the body: it uses
	// neither TokenRequestStyle nor AuthHeaderStyle, so the request is built by
	// hand instead of going through doTokenRequest. The client is still
	// cfg.HTTPClient (via oauthHTTPClient) so the flow is testable and
	// consistent with the RFC 8628 flow.
	req, err := http.NewRequest(http.MethodPost, deviceCodeURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("creating device code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := oauthHTTPClient(cfg).Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed: %s", string(body))
	}

	deviceResp, err := parseDeviceCodeResponse(body)
	if err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}

	if deviceResp.Interval < 1 {
		deviceResp.Interval = 5
	}

	fmt.Fprintf(loginDeviceCodeOut, "\nTo authenticate, open this URL in your browser:\n\n  %s\n\nThen enter this code: %s\n\nWaiting for authentication...\n",
		verifyURL, deviceResp.UserCode)

	deadline := time.After(15 * time.Minute)
	ticker := time.NewTicker(time.Duration(deviceResp.Interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("device code authentication timed out after 15 minutes")
		case <-ticker.C:
			cred, err := pollDeviceCode(cfg, deviceResp.DeviceAuthID, deviceResp.UserCode)
			if err != nil {
				continue
			}
			if cred != nil {
				return cred, nil
			}
		}
	}
}

func pollDeviceCode(cfg OAuthProviderConfig, deviceAuthID, userCode string) (*AuthCredential, error) {
	_, deviceTokenURL, _ := legacyDeviceEndpoints(cfg)

	reqBody, _ := json.Marshal(map[string]string{
		"device_auth_id": deviceAuthID,
		"user_code":      userCode,
	})

	req, err := http.NewRequest(http.MethodPost, deviceTokenURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("creating device token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := oauthHTTPClient(cfg).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pending")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	var tokenResp struct {
		AuthorizationCode string `json:"authorization_code"`
		CodeChallenge     string `json:"code_challenge"`
		CodeVerifier      string `json:"code_verifier"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, err
	}

	redirectURI := cfg.Issuer + "/deviceauth/callback"
	return exchangeCodeForTokens(cfg, tokenResp.AuthorizationCode, tokenResp.CodeVerifier, redirectURI)
}

// refreshScope returns the scope string sent on a refresh_token request, or ""
// to omit the scope parameter entirely. cfg.OmitRefreshScope wins and means
// "omit the parameter" (RFC 6749 §6: the server then applies the scope
// originally granted). Otherwise an explicit cfg.RefreshScopes is used, falling
// back to cfg.Scopes (the scopes the credential was originally granted with).
// An empty result means "omit the scope parameter".
func refreshScope(cfg OAuthProviderConfig) string {
	if cfg.OmitRefreshScope {
		return ""
	}
	if cfg.RefreshScopes != "" {
		return cfg.RefreshScopes
	}
	return cfg.Scopes
}

func RefreshAccessToken(cred *AuthCredential, cfg OAuthProviderConfig) (*AuthCredential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	// The scope is resolved from the config instead of being hardcoded: providers
	// such as Google reject a refresh whose scope differs from the one the
	// credential was originally granted with (invalid_scope).
	data := url.Values{
		"client_id":     {cfg.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}
	if scope := refreshScope(cfg); scope != "" {
		data.Set("scope", scope)
	}

	tokenURL := cfg.Issuer + "/oauth/token"
	if cfg.TokenURL != "" {
		tokenURL = cfg.TokenURL
	}

	resp, err := doTokenRequest(cfg, tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("refreshing token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed: %s", string(body))
	}

	refreshed, err := parseTokenResponse(body, cred.Provider)
	if err != nil {
		return nil, err
	}
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = cred.RefreshToken
	}
	if refreshed.AccountID == "" {
		refreshed.AccountID = cred.AccountID
	}
	if cred.Email != "" && refreshed.Email == "" {
		refreshed.Email = cred.Email
	}
	if cred.ProjectID != "" && refreshed.ProjectID == "" {
		refreshed.ProjectID = cred.ProjectID
	}
	return refreshed, nil
}

func BuildAuthorizeURL(cfg OAuthProviderConfig, pkce PKCECodes, state, redirectURI string) string {
	return buildAuthorizeURL(cfg, pkce, state, redirectURI)
}

func buildAuthorizeURL(cfg OAuthProviderConfig, pkce PKCECodes, state, redirectURI string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {cfg.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {cfg.Scopes},
		"code_challenge":        {pkce.CodeChallenge},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}

	// Provider-specific authorize parameters are declared explicitly by the
	// config instead of being sniffed from the issuer. The OpenAI `originator`
	// param is intentionally not part of ExtraAuthParams: it stays derived from
	// the dedicated Originator field.
	for k, v := range cfg.ExtraAuthParams {
		params.Set(k, v)
	}
	if cfg.Originator != "" {
		params.Set("originator", cfg.Originator)
	}

	// Prefer the explicit authorize endpoint; fall back to deriving it from the
	// issuer for legacy, hand-built configs that leave AuthorizeURL unset
	// (Google uses /auth, OpenAI uses /oauth/authorize).
	endpoint := cfg.AuthorizeURL
	if endpoint == "" {
		if strings.Contains(strings.ToLower(cfg.Issuer), "accounts.google.com") {
			endpoint = cfg.Issuer + "/auth"
		} else {
			endpoint = cfg.Issuer + "/oauth/authorize"
		}
	}
	return endpoint + "?" + params.Encode()
}

// oauthHTTPClient returns the HTTP client used for token-endpoint calls:
// cfg.HTTPClient when set, otherwise http.DefaultClient.
func oauthHTTPClient(cfg OAuthProviderConfig) *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	return http.DefaultClient
}

// tokenRequest encodes a token-endpoint POST body for the configured styles.
// It does NOT mutate the params passed in. When cfg.AuthHeaderStyle is
// TokenAuthBasic, client_secret is removed from the body (it travels in the
// Authorization header instead) and the returned header value is
// "Basic " + base64(client_id + ":" + client_secret); otherwise the returned
// header value is "".
//
// TokenAuthBasic is applied even when cfg.ClientSecret is empty: the header is
// still "Basic " + base64(client_id + ":") (a Basic credential with an empty
// password) and client_secret is still stripped from the body. There is no
// silent fall-back to body auth; that would change the wire format behind the
// caller's back.
//
// Body encoding follows cfg.TokenRequestStyle:
//   - TokenRequestFormURLEncoded (zero value): params.Encode(), Content-Type
//     application/x-www-form-urlencoded
//   - TokenRequestJSON: JSON object with one string per key (the FIRST value of
//     each url.Values entry), Content-Type application/json
func tokenRequest(cfg OAuthProviderConfig, params url.Values) (body io.Reader, contentType string, authorization string) {
	// url.Values is a map: clone it before touching it so the caller's params
	// (reused after this call, e.g. by tests or retries) stay intact.
	cloned := make(url.Values, len(params))
	for k, vs := range params {
		cloned[k] = append([]string(nil), vs...)
	}

	if cfg.AuthHeaderStyle == TokenAuthBasic {
		// Basic credentials ride in the Authorization header; drop client_secret
		// from the body so it is not sent twice.
		cloned.Del("client_secret")
		credentials := base64.StdEncoding.EncodeToString([]byte(cfg.ClientID + ":" + cfg.ClientSecret))
		authorization = "Basic " + credentials
	}

	switch cfg.TokenRequestStyle {
	case TokenRequestJSON:
		// The JSON encoding is a flat object, so a key with several values keeps
		// only its first one.
		fields := make(map[string]string, len(cloned))
		for k, vs := range cloned {
			if len(vs) == 0 {
				continue
			}
			fields[k] = vs[0]
		}
		encoded, _ := json.Marshal(fields) // map[string]string cannot fail to marshal
		return bytes.NewReader(encoded), "application/json", authorization
	default: // TokenRequestFormURLEncoded
		// strings.NewReader lets http.NewRequest set ContentLength, so the request
		// is byte-for-byte the one http.PostForm used to send.
		return strings.NewReader(cloned.Encode()), "application/x-www-form-urlencoded", authorization
	}
}

// doTokenRequest POSTs params to endpoint using the configured request style,
// auth header style and HTTP client.
func doTokenRequest(cfg OAuthProviderConfig, endpoint string, params url.Values) (*http.Response, error) {
	body, contentType, authorization := tokenRequest(cfg, params)
	req, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	return oauthHTTPClient(cfg).Do(req)
}

func exchangeCodeForTokens(cfg OAuthProviderConfig, code, codeVerifier, redirectURI string) (*AuthCredential, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {codeVerifier},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	tokenURL := cfg.Issuer + "/oauth/token"
	if cfg.TokenURL != "" {
		tokenURL = cfg.TokenURL
	}

	// Determine provider name from config
	provider := cfg.Name
	if provider == "" {
		provider = "openai" // legacy default for hand-built configs
	}

	resp, err := doTokenRequest(cfg, tokenURL, data)
	if err != nil {
		return nil, fmt.Errorf("exchanging code for tokens: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	return parseTokenResponse(body, provider)
}

func parseTokenResponse(body []byte, provider string) (*AuthCredential, error) {
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		IDToken      string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response")
	}

	var expiresAt time.Time
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	cred := &AuthCredential{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    expiresAt,
		Provider:     provider,
		AuthMethod:   "oauth",
	}

	if accountID := extractAccountID(tokenResp.IDToken); accountID != "" {
		cred.AccountID = accountID
	} else if accountID := extractAccountID(tokenResp.AccessToken); accountID != "" {
		cred.AccountID = accountID
	}

	return cred, nil
}

func extractAccountID(token string) string {
	claims, err := parseJWTClaims(token)
	if err != nil {
		return ""
	}

	if accountID, ok := claims["chatgpt_account_id"].(string); ok && accountID != "" {
		return accountID
	}

	if accountID, ok := claims["https://api.openai.com/auth.chatgpt_account_id"].(string); ok && accountID != "" {
		return accountID
	}

	if authClaim, ok := claims["https://api.openai.com/auth"].(map[string]interface{}); ok {
		if accountID, ok := authClaim["chatgpt_account_id"].(string); ok && accountID != "" {
			return accountID
		}
	}

	if orgs, ok := claims["organizations"].([]interface{}); ok {
		for _, org := range orgs {
			if orgMap, ok := org.(map[string]interface{}); ok {
				if accountID, ok := orgMap["id"].(string); ok && accountID != "" {
					return accountID
				}
			}
		}
	}

	return ""
}

func parseJWTClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("token is not a JWT")
	}

	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64URLDecode(payload)
	if err != nil {
		return nil, err
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(decoded, &claims); err != nil {
		return nil, err
	}

	return claims, nil
}

func base64URLDecode(s string) ([]byte, error) {
	s = strings.NewReplacer("-", "+", "_", "/").Replace(s)
	return base64.StdEncoding.DecodeString(s)
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "linux":
		return exec.Command("xdg-open", url).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", url).Start()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
