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
	OmitRefreshScope bool

	// ManualRedirectURI is the hosted redirect_uri for the headless paste-code
	// flow (LoginPasteCode) and the alternate URL advertised when the loopback
	// listener cannot be used. Empty => the provider has no paste-code flow.
	// It never affects LoginBrowser's loopback redirect. The paste flow always
	// uses this value for both the authorize URL and the exchange, so a legacy
	// console redirect pasted by the user (correction V6) is normalized on input
	// only and is never transmitted.
	ManualRedirectURI string
	// StateIsVerifier makes LoginBrowser use the PKCE code_verifier as the OAuth
	// `state` (Claude/jcode behavior) and echo it on the token exchange.
	// Zero value keeps the historical random-state CSRF check.
	StateIsVerifier bool
	// RequireInferenceScope enables the jcode Claude gate: a token response
	// whose non-empty scope list has no inference scope is rejected.
	RequireInferenceScope bool
	// RequireRefreshToken, when true, makes a successful token exchange require a
	// non-empty refresh token AND a non-zero expiry (spec correction V7). A
	// credential with no refresh token is unusable for a provider whose access
	// tokens expire: the login would "succeed" and store a credential that can
	// never be refreshed, and the zero expiry would additionally make
	// NeedsRefresh() report true forever. The exchange fails instead, with an
	// actionable error.
	// Opt-in: only the Claude preset sets it (openai/google-* keep their behavior).
	RequireRefreshToken bool
	// DisplayName is the provider name used in user-facing gate messages (e.g.
	// "Claude", "Gemini"). Its lower-cased form is also used as the --provider
	// value in the recovery commands those messages suggest, so it must be a
	// spelling the registry accepts ("Claude" -> claude, "Gemini" -> gemini);
	// use Name for any other labelling. Empty keeps the historical wording, so a
	// preset that predates the field is unaffected: "Claude" in the gate
	// messages, "Anthropic" in the Cloudflare pre-flight prose (see
	// displayNameOr).
	DisplayName string

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

// oauthRedirectURI returns the listener-free derivation of the redirect_uri:
// an explicit cfg.RedirectURI wins; otherwise it is derived from the CONFIGURED
// port (http://localhost:{Port}{CallbackPath}). It stays a pure function so the
// derivation can be unit-tested without starting a listener.
//
// LoginBrowser does NOT use it anymore to build the authorize URL: it binds the
// loopback listener first and derives the redirect from the port the OS actually
// assigned (redirectURIForListener), which matters when cfg.Port is 0. For a
// fixed port both derivations return the same string.
func oauthRedirectURI(cfg OAuthProviderConfig) string {
	if cfg.RedirectURI != "" {
		return cfg.RedirectURI
	}
	return fmt.Sprintf("http://localhost:%d%s", cfg.Port, oauthCallbackPath(cfg))
}

// bindCallbackListener binds the loopback listener for LoginBrowser. The bind
// address is always 127.0.0.1 (jcode binds loopback too) and the advertised
// redirect host stays "localhost"; cfg.Port == 0 asks the OS for an ephemeral
// port (jcode bind_callback_listener(0)).
func bindCallbackListener(cfg OAuthProviderConfig) (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
}

// redirectURIForListener derives the authorize redirect from the listener that
// was actually bound, so an ephemeral (cfg.Port == 0) flow advertises the real
// port. An explicit cfg.RedirectURI still wins verbatim, and for a fixed port
// the result is identical to oauthRedirectURI.
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

// LoginBrowser runs the interactive browser (loopback redirect) OAuth flow for a
// provider that can bind a local callback listener. It never degrades to the
// paste-code flow: use LoginBrowserWithPasteFallback for providers that can.
func LoginBrowser(cfg OAuthProviderConfig) (*AuthCredential, error) {
	return loginBrowser(cfg, false, nil)
}

// LoginBrowserWithPasteFallback is LoginBrowser with a graceful degradation for
// headless environments: when the provider advertises a hosted
// ManualRedirectURI (Claude) and the loopback listener cannot be bound or the
// callback never arrives within five minutes, it falls back to the manual
// paste-code flow (LoginPasteCode) instead of failing.
//
// Providers without a ManualRedirectURI have no paste-code flow, so they are
// handled by LoginBrowser unchanged.
func LoginBrowserWithPasteFallback(cfg OAuthProviderConfig, r io.Reader) (*AuthCredential, error) {
	if cfg.ManualRedirectURI == "" {
		return LoginBrowser(cfg)
	}
	return loginBrowser(cfg, true, r)
}

// printHeadlessHint prints the provider-specific "run this instead" line shown
// by loginBrowser, i.e. the headless fallback available to that provider.
//
// The selection is by cfg.Name ON PURPOSE (D9). The previous implementation
// keyed off `cfg.ManualRedirectURI != ""`, which meant any future preset with a
// hosted redirect would advertise `--provider claude`: the hint named the wrong
// provider and pointed at a CLI flow that may not exist for it.
//
// The three cases below (plus the silent default) are the complete set of
// providers that have a headless alternative: openai has the device-code flow,
// anthropic and gemini have a hosted paste redirect (--paste-code). Everything
// else — google-antigravity in particular — must stay silent, so the misleading
// openai hint it used to print is gone. The openai and anthropic lines are
// byte-identical to the historical output; a test pins all four cases.
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

