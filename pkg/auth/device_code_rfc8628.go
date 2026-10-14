package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// This file adds a *generic* OAuth 2.0 Device Authorization Grant (RFC 8628)
// on top of the shared OAuth plumbing in oauth.go (oauthHTTPClient,
// doTokenRequest, parseTokenResponse, parseFlexibleInt). It is the path used by
// providers that implement the standard (GitHub Copilot, xAI Grok, any OIDC
// device flow) and it is what the `google-antigravity`-style presets fail fast
// on because they have no DeviceCodeURL/DeviceTokenURL configured.
//
// Scope note: the OpenAI-proprietary flow lives in oauth.go as
// LoginDeviceCode/pollDeviceCode and talks to
// /api/accounts/deviceauth/{usercode,token} with its own JSON bodies. Task T12
// moved it onto oauthHTTPClient(cfg) and the explicit cfg.LegacyDeviceCodeURL /
// cfg.LegacyDeviceTokenURL / cfg.DeviceVerifyURL fields (falling back to the
// historical Issuer-derived URLs), so no package-level http.Post is left in
// pkg/auth. It still speaks the proprietary JSON protocol and therefore does NOT
// use doTokenRequest/TokenRequestStyle: that is by design, not debt.
//
// The device_code is a bearer-ish secret: whoever knows it can redeem it for
// tokens before the user approves. It is therefore never written to opts.Out,
// to stdout or to any error returned by this file.

const (
	// rfc8628DefaultInterval is the polling interval used when the server does
	// not send one (historically the repo defaulted to 5 s).
	rfc8628DefaultInterval = 5 * time.Second
	// rfc8628DefaultExpiresIn is the fallback lifetime of a device code when the
	// server omits expires_in.
	rfc8628DefaultExpiresIn = 15 * time.Minute
	// rfc8628SlowDownIncrement is the RFC 8628 §3.5 back-off step: on slow_down
	// the client MUST increase its polling interval by 5 seconds.
	rfc8628SlowDownIncrement = 5 * time.Second
	// rfc8628DefaultMinInterval is the floor applied to the polling interval so a
	// server sending 0 (or an absurdly small value) cannot cause a busy loop.
	rfc8628DefaultMinInterval = 1 * time.Second
	// rfc8628MaxErrorBodyBytes caps how much of an unexpected response body is
	// echoed back in errors, matching the rest of the package.
	rfc8628MaxErrorBodyBytes = 512
	// rfc8628DeviceGrantType is the URN required by RFC 8628 §3.4.
	rfc8628DeviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
)

// DeviceCodeOptions injects the clock, the sleep function and the output sink so
// that tests can drive the flow deterministically. Every field is optional: a
// zero value is replaced by the production default.
type DeviceCodeOptions struct {
	// Sleep waits between polls. nil => time.Sleep. Tests inject a fake that
	// records the durations without sleeping. It must honour ctx.Done().
	Sleep func(ctx context.Context, d time.Duration)
	// Now returns the current instant. nil => time.Now.
	Now func() time.Time
	// Out receives the instructions shown to the user. nil => os.Stdout.
	Out io.Writer
	// MinInterval is the floor for the polling interval. 0 => 1 * time.Second.
	MinInterval time.Duration
	// MaxWait caps the total wait. 0 => the server's expires_in (default 15m).
	MaxWait time.Duration
}

// rfc8628DeviceAuth is the RFC 8628 §3.2 device authorization response. The
// numeric fields stay as raw JSON so they can be decoded with parseFlexibleInt
// (which accepts both a number and a quoted string).
type rfc8628DeviceAuth struct {
	DeviceCode              string          `json:"device_code"`
	UserCode                string          `json:"user_code"`
	VerificationURI         string          `json:"verification_uri"`
	VerificationURIComplete string          `json:"verification_uri_complete"`
	ExpiresIn               json.RawMessage `json:"expires_in"`
	Interval                json.RawMessage `json:"interval"`
}

// rfc8628TokenError is the RFC 8628 §3.5 token error response. Providers are
// inconsistent about types, so only string fields are decoded here.
type rfc8628TokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// rfc8628Endpoints resolves the two endpoints the device grant needs. They are
// mandatory (unlike the browser flow, there is no legacy fallback): a preset
// without them (e.g. google-antigravity, which does not support device flow)
// fails here, before any HTTP request is issued.
func rfc8628Endpoints(cfg OAuthProviderConfig) (codeURL, tokenURL string, err error) {
	if cfg.DeviceCodeURL == "" || cfg.DeviceTokenURL == "" {
		return "", "", fmt.Errorf("oauth: provider %q has no DeviceCodeURL/DeviceTokenURL configured", cfg.Name)
	}
	return cfg.DeviceCodeURL, cfg.DeviceTokenURL, nil
}

