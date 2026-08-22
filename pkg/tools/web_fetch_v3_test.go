package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWebFetch_RedirectLimit exercises the CheckRedirect error branch when a
// chain of more than 5 redirects is encountered (line 518-522 of web.go).
func TestWebFetch_RedirectLimit(t *testing.T) {
	// Create a server that redirects to itself forever.
	var handler http.HandlerFunc
	handler = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	tool := NewWebFetchTool(50000)
	result := tool.Execute(context.Background(), map[string]interface{}{"url": srv.URL})
	if !result.IsError {
		t.Fatal("expected error for redirect limit, got success")
	}
	if result.ForLLM == "" {
		t.Error("expected non-empty error message")
	}
}

// TestWebFetch_ReadResponseError exercises the failed to read response branch
// (line 540-542) by sending a body whose Content-Length exceeds actual bytes,
// producing an unexpected EOF on read.
func TestWebFetch_ReadResponseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "50000")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("short body"))
	}))
	defer srv.Close()

	tool := NewWebFetchTool(50000)
	result := tool.Execute(context.Background(), map[string]interface{}{"url": srv.URL})
	if !result.IsError {
		t.Fatal("expected error reading truncated body, got success")
	}
}

// TestWebFetch_InvalidJSONContentType exercises the.json raw fallback branch
// (lines 554-557) when Content-Type is application/json but the body is not
// valid JSON.
func TestWebFetch_InvalidJSONContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{this is not json"))
	}))
	defer srv.Close()

	tool := NewWebFetchTool(50000)
	result := tool.Execute(context.Background(), map[string]interface{}{"url": srv.URL})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(result.ForUser), &m); err != nil {
		t.Fatalf("invalid result json: %v", err)
	}
	if m["extractor"] != "raw" {
		t.Errorf("extractor = %v, want raw", m["extractor"])
	}
}

// TestWebFetch_RequestFailedNoServer exercises the request failed branch
// (line 527-529) by pointing at an unreachable address.
func TestWebFetch_RequestFailedNoServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	// Close immediately to make the address unreachable.
	srv.Close()

	tool := NewWebFetchTool(50000)
	result := tool.Execute(context.Background(), map[string]interface{}{"url": url})
	if !result.IsError {
		t.Fatal("expected error for unreachable server, got success")
	}
}

// TestWebFetch_HTTPSuccessRawText exercises a plain-text (non-html) response
// hitting the else branch (lines 562-565).
func TestWebFetch_HTTPSuccessRawText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("just some plain text"))
	}))
	defer srv.Close()

	tool := NewWebFetchTool(50000)
	result := tool.Execute(context.Background(), map[string]interface{}{"url": srv.URL})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(result.ForUser), &m); err != nil {
		t.Fatalf("invalid result json: %v", err)
	}
	if m["extractor"] != "raw" {
		t.Errorf("extractor = %v, want raw", m["extractor"])
	}
}
