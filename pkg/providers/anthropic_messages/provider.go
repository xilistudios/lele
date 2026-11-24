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

const noContentPlaceholder = "[no content]"

const (
	defaultAPIVersion     = "2023-06-01"
	defaultBaseURL        = "https://api.anthropic.com/v1"
	defaultRequestTimeout = 120 * time.Second
)

var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

type Provider struct {
	apiKey                string
	apiBase               string
	httpClient            *http.Client
	requestTimeout        time.Duration
	sessionHeader         string
	sessionHeaderRequired bool
	customHeaders         map[string]string
}

type Options struct {
	SessionHeader         string
	SessionHeaderRequired bool
	Headers               map[string]string
}

func NewProvider(apiKey, apiBase string) *Provider {
	return NewProviderWithTimeout(apiKey, apiBase, 0)
}

func NewProviderWithTimeout(apiKey, apiBase string, timeoutSeconds int) *Provider {
	return newProvider(apiKey, apiBase, timeoutSeconds, Options{})
}

func NewProviderWithOptions(apiKey, apiBase string, opts Options) *Provider {
	return newProvider(apiKey, apiBase, 0, opts)
}

func newProvider(apiKey, apiBase string, timeoutSeconds int, opts Options) *Provider {
	baseURL := normalizeBaseURL(apiBase)
	timeout := defaultRequestTimeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}

	return &Provider{
		apiKey:                apiKey,
		apiBase:               baseURL,
		httpClient:            common.NewStreamingHTTPClient(""),
		requestTimeout:        timeout,
		sessionHeader:         opts.SessionHeader,
		sessionHeaderRequired: opts.SessionHeaderRequired,
		customHeaders:         opts.Headers,
	}
}

func (p *Provider) setRequestHeaders(req *http.Request, options map[string]any) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", common.UserAgent())
	req.Header.Set("X-Api-Key", p.apiKey)
	req.Header.Set("Anthropic-Version", defaultAPIVersion)

	if p.sessionHeader != "" {
		sessionID := protocoltypes.WireSessionFromOptions(options)
		switch {
		case sessionID != "":
			req.Header.Set(p.sessionHeader, sessionID)
		case p.sessionHeaderRequired:
			return fmt.Errorf("%w: this provider requires %q and the call carried no %q option (a call site is missing providers.WithWireSessionID)",
				common.ErrMissingWireSession, p.sessionHeader, protocoltypes.OptWireSessionID)
		default:
		}
	}

	if len(p.customHeaders) == 0 {
		return nil
	}
	sessionID := protocoltypes.WireSessionForHeaders(options)
	for name, value := range p.customHeaders {
		req.Header.Set(name, protocoltypes.ExpandHeaderValue(value, sessionID))
	}
	return nil
}

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

	requestBody, err := buildRequestBody(messages, tools, model, options)
	if err != nil {
		return nil, fmt.Errorf("building request body: %w", err)
	}

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

	if err := p.setRequestHeaders(req, options); err != nil {
		return nil, fmt.Errorf("setting request headers: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := common.ReadLimitedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		return parseResponseBody(body)
	}

	apiErr := common.NewAPIError(resp, body, endpointURL)
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		apiErr.Message = "authentication failed (401): check your API key"
	case http.StatusTooManyRequests:
		apiErr.Message = fmt.Sprintf("rate limited (429): %s", string(body))
	case http.StatusBadRequest:
		apiErr.Message = fmt.Sprintf("bad request (400): %s", string(body))
	case http.StatusNotFound:
		apiErr.Message = fmt.Sprintf("endpoint not found (404): %s", string(body))
	case http.StatusInternalServerError:
		apiErr.Message = fmt.Sprintf("internal server error (500): %s", string(body))
	case http.StatusServiceUnavailable:
		apiErr.Message = fmt.Sprintf("service unavailable (503): %s", string(body))
	}

	return nil, apiErr
}

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

	if err := p.setRequestHeaders(req, options); err != nil {
		return nil, fmt.Errorf("setting request headers: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := common.ReadLimitedBody(resp.Body)
		return nil, common.NewAPIError(resp, body, endpointURL)
	}

	streamBody := common.NewIdleTimeoutReader(resp.Body, common.DefaultStreamIdleTimeout)
	return parseAnthropicSSEStream(ctx, streamBody, onChunk, onReasoning)
}

