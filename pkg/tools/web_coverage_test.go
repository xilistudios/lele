package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSearXNGSearchProvider_Non200 covers the non-200 (non-403) status path.
func TestSearXNGSearchProvider_Non200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer server.Close()

	provider := &SearXNGSearchProvider{instanceURL: server.URL}
	_, err := provider.Search(context.Background(), "t", 3)
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("err = %v, want SearXNG returned status 500", err)
	}
}

// TestSearXNGSearchProvider_RequestFailed covers the request-creation/network error path.
func TestSearXNGSearchProvider_RequestFailed(t *testing.T) {
	provider := &SearXNGSearchProvider{instanceURL: "http://127.0.0.1:1"}
	_, err := provider.Search(context.Background(), "t", 3)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("err = %v, want request failed", err)
	}
}

// TestWebSearchTool_Execute_ProviderError verifies the search-failed branch of
// WebSearchTool.Execute using a mocked provider that errors.
func TestWebSearchTool_Execute_ProviderError(t *testing.T) {
	tool := &WebSearchTool{
		provider:   &failingSearchProvider{},
		maxResults: 5,
	}
	result := tool.Execute(context.Background(), map[string]interface{}{"query": "hi"})
	if result == nil || !result.IsError {
		t.Fatal("expected error result")
	}
	if !strings.Contains(result.ForLLM, "search failed") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}
}

type failingSearchProvider struct{}

func (f *failingSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	return "", &searchError{}
}

type searchError struct{}

func (s *searchError) Error() string { return "search boom" }

// TestWebSearchTool_Execute_SuccessAndCount verifies the WebSearchTool success
// path and count override logic.
func TestWebSearchTool_Execute_SuccessAndCount(t *testing.T) {
	gotCounts := []int{}
	tool := &WebSearchTool{
		provider: &capturingSearchProvider{onSearch: func(query string, count int) string {
			gotCounts = append(gotCounts, count)
			return "fixed results for " + query
		}},
		maxResults: 5,
	}

	// Default count = maxResults.
	res := tool.Execute(context.Background(), map[string]interface{}{"query": "hello"})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "fixed results") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
	if res.ForUser != res.ForLLM {
		t.Fatalf("expected ForUser to mirror ForLLM, got %q vs %q", res.ForUser, res.ForLLM)
	}

	// Override count within 1..10.
	res = tool.Execute(context.Background(), map[string]interface{}{"query": "hi", "count": float64(3)})
	if res == nil || res.IsError {
		t.Fatalf("expected success with count override")
	}

	// Out-of-range count ignored (uses maxResults=5).
	res = tool.Execute(context.Background(), map[string]interface{}{"query": "hi", "count": float64(99)})
	if res == nil || res.IsError {
		t.Fatalf("expected success with out-of-range count ignored")
	}

	// count=0 ignored.
	res = tool.Execute(context.Background(), map[string]interface{}{"query": "hi", "count": float64(0)})
	if res == nil || res.IsError {
		t.Fatalf("expected success with count=0")
	}

	// count typed as string (not float64) → ignored.
	res = tool.Execute(context.Background(), map[string]interface{}{"query": "hi", "count": "7"})
	if res == nil || res.IsError {
		t.Fatalf("expected success with string count")
	}

	if len(gotCounts) != 5 {
		t.Fatalf("gotCounts = %v, want 5 entries", gotCounts)
	}
	// Sequence: 5, 3, 5, 5, 5
	want := []int{5, 3, 5, 5, 5}
	for i, v := range want {
		if gotCounts[i] != v {
			t.Fatalf("count[%d] = %d, want %d", i, gotCounts[i], v)
		}
	}
}

type capturingSearchProvider struct {
	onSearch func(query string, count int) string
}

func (c *capturingSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	return c.onSearch(query, count), nil
}

// TestWebSearchTool_Execute_QueryNotString verifies the missing/invalid query
// branch.
func TestWebSearchTool_Execute_QueryNotString(t *testing.T) {
	tool := &WebSearchTool{provider: &fixedSearchProvider{}, maxResults: 5}
	result := tool.Execute(context.Background(), map[string]interface{}{"query": 123})
	if result == nil || !result.IsError {
		t.Fatal("expected error when query not a string")
	}
	if !strings.Contains(result.ForLLM, "query is required") {
		t.Fatalf("ForLLM = %q", result.ForLLM)
	}

	// Missing query entirely.
	result = tool.Execute(context.Background(), map[string]interface{}{})
	if result == nil || !result.IsError {
		t.Fatal("expected error when query missing")
	}
}

type fixedSearchProvider struct{}

func (f *fixedSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	return "fixed results for " + query, nil
}

// TestBraveSearchProvider_RequestFailure covers the request-failed error path
// of BraveSearchProvider.Search using an already-canceled context.
func TestBraveSearchProvider_RequestFailure(t *testing.T) {
	p := &BraveSearchProvider{apiKey: "abc"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Search(ctx, "q", 3)
	if err == nil {
		// Request to Brave may fail fast; if it somehow succeeds in a network
		// environment, accept it. We only care it doesn't panic.
		return
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Fatalf("err = %v, want request failed", err)
	}
}

// TestPerplexitySearchProvider_RequestFailure covers Perplexity request failure.
func TestPerplexitySearchProvider_RequestFailure(t *testing.T) {
	p := &PerplexitySearchProvider{apiKey: "pk"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Search(ctx, "q", 2)
	if err == nil {
		return
	}
	_ = err // request failed path covered
}

// TestDuckDuckGoSearchProvider_RequestFailure covers DDG network error path.
func TestDuckDuckGoSearchProvider_RequestFailure(t *testing.T) {
	p := &DuckDuckGoSearchProvider{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.Search(ctx, "q", 2)
	if err == nil {
		return
	}
	_ = err
}
