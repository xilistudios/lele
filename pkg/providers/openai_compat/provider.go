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

var builderPool = sync.Pool{
	New: func() interface{} {
		return &strings.Builder{}
	},
}

type Provider struct {
	apiKey                string
	apiBase               string
	httpClient            *http.Client
	sessionHeader         string
	sessionHeaderRequired bool
	customHeaders         map[string]string
}

type Options struct {
	SessionHeader         string
	SessionHeaderRequired bool
	Headers               map[string]string
}

func NewProvider(apiKey, apiBase, proxy string) *Provider {
	return NewProviderWithOptions(apiKey, apiBase, proxy, Options{})
}

func NewProviderWithOptions(apiKey, apiBase, proxy string, opts Options) *Provider {
	client := common.NewStreamingHTTPClient(proxy)

	return &Provider{
		apiKey:                apiKey,
		apiBase:               strings.TrimRight(apiBase, "/"),
		httpClient:            client,
		sessionHeader:         opts.SessionHeader,
		sessionHeaderRequired: opts.SessionHeaderRequired,
		customHeaders:         opts.Headers,
	}
}

func (p *Provider) setRequestHeaders(req *http.Request, options map[string]interface{}) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", common.UserAgent())
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

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
		"messages": common.SerializeMessages(messages),
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

	applyReasoningOptions(requestBody, options, p.apiBase)

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := p.setRequestHeaders(req, options); err != nil {
		return nil, fmt.Errorf("setting request headers: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := common.ReadLimitedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, common.NewAPIError(resp, body, "")
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
		"messages": common.SerializeMessages(messages),
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

	applyReasoningOptions(requestBody, options, p.apiBase)

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.apiBase+"/chat/completions", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := p.setRequestHeaders(req, options); err != nil {
		return nil, fmt.Errorf("setting request headers: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := common.ReadLimitedBody(resp.Body)
		return nil, common.NewAPIError(resp, body, req.URL.String())

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

		truncated := false
		if tc.Function != nil {
			name = tc.Function.Name
			arguments, truncated = common.DecodeToolCallArgumentsTruncated(tc.Function.Arguments, name)
		}

		toolCalls = append(toolCalls, ToolCall{
			ID:                 tc.ID,
			Name:               name,
			Arguments:          arguments,
			ArgumentsTruncated: truncated,
		})
	}

	apiResponse.Usage.NormalizeCacheUsage()

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

	if strings.Contains(strings.ToLower(apiBase), "openrouter.ai") {
		if idx := strings.Index(model, "/"); idx > 0 {
			if strings.HasPrefix(strings.ToLower(model), "openrouter/") {
				return model[idx+1:]
			}
		}
		return model
	}

	return model
}

func isOpenRouterEndpoint(apiBase string) bool {
	return strings.Contains(strings.ToLower(apiBase), "openrouter.ai")
}

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
			chunk.Usage.NormalizeCacheUsage()
			usage = chunk.Usage
		}
	}

	if err := scanner.Err(); err != nil {
		return &LLMResponse{
			Content:          contentBuf.String(),
			ReasoningContent: reasoningBuf.String(),
			ToolCalls:        toolCalls,
			FinishReason:     finishReason,
			Usage:            usage,
		}, common.StreamScanError(err)
	}

	onChunk("", true)

	for i := range toolCalls {
		tc := &toolCalls[i]
		if tc.Function != nil && tc.Function.Arguments != "" {
			arguments, truncated := common.DecodeToolCallArgumentsTruncated(json.RawMessage(tc.Function.Arguments), tc.Name)
			tc.Arguments = arguments
			tc.ArgumentsTruncated = truncated
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

const (
	thinkingStyleAuto       = "auto"
	thinkingStyleDeepSeek   = "deepseek"
	thinkingStyleOpenAI     = "openai"
	thinkingStyleOpenRouter = "openrouter"
	thinkingStyleQwen       = "qwen"
	thinkingStyleNone       = "none"
)

func resolveThinkingStyle(options map[string]interface{}) string {
	if tt, ok := options["thinking_type"].(string); ok {
		switch v := strings.ToLower(strings.TrimSpace(tt)); v {
		case thinkingStyleDeepSeek, thinkingStyleOpenAI, thinkingStyleOpenRouter, thinkingStyleQwen, thinkingStyleNone:
			return v
		}
	}
	return thinkingStyleAuto
}

func reasoningState(options map[string]interface{}) (enabled bool, ok bool) {
	if r, ok := options["reasoning"].(map[string]interface{}); ok {
		if e, ok := r["enabled"].(bool); ok {
			return e, true
		}
		if effort, ok := r["effort"].(string); ok && effort != "" {
			return true, true
		}
	}
	if t, ok := options["thinking"].(bool); ok && t {
		return true, true
	}
	return false, false
}

func reasoningEffort(options map[string]interface{}) string {
	if effort, ok := options["reasoning_effort"].(string); ok && effort != "" {
		return effort
	}
	if reasoning, ok := options["reasoning"].(map[string]interface{}); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			return effort
		}
	}
	return ""
}

