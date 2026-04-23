package openai_compat

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/xilistudios/lele/pkg/providers/protocoltypes"
)

func TestProviderChat_UsesMaxCompletionTokensForGLM(t *testing.T) {
	var requestBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]interface{}{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "glm-4.7", map[string]interface{}{"max_tokens": 1234})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if _, ok := requestBody["max_completion_tokens"]; !ok {
		t.Fatalf("expected max_completion_tokens in request body")
	}
	if _, ok := requestBody["max_tokens"]; ok {
		t.Fatalf("did not expect max_tokens key for glm model")
	}
}

func TestProviderChat_ParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message": map[string]interface{}{
						"content": "",
						"tool_calls": []map[string]interface{}{
							{
								"id":   "call_1",
								"type": "function",
								"function": map[string]interface{}{
									"name":      "get_weather",
									"arguments": "{\"city\":\"SF\"}",
								},
							},
						},
					},
					"finish_reason": "tool_calls",
				},
			},
			"usage": map[string]interface{}{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	out, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("ToolCalls[0].Name = %q, want %q", out.ToolCalls[0].Name, "get_weather")
	}
	if out.ToolCalls[0].Arguments["city"] != "SF" {
		t.Fatalf("ToolCalls[0].Arguments[city] = %v, want SF", out.ToolCalls[0].Arguments["city"])
	}
}

func TestProviderChat_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestProviderChat_StripsMoonshotPrefixAndNormalizesKimiTemperature(t *testing.T) {
	var requestBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]interface{}{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"moonshot/kimi-k2.5",
		map[string]interface{}{"temperature": 0.3},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if requestBody["model"] != "kimi-k2.5" {
		t.Fatalf("model = %v, want kimi-k2.5", requestBody["model"])
	}
	if requestBody["temperature"] != 1.0 {
		t.Fatalf("temperature = %v, want 1.0", requestBody["temperature"])
	}
}

func TestProviderChat_StripsGroqAndOllamaPrefixes(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantModel string
	}{
		{
			name:      "strips groq prefix and keeps nested model",
			input:     "groq/openai/gpt-oss-120b",
			wantModel: "openai/gpt-oss-120b",
		},
		{
			name:      "strips ollama prefix",
			input:     "ollama/qwen2.5:14b",
			wantModel: "qwen2.5:14b",
		},
		{
			name:      "strips deepseek prefix",
			input:     "deepseek/deepseek-chat",
			wantModel: "deepseek-chat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestBody map[string]interface{}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				resp := map[string]interface{}{
					"choices": []map[string]interface{}{
						{
							"message":       map[string]interface{}{"content": "ok"},
							"finish_reason": "stop",
						},
					},
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(resp)
			}))
			defer server.Close()

			p := NewProvider("key", server.URL, "")
			_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, tt.input, nil)
			if err != nil {
				t.Fatalf("Chat() error = %v", err)
			}

			if requestBody["model"] != tt.wantModel {
				t.Fatalf("model = %v, want %s", requestBody["model"], tt.wantModel)
			}
		})
	}
}

func TestProvider_ProxyConfigured(t *testing.T) {
	proxyURL := "http://127.0.0.1:8080"
	p := NewProvider("key", "https://example.com", proxyURL)

	transport, ok := p.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("expected http transport with proxy, got %T", p.httpClient.Transport)
	}

	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.example.com"}}
	gotProxy, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("proxy function returned error: %v", err)
	}
	if gotProxy == nil || gotProxy.String() != proxyURL {
		t.Fatalf("proxy = %v, want %s", gotProxy, proxyURL)
	}
}

func TestProviderChat_AcceptsNumericOptionTypes(t *testing.T) {
	var requestBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]interface{}{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-4o",
		map[string]interface{}{"max_tokens": float64(512), "temperature": 1},
	)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if requestBody["max_tokens"] != float64(512) {
		t.Fatalf("max_tokens = %v, want 512", requestBody["max_tokens"])
	}
	if requestBody["temperature"] != float64(1) {
		t.Fatalf("temperature = %v, want 1", requestBody["temperature"])
	}
}

func TestProviderChat_SendsMultimodalContentParts(t *testing.T) {
	var requestBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{{
				"message":       map[string]interface{}{"content": "ok"},
				"finish_reason": "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.Chat(t.Context(), []Message{{
		Role: "user",
		ContentParts: []protocoltypes.ContentPart{
			{Type: "text", Text: "Describe this image"},
			{Type: "image_url", ImageURL: &protocoltypes.ImageURL{URL: "data:image/png;base64,abcd", Detail: "high"}},
		},
	}}, nil, "gpt-4o", nil)
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	messages, ok := requestBody["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %#v", requestBody["messages"])
	}
	msg, ok := messages[0].(map[string]interface{})
	if !ok {
		t.Fatalf("message[0] type = %T", messages[0])
	}
	content, ok := msg["content"].([]interface{})
	if !ok || len(content) != 2 {
		t.Fatalf("content = %#v", msg["content"])
	}
	imagePart, ok := content[1].(map[string]interface{})
	if !ok {
		t.Fatalf("image part type = %T", content[1])
	}
	if imagePart["type"] != "image_url" {
		t.Fatalf("image part type = %v, want image_url", imagePart["type"])
	}
}

