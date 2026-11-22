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

const (
	rfc8628DefaultInterval    = 5 * time.Second
	rfc8628DefaultExpiresIn   = 15 * time.Minute
	rfc8628SlowDownIncrement  = 5 * time.Second
	rfc8628DefaultMinInterval = 1 * time.Second
	rfc8628MaxErrorBodyBytes  = 512
	rfc8628DeviceGrantType    = "urn:ietf:params:oauth:grant-type:device_code"
)

type DeviceCodeOptions struct {
	Sleep       func(ctx context.Context, d time.Duration)
	Now         func() time.Time
	Out         io.Writer
	MinInterval time.Duration
	MaxWait     time.Duration
}

type rfc8628DeviceAuth struct {
	DeviceCode              string          `json:"device_code"`
	UserCode                string          `json:"user_code"`
	VerificationURI         string          `json:"verification_uri"`
	VerificationURIComplete string          `json:"verification_uri_complete"`
	ExpiresIn               json.RawMessage `json:"expires_in"`
	Interval                json.RawMessage `json:"interval"`
}

type rfc8628TokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func rfc8628Endpoints(cfg OAuthProviderConfig) (codeURL, tokenURL string, err error) {
	if cfg.DeviceCodeURL == "" || cfg.DeviceTokenURL == "" {
		return "", "", fmt.Errorf("oauth: provider %q has no DeviceCodeURL/DeviceTokenURL configured", cfg.Name)
	}
	return cfg.DeviceCodeURL, cfg.DeviceTokenURL, nil
}

func rfc8628Now(opts DeviceCodeOptions) func() time.Time {
	if opts.Now != nil {
		return opts.Now
	}
	return time.Now
}

func rfc8628Out(opts DeviceCodeOptions) io.Writer {
	if opts.Out != nil {
		return opts.Out
	}
	return os.Stdout
}

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

func rfc8628MinInterval(opts DeviceCodeOptions) time.Duration {
	if opts.MinInterval > 0 {
		return opts.MinInterval
	}
	return rfc8628DefaultMinInterval
}

func rfc8628TruncateBody(body []byte) string {
	if len(body) > rfc8628MaxErrorBodyBytes {
		return string(body[:rfc8628MaxErrorBodyBytes]) + "…"
	}
	return string(body)
}

func rfc8628RequestDeviceCode(ctx context.Context, cfg OAuthProviderConfig, codeURL string) (*rfc8628DeviceAuth, error) {
	form := url.Values{"client_id": {cfg.ClientID}}
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

func rfc8628DisplayURI(cfg OAuthProviderConfig, auth *rfc8628DeviceAuth) string {
	if auth.VerificationURIComplete != "" {
		return auth.VerificationURIComplete
	}
	if auth.VerificationURI != "" {
		return auth.VerificationURI
	}
	return cfg.DeviceVerifyURL
}

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

	if jsonErr != nil || parsed.Error == "" {
		if success {
			return nil, interval, fmt.Errorf("device code token response missing access_token: %s", rfc8628TruncateBody(body))
		}
		return nil, interval, fmt.Errorf("device code token request failed: status %d: %s", resp.StatusCode, rfc8628TruncateBody(body))
	}

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
		provider = "openai"
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
