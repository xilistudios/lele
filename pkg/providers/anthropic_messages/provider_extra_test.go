package anthropicmessages

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAsFloat(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want float64
		ok   bool
	}{
		{"float64", float64(2.5), 2.5, true},
		{"int", 3, 3.0, true},
		{"int64", int64(7), 7.0, true},
		{"string", "2.5", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
		{"float32", float32(1.5), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := asFloat(tt.v)
			if ok != tt.ok {
				t.Fatalf("asFloat(%v) ok = %v, want %v", tt.v, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("asFloat(%v) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}

func TestAsInt_FloatAndInt64(t *testing.T) {
	// asInt is at 60% coverage; exercise remaining paths (float64, int64).
	tests := []struct {
		name string
		v    any
		want int
		ok   bool
	}{
		{"float64", float64(42), 42, true},
		{"int64", int64(99), 99, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := asInt(tt.v)
			if ok != tt.ok || got != tt.want {
				t.Errorf("asInt(%v) = (%v, %v), want (%v, %v)", tt.v, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestBuildRequestBody_WithReasoningEnabled(t *testing.T) {
	got, err := buildRequestBody(
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"test-model",
		map[string]any{
			"max_tokens": 1000,
			"reasoning": map[string]any{
				"enabled": true,
			},
		},
	)
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}
	thinking, ok := got["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("expected thinking config, got %#v", got["thinking"])
	}
	if thinking["type"] != "adaptive" {
		t.Errorf("thinking.type = %v, want adaptive", thinking["type"])
	}
	// Default effort "high"
	oc := got["output_config"].(map[string]any)
	if oc["effort"] != "high" {
		t.Errorf("output_config.effort = %v, want high", oc["effort"])
	}
}

func TestBuildRequestBody_WithReasoningEnabledCustomEffort(t *testing.T) {
	got, err := buildRequestBody(
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"test-model",
		map[string]any{
			"max_tokens": 1000,
			"reasoning": map[string]any{
				"enabled": true,
				"effort":  "low",
			},
		},
	)
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}
	oc := got["output_config"].(map[string]any)
	if oc["effort"] != "low" {
		t.Errorf("output_config.effort = %v, want low", oc["effort"])
	}
}

func TestBuildRequestBody_WithReasoningDisabled(t *testing.T) {
	got, err := buildRequestBody(
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"test-model",
		map[string]any{
			"max_tokens": 1000,
			"reasoning": map[string]any{
				"enabled": false,
			},
		},
	)
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}
	if _, ok := got["thinking"]; ok {
		t.Error("thinking should not be set when reasoning disabled")
	}
}

func TestBuildRequestBody_WithReasoningNotMap(t *testing.T) {
	got, err := buildRequestBody(
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		"test-model",
		map[string]any{
			"max_tokens":   1000,
			"reasoning":    "not-a-map",
			"temperature":  0.7,
		},
	)
	if err != nil {
		t.Fatalf("buildRequestBody() error: %v", err)
	}
	if _, ok := got["thinking"]; ok {
		t.Error("thinking should not be set when reasoning is not a map")
	}
}

// TestChatStream_ChatStreamWithHTTPServer exercises the full ChatStream code
// path against a locally stubbed Anthropic Messages SSE endpoint.
func TestChatStream_ChatStreamWithHTTPServer(t *testing.T) {
	sseBody := joinSSEEvents(
		sseEvent("message_start", `{"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}`),
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`),
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" from server"}}`),
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":8}}`),
		sseEvent("message_stop", `{"type":"message_stop"}`),
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Validate headers
		if r.Header.Get("X-Api-Key") != "test-key" {
			t.Errorf("X-Api-Key header = %q, want test-key", r.Header.Get("X-Api-Key"))
		}
		if r.Header.Get("Anthropic-Version") != defaultAPIVersion {
			t.Errorf("Anthropic-Version = %q, want %q", r.Header.Get("Anthropic-Version"), defaultAPIVersion)
		}
		// Validate the body stream flag
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":true`) {
			t.Errorf("body does not contain stream:true, got: %s", string(body))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseBody)
	}))
	defer server.Close()

	provider := NewProvider("test-key", server.URL)
	var chunks []string
	_, err := provider.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "Test"}},
		nil,
		"test-model",
		map[string]any{"max_tokens": 100},
		func(chunk string, done bool) {
			if !done {
				chunks = append(chunks, chunk)
			}
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	got := strings.Join(chunks, "")
	if got != "Hello from server" {
		t.Errorf("chunked content = %q, want %q", got, "Hello from server")
	}
}

func TestChatStream_ServerErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":"rate limited"}`)
	}))
	defer server.Close()

	provider := NewProvider("test-key", server.URL)
	_, err := provider.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "Test"}},
		nil,
		"test-model",
		map[string]any{"max_tokens": 100},
		func(chunk string, done bool) {},
		nil,
	)
	if err == nil {
		t.Fatal("expected error for 429 status")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error should mention status 429, got: %v", err)
	}
}

func TestChat_ChatWithHTTPServer_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"hi there"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":5,"output_tokens":2}
		}`)
	}))
	defer server.Close()

	provider := NewProvider("test-key", server.URL)
	resp, err := provider.Chat(
		context.Background(),
		[]Message{{Role: "user", Content: "Hello"}},
		nil,
		"test-model",
		map[string]any{"max_tokens": 100},
	)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "hi there" {
		t.Errorf("Content = %q, want hi there", resp.Content)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
}

func TestChat_ChatWithHTTPServer_Errors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantSubstr string
	}{
		{"401", http.StatusUnauthorized, `{}`, "401"},
		{"429", http.StatusTooManyRequests, `rate limited body`, "429"},
		{"400", http.StatusBadRequest, `bad request`, "400"},
		{"404", http.StatusNotFound, `nope`, "404"},
		{"500", http.StatusInternalServerError, `boom`, "500"},
		{"503", http.StatusServiceUnavailable, `down`, "503"},
		{"other status", 418, `teapot`, "418"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			provider := NewProvider("test-key", server.URL)
			_, err := provider.Chat(
				context.Background(),
				[]Message{{Role: "user", Content: "Hello"}},
				nil,
				"test-model",
				map[string]any{"max_tokens": 100},
			)
			if err == nil {
				t.Fatalf("expected error for status %d", tt.status)
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Errorf("error = %q, want substr %q", err.Error(), tt.wantSubstr)
			}
		})
	}
}