var thinkingObjectModelFragments = []string{"deepseek", "mimo", "glm"}

func isThinkingObjectModel(model interface{}) bool {
	m, ok := model.(string)
	if !ok {
		return false
	}
	lower := strings.ToLower(m)
	for _, frag := range thinkingObjectModelFragments {
		if strings.Contains(lower, frag) {
			return true
		}
	}
	return false
}

func applyReasoningOptions(requestBody map[string]interface{}, options map[string]interface{}, apiBase string) {
	switch resolveThinkingStyle(options) {
	case thinkingStyleNone:

	case thinkingStyleDeepSeek:
		if enabled, ok := reasoningState(options); ok {
			typ := "disabled"
			if enabled {
				typ = "enabled"
			}
			requestBody["thinking"] = map[string]interface{}{"type": typ}
		}

	case thinkingStyleQwen:
		if enabled, ok := reasoningState(options); ok {
			requestBody["enable_thinking"] = enabled
		}

	case thinkingStyleOpenAI:
		if enabled, ok := reasoningState(options); ok && !enabled {
			requestBody["reasoning_effort"] = "none"
			return
		}
		if effort := reasoningEffort(options); effort != "" {
			requestBody["reasoning_effort"] = effort
		}
		if reasoning, ok := options["reasoning"].(map[string]interface{}); ok {
			if summary, ok := reasoning["summary"].(string); ok && summary != "" && isOpenAIEndpoint(apiBase) {
				requestBody["reasoning"] = map[string]interface{}{"summary": summary}
			}
		}

	case thinkingStyleOpenRouter:
		if reasoning, ok := options["reasoning"].(map[string]interface{}); ok && reasoning != nil {
			reasoningBody := map[string]interface{}{}
			if effort := reasoningEffort(options); effort != "" {
				reasoningBody["effort"] = effort
			}
			if maxTokens, ok := reasoning["max_tokens"].(int); ok && maxTokens > 0 {
				reasoningBody["max_tokens"] = maxTokens
			}
			if exclude, ok := reasoning["exclude"].(bool); ok {
				reasoningBody["exclude"] = exclude
			}
			if enabled, ok := reasoning["enabled"].(bool); ok {
				reasoningBody["enabled"] = enabled
			}
			if len(reasoningBody) > 0 {
				requestBody["reasoning"] = reasoningBody
			}
		}

	default:
		applyLegacyReasoningBody(requestBody, options, apiBase)
		applyThinkingMode(requestBody, options, apiBase)
		if enabled, ok := reasoningState(options); ok && !enabled && isThinkingObjectModel(requestBody["model"]) {
			requestBody["thinking"] = map[string]interface{}{"type": "disabled"}
		}
	}
}

func applyLegacyReasoningBody(requestBody map[string]interface{}, options map[string]interface{}, apiBase string) {
	reasoning, ok := options["reasoning"].(map[string]interface{})
	if !ok || reasoning == nil {
		return
	}
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
		if isOpenAIEndpoint(apiBase) {
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

func applyThinkingMode(requestBody map[string]interface{}, options map[string]interface{}, apiBase string) {
	thinkingEnabled, _ := options["thinking"].(bool)
	if !thinkingEnabled {
		return
	}

	if isOpenRouterEndpoint(apiBase) {
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
