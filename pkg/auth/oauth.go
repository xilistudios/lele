package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
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

type OAuthProviderConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	TokenURL     string
	Scopes       string
	Originator   string
	Port         int

	Name                 string
	AuthorizeURL         string
	DeviceCodeURL        string
	DeviceTokenURL       string
	LegacyDeviceCodeURL  string
	LegacyDeviceTokenURL string
	DeviceVerifyURL      string
	CallbackPath         string
	RedirectURI          string
	ExtraAuthParams      map[string]string
	RefreshScopes        string

	OmitRefreshScope bool

	ManualRedirectURI     string
	StateIsVerifier       bool
	RequireInferenceScope bool
	RequireRefreshToken   bool
	DisplayName           string

	TokenRequestStyle TokenRequestStyle
	AuthHeaderStyle   TokenAuthStyle
	HTTPClient        *http.Client
}

type TokenRequestStyle int

const (
	TokenRequestFormURLEncoded TokenRequestStyle = iota
	TokenRequestJSON
)

type TokenAuthStyle int

const (
	TokenAuthBody TokenAuthStyle = iota
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

func GoogleAntigravityOAuthConfig() OAuthProviderConfig {
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

func oauthCallbackPath(cfg OAuthProviderConfig) string {
	if cfg.CallbackPath != "" {
		return cfg.CallbackPath
	}
	return "/auth/callback"
}

func oauthRedirectURI(cfg OAuthProviderConfig) string {
	if cfg.RedirectURI != "" {
		return cfg.RedirectURI
	}
	return fmt.Sprintf("http://localhost:%d%s", cfg.Port, oauthCallbackPath(cfg))
}

func bindCallbackListener(cfg OAuthProviderConfig) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
}

func redirectURIForListener(cfg OAuthProviderConfig, ln net.Listener) string {
	if cfg.RedirectURI != "" {
		return cfg.RedirectURI
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return oauthRedirectURI(cfg)
	}
	return fmt.Sprintf("http://localhost:%d%s", tcpAddr.Port, oauthCallbackPath(cfg))
}

func LoginBrowser(cfg OAuthProviderConfig) (*AuthCredential, error) {
	return loginBrowser(cfg, false, nil)
}

func LoginBrowserWithPasteFallback(cfg OAuthProviderConfig, r io.Reader) (*AuthCredential, error) {
	if cfg.ManualRedirectURI == "" {
		return LoginBrowser(cfg)
	}
	return loginBrowser(cfg, true, r)
}

func printHeadlessHint(cfg OAuthProviderConfig) {
	switch cfg.Name {
	case "openai":
		fmt.Println("If you're running in a headless environment, use: lele auth login --provider openai --device-code")
	case "anthropic":
		fmt.Println("If you're running in a headless environment, use: lele auth login --provider claude --paste-code")
	case "gemini":
		fmt.Println("If you're running in a headless environment, use: lele auth login --provider gemini --paste-code")
	}
}

func loginBrowser(cfg OAuthProviderConfig, fallbackToPaste bool, r io.Reader) (*AuthCredential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE: %w", err)
	}

	var state string
	if cfg.StateIsVerifier {
		state = pkce.CodeVerifier
	} else {
		state, err = generateState()
		if err != nil {
			return nil, fmt.Errorf("generating state: %w", err)
		}
	}

	listener, err := bindCallbackListener(cfg)
	if err != nil {
		bindErr := fmt.Errorf("starting callback server on port %d: %w", cfg.Port, err)
		if fallbackToPaste {
			fmt.Printf("Automatic browser login failed (%v). Falling back to manual code paste.\n", bindErr)
			return LoginPasteCode(cfg, r)
		}
		return nil, bindErr
	}

	redirectURI := redirectURIForListener(cfg, listener)

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

	printHeadlessHint(cfg)
	fmt.Println("Waiting for authentication in browser...")

	select {
	case result := <-resultCh:
		if result.err != nil {
			return nil, result.err
		}
		ex := exchangeParams{
			Code:         result.code,
			CodeVerifier: pkce.CodeVerifier,
			RedirectURI:  redirectURI,
		}
		if cfg.StateIsVerifier {
			ex.State = state
		}
		return exchangeCodeForTokensWithParams(cfg, ex)
	case <-time.After(5 * time.Minute):
		timeoutErr := fmt.Errorf("authentication timed out after 5 minutes")
		if fallbackToPaste {
			fmt.Printf("Automatic browser login failed (%v). Falling back to manual code paste.\n", timeoutErr)
			return LoginPasteCode(cfg, r)
		}
		return nil, timeoutErr
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

var loginDeviceCodeOut io.Writer = os.Stdout

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

func refreshScope(cfg OAuthProviderConfig) string {
	if cfg.OmitRefreshScope {
		return ""
	}
	if cfg.RefreshScopes != "" {
		return cfg.RefreshScopes
	}
	return cfg.Scopes
}

func refreshErrorIsInvalidScope(body []byte) bool {
	lower := strings.ToLower(string(body))
	for _, marker := range []string{
		"invalid_scope",
		"requested scope is invalid",
		"scope is invalid",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func RefreshAccessToken(cred *AuthCredential, cfg OAuthProviderConfig) (*AuthCredential, error) {
	if cred.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token available")
	}

	data := url.Values{
		"client_id":     {cfg.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {cred.RefreshToken},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}
	scopeSent := refreshScope(cfg)
	if scopeSent != "" {
		data.Set("scope", scopeSent)
	}

	tokenURL := cfg.Issuer + "/oauth/token"
	if cfg.TokenURL != "" {
		tokenURL = cfg.TokenURL
	}

	performRefresh := func(params url.Values) (int, []byte, error) {
		resp, err := doTokenRequest(cfg, tokenURL, params)
		if err != nil {
			return 0, nil, fmt.Errorf("refreshing token: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return 0, nil, fmt.Errorf("reading response body: %w", err)
		}
		return resp.StatusCode, body, nil
	}

	status, body, err := performRefresh(data)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		if scopeSent == "" || !refreshErrorIsInvalidScope(body) {
			return nil, fmt.Errorf("token refresh failed: %s", string(body))
		}

		retryData := make(url.Values, len(data))
		for k, vs := range data {
			retryData[k] = vs
		}
		retryData.Del("scope")

		retryStatus, retryBody, err := performRefresh(retryData)
		if err != nil {
			return nil, err
		}
		if retryStatus != http.StatusOK {
			return nil, fmt.Errorf("token refresh failed: fallback without scope failed: %s; scoped refresh error: %s", string(retryBody), string(body))
		}
		body = retryBody
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
	if cfg.RequireInferenceScope {
		if err := ensureClaudeInferenceScopeForCredential(refreshed, "token refresh"); err != nil {
			return refreshed, err
		}
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

	for k, v := range cfg.ExtraAuthParams {
		params.Set(k, v)
	}
	if cfg.Originator != "" {
		params.Set("originator", cfg.Originator)
	}

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

func oauthHTTPClient(cfg OAuthProviderConfig) *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	return http.DefaultClient
}

func tokenRequest(cfg OAuthProviderConfig, params url.Values) (body io.Reader, contentType string, authorization string) {
	cloned := make(url.Values, len(params))
	for k, vs := range params {
		cloned[k] = append([]string(nil), vs...)
	}

	if cfg.AuthHeaderStyle == TokenAuthBasic {
		cloned.Del("client_secret")
		credentials := base64.StdEncoding.EncodeToString([]byte(cfg.ClientID + ":" + cfg.ClientSecret))
		authorization = "Basic " + credentials
	}

	switch cfg.TokenRequestStyle {
	case TokenRequestJSON:
		fields := make(map[string]string, len(cloned))
		for k, vs := range cloned {
			if len(vs) == 0 {
				continue
			}
			fields[k] = vs[0]
		}
		encoded, _ := json.Marshal(fields)
		return bytes.NewReader(encoded), "application/json", authorization
	default:
		return strings.NewReader(cloned.Encode()), "application/x-www-form-urlencoded", authorization
	}
}

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

type exchangeParams struct {
	Code         string
	CodeVerifier string
	RedirectURI  string
	State        string
}

func exchangeCodeForTokens(cfg OAuthProviderConfig, code, codeVerifier, redirectURI string) (*AuthCredential, error) {
	return exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         code,
		CodeVerifier: codeVerifier,
		RedirectURI:  redirectURI,
	})
}

func (c OAuthProviderConfig) displayNameOr(fallback string) string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return fallback
}

func (c OAuthProviderConfig) displayName() string { return c.displayNameOr("Claude") }

func requireRefreshTokenAndExpiry(cred *AuthCredential, label string) error {
	if cred.RefreshToken == "" {
		return fmt.Errorf("%s OAuth token exchange returned no refresh token; the credential could not be refreshed. Re-run: lele auth login --provider %s", label, strings.ToLower(label))
	}
	if cred.ExpiresAt.IsZero() {
		return fmt.Errorf("%s OAuth token exchange returned no expires_in; a credential without an expiry would refresh on every use. Re-run: lele auth login --provider %s", label, strings.ToLower(label))
	}
	return nil
}

func exchangeCodeForTokensWithParams(cfg OAuthProviderConfig, p exchangeParams) (*AuthCredential, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {p.Code},
		"redirect_uri":  {p.RedirectURI},
		"client_id":     {cfg.ClientID},
		"code_verifier": {p.CodeVerifier},
	}
	if p.State != "" {
		data.Set("state", p.State)
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	tokenURL := cfg.Issuer + "/oauth/token"
	if cfg.TokenURL != "" {
		tokenURL = cfg.TokenURL
	}

	provider := cfg.Name
	if provider == "" {
		provider = "openai"
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
	if resp.StatusCode == http.StatusForbidden && looksLikeCloudflareChallenge(string(body)) {
		name := cfg.displayNameOr("Anthropic")
		return nil, fmt.Errorf("token exchange was blocked by Cloudflare before %s returned OAuth tokens. Switch VPN exit IP or network, then retry with `lele auth login --provider %s --paste-code` and paste the callback URL", name, strings.ToLower(cfg.displayName()))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed: %s", string(body))
	}

	cred, err := parseTokenResponse(body, provider)
	if err != nil {
		return nil, err
	}
	if cfg.RequireRefreshToken {
		if err := requireRefreshTokenAndExpiry(cred, cfg.displayName()); err != nil {
			return nil, err
		}
	}
	if cfg.RequireInferenceScope {
		if err := ensureClaudeInferenceScope(cred.Scopes, "token exchange"); err != nil {
			return nil, err
		}
	}
	return cred, nil
}

func looksLikeCloudflareChallenge(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{
		"cf-hit-challenge",
		"cf-challenge",
		"cloudflare",
		"just a moment",
		"/cdn-cgi/challenge-platform",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func parseTokenResponse(body []byte, provider string) (*AuthCredential, error) {
	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		IDToken      string `json:"id_token"`
		Scope        string `json:"scope"`
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
	cred.Scopes = parseOAuthScopes(tokenResp.Scope)

	if accountID := extractAccountID(tokenResp.IDToken); accountID != "" {
		cred.AccountID = accountID
	} else if accountID := extractAccountID(tokenResp.AccessToken); accountID != "" {
		cred.AccountID = accountID
	}

	return cred, nil
}

func parseOAuthScopes(s string) []string {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil
	}
	return fields
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

func browserCommand(url string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url), nil
	case "linux":
		return exec.Command("xdg-open", url), nil
	case "windows":
		return exec.Command("cmd", "/c", "start", url), nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func openBrowser(url string) error {
	cmd, err := browserCommand(url)
	if err != nil {
		return err
	}
	return cmd.Start()
}

const DefaultBrowserConfirmWait = 750 * time.Millisecond

func OpenBrowser(url string) error { return openBrowser(url) }

func OpenBrowserConfirming(url string, wait time.Duration) error {
	cmd, err := browserCommand(url)
	if err != nil {
		return err
	}
	return startAndConfirm(cmd, wait)
}

func startAndConfirm(cmd *exec.Cmd, wait time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-done:
		return launcherExitError(err)
	case <-timer.C:
		return nil
	}
}

func launcherExitError(err error) error {
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return fmt.Errorf("browser launcher exited with status %d", exit.ExitCode())
	}
	return errors.New("browser launcher failed")
}
