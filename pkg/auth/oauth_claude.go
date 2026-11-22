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

var claudeInferenceScopes = map[string]bool{
	"user:inference":            true,
	"user:ccr_inference":        true,
	"user:voice":                true,
	"org:service_key_inference": true,
	"workspace:developer":       true,
	"workspace:inference":       true,
}

func claudeScopesHaveInference(scopes []string) bool {
	for _, scope := range scopes {
		if claudeInferenceScopes[scope] {
			return true
		}
	}
	return false
}

func claudeInferenceScopeMessage(scopes []string, action string) string {
	return fmt.Sprintf("Claude OAuth %s returned a token without the required user:inference scope (scopes: %s). Re-run `lele auth login --provider claude` so lele opens the Claude.ai OAuth flow, or import/use a fresh Claude Code login.",
		action, strings.Join(scopes, " "))
}

func ensureClaudeInferenceScope(scopes []string, action string) error {
	if len(scopes) == 0 || claudeScopesHaveInference(scopes) {
		return nil
	}
	return errors.New(claudeInferenceScopeMessage(scopes, action))
}

type inferenceScopeError struct {
	Credential *AuthCredential
	Action     string
	Scopes     []string
}

func (e *inferenceScopeError) Error() string {
	return claudeInferenceScopeMessage(e.Scopes, e.Action)
}

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

func ClaudeOAuthConfig() OAuthProviderConfig {
	return OAuthProviderConfig{
		Name:              "anthropic",
		DisplayName:       "Claude",
		ClientID:          "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
		ClientSecret:      "",
		Issuer:            "",
		AuthorizeURL:      "https://claude.com/cai/oauth/authorize",
		TokenURL:          "https://platform.claude.com/v1/oauth/token",
		Scopes:            "org:create_api_key user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload",
		RefreshScopes:     "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload",
		OmitRefreshScope:  false,
		CallbackPath:      "/callback",
		Port:              0,
		RedirectURI:       "",
		ManualRedirectURI: "https://platform.claude.com/oauth/code/callback",
		ExtraAuthParams: map[string]string{
			"code": "true",
		},
		Originator:            "",
		TokenRequestStyle:     TokenRequestJSON,
		AuthHeaderStyle:       TokenAuthBody,
		StateIsVerifier:       true,
		RequireInferenceScope: true,
		RequireRefreshToken:   true,
		HTTPClient:            &http.Client{Timeout: 15 * time.Second},
	}
}

func parseAuthorizeCodeInput(raw string) (code, state string, err error) {
	trimmed := strings.TrimSpace(raw)

	var (
		rawCode        string
		stateFromQuery string
	)

	if strings.Contains(trimmed, "code=") {
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

func pasteFlowState(cfg OAuthProviderConfig, pkce PKCECodes) (string, error) {
	if cfg.StateIsVerifier {
		return pkce.CodeVerifier, nil
	}
	return generateState()
}

func loginPasteCodeWithPKCE(cfg OAuthProviderConfig, pkce PKCECodes, r io.Reader) (*AuthCredential, error) {
	state, err := pasteFlowState(cfg, pkce)
	if err != nil {
		return nil, fmt.Errorf("generating state: %w", err)
	}

	return loginPasteCodeWithPKCEAndState(cfg, pkce, state, r)
}

func loginPasteCodeWithPKCEAndState(cfg OAuthProviderConfig, pkce PKCECodes, state string, r io.Reader) (*AuthCredential, error) {
	redirectURI := cfg.ManualRedirectURI

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

	if cfg.StateIsVerifier {
		code, pastedState, err := parseAuthorizeCodeInput(line)
		if err != nil {
			return nil, err
		}
		if pastedState != "" && pastedState != pkce.CodeVerifier {
			return nil, errors.New("OAuth state mismatch. Start login again and use the latest callback/code.")
		}

		return exchangeCodeForTokensWithParams(cfg, exchangeParams{
			Code:         code,
			CodeVerifier: pkce.CodeVerifier,
			RedirectURI:  redirectURI,
			State:        pkce.CodeVerifier,
		})
	}

	code := line
	if looksLikeCallbackInput(line) {
		parsedCode, pastedState, err := parseCallbackInputWithState(line)
		if err != nil {
			return nil, err
		}
		if pastedState != state {
			return nil, errors.New("OAuth state mismatch. Start login again and use the latest callback URL.")
		}
		code = parsedCode
	}
	return exchangeCodeForTokensWithParams(cfg, exchangeParams{
		Code:         code,
		CodeVerifier: pkce.CodeVerifier,
		RedirectURI:  redirectURI,
	})
}

func looksLikeCallbackInput(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	return strings.HasPrefix(trimmed, "http://") ||
		strings.HasPrefix(trimmed, "https://") ||
		strings.HasPrefix(trimmed, "?") ||
		strings.Contains(trimmed, "code=") ||
		strings.Contains(trimmed, "state=")
}

func parseCallbackInputWithState(raw string) (code, state string, err error) {
	trimmed := strings.TrimSpace(raw)

	code, state, err = parseAuthorizeCodeInput(trimmed)
	if err != nil && strings.HasPrefix(trimmed, "?") {
		code, state, err = parseAuthorizeCodeInput(strings.TrimPrefix(trimmed, "?"))
	}
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(state) == "" {
		return "", "", errors.New("Please paste the full callback URL or query string so lele can verify the login state.")
	}
	return code, state, nil
}