func (p *Provider) GetDefaultModel() string {
	return "claude-sonnet-4.6"
}

func buildRequestBody(
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (map[string]any, error) {
	maxTokens, ok := asInt(options["max_tokens"])
	if !ok {
		return nil, fmt.Errorf("max_tokens is required in options")
	}

	result := map[string]any{
		"model":      model,
		"max_tokens": int64(maxTokens),
		"messages":   []any{},
	}

	if reasonOpts, hasReasoning := options["reasoning"].(map[string]any); hasReasoning {
		if enabled, isSet := reasonOpts["enabled"].(bool); isSet {
			if enabled {
				result["thinking"] = map[string]any{
					"type": "adaptive",
				}

				effort := "high"
				if e, ok := reasonOpts["effort"].(string); ok && e != "" {
					effort = e
				}
				result["output_config"] = map[string]any{
					"effort": effort,
				}
			} else {
				result["thinking"] = map[string]any{
					"type": "disabled",
				}
			}
		}
	}

	var systemPrompt string
	var apiMessages []any

	for _, msg := range messages {
		switch msg.Role {
		case "system":
			if systemPrompt != "" {
				systemPrompt += "\n\n" + msg.Content
			} else {
				systemPrompt = msg.Content
			}

		case "user":
			if msg.ToolCallID != "" {
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
				apiMessages = append(apiMessages, map[string]any{
					"role":    "user",
					"content": buildUserContent(msg),
				})
			}

		case "assistant":
			content := []any{}

			if msg.Content != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": msg.Content,
				})
			}

			for _, tc := range msg.ToolCalls {
				if strings.TrimSpace(tc.Name) == "" {
					continue
				}

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

			if len(content) == 0 {
				continue
			}

			apiMessages = append(apiMessages, map[string]any{
				"role":    "assistant",
				"content": content,
			})

		case "tool":
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

	if systemPrompt != "" {
		result["system"] = systemPrompt
	}

	if len(tools) > 0 {
		result["tools"] = buildTools(tools)
	}

	if protocoltypes.CacheEnabled(options) {
		applyPromptCacheBreakpoints(result, protocoltypes.CacheTTLOptions(options))
	}

	return result, nil
}

func buildUserContent(msg Message) any {
	if len(msg.ContentParts) == 0 && len(msg.Media) == 0 {
		if strings.TrimSpace(msg.Content) == "" {
			return []map[string]any{textContentBlock(noContentPlaceholder)}
		}
		return msg.Content
	}
	return buildUserContentBlocks(msg)
}

func buildUserContentBlocks(msg Message) []map[string]any {
	var blocks []map[string]any

	if len(msg.ContentParts) > 0 {
		for _, part := range msg.ContentParts {
			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) != "" {
					blocks = append(blocks, textContentBlock(part.Text))
				}
			case "image_url":
				if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
					continue
				}
				if mediaType, encoded, ok := splitDataURL(part.ImageURL.URL); ok {
					blocks = append(blocks, imageContentBlock(mediaType, encoded))
					continue
				}
				blocks = append(blocks, textContentBlock("[image: "+strings.TrimSpace(part.ImageURL.URL)+"]"))
			case "video_url":
				if part.VideoURL == nil || strings.TrimSpace(part.VideoURL.URL) == "" {
					continue
				}
				blocks = append(blocks, textContentBlock("[video: "+strings.TrimSpace(part.VideoURL.URL)+"]"))
			}
		}
	} else {
		for _, mediaURL := range msg.Media {
			if mediaType, encoded, ok := splitDataURL(mediaURL); ok && strings.HasPrefix(mediaType, "image/") {
				blocks = append(blocks, imageContentBlock(mediaType, encoded))
				continue
			}
			blocks = append(blocks, textContentBlock(mediaFallbackText(mediaURL)))
		}

		if strings.TrimSpace(msg.Content) != "" {
			blocks = append(blocks, textContentBlock(msg.Content))
		}
	}

	if len(blocks) == 0 {
		blocks = append(blocks, textContentBlock(noContentPlaceholder))
	}

	return blocks
}

