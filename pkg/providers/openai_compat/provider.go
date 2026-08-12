package openai_compat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/xilistudios/lele/pkg/providers/common"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

type ToolCall = protocoltypes.ToolCall
type FunctionCall = protocoltypes.FunctionCall
type LLMResponse = protocoltypes.LLMResponse

type UsageInfo = protocoltypes.UsageInfo
type Message = protocoltypes.Message
type ToolDefinition = protocoltypes.ToolDefinition
type ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition

// builderPool provides reusable strings.Builder instances to reduce
// allocations during SSE stream parsing.
var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

type Provider struct {
	apiKey     string
	apiBase    string
	httpClient *http.Client
}

func NewProvider(apiKey, apiBase, proxy string) *Provider {
	client := common.NewStreamingHTTPClient(proxy)

	return &Provider{
		apiKey:     apiKey,
		apiBase:    strings.TrimRight(apiBase, "/"),
		httpClient: client,
	}
}

func (p *Provider) Chat(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}) (*LLMResponse, error) {
	if p.apiBase == "" {
		return nil, fmt.Errorf("API base not configured")
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, common.DefaultRequestTimeout)
		defer cancel()
	}

	model = normalizeModel(model, p.apiBase)

	requestBody := map[string]interface{}{
		"model":    model,
		"messages": messages,
	}

	if len(tools) > 0 {
		requestBody["tools"] = tools
		requestBody["tool_choice"] = "auto"
	}

	if maxTokens, ok := asInt(options["max_tokens"]); ok {
		lowerModel := strings.ToLower(model)
		if strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "o1") || strings.Contains(lowerModel, "gpt-5") {
			requestBody["max_completion_tokens"] = maxTokens
		} else {
			requestBody["max_tokens"] = maxTokens
		}
	}

	if temperature, ok := asFloat(options["temperature"]); ok {
		lowerModel := strings.ToLower(model)
		// Kimi k2 models only support temperature=1.
		if strings.Contains(lowerModel, "kimi") && strings.Contains(lowerModel, "k2") {
			requestBody["temperature"] = 1.0
		} else {
			requestBody["temperature"] = temperature
		}
	}

	// Handle reasoning config (for OpenAI o-series, OpenRouter, and compatible models)
	if reasoning, ok := options["reasoning"].(map[string]interface{}); ok && reasoning != nil {
		reasoningBody := map[string]interface{}{}
		if v, ok := reasoning["effort"]; ok {
			if s, ok := v.(string); ok && s != "" {
				reasoningBody["effort"] = s
			}
		}
		if maxTokens, ok := reasoning["max_tokens"].(int); ok && maxTokens > 0 {
			reasoningBody["max_tokens"] = maxTokens
		}
		if exclude, ok := reasoning["exclude"].(bool); ok {
			reasoningBody["exclude"] = exclude
		}
		if summary, ok := reasoning["summary"].(string); ok && summary != "" {
			// summary is OpenAI-specific; only send to OpenAI API endpoints
			if isOpenAIEndpoint(p.apiBase) {
				reasoningBody["summary"] = summary
			}
		}
		if enabled, ok := reasoning["enabled"].(bool); ok {
			reasoningBody["enabled"] = enabled
		}
		if len(reasoningBody) > 0 {
			requestBody["reasoning"] = reasoningBody
		}
	}

	applyThinkingMode(requestBody, options, p.apiBase)

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed:\n  Status: %d\n  Body:   %s", resp.StatusCode, string(body))
	}

	return parseResponse(body)
}

