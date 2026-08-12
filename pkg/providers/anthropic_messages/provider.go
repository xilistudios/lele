// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package anthropicmessages

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/providers/common"
	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

type (
	ToolCall               = protocoltypes.ToolCall
	FunctionCall           = protocoltypes.FunctionCall
	LLMResponse            = protocoltypes.LLMResponse
	UsageInfo              = protocoltypes.UsageInfo
	Message                = protocoltypes.Message
	ToolDefinition         = protocoltypes.ToolDefinition
	ToolFunctionDefinition = protocoltypes.ToolFunctionDefinition
)

const (
	defaultAPIVersion     = "2023-06-01"
	defaultBaseURL        = "https://api.anthropic.com/v1"
	defaultRequestTimeout = 120 * time.Second
)

// builderPool provides reusable strings.Builder instances to reduce
// allocations during SSE stream parsing.
var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

// Provider implements Anthropic Messages API via HTTP (without SDK).
// It supports custom endpoints that use Anthropic's native message format.
type Provider struct {
	apiKey         string
	apiBase        string
	httpClient     *http.Client
	requestTimeout time.Duration
}

// NewProvider creates a new Anthropic Messages API provider.
func NewProvider(apiKey, apiBase string) *Provider {
	return NewProviderWithTimeout(apiKey, apiBase, 0)
}

// NewProviderWithTimeout creates a provider with custom request timeout.
func NewProviderWithTimeout(apiKey, apiBase string, timeoutSeconds int) *Provider {
	baseURL := normalizeBaseURL(apiBase)
	timeout := defaultRequestTimeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}

	return &Provider{
		apiKey:         apiKey,
		apiBase:        baseURL,
		httpClient:     common.NewStreamingHTTPClient(""),
		requestTimeout: timeout,
	}
}

// Chat sends messages to the Anthropic Messages API and returns the response.
func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured")
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, p.requestTimeout)
		defer cancel()
	}

	// Strip provider prefix from model name.
	// Also strips anthropic. prefix only when using official Anthropic API
	// (e.g. "aws_ant/anthropic.claude-opus-4-7" → "claude-opus-4-7")
	//model = normalizeModel(model)

	// Build request body
	requestBody, err := buildRequestBody(messages, tools, model, options)
	if err != nil {
		return nil, fmt.Errorf("building request body: %w", err)
	}

	// Serialize to JSON
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("serializing request body: %w", err)
	}

	// Build request URL
	endpointURL, err := url.JoinPath(p.apiBase, "messages")
	if err != nil {
		return nil, fmt.Errorf("building endpoint URL: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", p.apiKey)
	req.Header.Set("Anthropic-Version", defaultAPIVersion)

	// Execute request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	// Check for HTTP errors with detailed messages
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("authentication failed (401): check your API key")
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("rate limited (429): %s", string(body))
	case http.StatusBadRequest:
		return nil, fmt.Errorf("bad request (400): %s", string(body))
	case http.StatusNotFound:
		return nil, fmt.Errorf("endpoint not found (404): %s", string(body))
	case http.StatusInternalServerError:
		return nil, fmt.Errorf("internal server error (500): %s", string(body))
	case http.StatusServiceUnavailable:
		return nil, fmt.Errorf("service unavailable (503): %s", string(body))
	default:
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("API request failed:\n  Status: %d\n  Body:   %s\n URL: %s", resp.StatusCode, string(body), endpointURL)

		}
	}

	// Parse response
	return parseResponseBody(body)
}

// ChatStream sends messages to the Anthropic Messages API with streaming (SSE).
func (p *Provider) ChatStream(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
	onChunk func(chunk string, done bool),
	onReasoning func(reasoningChunk string),
) (*LLMResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured")
	}
	if onChunk == nil {
		return p.Chat(ctx, messages, tools, model, options)
	}

	// Build request body with streaming enabled
	requestBody, err := buildRequestBody(messages, tools, model, options)
	if err != nil {
		return nil, fmt.Errorf("building request body: %w", err)
	}
	requestBody["stream"] = true

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("serializing request body: %w", err)
	}

	endpointURL, err := url.JoinPath(p.apiBase, "messages")
	if err != nil {
		return nil, fmt.Errorf("building endpoint URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpointURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", p.apiKey)
	req.Header.Set("Anthropic-Version", defaultAPIVersion)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed:\n  Status: %d\n  Body:   %s\n URL: %s", resp.StatusCode, string(body), endpointURL)
	}

	streamBody := common.NewIdleTimeoutReader(resp.Body, common.DefaultStreamIdleTimeout)
	return parseAnthropicSSEStream(ctx, streamBody, onChunk, onReasoning)
}

