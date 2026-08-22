package openai_compat

import "testing"

// TestApplyThinkingMode_ThinkingDisabled covers the early-return branch.
func TestApplyThinkingMode_ThinkingDisabled(t *testing.T) {
	requestBody := map[string]interface{}{}
	options := map[string]interface{}{"thinking": false}
	applyThinkingMode(requestBody, options, "https://openrouter.ai/api/v1")
	if len(requestBody) != 0 {
		t.Errorf("requestBody should be unchanged, got %#v", requestBody)
	}
}

// TestApplyThinkingMode_OpenRouterReasoningMapFallback covers the
// `else if reasoning map` branch on the OpenRouter path when reasoning_effort
// is absent but a reasoning map with effort is provided.
func TestApplyThinkingMode_OpenRouterReasoningMapFallback(t *testing.T) {
	requestBody := map[string]interface{}{
		"reasoning": map[string]interface{}{"something": "else"},
	}
	options := map[string]interface{}{
		"thinking": true,
		"reasoning": map[string]interface{}{
			"effort": "low",
		},
	}
	applyThinkingMode(requestBody, options, "https://openrouter.ai/api/v1")

	reasoning, ok := requestBody["reasoning"].(map[string]interface{})
	if !ok || reasoning["effort"] != "low" {
		t.Fatalf("reasoning = %#v, want effort low", requestBody["reasoning"])
	}
	if _, ok := requestBody["thinking"].(map[string]interface{}); !ok {
		t.Fatalf("thinking = %#v, want {type: enabled}", requestBody["thinking"])
	}
}

// TestApplyThinkingMode_OpenRouterEmptyEffortUsesMap covers the branch where
// reasoning_effort is a non-string value, falling back to the reasoning map.
func TestApplyThinkingMode_OpenRouterEmptyEffortUsesMap(t *testing.T) {
	requestBody := map[string]interface{}{}
	options := map[string]interface{}{
		"thinking":         true,
		"reasoning_effort": 42, // non-string type -> fall through
		"reasoning": map[string]interface{}{
			"effort": "high",
		},
	}
	applyThinkingMode(requestBody, options, "https://openrouter.ai/api/v1")

	reasoning, ok := requestBody["reasoning"].(map[string]interface{})
	if !ok || reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v, want effort high", requestBody["reasoning"])
	}
}

// TestApplyThinkingMode_OpenRouterNoEffortSource covers the path where neither
// reasoning_effort nor reasoning map is available, but thinking type is still set.
func TestApplyThinkingMode_OpenRouterNoEffortSource(t *testing.T) {
	requestBody := map[string]interface{}{}
	options := map[string]interface{}{"thinking": true}
	applyThinkingMode(requestBody, options, "https://openrouter.ai/api/v1")

	thinking, ok := requestBody["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %#v, want {type: enabled}", requestBody["thinking"])
	}
	reasoning, ok := requestBody["reasoning"].(map[string]interface{})
	if !ok || len(reasoning) != 0 {
		t.Fatalf("reasoning = %#v, want empty map", requestBody["reasoning"])
	}
}

// TestApplyThinkingMode_NonOpenRouterReasoningMapFallback covers the reasoning
// map fallback on the non-OpenRouter path.
func TestApplyThinkingMode_NonOpenRouterReasoningMapFallback(t *testing.T) {
	requestBody := map[string]interface{}{}
	options := map[string]interface{}{
		"thinking": true,
		"reasoning": map[string]interface{}{
			"effort": "medium",
		},
	}
	applyThinkingMode(requestBody, options, "https://api.deepseek.com/v1")

	if requestBody["reasoning_effort"] != "medium" {
		t.Fatalf("reasoning_effort = %v, want medium", requestBody["reasoning_effort"])
	}
	if _, ok := requestBody["thinking"].(map[string]interface{}); !ok {
		t.Fatalf("thinking = %#v", requestBody["thinking"])
	}
}

// TestApplyThinkingMode_NonOpenRouterEmptyEffortFallsBack covers a non-string
// reasoning_effort falling back to reasoning map on non-OpenRouter.
func TestApplyThinkingMode_NonOpenRouterEmptyEffortFallsBack(t *testing.T) {
	requestBody := map[string]interface{}{}
	options := map[string]interface{}{
		"thinking":         true,
		"reasoning_effort": 3.14,
		"reasoning": map[string]interface{}{
			"effort": "low",
		},
	}
	applyThinkingMode(requestBody, options, "https://api.deepseek.com/v1")

	if requestBody["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort = %v, want low", requestBody["reasoning_effort"])
	}
}

// TestApplyThinkingMode_NonOpenRouterNoEffortSource covers no effort source.
func TestApplyThinkingMode_NonOpenRouterNoEffortSource(t *testing.T) {
	requestBody := map[string]interface{}{}
	options := map[string]interface{}{"thinking": true}
	applyThinkingMode(requestBody, options, "https://api.deepseek.com/v1")

	if _, exists := requestBody["reasoning_effort"]; exists {
		t.Fatalf("reasoning_effort should not be set, got %v", requestBody["reasoning_effort"])
	}
	if _, ok := requestBody["thinking"].(map[string]interface{}); !ok {
		t.Fatalf("thinking = %#v", requestBody["thinking"])
	}
}
