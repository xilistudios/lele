package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// claudeInferenceScopes is the allowlist of scopes that grant access to the
// Anthropic inference API from a Claude.ai OAuth token. It is a direct port of
// jcode's `claude_scopes_have_inference` allowlist (jcode-base
// `auth/oauth.rs`): the inference privilege is granted by ANY of these six
// scopes, not only by the self-explanatory `user:inference`. Treating it as a
// single loose string would reject perfectly usable tokens (e.g. a
// `workspace:developer` or `org:service_key_inference` login).
var claudeInferenceScopes = map[string]bool{
	"user:inference":            true,
	"user:ccr_inference":        true,
	"user:voice":                true,
	"org:service_key_inference": true,
	"workspace:developer":       true,
	"workspace:inference":       true,
}

// claudeScopesHaveInference reports whether scopes contains at least one scope
// from claudeInferenceScopes. An empty/nil list carries no information, so it
// reports false; callers decide what an unscoped token means.
func claudeScopesHaveInference(scopes []string) bool {
	for _, scope := range scopes {
		if claudeInferenceScopes[scope] {
			return true
		}
	}
	return false
}

// claudeInferenceScopeMessage is the single source of truth for the
// inference-scope failure text. It is shared by ensureClaudeInferenceScope and
// inferenceScopeError so both surfaces stay byte-identical.
func claudeInferenceScopeMessage(scopes []string, action string) string {
	return fmt.Sprintf("Claude OAuth %s returned a token without the required user:inference scope (scopes: %s). Re-run `lele auth login --provider claude` so lele opens the Claude.ai OAuth flow, or import/use a fresh Claude Code login.",
		action, strings.Join(scopes, " "))
}

// ensureClaudeInferenceScope validates the scope list of a Claude OAuth token
// response. It returns nil when the list is empty (the server simply did not
// report scopes, so there is nothing to reject) or when any allowlisted
// inference scope is present, and a descriptive error otherwise. `action`
// names the step that produced the token, e.g. "token exchange" or
// "token refresh".
//
// This is the pure message-builder variant, used by the exchange path where
// there is no issued credential worth handing back to the caller. The refresh
// path uses ensureClaudeInferenceScopeForCredential so a rotated refresh token
// can still be persisted before the gate failure is surfaced.
func ensureClaudeInferenceScope(scopes []string, action string) error {
	if len(scopes) == 0 || claudeScopesHaveInference(scopes) {
		return nil
	}
	return errors.New(claudeInferenceScopeMessage(scopes, action))
}

// inferenceScopeError is the typed form of the inference-scope gate failure.
// It carries the credential the token endpoint just issued so the caller can
// persist a ROTATED refresh token before surfacing the failure.
//
// This exists because of a jcode bug that must not be copied: jcode runs the
// inference gate before persisting the rotated refresh token, so a rotation
// that comes back without an inference scope consumes the old token and loses
// the new one, breaking the account. lele hands the fresh credential back with
// the error instead, and the refresh coordinator persists it first (see
// refresh_coordinator.go).
type inferenceScopeError struct {
	Credential *AuthCredential // credential issued by the endpoint (may be nil)
	Action     string
	Scopes     []string
}

// Error returns the same text as ensureClaudeInferenceScope.
func (e *inferenceScopeError) Error() string {
	return claudeInferenceScopeMessage(e.Scopes, e.Action)
}

// ensureClaudeInferenceScopeForCredential is the refresh call site's variant of
// ensureClaudeInferenceScope: on failure it returns an *inferenceScopeError
// wrapping the freshly issued credential (nil when the gate passes), so the
// caller can recover the rotated refresh token with errors.As.
func ensureClaudeInferenceScopeForCredential(cred *AuthCredential, action string) error {
	if cred == nil {
		return ensureClaudeInferenceScope(nil, action)
	}
	if err := ensureClaudeInferenceScope(cred.Scopes, action); err != nil {
		return &inferenceScopeError{
			Credential: cred,
			Action:     action,
			Scopes:     cred.Scopes,
		}
	}
	return nil
}