// GetDefaultModel returns the default model for this provider.
func (p *Provider) GetDefaultModel() string {
	return "claude-sonnet-4.6"
}

// buildRequestBody converts internal message format to Anthropic Messages API format.
func buildRequestBody(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (map[string]any, error) {
	// max_tokens is required and guaranteed by agent loop
	maxTokens, ok := asInt(options["max_tokens"])
	if !ok {
		return nil, fmt.Errorf("max_tokens is required in options")
	}

	result := map[string]any{
		"model":      model,
		"max_tokens": int64(maxTokens),
		"messages":   []any{},
	}

	// Add thinking config for models with reasoning enabled
	if reasonOpts, hasReasoning := options["reasoning"].(map[string]any); hasReasoning {
		if enabled, _ := reasonOpts["enabled"].(bool); enabled {
			thinking := map[string]any{
				"type": "adaptive",
			}
			result["thinking"] = thinking

			effort := "high" // default
			if e, ok := reasonOpts["effort"].(string); ok && e != "" {
				effort = e
			}
			result["output_config"] = map[string]any{
				"effort": effort,
			}
		}
	}

	// Process messages
	var systemPrompt string
	var apiMessages []any

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			// Accumulate system messages
			if systemPrompt != "" {
				systemPrompt += "\n\n" + msg.Content
			} else {
				systemPrompt = msg.Content
			}

		case "user":
			if msg.ToolCallID != "" {
				// Tool result message — merge into previous user message if it contains tool_results
				toolResultBlock := map[string]any{
					"type":        "tool_result",
					"tool_use_id": common.SanitizeToolCallID(msg.ToolCallID),
					"content":     msg.Content,
				}
				if len(apiMessages) > 0 {
					if prev, ok := apiMessages[len(apiMessages)-1].(map[string]any); ok && prev["role"] == "user" {
						if content, ok := prev["content"].([]map[string]any); ok {
							prev["content"] = append(content, toolResultBlock)
							continue
						}
					}
				}
				apiMessages = append(apiMessages, map[string]any{
					"role":    "user",
					"content": []map[string]any{toolResultBlock},
				})
			} else {
				// Regular user message
				apiMessages = append(apiMessages, map[string]any{
					"role":    "user",
					"content": msg.Content,
				})
			}

		case "assistant":
			content := []any{}

			// Add text content if present
			if msg.Content != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": msg.Content,
				})
			}

			// Add tool_use blocks
			for _, tc := range msg.ToolCalls {
				if strings.TrimSpace(tc.Name) == "" {
					continue
				}

				// Handle nil Arguments (GLM-4 may return null input)
				input := tc.Arguments
				if input == nil {
					input = map[string]any{}
				}

				toolUse := map[string]any{
					"type":  "tool_use",
					"id":    common.SanitizeToolCallID(tc.ID),
					"name":  tc.Name,
					"input": input,
				}
				content = append(content, toolUse)
			}

			apiMessages = append(apiMessages, map[string]any{
				"role":    "assistant",
				"content": content,
			})

		case "tool":
			// Tool result (alternative format) — merge into previous user message if it contains tool_results
			toolResultBlock := map[string]any{
				"type":        "tool_result",
				"tool_use_id": common.SanitizeToolCallID(msg.ToolCallID),
				"content":     msg.Content,
			}
			if len(apiMessages) > 0 {
				if prev, ok := apiMessages[len(apiMessages)-1].(map[string]any); ok && prev["role"] == "user" {
					if content, ok := prev["content"].([]map[string]any); ok {
						prev["content"] = append(content, toolResultBlock)
						continue
					}
				}
			}
			apiMessages = append(apiMessages, map[string]any{
				"role":    "user",
				"content": []map[string]any{toolResultBlock},
			})
		}
	}

	result["messages"] = apiMessages

	// Set system prompt if present
	if systemPrompt != "" {
		result["system"] = systemPrompt
	}

	// Add tools if present
	if len(tools) > 0 {
		result["tools"] = buildTools(tools)
	}

	return result, nil
}