func (p *Provider) ChatStream(ctx context.Context, messages []Message, tools []ToolDefinition, model string, options map[string]interface{}, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error) {
	if p.apiBase == "" {
		return nil, fmt.Errorf("API base not configured")
	}
	if onChunk == nil {
		return p.Chat(ctx, messages, tools, model, options)
	}

	model = normalizeModel(model, p.apiBase)

	requestBody := map[string]interface{}{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}

	if len(tools) > 0 {
		requestBody["tools"] = tools
		requestBody["tool_choice"] = "auto"
	}

	if maxTokens, ok := asInt(options["max_tokens"]); ok {
		lowerModel := strings.ToLower(model)
		if strings.Contains(lowerModel, "glm") || strings.Contains(lowerModel, "o1") || strings.Contains(lowerModel, "gpt-5") {
			requestBody["max_completion_tokens"] = maxTokens
		} else {
			requestBody["max_tokens"] = maxTokens
		}
	}

	if temperature, ok := asFloat(options["temperature"]); ok {
		lowerModel := strings.ToLower(model)
		if strings.Contains(lowerModel, "kimi") && strings.Contains(lowerModel, "k2") {
			requestBody["temperature"] = 1.0
		} else {
			requestBody["temperature"] = temperature
		}
	}

	// Handle reasoning config (for OpenAI o-series, OpenRouter, and compatible models)
	if reasoning, ok := options["reasoning"].(map[string]interface{}); ok && reasoning != nil {
		reasoningBody := map[string]interface{}{}
		if v, ok := reasoning["effort"]; ok {
			if s, ok := v.(string); ok && s != "" {
				reasoningBody["effort"] = s
			}
		}
		if maxTokens, ok := reasoning["max_tokens"].(int); ok && maxTokens > 0 {
			reasoningBody["max_tokens"] = maxTokens
		}
		if exclude, ok := reasoning["exclude"].(bool); ok {
			reasoningBody["exclude"] = exclude
		}
		if summary, ok := reasoning["summary"].(string); ok && summary != "" {
			if isOpenAIEndpoint(p.apiBase) {
				reasoningBody["summary"] = summary
			}
		}
		if enabled, ok := reasoning["enabled"].(bool); ok {
			reasoningBody["enabled"] = enabled
		}
		if len(reasoningBody) > 0 {
			requestBody["reasoning"] = reasoningBody
		}
	}

	applyThinkingMode(requestBody, options, p.apiBase)

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed(OpenIA):\n  Status: %d\n  Body:   %s\n URL: %s", resp.StatusCode, string(body), req.URL)

	}

	streamBody := common.NewIdleTimeoutReader(resp.Body, common.DefaultStreamIdleTimeout)
	return parseSSEStream(ctx, streamBody, onChunk, onReasoning)
}

