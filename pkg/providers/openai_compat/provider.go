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
		// Kimi k2 models only support temperature=1.
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
		// Structured error: keeps the historical message text (no URL line, no
		// body truncation - both are this provider's observable behaviour) but
		// carries the status code and the server's Retry-After hint so the
		// classifier does not have to rebuild them from the message string.
		// See common.APIError.
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
		// Structured error, same as Chat(). The old message carried a
		// "(OpenIA)" typo in its first line; nothing parses that string (only
		// the "Status: %d" pattern does, which the canonical format preserves),
		// so the two error paths now emit one identical shape.
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
			chunk.Usage.NormalizeCacheUsage()
			usage = chunk.Usage
		}
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

// Thinking wire styles accepted in options["thinking_type"], kept in sync with
// config.ThinkingType* (per-model config: providers.<name>.models.<alias>.thinking_type).
const (
	thinkingStyleAuto       = "auto"
	thinkingStyleDeepSeek   = "deepseek"
	thinkingStyleOpenAI     = "openai"
	thinkingStyleOpenRouter = "openrouter"
	thinkingStyleQwen       = "qwen"
	thinkingStyleNone       = "none"
)

// resolveThinkingStyle picks the wire dialect for reasoning params. An explicit
// options["thinking_type"] wins; "auto"/absent/unknown keeps the legacy
// behavior decided by endpoint and model-name heuristics.
func resolveThinkingStyle(options map[string]interface{}) string {
	if tt, ok := options["thinking_type"].(string); ok {
		switch v := strings.ToLower(strings.TrimSpace(tt)); v {
		case thinkingStyleDeepSeek, thinkingStyleOpenAI, thinkingStyleOpenRouter, thinkingStyleQwen, thinkingStyleNone:
			return v
		}
	}
	return thinkingStyleAuto
}

// reasoningState reports the explicit thinking on/off request carried in the
// options, if any. options["reasoning"]["enabled"] wins, an explicit effort
// implies enabled, and the legacy options["thinking"] bool is honored last.
// ok=false means the caller has no opinion and the model default applies.
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

// reasoningEffort returns the requested effort level from the legacy
// options["reasoning_effort"] or the agent's options["reasoning"]["effort"].
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

// thinkingObjectModelFragments lists model-name fragments for families whose
// endpoints use DeepSeek's `thinking: {"type": ...}` object and default
// thinking ON (so "off" must send an explicit disable).
var thinkingObjectModelFragments = []string{"deepseek", "mimo", "glm"}

// isThinkingObjectModel reports whether a model name belongs to a known
// thinking-object family (DeepSeek, Xiaomi MiMo, Zhipu GLM).
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

// applyReasoningOptions translates the agent's reasoning options into the
// wire-level "think system" parameters for the target endpoint. The dialect is
// selected by options["thinking_type"] (per-model config field
// providers.<name>.models.<alias>.thinking_type); "auto" keeps the legacy
// behavior. This is the single translation point shared by Chat and ChatStream.
func applyReasoningOptions(requestBody map[string]interface{}, options map[string]interface{}, apiBase string) {
	switch resolveThinkingStyle(options) {
	case thinkingStyleNone:
		// The model has no thinking switch — never send thinking params.

	case thinkingStyleDeepSeek:
		// DeepSeek V3.2+, Xiaomi MiMo, Zhipu GLM-4.5+: `thinking` object with
		// an explicit type. These endpoints default thinking ON, so "off" must
		// send the disable or reasoning keeps running.
		if enabled, ok := reasoningState(options); ok {
			typ := "disabled"
			if enabled {
				typ = "enabled"
			}
			requestBody["thinking"] = map[string]interface{}{"type": typ}
		}

	case thinkingStyleQwen:
		// Alibaba DashScope / Qwen3 compatible-mode: enable_thinking bool.
		if enabled, ok := reasoningState(options); ok {
			requestBody["enable_thinking"] = enabled
		}

	case thinkingStyleOpenAI:
		// OpenAI o-series / gpt-5: top-level reasoning_effort ("none" disables
		// on gpt-5.1+; models without a disable switch reject it — use
		// thinking_type "none" for those).
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
		// OpenRouter: everything lives inside the `reasoning` object.
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

	default: // thinkingStyleAuto: legacy behavior.
		applyLegacyReasoningBody(requestBody, options, apiBase)
		applyThinkingMode(requestBody, options, apiBase)
		// Thinking-object families (DeepSeek/MiMo/GLM) default thinking ON and
		// ignore the `reasoning` object, so an explicit disable must go out as
		// `thinking: {"type": "disabled"}` or "off" silently does nothing.
		if enabled, ok := reasoningState(options); ok && !enabled && isThinkingObjectModel(requestBody["model"]) {
			requestBody["thinking"] = map[string]interface{}{"type": "disabled"}
		}
	}
}

// applyLegacyReasoningBody is the pre-thinking_type behavior: every field in
// options["reasoning"] is forwarded into a top-level `reasoning` object
// (summary only for OpenAI endpoints).
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
		// summary is OpenAI-specific; only send to OpenAI API endpoints
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

// applyThinkingMode is the legacy (auto) thinking adapter: it only acts when
// options["thinking"] is true, mapping effort to the right wire position for
// OpenRouter vs. other endpoints. Kept for the auto path; explicit thinking
// types are handled by applyReasoningOptions instead.
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