func TestProviderChat_RepeatedEquivalentCallsProduceIdenticalRequests(t *testing.T) {
	requestBodies := make([][]byte, 0, 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestBodies = append(requestBodies, bytes.Clone(body))
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{{
				"message":       map[string]interface{}{"content": "ok"},
				"finish_reason": "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	messages := []Message{
		{Role: "system", Content: "cached system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "calling tool", ToolCalls: []ToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: &FunctionCall{
				Name:      "get_weather",
				Arguments: `{"city":"SF"}`,
			},
		}}},
		{Role: "tool", Content: "sunny", ToolCallID: "call_1"},
	}
	tools := []ToolDefinition{{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        "get_weather",
			Description: "Get weather",
			Parameters: map[string]interface{}{
				"type": "object",
			},
		},
	}}
	options := map[string]interface{}{
		"max_tokens":  512,
		"temperature": 0.7,
	}

	for i := 0; i < 2; i++ {
		if _, err := p.Chat(t.Context(), messages, tools, "gpt-4o", options); err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
	}

	if len(requestBodies) != 2 {
		t.Fatalf("captured %d requests, want 2", len(requestBodies))
	}
	if !bytes.Equal(requestBodies[0], requestBodies[1]) {
		t.Fatalf("expected equivalent Chat calls to produce identical request bodies\nfirst:  %s\nsecond: %s", requestBodies[0], requestBodies[1])
	}
}

func TestProviderChat_ToolOrderAffectsRequestOnlyWhenInputOrderChanges(t *testing.T) {
	var requestBodies []map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestBodies = append(requestBodies, body)
		resp := map[string]interface{}{
			"choices": []map[string]interface{}{{
				"message":       map[string]interface{}{"content": "ok"},
				"finish_reason": "stop",
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	toolsA := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "alpha", Description: "A", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunctionDefinition{Name: "beta", Description: "B", Parameters: map[string]interface{}{"type": "object"}}},
	}
	toolsB := []ToolDefinition{
		{Type: "function", Function: ToolFunctionDefinition{Name: "beta", Description: "B", Parameters: map[string]interface{}{"type": "object"}}},
		{Type: "function", Function: ToolFunctionDefinition{Name: "alpha", Description: "A", Parameters: map[string]interface{}{"type": "object"}}},
	}

	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, toolsA, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if _, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, toolsB, "gpt-4o", nil); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	if len(requestBodies) != 2 {
		t.Fatalf("captured %d requests, want 2", len(requestBodies))
	}

	toolsPayloadA, ok := requestBodies[0]["tools"].([]interface{})
	if !ok {
		t.Fatalf("requestBodies[0][tools] type = %T", requestBodies[0]["tools"])
	}
	toolsPayloadB, ok := requestBodies[1]["tools"].([]interface{})
	if !ok {
		t.Fatalf("requestBodies[1][tools] type = %T", requestBodies[1]["tools"])
	}
	if reflect.DeepEqual(toolsPayloadA, toolsPayloadB) {
		t.Fatalf("expected differently ordered tool inputs to remain distinguishable in provider request payload")
	}
	if len(toolsPayloadA) != 2 || len(toolsPayloadB) != 2 {
		t.Fatalf("unexpected tool payload lengths: %d and %d", len(toolsPayloadA), len(toolsPayloadB))
	}
}

func TestNormalizeModel_UsesAPIBase(t *testing.T) {
	if got := normalizeModel("deepseek/deepseek-chat", "https://api.deepseek.com/v1"); got != "deepseek-chat" {
		t.Fatalf("normalizeModel(deepseek) = %q, want %q", got, "deepseek-chat")
	}
	if got := normalizeModel("chutes/minimax-m2.5", "https://llm.chutes.ai/v1"); got != "minimax-m2.5" {
		t.Fatalf("normalizeModel(chutes) = %q, want %q", got, "minimax-m2.5")
	}
	if got := normalizeModel("openrouter/auto", "https://openrouter.ai/api/v1"); got != "openrouter/auto" {
		t.Fatalf("normalizeModel(openrouter) = %q, want %q", got, "openrouter/auto")
	}
}