// rfc8628Now returns the clock injected through opts, or time.Now.
func rfc8628Now(opts DeviceCodeOptions) func() time.Time {
	if opts.Now != nil {
		return opts.Now
	}
	return time.Now
}

// rfc8628Out returns the writer injected through opts, or os.Stdout.
func rfc8628Out(opts DeviceCodeOptions) io.Writer {
	if opts.Out != nil {
		return opts.Out
	}
	return os.Stdout
}

// rfc8628Sleep returns the sleep function injected through opts, or a
// context-aware equivalent of time.Sleep. The default implementation returns as
// soon as ctx is done so a cancelled flow does not linger for a whole interval;
// the caller still re-checks ctx.Err() afterwards, which also covers injected
// fakes that ignore ctx.
func rfc8628Sleep(opts DeviceCodeOptions) func(ctx context.Context, d time.Duration) {
	if opts.Sleep != nil {
		return opts.Sleep
	}
	return func(ctx context.Context, d time.Duration) {
		if d <= 0 {
			return
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}
}

// rfc8628MinInterval resolves the polling-interval floor.
func rfc8628MinInterval(opts DeviceCodeOptions) time.Duration {
	if opts.MinInterval > 0 {
		return opts.MinInterval
	}
	return rfc8628DefaultMinInterval
}

// rfc8628TruncateBody renders an unexpected response body for an error message,
// capped at rfc8628MaxErrorBodyBytes.
func rfc8628TruncateBody(body []byte) string {
	if len(body) > rfc8628MaxErrorBodyBytes {
		return string(body[:rfc8628MaxErrorBodyBytes]) + "…"
	}
	return string(body)
}

// rfc8628RequestDeviceCode performs the RFC 8628 §3.1 device authorization
// request and validates the response.
func rfc8628RequestDeviceCode(ctx context.Context, cfg OAuthProviderConfig, codeURL string) (*rfc8628DeviceAuth, error) {
	// RFC 8628 mandates form encoding for this request, so TokenRequestStyle is
	// deliberately NOT applied here (it only governs the token endpoint).
	form := url.Values{"client_id": {cfg.ClientID}}
	// cfg.Scopes is a single space-delimited string in this package (already the
	// wire format RFC 6749 §3.3 expects), so it is forwarded verbatim; empty means
	// "omit the parameter".
	if cfg.Scopes != "" {
		form.Set("scope", cfg.Scopes)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := oauthHTTPClient(cfg).Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting device code: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading device code response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("device code request failed: status %d: %s", resp.StatusCode, rfc8628TruncateBody(body))
	}

	var auth rfc8628DeviceAuth
	if err := json.Unmarshal(body, &auth); err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}
	if auth.DeviceCode == "" {
		return nil, fmt.Errorf("device code response missing device_code")
	}
	if auth.VerificationURI == "" && auth.VerificationURIComplete == "" && cfg.DeviceVerifyURL == "" {
		return nil, fmt.Errorf("device code response missing verification URI")
	}
	return &auth, nil
}

// rfc8628DisplayURI picks the URI to show the user: verification_uri_complete
// (which already embeds the user code) wins, then verification_uri, then the
// preset's DeviceVerifyURL as a last resort.
func rfc8628DisplayURI(cfg OAuthProviderConfig, auth *rfc8628DeviceAuth) string {
	if auth.VerificationURIComplete != "" {
		return auth.VerificationURIComplete
	}
	if auth.VerificationURI != "" {
		return auth.VerificationURI
	}
	return cfg.DeviceVerifyURL
}