// ClaudeOAuthConfig returns the OAuth preset for a Claude.ai (Anthropic
// Pro/Max subscription) login. It is registered by F1-T07 under the canonical
// key "anthropic" (oauth_registry.go), with the user-facing alias "claude":
// OAuthProviderConfigByName("claude") returns this exact value and the CLI
// stores credentials under "anthropic".
//
// The AuthorizeURL is the claude.ai surface and MUST stay that way. The
// console/platform authorize surface (https://platform.claude.com/oauth/authorize,
// jcode's dead CONSOLE_AUTHORIZE_URL) issues tokens that refresh fine but are
// rejected by the inference API: logging in through it "succeeds" and leaves a
// permanently unusable credential. This is the single most expensive trap in
// the whole Claude OAuth flow, hence the explicit regression assertions in
// TestClaudeOAuthConfigPreset and TestClaudeAuthorizeURLGolden.
//
// Port 0 asks the OS for an ephemeral loopback port (jcode
// bind_callback_listener(0)); RedirectURI stays empty so the redirect is derived
// from the port actually bound (redirectURIForListener), while
// ManualRedirectURI carries the HOSTED paste-code redirect used by the headless
// flow (F1-T06) and only recognized — never sent spontaneously.
//
// Correction V3: no User-Agent is set anywhere here. The token endpoint sees
// jcode's own UA, not the claude-cli Messages-API UA; the claude-cli UA is
// added by the Anthropic provider in OAuth mode (F1-T09), not by this preset.
func ClaudeOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Name:         "anthropic",
		ClientID:     "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
		ClientSecret: "",
		// Issuer stays empty on purpose: every endpoint below is explicit, so no
		// URL is ever derived from the issuer for Claude.
		Issuer:       "",
		AuthorizeURL: "https://claude.com/cai/oauth/authorize",
		TokenURL:     "https://platform.claude.com/v1/oauth/token",
		Scopes:       "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload",
		// RefreshScopes drops org:create_api_key: refresh must never be able to
		// mint an API key.
		RefreshScopes:    "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload",
		OmitRefreshScope: false,
		CallbackPath:     "/callback",
		// Port 0 => ephemeral loopback port (bound by bindCallbackListener).
		Port:        0,
		RedirectURI: "",
		// ManualRedirectURI is the hosted paste redirect for LoginPasteCode and
		// the alternate URL printed when the loopback listener cannot be used.
		ManualRedirectURI: "https://platform.claude.com/oauth/code/callback",
		ExtraAuthParams: map[string]string{
			// UNVERIFIED against the real API — see .lele/tasks/oauth-providers-f1-tasks.md §0
			// Flip switch for fact #2: deleting this entry drops code=true everywhere.
			"code": "true",
		},
		Originator:        "",
		TokenRequestStyle: TokenRequestJSON,
		AuthHeaderStyle:   TokenAuthBody,
		// UNVERIFIED against the real API — see .lele/tasks/oauth-providers-f1-tasks.md §0
		// Flip switch for fact #1: setting this to false removes `state` from the
		// token exchange without touching the parser or the flow.
		StateIsVerifier:       true,
		RequireInferenceScope: true,
		// Spec correction V7: a Claude credential without a refresh token (or
		// with no expiry) is unusable — it can never be refreshed and would make
		// NeedsRefresh() permanently true — so the exchange refuses it.
		RequireRefreshToken: true,
		// jcode's CLAUDE_TOKEN_TIMEOUT_SECS: the token endpoint is the only call
		// this preset makes, so the timeout lives on its client. Nothing assumes
		// a nil HTTPClient means http.DefaultClient for Claude.
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// parseAuthorizeCodeInput extracts the authorization code and, when present,
// the OAuth state from whatever the user pasted into the headless Claude flow.
// It is a direct port of jcode's `parse_claude_code_input` precedence:
//
//  1. an input containing "code=" is parsed as a URL, falling back to
//     "https://example.com?<raw>" for bare query strings; `code` is required and
//     `state` is optional;
//  2. otherwise the trimmed input IS the code;
//  3. a "#" in the code-so-far splits it on the FIRST "#" into code#state
//     (OpenCode-style pastes), overriding any state from the query string;
//  4. an empty code is an error.
//
// Note the parser is deliberately lenient about the state (correction V8): the
// Claude paste flow accepts a code without any state and substitutes the PKCE
// verifier, so callers must NOT use the OpenAI/Google parser that requires one.
func parseAuthorizeCodeInput(raw string) (code, state string, err error) {
	trimmed := strings.TrimSpace(raw)

	var (
		rawCode        string
		stateFromQuery string
	)

	if strings.Contains(trimmed, "code=") {
		// Go's url.Parse accepts relative references that Rust's Url::parse
		// rejects ("code=abc&state=xyz" parses as a path), so a schemeless input
		// is routed through the query-string fallback exactly like jcode does.
		parsed, parseErr := url.Parse(trimmed)
		if parseErr != nil || parsed.Scheme == "" {
			parsed, parseErr = url.Parse("https://example.com?" + trimmed)
		}
		if parseErr != nil {
			return "", "", errors.New("no authorization code found in the URL")
		}

		values, ok := parsed.Query()["code"]
		if !ok || len(values) == 0 {
			return "", "", errors.New("no authorization code found in the URL")
		}
		rawCode = values[0]
		stateFromQuery = parsed.Query().Get("state")
	} else {
		rawCode = trimmed
	}

	if idx := strings.Index(rawCode, "#"); idx >= 0 {
		state = rawCode[idx+1:]
		rawCode = rawCode[:idx]
	} else {
		state = stateFromQuery
	}

	if strings.TrimSpace(rawCode) == "" {
		return "", "", errors.New("no authorization code provided")
	}

	return rawCode, state, nil
}

