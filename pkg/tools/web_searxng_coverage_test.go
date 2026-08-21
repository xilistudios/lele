package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSearXNGSearchProvider_Success covers the success path with a local server.
func TestSearXNGSearchProvider_Success(t *testing.T) {
	var gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"number_of_results":1,"results":[
			{"title":"First","url":"https://a.example","content":"desc one","engine":"google"}
		]}`))
	}))
	defer server.Close()

	p := &SearXNGSearchProvider{instanceURL: server.URL, categories: "general", language: "auto"}
	out, err := p.Search(context.Background(), "hello world", 3)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotQuery != "hello world" {
		t.Fatalf("query sent = %q", gotQuery)
	}
	if !strings.Contains(out, "First") || !strings.Contains(out, "https://a.example") {
		t.Fatalf("out = %q", out)
	}
	if !strings.Contains(out, "desc one") {
		t.Fatalf("out missing desc: %q", out)
	}
	if !strings.Contains(out, "SearXNG") {
		t.Fatalf("out missing provider marker: %q", out)
	}
}

// TestSearXNGSearchProvider_Forbidden covers the 403 branch.
func TestSearXNGSearchProvider_Forbidden(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	p := &SearXNGSearchProvider{instanceURL: server.URL}
	_, err := p.Search(context.Background(), "q", 2)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v, want 403", err)
	}
}

// TestSearXNGSearchProvider_NoResults covers the empty-results branch.
func TestSearXNGSearchProvider_NoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"number_of_results":0,"results":[]}`))
	}))
	defer server.Close()

	p := &SearXNGSearchProvider{instanceURL: server.URL}
	out, err := p.Search(context.Background(), "q", 3)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(out, "No results for") {
		t.Fatalf("out = %q", out)
	}
}

// TestSearXNGSearchProvider_ParseError covers the JSON unmarshal failure branch.
func TestSearXNGSearchProvider_ParseError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`not-json{`))
	}))
	defer server.Close()

	p := &SearXNGSearchProvider{instanceURL: server.URL}
	_, err := p.Search(context.Background(), "q", 3)
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Fatalf("err = %v, want parse error", err)
	}
}

// TestSearXNGSearchProvider_LimitResults verifies results are truncated to count.
func TestSearXNGSearchProvider_LimitResults(t *testing.T) {
	var payload []byte
	payload = []byte(`{"results":[
		{"title":"One","url":"u1"},
		{"title":"Two","url":"u2"},
		{"title":"Three","url":"u3"}
	]}`)
	_ = payload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(payload)
	}))
	defer server.Close()

	p := &SearXNGSearchProvider{instanceURL: server.URL}
	out, err := p.Search(context.Background(), "q", 2)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if strings.Contains(out, "Three") {
		t.Fatalf("expected limit 2, got: %q", out)
	}
	if !strings.Contains(out, "One") || !strings.Contains(out, "Two") {
		t.Fatalf("out = %q", out)
	}
}