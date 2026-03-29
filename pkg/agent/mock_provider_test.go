package agent

import (
	"context"
	"fmt"

	"github.com/xilistudios/lele/pkg/providers"
)

type mockProvider struct {
	mockResponse string
	shouldError  bool
}

func (m *mockProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, opts map[string]interface{}) (*providers.LLMResponse, error) {
	if m.shouldError {
		return nil, fmt.Errorf("Mock provider error for testing")
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