// loginBrowser is the shared implementation behind LoginBrowser and
// LoginBrowserWithPasteFallback. fallbackToPaste enables the manual paste-code
// degradation; r is only read in that path (LoginBrowser passes nil).
func loginBrowser(cfg OAuthProviderConfig, fallbackToPaste bool, r io.Reader) (*AuthCredential, error) {
	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE: %w", err)
	}

	// Claude/jcode binds the OAuth `state` to the PKCE verifier (cfg.StateIsVerifier);
	// every other provider keeps the historical random-state CSRF check.
	var state string
	if cfg.StateIsVerifier {
		state = pkce.CodeVerifier
	} else {
		state, err = generateState()
		if err != nil {
			return nil, fmt.Errorf("generating state: %w", err)
		}
	}

	// Bind before building the authorize URL so the redirect_uri carries the real
	// bound port (cfg.Port may be 0, i.e. an OS-assigned ephemeral port).
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
		// Only Claude echoes the state on the exchange; for every other provider
		// the field is omitted from the wire (historical OpenAI behavior).
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

// refreshErrorIsInvalidScope reports whether a failed refresh response body
// carries one of the invalid_scope markers jcode matches
// (claude_refresh_error_is_invalid_scope). The body is matched
// case-insensitively because the casing of the OAuth error document is not
// stable across providers/proxies.
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
	scopeSent := refreshScope(cfg)
	if scopeSent != "" {
		data.Set("scope", scopeSent)
	}

	tokenURL := cfg.Issuer + "/oauth/token"
	if cfg.TokenURL != "" {
		tokenURL = cfg.TokenURL
	}

	// performRefresh sends the refresh POST once and reads its response body.
	// It is invoked at most twice: the retry reuses it with the `scope` field
	// deleted. doTokenRequest/tokenRequest never mutate the params they are
	// given, so `data` can be reused for both attempts.
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
		// Retry exactly once without `scope` when a scope was actually sent and
		// the server rejected it as invalid_scope: the credential may have been
		// granted a narrower scope than the config asks for, and RFC 6749 §6
		// lets the request omit `scope` so the server applies the originally
		// granted one. Any other failure (or a refresh that sent no scope) is
		// surfaced unchanged.
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
			// Both attempts failed: keep BOTH diagnostics (jcode parity —
			// "fallback without scope failed: …; scoped refresh error: …").
			// Dropping the first body hides why the scope was rejected in the
			// first place, which is the diagnostic a user actually needs.
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
	// The refreshed credential is returned TOGETHER with the gate failure: the
	// token endpoint may have rotated the refresh token, and the old one is
	// already consumed. The refresh coordinator uses errors.As to recover the
	// credential and persist the rotated refresh token before surfacing the
	// error, which is what prevents the account from being bricked (jcode's
	// ordering bug, not copied here). The error text is identical to the one
	// ensureClaudeInferenceScope produces.
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

// exchangeParams are the values the token exchange echoes back to the authorize
// request. State == "" omits the `state` field (OpenAI/Antigravity behavior);
// Claude passes the PKCE verifier (jcode sends `state` on the exchange).
type exchangeParams struct {
	Code         string
	CodeVerifier string
	RedirectURI  string
	State        string
}

// exchangeCodeForTokens is the historical 4-argument entry point. It never sends
// a `state` on the wire, which keeps the OpenAI/Google exchange byte-identical
// to the pre-Claude implementation.
func exchangeCodeForTokens(cfg OAuthProviderConfig, code, codeVerifier, redirectURI string) (*AuthCredential, error) {
	return exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         code,
		CodeVerifier: codeVerifier,
		RedirectURI:  redirectURI,
	})
}

// displayNameOr returns DisplayName, or fallback when it is unset. It is the
// single place implementing the "unset keeps the historical wording" rule:
// DisplayName is only absent on a hand-built config that predates the field (or
// on a preset that does not opt in), and such a config must keep producing the
// exact sentence it produced before the field existed. Each call site passes
// its own historical word, because they are not all the same: the gate messages
// say "Claude", while the pre-flight error names the "Anthropic" product.
func (c OAuthProviderConfig) displayNameOr(fallback string) string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return fallback
}

// displayName returns the provider label used in user-facing messages, falling
// back to the historical "Claude" wording when unset. The CLI flag is its
// lower-case form, which matches each preset's accepted --provider value
// ("Claude" -> claude, "Gemini" -> gemini).
func (c OAuthProviderConfig) displayName() string { return c.displayNameOr("Claude") }