func mediaFallbackText(mediaURL string) string {
	if mediaType, _, ok := splitDataURL(mediaURL); ok {
		return "[media: " + mediaType + "]"
	}
	return "[media: " + strings.TrimSpace(mediaURL) + "]"
}

func textContentBlock(text string) map[string]any {
	return map[string]any{
		"type": "text",
		"text": text,
	}
}

func imageContentBlock(mediaType, encoded string) map[string]any {
	return map[string]any{
		"type": "image",
		"source": map[string]any{
			"type":       "base64",
			"media_type": mediaType,
			"data":       encoded,
		},
	}
}

func splitDataURL(raw string) (mediaType string, encoded string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "data:") {
		return "", "", false
	}
	comma := strings.Index(trimmed, ",")
	if comma < 0 {
		return "", "", false
	}
	header := trimmed[5:comma]
	if !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return "", "", false
	}
	mediaType = strings.TrimSuffix(header, ";base64")
	encoded = trimmed[comma+1:]
	if mediaType == "" || encoded == "" {
		return "", "", false
	}
	return mediaType, encoded, true
}

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

func parseResponseBody(body []byte) (*LLMResponse, error) {
	var resp anthropicMessageResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing JSON response: %w", err)
	}

	var content strings.Builder
	toolCalls := make([]ToolCall, 0)

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
			PromptTokens:             int(resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens),
			CompletionTokens:         int(resp.Usage.OutputTokens),
			TotalTokens:              int(resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens + resp.Usage.OutputTokens),
			CacheReadInputTokens:     int(resp.Usage.CacheReadInputTokens),
			CacheCreationInputTokens: int(resp.Usage.CacheCreationInputTokens),
		},
	}, nil
}

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

	type blockState struct {
		blockType string
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
					InputTokens              int64 `json:"input_tokens"`
					OutputTokens             int64 `json:"output_tokens"`
					CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
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
			u := event.Message.Usage
			if u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheReadInputTokens > 0 || u.CacheCreationInputTokens > 0 {
				usage = &UsageInfo{
					PromptTokens:             int(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens),
					CompletionTokens:         int(u.OutputTokens),
					TotalTokens:              int(u.InputTokens + u.OutputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens),
					CacheReadInputTokens:     int(u.CacheReadInputTokens),
					CacheCreationInputTokens: int(u.CacheCreationInputTokens),
				}
			}

		case "content_block_start":
			bs := &blockState{blockType: event.ContentBlock.Type}
			switch event.ContentBlock.Type {
			case "text":
			case "thinking":
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
				arguments, truncated := common.DecodeToolCallArgumentsTruncated(json.RawMessage(argsJSON), bs.name)
				toolCalls = append(toolCalls, ToolCall{
					ID:                 bs.id,
					Name:               bs.name,
					Arguments:          arguments,
					ArgumentsTruncated: truncated,
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
		}
	}

	if err := scanner.Err(); err != nil {
		return &LLMResponse{
			Content:      contentBuf.String(),
			Reasoning:    reasoningBuf.String(),
			ToolCalls:    toolCalls,
			FinishReason: finishReason,
			Usage:        usage,
		}, common.StreamScanError(err)
	}

	onChunk("", true)

	return &LLMResponse{
		Content:      contentBuf.String(),
		Reasoning:    reasoningBuf.String(),
		ToolCalls:    toolCalls,
		FinishReason: finishReason,
		Usage:        usage,
	}, nil
}

func normalizeBaseURL(apiBase string) string {
	base := strings.TrimSpace(apiBase)
	if base == "" {
		return defaultBaseURL
	}

	base = strings.TrimRight(base, "/")

	if before, ok := strings.CutSuffix(base, "/v1"); ok {
		base = before
	}

	if base == "" {
		return defaultBaseURL
	}

	return base + "/v1"
}

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

func normalizeModel(model string) string {
	if idx := strings.Index(model, "/"); idx >= 0 {
		model = model[idx+1:]
	}
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
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}