// rfc8628Poll performs a single token-endpoint poll (§3.4).
//
// It returns (credential, nil) on success, (nil, nil) when the authorization is
// still pending, and (nil, err) for a terminal failure. The second return value
// is the polling interval to use next: unchanged for authorization_pending, and
// increased by rfc8628SlowDownIncrement for slow_down (RFC 8628 §3.5).
func rfc8628Poll(cfg OAuthProviderConfig, tokenURL string, params url.Values, interval time.Duration, provider string) (*AuthCredential, time.Duration, error) {
	resp, err := doTokenRequest(cfg, tokenURL, params)
	if err != nil {
		return nil, interval, fmt.Errorf("polling device code token: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, interval, fmt.Errorf("reading token response: %w", err)
	}

	// One tolerant decode covers both shapes: a token response and an OAuth error.
	var parsed struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	jsonErr := json.Unmarshal(body, &parsed)

	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	if success && parsed.AccessToken != "" {
		cred, err := parseTokenResponse(body, provider)
		if err != nil {
			return nil, interval, err
		}
		return cred, interval, nil
	}

	// No structured error in the body: report the raw status. A 2xx without an
	// access token and without an error field is a malformed success.
	if jsonErr != nil || parsed.Error == "" {
		if success {
			return nil, interval, fmt.Errorf("device code token response missing access_token: %s", rfc8628TruncateBody(body))
		}
		return nil, interval, fmt.Errorf("device code token request failed: status %d: %s", resp.StatusCode, rfc8628TruncateBody(body))
	}

	// Some providers (GitHub, for one) answer 200 with an error body, so the error
	// code — not the status — drives the state machine below.
	switch parsed.Error {
	case "authorization_pending":
		return nil, interval, nil
	case "slow_down":
		return nil, interval + rfc8628SlowDownIncrement, nil
	case "access_denied":
		return nil, interval, fmt.Errorf("device code authentication denied by user: %s", parsed.ErrorDescription)
	case "expired_token", "expired_device_code":
		return nil, interval, fmt.Errorf("device code expired before authorization")
	default:
		if parsed.ErrorDescription != "" {
			return nil, interval, fmt.Errorf("device code authentication failed: %s: %s", parsed.Error, parsed.ErrorDescription)
		}
		return nil, interval, fmt.Errorf("device code authentication failed: %s", parsed.Error)
	}
}

// LoginDeviceCodeRFC8628 ejecuta un device authorization grant RFC 8628
// (sección 3.1 device authorization request + 3.4 token polling) usando los
// endpoints explícitos de cfg. Devuelve una credencial con Provider == cfg.Name.
func LoginDeviceCodeRFC8628(ctx context.Context, cfg OAuthProviderConfig, opts DeviceCodeOptions) (*AuthCredential, error) {
	codeURL, tokenURL, err := rfc8628Endpoints(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("oauth: provider %q has no ClientID configured", cfg.Name)
	}

	auth, err := rfc8628RequestDeviceCode(ctx, cfg, codeURL)
	if err != nil {
		return nil, err
	}

	intervalSeconds, err := parseFlexibleInt(auth.Interval)
	if err != nil {
		return nil, fmt.Errorf("parsing device code interval: %w", err)
	}
	interval := rfc8628DefaultInterval
	if intervalSeconds > 0 {
		interval = time.Duration(intervalSeconds) * time.Second
	}
	if minInterval := rfc8628MinInterval(opts); interval < minInterval {
		interval = minInterval
	}

	expiresSeconds, err := parseFlexibleInt(auth.ExpiresIn)
	if err != nil {
		return nil, fmt.Errorf("parsing device code expires_in: %w", err)
	}
	maxWait := opts.MaxWait
	if maxWait <= 0 {
		maxWait = rfc8628DefaultExpiresIn
		if expiresSeconds > 0 {
			maxWait = time.Duration(expiresSeconds) * time.Second
		}
	}

	// The instructions are printed exactly once, before the first poll, and never
	// contain the device_code.
	if _, err := fmt.Fprintf(
		rfc8628Out(opts),
		"\nTo authenticate, open this URL in your browser:\n\n  %s\n\nThen enter this code: %s\n\nWaiting for authentication...\n",
		rfc8628DisplayURI(cfg, auth),
		auth.UserCode,
	); err != nil {
		return nil, fmt.Errorf("writing device code instructions: %w", err)
	}

	provider := cfg.Name
	if provider == "" {
		provider = "openai" // legacy default for hand-built configs
	}

	params := url.Values{
		"grant_type":  {rfc8628DeviceGrantType},
		"device_code": {auth.DeviceCode},
		"client_id":   {cfg.ClientID},
	}

	now := rfc8628Now(opts)
	sleep := rfc8628Sleep(opts)
	start := now()

	for {
		if now().Sub(start) >= maxWait {
			return nil, fmt.Errorf("device code authentication timed out after %s", maxWait)
		}
		// Wait before polling (as the proprietary flow does): the first poll must
		// not happen earlier than `interval` after the device authorization.
		sleep(ctx, interval)
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("device code authentication cancelled: %w", err)
		}

		cred, nextInterval, err := rfc8628Poll(cfg, tokenURL, params, interval, provider)
		if err != nil {
			return nil, err
		}
		if cred != nil {
			return cred, nil
		}
		interval = nextInterval
	}
}