// requireRefreshTokenAndExpiry enforces the complete-credential contract of a
// successful token exchange for providers whose access tokens expire (spec
// correction V7). It returns nil for a credential that carries both a refresh
// token and an expiry, and an actionable error otherwise. Both failures name
// the missing piece and the command that recovers from it.
//
// label labels the provider in those messages and is expected to be non-empty:
// callers pass OAuthProviderConfig.displayName(), which owns the historical
// "Claude" default, so that wording lives in exactly one place.
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
	// A Cloudflare interstitial answers 403 with an HTML challenge instead of the
	// OAuth error document; surface the actionable fix instead of dumping HTML.
	if resp.StatusCode == http.StatusForbidden && looksLikeCloudflareChallenge(string(body)) {
		// This pre-flight is shared by the Claude and Gemini Code Assist flows, so
		// both the prose and the recovery command follow the provider label; they
		// must stay in sync with cfg.DisplayName. The historical sentence mixes
		// the two spellings ("Anthropic" in the prose, --provider claude in the
		// command), so each keeps its own fallback: an unset DisplayName still
		// yields that exact sentence, byte-for-byte.
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
	// Spec correction V7: inside the Claude flow a refresh token and a non-zero
	// expiry are mandatory, because a credential without them is unusable (it can
	// never be refreshed, and a zero expiry keeps NeedsRefresh() true forever).
	// Nothing has been issued to persist here, so the exchange simply fails.
	// Opt-in via the config flag: openai and google-antigravity never set it.
	if cfg.RequireRefreshToken {
		if err := requireRefreshTokenAndExpiry(cred, cfg.displayName()); err != nil {
			return nil, err
		}
	}
	// A Claude token that grants no inference scope is useless for the Messages
	// API, so fail the login instead of storing a credential that can only 401.
	// Nothing has been issued to persist here (the flow is interactive), hence
	// the plain message error. Opt-in via the config flag: openai and
	// google-antigravity never set it.
	if cfg.RequireInferenceScope {
		if err := ensureClaudeInferenceScope(cred.Scopes, "token exchange"); err != nil {
			return nil, err
		}
	}
	return cred, nil
}

// looksLikeCloudflareChallenge reports whether body is a Cloudflare blocking
// interstitial (jcode's looks_like_cloudflare_challenge port). The markers are
// matched case-insensitively because the HTML casing is not stable.
func looksLikeCloudflareChallenge(body string) bool {
	lower := strings.ToLower(body)
	for _, marker := range []string{
		// Both spellings are matched: jcode's reference detector matches
		// "cf-challenge", while the real Anthropic interstitial reports
		// "cf-hit-challenge" (which does NOT contain "cf-challenge").
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

// parseOAuthScopes splits a space-delimited OAuth `scope` value into its
// individual scope strings, mirroring RFC 6749 section 3.3. It returns nil
// when the input carries no scopes so that the `omitempty` JSON tag drops the
// field entirely.
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

// browserCommand builds the platform's "open this URL" command. It is the ONE
// platform switch of this package: openBrowser starts it and forgets,
// OpenBrowserConfirming starts it and watches it exit.
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

// DefaultBrowserConfirmWait is the budget OpenBrowserConfirming spends watching
// the launcher before it assumes the hand-off worked. It is short on purpose:
// a working launcher exits in well under a second, and a caller that renders UI
// (the TUI) pays this delay before it can tell the user anything.
const DefaultBrowserConfirmWait = 750 * time.Millisecond

// OpenBrowser launches url in the user's default browser. It is exported so
// callers that must NOT import this package's OAuth machinery (pkg/mcp injects
// its browser opener as a function) can reuse the one platform switch.
func OpenBrowser(url string) error { return openBrowser(url) }

// OpenBrowserConfirming launches url in the user's default browser and reports
// whether the LAUNCHER worked, not merely whether it could be started: it waits
// up to wait for the launcher to exit, so `xdg-open` that starts fine and then
// dies with "no method available" (a headless host, a container, a session with
// no DISPLAY) is reported as a failure instead of being mistaken for a browser
// that opened.
//
// Three outcomes:
//
//   - the launcher could not be started: its error;
//   - it exited non-zero within wait: a value-free error naming the status (an
//     exit error's text includes the command line, i.e. the URL, so it is
//     deliberately not wrapped verbatim);
//   - still running when wait elapses: nil. A launcher that hands the URL to a
//     browser either exits or stays as that browser's parent; in both cases the
//     hand-off worked. The process is reaped by a goroutine that ends with it.
func OpenBrowserConfirming(url string, wait time.Duration) error {
	cmd, err := browserCommand(url)
	if err != nil {
		return err
	}
	return startAndConfirm(cmd, wait)
}

// startAndConfirm implements OpenBrowserConfirming for an already-built command
// (that split is what makes the behaviour testable without a platform switch or
// a real browser).
func startAndConfirm(cmd *exec.Cmd, wait time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	// The goroutine reaps the launcher whenever it exits, including the
	// "still running when wait elapsed" case — otherwise a long-lived parent
	// would stay a zombie for the life of the process.
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

// launcherExitError converts a launcher's exit into a value-free error: the
// exit status only, never the command line (which carries the URL) and never
// the launcher's stderr.
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