// buildTools converts tool definitions to Anthropic format.
func buildTools(tools []ToolDefinition) []any {
	result := make([]any, len(tools))
	for i, tool := range tools {
		toolDef := map[string]any{
			"name":         tool.Function.Name,
			"description":  tool.Function.Description,
			"input_schema": tool.Function.Parameters,
		}
		result[i] = toolDef
	}
	return result
}

// parseResponseBody parses Anthropic Messages API response.
func parseResponseBody(body []byte) (*LLMResponse, error) {
	var resp anthropicMessageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing JSON response: %w", err)
	}

	// Extract content and tool calls
	var content strings.Builder
	toolCalls := make([]ToolCall, 0) // Initialize as empty slice (not nil) for consistent JSON serialization

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
				Function: &FunctionCall{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}

	// Map stop_reason
	finishReason := "stop"
	switch resp.StopReason {
	case "tool_use":
		finishReason = "tool_calls"
	case "max_tokens":
		finishReason = "length"
	case "end_turn":
		finishReason = "stop"
	case "stop_sequence":
		finishReason = "stop"
	}

	return &LLMResponse{
		Content:      content.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage: &UsageInfo{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
			TotalTokens:      int(resp.Usage.InputTokens + resp.Usage.OutputTokens),
		},
	}, nil
}

// parseAnthropicSSEStream parses the Anthropic SSE streaming response.
// Events: message_start, content_block_start, content_block_delta, content_block_stop, message_delta, message_stop, ping
func parseAnthropicSSEStream(ctx context.Context, body io.Reader, onChunk func(chunk string, done bool), onReasoning func(reasoningChunk string)) (*LLMResponse, error) {
	contentBuf := builderPool.Get().(*strings.Builder)
	contentBuf.Reset()
	defer builderPool.Put(contentBuf)

	reasoningBuf := builderPool.Get().(*strings.Builder)
	reasoningBuf.Reset()
	defer builderPool.Put(reasoningBuf)

	var toolCalls []ToolCall
	var finishReason string
	var usage *UsageInfo

	// Track content blocks by index for tool use accumulation
	type blockState struct {
		blockType string // "text", "thinking", "tool_use"
		id        string
		name      string
		inputJSON strings.Builder
	}
	blocks := make(map[int]*blockState)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		line := scanner.Text()

		// SSE format: "event: <type>" followed by "data: <json>"
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

		var event struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
				Text string `json:"text"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Message struct {
				Usage struct {
					InputTokens  int64 `json:"input_tokens"`
					OutputTokens int64 `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			Usage *struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}

		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		switch event.Type {
		case "message_start":
			if event.Message.Usage.InputTokens > 0 || event.Message.Usage.OutputTokens > 0 {
				usage = &UsageInfo{
					PromptTokens:     int(event.Message.Usage.InputTokens),
					CompletionTokens: int(event.Message.Usage.OutputTokens),
					TotalTokens:      int(event.Message.Usage.InputTokens + event.Message.Usage.OutputTokens),
				}
			}

		case "content_block_start":
			bs := &blockState{blockType: event.ContentBlock.Type}
			switch event.ContentBlock.Type {
			case "text":
				// text blocks start empty
			case "thinking":
				// thinking blocks accumulate in reasoningBuf
			case "tool_use":
				bs.id = event.ContentBlock.ID
				bs.name = event.ContentBlock.Name
			}
			blocks[event.Index] = bs

		case "content_block_delta":
			bs := blocks[event.Index]
			if bs == nil {
				continue
			}
			switch event.Delta.Type {
			case "text_delta":
				contentBuf.WriteString(event.Delta.Text)
				onChunk(event.Delta.Text, false)
			case "thinking_delta":
				reasoningBuf.WriteString(event.Delta.Thinking)
				if onReasoning != nil {
					onReasoning(event.Delta.Thinking)
				}
			case "input_json_delta":
				bs.inputJSON.WriteString(event.Delta.PartialJSON)
			}

		case "content_block_stop":
			bs := blocks[event.Index]
			if bs == nil {
				continue
			}
			if bs.blockType == "tool_use" {
				argsJSON := bs.inputJSON.String()
				toolCalls = append(toolCalls, ToolCall{
					ID:        bs.id,
					Name:      bs.name,
					Arguments: common.DecodeToolCallArguments(json.RawMessage(argsJSON), bs.name),
					Function: &FunctionCall{
						Name:      bs.name,
						Arguments: argsJSON,
					},
				})
			}

		case "message_delta":
			if event.Delta.StopReason != "" {
				finishReason = mapStopReason(event.Delta.StopReason)
			}
			if event.Usage != nil {
				if usage == nil {
					usage = &UsageInfo{}
				}
				usage.CompletionTokens = int(event.Usage.OutputTokens)
				usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
			}

		case "message_stop":
			// Stream complete
		}
	}

	onChunk("", true) // Signal completion

	// If we didn't get usage from events, there's none available
	if usage == nil {
		usage = &UsageInfo{}
	}

	return &LLMResponse{
		Content:      contentBuf.String(),
		Reasoning:    reasoningBuf.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}

