// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package providers

import (
	"context"

	"github.com/xilistudios/lele/pkg/providers/openai_compat"
)

type HTTPProvider struct {
	delegate *openai_compat.Provider
}

var _ StreamingLLMProvider = (*HTTPProvider)(nil)

func NewHTTPProvider(apiKey, apiBase, proxy string) *HTTPProvider {
	return NewHTTPProviderWithOptions(apiKey, apiBase, proxy, openai_compat.Options{})
}

// NewHTTPProviderWithOptions creates an HTTPProvider with static transport
// extras (e.g. the OpenCode Go session header).
func NewHTTPProviderWithOptions(apiKey, apiBase, proxy string, opts openai_compat.Options) *HTTPProvider {
	return &HTTPProvider{
		delegate: openai_compat.NewProviderWithOptions(apiKey, apiBase, proxy, opts),
	}
}

func (p *HTTPProvider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	return p.delegate.Chat(ctx, messages, tools, model, options)
}

func (p *HTTPProvider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error) {
	return p.delegate.ChatStream(ctx, messages, tools, model, options, onChunk, onReasoning)
}

func (p *HTTPProvider) GetDefaultModel() string {
	return ""
}
