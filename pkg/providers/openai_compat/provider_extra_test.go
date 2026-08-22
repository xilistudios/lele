package openai_compat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAsInt_AllNumericTypes(t *testing.T) {
	tests := []struct {
		name string
		v    interface{}
		want int
		ok   bool
	}{
		{"int", 7, 7, true},
		{"int64", int64(8), 8, true},
		{"float64", float64(9.7), 9, true},
		{"float32", float32(3.9), 3, true},
		{"string", "10", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := asInt(tt.v)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("asInt(%v) = (%v, %v), want (%v, %v)", tt.v, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestAsFloat_AllNumericTypes(t *testing.T) {
	tests := []struct {
		name string
		v    interface{}
		want float64
		ok   bool
	}{
		{"float64", float64(2.5), 2.5, true},
		{"float32", float32(1.5), 1.5, true},
		{"int", 3, 3.0, true},
		{"int64", int64(4), 4.0, true},
		{"string", "2.5", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := asFloat(tt.v)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("asFloat(%v) = (%v, %v), want (%v, %v)", tt.v, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestChatStream_StreamRequestUsesMaxCompletionTokensForGLM(t *testing.T) {
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"glm-4",
		map[string]interface{}{"max_tokens": 100},
		func(chunk string, done bool) {},
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	if _, ok := requestBody["max_completion_tokens"]; !ok {
		t.Errorf("expected max_completion_tokens for glm model, got %#v", requestBody)
	}
}

func TestChatStream_StreamRequestUsesMaxTokensForRegularModel(t *testing.T) {
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-4o",
		map[string]interface{}{"max_tokens": 100, "temperature": float64(0.5)},
		func(chunk string, done bool) {},
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	if _, ok := requestBody["max_tokens"]; !ok {
		t.Errorf("expected max_tokens for regular model, got %#v", requestBody)
	}
	if requestBody["temperature"] != float64(0.5) {
		t.Errorf("temperature = %v, want 0.5", requestBody["temperature"])
	}
}

func TestChatStream_StreamRequestForcesKimiTemperature(t *testing.T) {
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"moonshotai/kimi-k2",
		map[string]interface{}{"temperature": float64(0.2)},
		func(chunk string, done bool) {},
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	if requestBody["temperature"] != 1.0 {
		t.Errorf("temperature = %v, want 1.0 (kimi override)", requestBody["temperature"])
	}
}

func TestChatStream_ReasoningConfigItsitoRequestBody(t *testing.T) {
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"o1",
		map[string]interface{}{
			"reasoning": map[string]interface{}{
				"effort":     "high",
				"enabled":    true,
				"summary":    "sum",
				"max_tokens": 100,
			},
		},
		func(chunk string, done bool) {},
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	reasoning, ok := requestBody["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected reasoning in request body, got %#v", requestBody)
	}
	// summary only added to OpenAI endpoint; server.URL is a non-OpenAI endpoint
	if _, hasSummary := reasoning["summary"]; hasSummary {
		t.Error("summary should not be present on non-OpenAI endpoint")
	}
	if reasoning["effort"] != "high" {
		t.Errorf("reasoning.effort = %v, want high", reasoning["effort"])
	}
}

func TestParseSSEStream_ToolCallsAndUsage(t *testing.T) {
	sse := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\\\"Tokyo\\\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"Let me check\"},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\n" +
		"data: [DONE]\n\n"
	resp, err := parseSSEStream(context.Background(), strings.NewReader(sse), func(string, bool) {}, nil)
	if err != nil {
		t.Fatalf("parseSSEStream() error: %v", err)
	}
	if resp.Content != "Let me check" {
		t.Errorf("Content = %q, want Let me check", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.Name != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", tc.Name)
	}
	if tc.Arguments == nil || tc.Arguments["city"] != "Tokyo" {
		t.Errorf("arguments = %v, want city=Tokyo", tc.Arguments)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want tool_calls", resp.FinishReason)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage = %+v, want total 15", resp.Usage)
	}
	// chunks should have received content
	var chunks []string
	parseSSEStream(context.Background(), strings.NewReader(sse), func(c string, done bool) {
		if !done && c != "" {
			chunks = append(chunks, c)
		}
	}, nil)
	if got := strings.Join(chunks, ""); got != "Let me check" {
		t.Errorf("chunks = %q, want Let me check", got)
	}
}

func TestParseSSEStream_ReasoningAndMalformedData(t *testing.T) {
	sse := "data: {bad json\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think step by step\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning\":\" and note caveats\",\"content\":\"done\"}}]}\n\n" +
		"data: [DONE]\n\n"
	var reasoning, content string
	resp, err := parseSSEStream(context.Background(), strings.NewReader(sse), func(c string, done bool) {
		if !done && c != "" {
			content += c
		}
	}, func(r string) { reasoning += r })
	if err != nil {
		t.Fatalf("parseSSEStream() error: %v", err)
	}
	if resp.ReasoningContent != "think step by step and note caveats" {
		t.Errorf("ReasoningContent = %q, want combined", resp.ReasoningContent)
	}
	if reasoning != "think step by step and note caveats" {
		t.Errorf("onReasoning called with %q", reasoning)
	}
	if content != "done" {
		t.Errorf("content = %q, want done", content)
	}
}

func TestParseSSEStream_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := parseSSEStream(ctx, strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"), func(string, bool) {}, nil)
	if err == nil {
		t.Fatal("expected context cancelled error")
	}
}

func TestChatStream_ServerErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-4o",
		map[string]interface{}{"max_tokens": 100},
		func(chunk string, done bool) {},
		nil,
	)
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention 500, got: %v", err)
	}
}

func TestChatStream_InvokesOnChunk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"b\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	var chunks []string
	_, err := p.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-4o",
		nil,
		func(chunk string, done bool) {
			if chunk != "" {
				chunks = append(chunks, chunk)
			}
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	if got := strings.Join(chunks, ""); got != "ab" {
		t.Errorf("chunks = %q, want ab", got)
	}
}

func TestChatStream_ReasoningConfigSummaryToOpenAI(t *testing.T) {
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&requestBody)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewProvider("key", server.URL, "")
	_, err := p.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"gpt-5",
		map[string]interface{}{"max_tokens": 100},
		func(chunk string, done bool) {},
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	// gpt-5 should use max_completion_tokens
	if _, ok := requestBody["max_completion_tokens"]; !ok {
		t.Errorf("expected max_completion_tokens for gpt-5, got %#v", requestBody)
	}
}