func parseResponse(body []byte) (*LLMResponse, error) {
	var apiResponse struct {
		Choices []struct {
			Message struct {
				Content          string                          `json:"content"`
				ReasoningContent string                          `json:"reasoning_content"`
				Reasoning        string                          `json:"reasoning"`
				ReasoningDetails []protocoltypes.ReasoningDetail `json:"reasoning_details"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function *struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *UsageInfo `json:"usage"`
	}

	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(apiResponse.Choices) == 0 {
		return &LLMResponse{
			Content:      "",
			FinishReason: "stop",
		}, nil
	}

	choice := apiResponse.Choices[0]
	toolCalls := make([]ToolCall, 0, len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		arguments := make(map[string]interface{})
		name := ""

		if tc.Function != nil {
			name = tc.Function.Name
			arguments = common.DecodeToolCallArguments(tc.Function.Arguments, name)
		}

		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      name,
			Arguments: arguments,
		})
	}

	return &LLMResponse{
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
		Reasoning:        choice.Message.Reasoning,
		ReasoningDetails: choice.Message.ReasoningDetails,
		ToolCalls:        toolCalls,
		FinishReason:     choice.FinishReason,
		Usage:            apiResponse.Usage,
	}, nil
}

func normalizeModel(model, apiBase string) string {
	// NOTE: Provider prefix stripping ("provider:model") is handled by
	// StripProviderPrefix in llm_caller.go before calling Chat.
	// Do NOT strip ":" here — model names may legitimately contain colons
	// (e.g., Ollama tags like "qwen2.5:14b").

	// Legacy: handle old "openrouter/deepseek/..." slash format.
	// The colon format ("openrouter:deepseek/...") is already handled by
	// StripProviderPrefix upstream.
	if strings.Contains(strings.ToLower(apiBase), "openrouter.ai") {
		if idx := strings.Index(model, "/"); idx > 0 {
			if strings.HasPrefix(strings.ToLower(model), "openrouter/") {
				return model[idx+1:]
			}
		}
		return model
	}

	// For all other providers: return model as-is.
	// The deprecated "provider/model" slash format is no longer stripped here;
	// models with slashes are legitimate identifiers (e.g., "namespace/model-name").
	return model
}

// isOpenRouterEndpoint checks if the apiBase belongs to OpenRouter.
func isOpenRouterEndpoint(apiBase string) bool {
	return strings.Contains(strings.ToLower(apiBase), "openrouter.ai")
}

// isOpenAIEndpoint checks if the apiBase belongs to OpenAI directly.
func isOpenAIEndpoint(apiBase string) bool {
	lower := strings.ToLower(apiBase)
	return strings.Contains(lower, "api.openai.com") ||
		strings.Contains(lower, "openai.azure.com")
}

func asInt(v interface{}) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case float64:
		return int(val), true
	case float32:
		return int(val), true
	default:
		return 0, false
	}
}

func asFloat(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}

func parseSSEStream(ctx context.Context, body io.Reader, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error) {
	contentBuf := builderPool.Get().(*strings.Builder)
	contentBuf.Reset()
	defer builderPool.Put(contentBuf)

	reasoningBuf := builderPool.Get().(*strings.Builder)
	reasoningBuf.Reset()
	defer builderPool.Put(reasoningBuf)

	var toolCalls []protocoltypes.ToolCall
	var finishReason string
	var usage *protocoltypes.UsageInfo

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		// Check for context cancellation (e.g., user cancel) to stop processing
		// the SSE stream promptly instead of waiting for the server to close.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		line := scanner.Text()

		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function *struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *protocoltypes.UsageInfo `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		if choice.Delta.ReasoningContent != "" {
			reasoningBuf.WriteString(choice.Delta.ReasoningContent)
			if onReasoning != nil {
				onReasoning(choice.Delta.ReasoningContent)
			}
		}

		if choice.Delta.Reasoning != "" {
			reasoningBuf.WriteString(choice.Delta.Reasoning)
			if onReasoning != nil {
				onReasoning(choice.Delta.Reasoning)
			}
		}

		if choice.Delta.Content != "" {
			contentBuf.WriteString(choice.Delta.Content)
			onChunk(choice.Delta.Content, false)
		}

		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Index
			for len(toolCalls) <= idx {
				toolCalls = append(toolCalls, protocoltypes.ToolCall{})
			}
			current := &toolCalls[idx]

			if tc.ID != "" {
				current.ID = tc.ID
			}
			if tc.Type != "" {
				current.Type = tc.Type
			}
			if tc.Function != nil {
				if tc.Function.Name != "" {
					current.Name = tc.Function.Name
					current.Function = &protocoltypes.FunctionCall{
						Name: tc.Function.Name,
					}
				}
				if tc.Function.Arguments != "" {
					if current.Function == nil {
						current.Function = &protocoltypes.FunctionCall{}
					}
					current.Function.Arguments += tc.Function.Arguments
				}
			}
		}

		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}

		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}

	onChunk("", true)

	for i := range toolCalls {
		tc := &toolCalls[i]
		if tc.Function != nil && tc.Function.Arguments != "" {
			tc.Arguments = common.DecodeToolCallArguments(json.RawMessage(tc.Function.Arguments), tc.Name)
		}
	}

	return &LLMResponse{
		Content:          contentBuf.String(),
		ReasoningContent: reasoningBuf.String(),
		ToolCalls:        toolCalls,
		FinishReason:     finishReason,
		Usage:            usage,
	}, nil
}

func applyThinkingMode(requestBody map[string]interface{}, options map[string]interface{}, apiBase string) {
	thinkingEnabled, _ := options["thinking"].(bool)
	if !thinkingEnabled {
		return
	}

	// For OpenRouter, reasoning_effort belongs inside the reasoning object,
	// not at top-level. OpenRouter ignores top-level reasoning_effort.
	if isOpenRouterEndpoint(apiBase) {
		// Ensure the reasoning object exists
		if _, ok := requestBody["reasoning"]; !ok {
			requestBody["reasoning"] = map[string]interface{}{}
		}
		reasoningObj := requestBody["reasoning"].(map[string]interface{})

		if effort, ok := options["reasoning_effort"].(string); ok && effort != "" {
			reasoningObj["effort"] = effort
		} else if reasoning, ok := options["reasoning"].(map[string]interface{}); ok {
			if effort, ok := reasoning["effort"].(string); ok && effort != "" {
				reasoningObj["effort"] = effort
			}
		}

		requestBody["thinking"] = map[string]interface{}{
			"type": "enabled",
		}
		return
	}

	// Non-OpenRouter: keep original behavior (OpenAI direct, DeepSeek, etc.)
	requestBody["thinking"] = map[string]interface{}{
		"type": "enabled",
	}

	if effort, ok := options["reasoning_effort"].(string); ok && effort != "" {
		requestBody["reasoning_effort"] = effort
	} else if reasoning, ok := options["reasoning"].(map[string]interface{}); ok {
		if effort, ok := reasoning["effort"].(string); ok && effort != "" {
			requestBody["reasoning_effort"] = effort
		}
	}
}
