//go:build !bedrock

package bedrock

import (
	"context"
	"fmt"
	"time"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

type (
	LLMResponse    = protocoltypes.LLMResponse
	Message        = protocoltypes.Message
	ToolDefinition = protocoltypes.ToolDefinition
)

type Provider struct{}

type Option func(*providerConfig)

type providerConfig struct{}

func WithRegion(region string) Option {
	return func(c *providerConfig) {}
}

func WithProfile(profile string) Option {
	return func(c *providerConfig) {}
}

func WithBaseEndpoint(endpoint string) Option {
	return func(c *providerConfig) {}
}

func WithRequestTimeout(timeout time.Duration) Option {
	return func(c *providerConfig) {}
}

func NewProvider(ctx context.Context, opts ...Option) (*Provider, error) {
	return nil, fmt.Errorf("bedrock provider not available: build with -tags bedrock to enable AWS Bedrock support")
}

func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	return nil, fmt.Errorf("bedrock provider not available: build with -tags bedrock to enable AWS Bedrock support")
}

func (p *Provider) GetDefaultModel() string {
	return ""
}
