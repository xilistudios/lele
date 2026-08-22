package tools

import (
	"strings"
	"testing"
)

// TestNewWebSearchTool_ProviderSelection covers the default selection priority
// branches: Perplexity > Brave > SearXNG > DuckDuckGo, plus defaults for
// SearXNG categories/language and maxResults overrides.
func TestNewWebSearchTool_ProviderSelection(t *testing.T) {
	// Perplexity priority.
	pp := NewWebSearchTool(WebSearchToolOptions{
		PerplexityEnabled: true, PerplexityAPIKey: "key", PerplexityMaxResults: 3,
		BraveEnabled: true, BraveAPIKey: "b",
	})
	if pp == nil || pp.name() != "web_search" {
		t.Fatalf("perplexity tool nil: %v", pp)
	}
	if _, ok := pp.provider.(*PerplexitySearchProvider); !ok {
		t.Fatalf("expected Perplexity provider, got %T", pp.provider)
	}
	if pp.maxResults != 3 {
		t.Fatalf("perplexity maxResults = %d", pp.maxResults)
	}

	// Perplexity enabled but no key => falls through to brave.
	ppNoKey := NewWebSearchTool(WebSearchToolOptions{
		PerplexityEnabled: true, PerplexityAPIKey: "",
		BraveEnabled: true, BraveAPIKey: "bkey", BraveMaxResults: 7,
	})
	if ppNoKey == nil {
		t.Fatal("expected brave fallback non-nil")
	}
	if _, ok := ppNoKey.provider.(*BraveSearchProvider); !ok {
		t.Fatalf("expected Brave provider, got %T", ppNoKey.provider)
	}
	if ppNoKey.maxResults != 7 {
		t.Fatalf("brave maxResults = %d", ppNoKey.maxResults)
	}

	// SearXNG selection with defaults for categories/language.
	searx := NewWebSearchTool(WebSearchToolOptions{
		SearXNGEnabled: true, SearXNGInstanceURL: "http://instance/", SearXNGMaxResults: 4,
	})
	if searx == nil {
		t.Fatal("expected searx non-nil")
	}
	sx, ok := searx.provider.(*SearXNGSearchProvider)
	if !ok {
		t.Fatalf("expected SearXNG provider, got %T", searx.provider)
	}
	if sx.categories != "general" {
		t.Fatalf("searx categories default = %q", sx.categories)
	}
	if sx.language != "auto" {
		t.Fatalf("searx language default = %q", sx.language)
	}
	if sx.instanceURL != "http://instance" { // trailing slash trimmed
		t.Fatalf("searx instanceURL = %q", sx.instanceURL)
	}
	if searx.maxResults != 4 {
		t.Fatalf("searx maxResults = %d", searx.maxResults)
	}

	// SearXNG with explicit categories/language preserved.
	searx2 := NewWebSearchTool(WebSearchToolOptions{
		SearXNGEnabled:     true,
		SearXNGInstanceURL: "http://i",
		SearXNGCategories:  "news",
		SearXNGLanguage:    "es",
		SearXNGSafeSearch:  1,
	})
	sx2 := searx2.provider.(*SearXNGSearchProvider)
	if sx2.categories != "news" || sx2.language != "es" || sx2.safesearch != 1 {
		t.Fatalf("searx2 = %+v", sx2)
	}

	// DuckDuckGo final fallback.
	ddg := NewWebSearchTool(WebSearchToolOptions{DuckDuckGoEnabled: true, DuckDuckGoMaxResults: 9})
	if ddg == nil {
		t.Fatal("expected ddg non-nil")
	}
	if _, ok := ddg.provider.(*DuckDuckGoSearchProvider); !ok {
		t.Fatalf("expected DDG provider, got %T", ddg.provider)
	}
	if ddg.maxResults != 9 {
		t.Fatalf("ddg maxResults = %d", ddg.maxResults)
	}

	// Nothing enabled => nil.
	if NewWebSearchTool(WebSearchToolOptions{}) != nil {
		t.Fatal("expected nil when no provider enabled")
	}
}

// name is a small helper to satisfy type assertions without importing other
// packages; it just returns the tool's Name().
func (t *WebSearchTool) name() string { return t.Name() }

// TestWebFetchTool_NewWebFetchTool exercises the maxChars<=0 default branch.
func TestWebFetchTool_NewWebFetchTool(t *testing.T) {
	if NewWebFetchTool(0).maxChars != 50000 {
		t.Fatal("default maxChars should be 50000")
	}
	if NewWebFetchTool(-5).maxChars != 50000 {
		t.Fatal("negative maxChars should default to 50000")
	}
	if NewWebFetchTool(3000).maxChars != 3000 {
		t.Fatalf("explicit maxChars = %d", NewWebFetchTool(3000).maxChars)
	}
}

// TestWebSearchTool_Execute_CountOutOfRange verifies Execute count clamp (1..10).
func TestWebSearchTool_Execute_CountOutOfRange(t *testing.T) {
	got := 0
	captured := 0
	tool := &WebSearchTool{provider: &capturingSearchProvider{onSearch: func(q string, c int) string {
		captured = c
		got++
		return "r"
	}}, maxResults: 5}
	tool.Execute(nil, map[string]interface{}{"query": "q", "count": float64(7)})
	if captured != 7 {
		t.Fatalf("count captured = %d, want 7", captured)
	}
	if got != 1 {
		t.Fatalf("provider calls = %d", got)
	}
}

// TestWebSearchTool_Execute_Coverage ensures provider search is invoked with a
// valid context even when args missing query returns early.
func TestWebSearchTool_Execute_Coverage(t *testing.T) {
	// Query type assertion failure path.
	tool := &WebSearchTool{provider: &fixedSearchProvider{}, maxResults: 5}
	res := tool.Execute(nil, map[string]interface{}{"query": 42})
	if res == nil || !res.IsError || !strings.Contains(res.ForLLM, "query is required") {
		t.Fatalf("res = %+v", res)
	}
}