// LoginPasteCode runs the headless Claude paste-code login: it mints a fresh
// PKCE pair, prints the authorize URL built with the provider's hosted
// ManualRedirectURI, reads ONE line of user input (the authorization code, the
// full callback URL or a `code#state` paste) and exchanges it for tokens.
//
// It is the flow behind `lele auth login --provider claude --paste-code` and
// the fallback used when the loopback browser flow cannot run
// (LoginBrowserWithPasteFallback). A provider without a ManualRedirectURI has
// no hosted paste redirect, so the flow is unavailable and an error is returned
// instead of building a loopback redirect the token endpoint would reject.
func LoginPasteCode(cfg OAuthProviderConfig, r io.Reader) (*AuthCredential, error) {
	if cfg.ManualRedirectURI == "" {
		return nil, fmt.Errorf("oauth: provider %q has no ManualRedirectURI configured for the paste-code flow", cfg.Name)
	}

	pkce, err := GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("generating PKCE: %w", err)
	}

	return loginPasteCodeWithPKCE(cfg, pkce, r)
}

// loginPasteCodeWithPKCE is the deterministic core of LoginPasteCode: the PKCE
// pair is injected so the flow can be exercised offline with a known verifier
// and so the authorize URL and the exchange share the exact same values.
//
// The PKCE verifier doubles as the OAuth `state` (Claude/jcode behavior), so a
// non-empty pasted state must equal it; otherwise the input belongs to an older
// login attempt and no request is issued.
//
// The prompts go straight to stdout with fmt.Printf/fmt.Print, following the
// package convention set by LoginPasteToken: only the reader is injected, never
// an io.Writer.
func loginPasteCodeWithPKCE(cfg OAuthProviderConfig, pkce PKCECodes, r io.Reader) (*AuthCredential, error) {
	redirectURI := cfg.ManualRedirectURI
	state := pkce.CodeVerifier

	authURL := buildAuthorizeURL(cfg, pkce, state, redirectURI)

	fmt.Printf("Open this URL in your browser to authenticate:\n\n%s\n\n", authURL)
	fmt.Print("After logging in, paste the authorization code (or the full callback URL):\n\n> ")

	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("reading authorization code: %w", err)
		}
		return nil, errors.New("no authorization code entered")
	}

	line := strings.TrimSpace(scanner.Text())
	if line == "" {
		return nil, errors.New("no authorization code entered")
	}

	code, pastedState, err := parseAuthorizeCodeInput(line)
	if err != nil {
		return nil, err
	}
	// A pasted state that differs from the verifier this flow generated belongs
	// to a different (older) login attempt; exchanging it would fail server-side
	// with a confusing error, so fail fast and ask for a fresh code.
	if pastedState != "" && pastedState != pkce.CodeVerifier {
		return nil, errors.New("OAuth state mismatch. Start login again and use the latest callback/code.")
	}

	return exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         code,
		CodeVerifier: pkce.CodeVerifier,
		RedirectURI:  redirectURI,
		// Claude echoes the PKCE verifier as `state` on the exchange (jcode
		// behavior); an absent pasted state is thus substituted by the verifier
		// here (correction V8).
		State: pkce.CodeVerifier,
	})
}