// normalizeBaseURL ensures the base URL is properly formatted.
// It removes /v1 suffix if present (to avoid duplication) and always appends /v1.
// This handles edge cases like "https://api.example.com/v1/proxy" correctly.
func normalizeBaseURL(apiBase string) string {
	base := strings.TrimSpace(apiBase)
	if base == "" {
		return defaultBaseURL
	}

	// Remove trailing slashes
	base = strings.TrimRight(base, "/")

	// Remove /v1 suffix if present (will be re-added)
	// This prevents duplication for URLs like "https://api.example.com/v1/proxy"
	if before, ok := strings.CutSuffix(base, "/v1"); ok {
		base = before
	}

	// Ensure we don't have an empty string after cutting
	if base == "" {
		return defaultBaseURL
	}

	// Add /v1 suffix (required by Anthropic Messages API)
	return base + "/v1"
}

// Helper functions for type conversion

func mapStopReason(anthropicStopReason string) string {
	switch anthropicStopReason {
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "end_turn", "stop_sequence":
		return "stop"
	default:
		return anthropicStopReason
	}
}

// normalizeModel strips provider and AWS Bedrock prefixes from model names.
// AWS Bedrock model IDs (e.g. "anthropic.claude-opus-4-7") include an "anthropic."
// prefix that is not part of the Anthropic Messages API model namespace.
// Bedrock proxy endpoints (e.g. bedrock-mantle) normalize this internally for
// non-streaming requests, but pass it through unchanged for SSE streaming,
// causing "Not supported model" errors. Always stripping is safe because all
// Anthropic Messages API-compatible endpoints use the base model name.
//
// Examples:
//   - "aws_ant/anthropic.claude-opus-4-7" → "claude-opus-4-7"
//   - "anthropic.claude-opus-4-7" → "claude-opus-4-7"
//   - "openrouter/deepseek/deepseek-chat" → "deepseek/deepseek-chat"
func normalizeModel(model string) string {
	if idx := strings.Index(model, "/"); idx >= 0 {
		model = model[idx+1:]
	}
	// Strip anthropic. prefix (AWS Bedrock model ID convention, not used by Anthropic API)
	model = strings.TrimPrefix(model, "anthropic.")
	return model
}

func asInt(v any) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case float64:
		return int(val), true
	case int64:
		return int(val), true
	default:
		return 0, false
	}
}

func asFloat(v any) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	default:
		return 0, false
	}
}

// Anthropic API response structures

type anthropicMessageResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []contentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Model      string         `json:"model"`
	Usage      usageInfo      `json:"usage"`
}

type contentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text,omitempty"`
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
}

type usageInfo struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}
