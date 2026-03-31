package agent

import (
	"context"
	"fmt"

	"github.com/xilistudios/lele/pkg/providers"
)

type mockProvider struct {
	mockResponse string
	shouldError  bool
	returnEmpty  bool // if true, return empty content response
}

func (m *mockProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	if m.shouldError {
		return nil, fmt.Errorf("Mock provider error for testing")
	}

	if m.returnEmpty {
		return &providers.LLMResponse{
			Content:   "",
			ToolCalls: []providers.ToolCall{},
		}, nil
	}

	response := "Mock response"
	if m.mockResponse != "" {
		response = m.mockResponse
	}

	return &providers.LLMResponse{
		Content:   response,
		ToolCalls: []providers.ToolCall{},
	}, nil
}

func (m *mockProvider) GetDefaultModel() string {
	return "mock-model"
}
