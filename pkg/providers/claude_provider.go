package providers

import (
	"context"
	"fmt"

	"github.com/xilistudios/lele/pkg/auth"
	anthropicprovider "github.com/xilistudios/lele/pkg/providers/anthropic"
)

// claudeOAuthConfig returns the OAuth preset used to refresh Claude
// credentials. It is a package-level variable — exactly like the
// `var getCredential = auth.GetCredential` seam in factory.go — so that tests
// can point the Claude token endpoint at a local httptest server:
// auth.EnsureFreshCredential receives the provider config as an argument, so
// overriding this seam is the only way to redirect the token endpoint from
// pkg/providers. Production always uses auth.ClaudeOAuthConfig.
var claudeOAuthConfig = auth.ClaudeOAuthConfig

type ClaudeProvider struct {
	delegate *anthropicprovider.Provider
}

func NewClaudeProvider(token string) *ClaudeProvider {
	return &ClaudeProvider{
		delegate: anthropicprovider.NewProvider(token),
	}
}

func NewClaudeProviderWithBaseURL(token, apiBase string) *ClaudeProvider {
	return &ClaudeProvider{
		delegate: anthropicprovider.NewProviderWithBaseURL(token, apiBase),
	}
}

func NewClaudeProviderWithTokenSource(token string, tokenSource func() (string, error)) *ClaudeProvider {
	return &ClaudeProvider{
		delegate: anthropicprovider.NewProviderWithTokenSource(token, tokenSource),
	}
}

// NewClaudeProviderWithTokenSourceAndBaseURL builds a Claude provider that
// refreshes its token through tokenSource. It keeps the historical non-OAuth
// behavior by delegating to the OAuth-aware constructor with oauth=false.
func NewClaudeProviderWithTokenSourceAndBaseURL(token string, tokenSource func() (string, error), apiBase string) *ClaudeProvider {
	return NewClaudeProviderWithTokenSourceBaseURLAndOAuthMode(token, tokenSource, apiBase, false)
}

// NewClaudeProviderWithTokenSourceBaseURLAndOAuthMode builds a Claude provider
// that refreshes its token through tokenSource and, when oauth is true, sends
// the Claude-Code-compatible OAuth request markers.
func NewClaudeProviderWithTokenSourceBaseURLAndOAuthMode(token string, tokenSource func() (string, error), apiBase string, oauth bool) *ClaudeProvider {
	return &ClaudeProvider{
		delegate: anthropicprovider.NewProviderWithTokenSourceBaseURLAndOAuthMode(token, tokenSource, apiBase, oauth),
	}
}

func newClaudeProviderWithDelegate(delegate *anthropicprovider.Provider) *ClaudeProvider {
	return &ClaudeProvider{delegate: delegate}
}

func (p *ClaudeProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	resp, err := p.delegate.Chat(ctx, messages, tools, model, options)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (p *ClaudeProvider) GetDefaultModel() string {
	return p.delegate.GetDefaultModel()
}

func createClaudeTokenSource() func() (string, error) {
	return func() (string, error) {
		// Routing through the coordinator (instead of reading the store
		// directly) gives Claude the same single-flight refresh as codex: N
		// concurrent requests sharing a key collapse into at most ONE token
		// request. It also owns the freshness margin — the 5-minute
		// NeedsRefresh window already subsumes the 60s margin of the reference
		// implementation — so no extra margin is applied here.
		cred, err := auth.EnsureFreshCredential("anthropic", claudeOAuthConfig(), auth.RefreshPolicy{RequireOAuthMethod: true})
		if err != nil {
			// The coordinator already contextualizes the failure with
			// "loading auth credentials:" / "refreshing token:" /
			// "saving refreshed token:" prefixes; wrapping it again would nest
			// them. Parity with createCodexTokenSource.
			return "", err
		}
		if cred == nil {
			return "", fmt.Errorf("no credentials for anthropic. Run: lele auth login --provider anthropic")
		}
		return cred.AccessToken, nil
	}
}
