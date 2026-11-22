package providers

import (
	"context"
	"fmt"

	"github.com/xilistudios/lele/pkg/auth"
	anthropicprovider "github.com/xilistudios/lele/pkg/providers/anthropic"
)

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

func NewClaudeProviderWithTokenSourceAndBaseURL(token string, tokenSource func() (string, error), apiBase string) *ClaudeProvider {
	return NewClaudeProviderWithTokenSourceBaseURLAndOAuthMode(token, tokenSource, apiBase, false)
}

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
		cred, err := auth.EnsureFreshCredential("anthropic", claudeOAuthConfig(), auth.RefreshPolicy{RequireOAuthMethod: true})
		if err != nil {
			return "", err
		}
		if cred == nil {
			return "", fmt.Errorf("no credentials for anthropic. Run: lele auth login --provider anthropic")
		}
		return cred.AccessToken, nil
	}
}
