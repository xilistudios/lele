package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHTTPProvider_GetDefaultModel(t *testing.T) {
	p := NewHTTPProvider("key", "https://api.example.com", "")
	if p == nil {
		t.Fatal("NewHTTPProvider() returned nil")
	}
	if got := p.GetDefaultModel(); got != "" {
		t.Errorf("GetDefaultModel() = %q, want empty", got)
	}
}

func TestHTTPProvider_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"test-model"`) {
			t.Errorf("body does not contain model, got: %s", string(body))
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`)
	}))
	defer server.Close()

	p := NewHTTPProvider("key", server.URL, "")
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}}, nil, "test-model", nil)
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("Content = %q, want hi", resp.Content)
	}
}

func TestHTTPProvider_ChatStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	p := NewHTTPProvider("key", server.URL, "")
	var chunks []string
	_, err := p.ChatStream(
		context.Background(),
		[]Message{{Role: "user", Content: "hello"}},
		nil,
		"test-model",
		nil,
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
	if got := strings.Join(chunks, ""); got != "hello" {
		t.Errorf("stream content = %q, want hello", got)
	}
}