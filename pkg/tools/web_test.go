package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebTool_WebFetch_Success verifies successful URL fetching
func TestWebTool_WebFetch_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html><body><h1>Test Page</h1><p>Content here</p></body></html>"))
	}))
	defer server.Close()

	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	args := map[string]interface{}{
		"url": server.URL,
	}

	result := tool.Execute(ctx, args)

	// Success should not be an error
	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	// ForUser should contain the fetched content
	if !strings.Contains(result.ForUser, "Test Page") {
		t.Errorf("Expected ForUser to contain 'Test Page', got: %s", result.ForUser)
	}

	// ForLLM should contain summary
	if !strings.Contains(result.ForLLM, "bytes") && !strings.Contains(result.ForLLM, "extractor") {
		t.Errorf("Expected ForLLM to contain summary, got: %s", result.ForLLM)
	}
}

// TestWebTool_WebFetch_JSON verifies JSON content handling
func TestWebTool_WebFetch_JSON(t *testing.T) {
	testData := map[string]string{"key": "value", "number": "123"}
	expectedJSON, _ := json.MarshalIndent(testData, "", "  ")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(expectedJSON)
	}))
	defer server.Close()

	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	args := map[string]interface{}{
		"url": server.URL,
	}

	result := tool.Execute(ctx, args)

	// Success should not be an error
	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	// ForUser should contain formatted JSON
	if !strings.Contains(result.ForUser, "key") && !strings.Contains(result.ForUser, "value") {
		t.Errorf("Expected ForUser to contain JSON data, got: %s", result.ForUser)
	}
}

// TestWebTool_WebFetch_InvalidURL verifies error handling for invalid URL
func TestWebTool_WebFetch_InvalidURL(t *testing.T) {
	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	args := map[string]interface{}{
		"url": "not-a-valid-url",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error for invalid URL")
	}

	// Should contain error message (either "invalid URL" or scheme error)
	if !strings.Contains(result.ForLLM, "URL") && !strings.Contains(result.ForUser, "URL") {
		t.Errorf("Expected error message for invalid URL, got ForLLM: %s", result.ForLLM)
	}
}

// TestWebTool_WebFetch_UnsupportedScheme verifies error handling for non-http URLs
func TestWebTool_WebFetch_UnsupportedScheme(t *testing.T) {
	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	args := map[string]interface{}{
		"url": "ftp://example.com/file.txt",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error for unsupported URL scheme")
	}

	// Should mention only http/https allowed
	if !strings.Contains(result.ForLLM, "http/https") && !strings.Contains(result.ForUser, "http/https") {
		t.Errorf("Expected scheme error message, got ForLLM: %s", result.ForLLM)
	}
}

// TestWebTool_WebFetch_MissingURL verifies error handling for missing URL
func TestWebTool_WebFetch_MissingURL(t *testing.T) {
	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	args := map[string]interface{}{}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when URL is missing")
	}

	// Should mention URL is required
	if !strings.Contains(result.ForLLM, "url is required") && !strings.Contains(result.ForUser, "url is required") {
		t.Errorf("Expected 'url is required' message, got ForLLM: %s", result.ForLLM)
	}
}

// TestWebTool_WebFetch_Truncation verifies content truncation
func TestWebTool_WebFetch_Truncation(t *testing.T) {
	longContent := strings.Repeat("x", 20000)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(longContent))
	}))
	defer server.Close()

	tool := NewWebFetchTool(1000) // Limit to 1000 chars
	ctx := context.Background()
	args := map[string]interface{}{
		"url": server.URL,
	}

	result := tool.Execute(ctx, args)

	// Success should not be an error
	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	// ForUser should contain truncated content (not the full 20000 chars)
	resultMap := make(map[string]interface{})
	json.Unmarshal([]byte(result.ForUser), &resultMap)
	if text, ok := resultMap["text"].(string); ok {
		if len(text) > 1100 { // Allow some margin
			t.Errorf("Expected content to be truncated to ~1000 chars, got: %d", len(text))
		}
	}

	// Should be marked as truncated
	if truncated, ok := resultMap["truncated"].(bool); !ok || !truncated {
		t.Errorf("Expected 'truncated' to be true in result")
	}
}

// TestWebTool_WebSearch_NoApiKey verifies that no tool is created when API key is missing
func TestWebTool_WebSearch_NoApiKey(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{BraveEnabled: true, BraveAPIKey: ""})
	if tool != nil {
		t.Errorf("Expected nil tool when Brave API key is empty")
	}

	// Also nil when nothing is enabled
	tool = NewWebSearchTool(WebSearchToolOptions{})
	if tool != nil {
		t.Errorf("Expected nil tool when no provider is enabled")
	}
}

// TestWebTool_WebSearch_MissingQuery verifies error handling for missing query
func TestWebTool_WebSearch_MissingQuery(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{BraveEnabled: true, BraveAPIKey: "test-key", BraveMaxResults: 5})
	ctx := context.Background()
	args := map[string]interface{}{}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when query is missing")
	}
}

// TestWebTool_WebFetch_HTMLExtraction verifies HTML text extraction
func TestWebTool_WebFetch_HTMLExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body><script>alert('test');</script><style>body{color:red;}</style><h1>Title</h1><p>Content</p></body></html>`))
	}))
	defer server.Close()

	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	args := map[string]interface{}{
		"url": server.URL,
	}

	result := tool.Execute(ctx, args)

	// Success should not be an error
	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	// ForUser should contain extracted text (without script/style tags)
	if !strings.Contains(result.ForUser, "Title") && !strings.Contains(result.ForUser, "Content") {
		t.Errorf("Expected ForUser to contain extracted text, got: %s", result.ForUser)
	}

	// Should NOT contain script or style tags
	if strings.Contains(result.ForUser, "<script>") || strings.Contains(result.ForUser, "<style>") {
		t.Errorf("Expected script/style tags to be removed, got: %s", result.ForUser)
	}
}

// TestWebFetchTool_extractText verifies text extraction preserves newlines
func TestWebFetchTool_extractText(t *testing.T) {
	tool := &WebFetchTool{}

	tests := []struct {
		name     string
		input    string
		wantFunc func(t *testing.T, got string)
	}{
		{
			name:  "preserves newlines between block elements",
			input: "<html><body><h1>Title</h1>\n<p>Paragraph 1</p>\n<p>Paragraph 2</p></body></html>",
			wantFunc: func(t *testing.T, got string) {
				lines := strings.Split(got, "\n")
				if len(lines) < 2 {
					t.Errorf("Expected multiple lines, got %d: %q", len(lines), got)
				}
				if !strings.Contains(got, "Title") || !strings.Contains(got, "Paragraph 1") || !strings.Contains(got, "Paragraph 2") {
					t.Errorf("Missing expected text: %q", got)
				}
			},
		},
		{
			name:  "removes script and style tags",
			input: "<script>alert('x');</script><style>body{}</style><p>Keep this</p>",
			wantFunc: func(t *testing.T, got string) {
				if strings.Contains(got, "alert") || strings.Contains(got, "body{}") {
					t.Errorf("Expected script/style content removed, got: %q", got)
				}
				if !strings.Contains(got, "Keep this") {
					t.Errorf("Expected 'Keep this' to remain, got: %q", got)
				}
			},
		},
		{
			name:  "collapses excessive blank lines",
			input: "<p>A</p>\n\n\n\n\n<p>B</p>",
			wantFunc: func(t *testing.T, got string) {
				if strings.Contains(got, "\n\n\n") {
					t.Errorf("Expected excessive blank lines collapsed, got: %q", got)
				}
			},
		},
		{
			name:  "collapses horizontal whitespace",
			input: "<p>hello     world</p>",
			wantFunc: func(t *testing.T, got string) {
				if strings.Contains(got, "     ") {
					t.Errorf("Expected spaces collapsed, got: %q", got)
				}
				if !strings.Contains(got, "hello world") {
					t.Errorf("Expected 'hello world', got: %q", got)
				}
			},
		},
		{
			name:  "empty input",
			input: "",
			wantFunc: func(t *testing.T, got string) {
				if got != "" {
					t.Errorf("Expected empty string, got: %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tool.extractText(tt.input)
			tt.wantFunc(t, got)
		})
	}
}

// TestWebTool_WebFetch_MissingDomain verifies error handling for URL without domain
func TestWebTool_WebFetch_MissingDomain(t *testing.T) {
	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	args := map[string]interface{}{
		"url": "https://",
	}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error for URL without domain")
	}

	// Should mention missing domain
	if !strings.Contains(result.ForLLM, "domain") && !strings.Contains(result.ForUser, "domain") {
		t.Errorf("Expected domain error message, got ForLLM: %s", result.ForLLM)
	}
}

// ========== DuckDuckGo Search Provider Tests ==========

func TestDuckDuckGoSearchProvider_ExtractResults_Success(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{}
	html := "<html><body>" +
		"<a class=\"result__a\" href=\"https://example.com/1\">Title 1</a>" +
		"<a class=\"result__snippet\">Snippet 1</a>" +
		"<a class=\"result__a\" href=\"https://example.com/2\">Title 2</a>" +
		"<a class=\"result__snippet\">Snippet 2</a>" +
		"</body></html>"
	result, err := provider.extractResults(html, 2, "test query")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, "Title 1") {
		t.Errorf("Expected 'Title 1' in result, got: %s", result)
	}
	if !strings.Contains(result, "https://example.com/1") {
		t.Errorf("Expected URL in result, got: %s", result)
	}
	if !strings.Contains(result, "Snippet 1") {
		t.Errorf("Expected 'Snippet 1' in result, got: %s", result)
	}
}

func TestDuckDuckGoSearchProvider_ExtractResults_NoMatches(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{}
	result, err := provider.extractResults("<html><body>No results found</body></html>", 5, "nothing")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, "No results found") {
		t.Errorf("Expected no-results message, got: %s", result)
	}
}

func TestDuckDuckGoSearchProvider_ExtractResults_TruncatesToCount(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{}
	html := "<a class=\"result__a\" href=\"https://a.com\">A</a>" +
		"<a class=\"result__a\" href=\"https://b.com\">B</a>" +
		"<a class=\"result__a\" href=\"https://c.com\">C</a>" +
		"<a class=\"result__a\" href=\"https://d.com\">D</a>" +
		"<a class=\"result__a\" href=\"https://e.com\">E</a>"
	result, err := provider.extractResults(html, 2, "query")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if strings.Contains(result, "C") {
		t.Errorf("Expected only 2 results, but got 'C': %s", result)
	}
}

func TestDuckDuckGoSearchProvider_ExtractResults_DecodesUDDG(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{}
	html := "<a class=\"result__a\" href=\"https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage\">Link</a>"
	result, err := provider.extractResults(html, 1, "query")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, "example.com/page") {
		t.Errorf("Expected decoded URL 'example.com/page', got: %s", result)
	}
}

func TestDuckDuckGoSearchProvider_ExtractResults_UDDGNotURLEncoded(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{}
	html := "<a class=\"result__a\" href=\"uddg=simple-url\">Link</a>"
	result, err := provider.extractResults(html, 1, "query")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, "simple-url") {
		t.Errorf("Expected 'simple-url' in result, got: %s", result)
	}
}

func TestDuckDuckGoSearchProvider_ExtractResults_AttributeOrderFlexible(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{}
	html := "<a class=\"result__a some-other\" href=\"https://example.com/x\">Flexible Order</a>"
	result, err := provider.extractResults(html, 1, "query")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, "Flexible Order") {
		t.Errorf("Expected 'Flexible Order' in result, got: %s", result)
	}
}

func TestDuckDuckGoSearchProvider_ExtractResults_HTMLInTitle(t *testing.T) {
	provider := &DuckDuckGoSearchProvider{}
	html := "<a class=\"result__a\" href=\"https://example.com\"><b>Bold</b> Title</a>"
	result, err := provider.extractResults(html, 1, "query")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !strings.Contains(result, "Bold Title") {
		t.Errorf("Expected stripped HTML 'Bold Title', got: %s", result)
	}
}

// ========== StripTags Tests ==========

func TestStripTags(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple tag", "<b>bold</b>", "bold"},
		{"nested tags", "<div><span>text</span></div>", "text"},
		{"no tags", "plain text", "plain text"},
		{"empty", "", ""},
		{"self-closing", "Hello <br/> World", "Hello  World"},
		{"with attributes", "<a href=\"x\" class=\"y\">link</a>", "link"},
		{"multi-line", "<p>line1</p>\n<p>line2</p>", "line1\nline2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripTags(tt.input)
			if got != tt.expected {
				t.Errorf("stripTags(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ========== WebSearchTool Metadata Tests ==========

func TestWebSearchTool_Name(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{DuckDuckGoEnabled: true})
	if tool.Name() != "web_search" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "web_search")
	}
}

func TestWebSearchTool_Description(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{DuckDuckGoEnabled: true})
	desc := tool.Description()
	if !strings.Contains(desc, "Search") {
		t.Errorf("Description() = %q, expected to contain 'Search'", desc)
	}
}

func TestWebSearchTool_Parameters(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{DuckDuckGoEnabled: true})
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters() returned nil")
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'properties' in params")
	}
	if _, ok := props["query"]; !ok {
		t.Error("Expected 'query' property")
	}
	if _, ok := props["count"]; !ok {
		t.Error("Expected 'count' property")
	}
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("Expected 'required' array")
	}
	if len(required) < 1 || required[0] != "query" {
		t.Errorf("Expected 'query' in required, got %v", required)
	}
}

// ========== NewWebSearchTool Constructor Tests ==========

func TestNewWebSearchTool_Perplexity(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{
		PerplexityEnabled:    true,
		PerplexityAPIKey:     "pk-test",
		PerplexityMaxResults: 7,
	})
	if tool == nil {
		t.Fatal("Expected non-nil tool for Perplexity")
	}
	if tool.maxResults != 7 {
		t.Errorf("maxResults = %d, want %d", tool.maxResults, 7)
	}
}

func TestNewWebSearchTool_PerplexityEmptyKey(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{
		PerplexityEnabled: true,
		PerplexityAPIKey:  "",
	})
	if tool != nil {
		t.Error("Expected nil when Perplexity enabled but no key and no fallback")
	}
}

func TestNewWebSearchTool_Brave(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{
		BraveEnabled:    true,
		BraveAPIKey:     "bsk-test",
		BraveMaxResults: 8,
	})
	if tool == nil {
		t.Fatal("Expected non-nil tool for Brave")
	}
	if tool.maxResults != 8 {
		t.Errorf("maxResults = %d, want %d", tool.maxResults, 8)
	}
}

func TestNewWebSearchTool_DuckDuckGo(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{
		DuckDuckGoEnabled:    true,
		DuckDuckGoMaxResults: 6,
	})
	if tool == nil {
		t.Fatal("Expected non-nil tool for DuckDuckGo")
	}
	if tool.maxResults != 6 {
		t.Errorf("maxResults = %d, want %d", tool.maxResults, 6)
	}
}

func TestNewWebSearchTool_DuckDuckGoDefaultMax(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{
		DuckDuckGoEnabled:    true,
		DuckDuckGoMaxResults: 0,
	})
	if tool == nil {
		t.Fatal("Expected non-nil tool for DuckDuckGo")
	}
	if tool.maxResults != 5 {
		t.Errorf("maxResults = %d, want %d (default)", tool.maxResults, 5)
	}
}

func TestNewWebSearchTool_Priority_PerplexityOverBrave(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{
		PerplexityEnabled:    true,
		PerplexityAPIKey:     "pk-1",
		PerplexityMaxResults: 3,
		BraveEnabled:         true,
		BraveAPIKey:          "bsk-1",
		BraveMaxResults:      5,
	})
	if tool == nil {
		t.Fatal("Expected non-nil tool")
	}
	if tool.maxResults != 3 {
		t.Errorf("Expected Perplexity max results (3), got %d", tool.maxResults)
	}
}

func TestNewWebSearchTool_Priority_BraveOverDuckDuckGo(t *testing.T) {
	tool := NewWebSearchTool(WebSearchToolOptions{
		BraveEnabled:         true,
		BraveAPIKey:          "bsk-1",
		BraveMaxResults:      4,
		DuckDuckGoEnabled:    true,
		DuckDuckGoMaxResults: 10,
	})
	if tool == nil {
		t.Fatal("Expected non-nil tool")
	}
	if tool.maxResults != 4 {
		t.Errorf("Expected Brave max results (4), got %d", tool.maxResults)
	}
}

// ========== WebFetchTool Metadata Tests ==========

func TestWebFetchTool_Name(t *testing.T) {
	tool := NewWebFetchTool(50000)
	if tool.Name() != "web_fetch" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "web_fetch")
	}
}

func TestWebFetchTool_Description(t *testing.T) {
	tool := NewWebFetchTool(50000)
	desc := tool.Description()
	if !strings.Contains(desc, "Fetch") {
		t.Errorf("Description() = %q, expected to contain 'Fetch'", desc)
	}
}

func TestWebFetchTool_Parameters(t *testing.T) {
	tool := NewWebFetchTool(50000)
	params := tool.Parameters()
	if params == nil {
		t.Fatal("Parameters() returned nil")
	}
	props, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected 'properties' in params")
	}
	if _, ok := props["url"]; !ok {
		t.Error("Expected 'url' property")
	}
	if _, ok := props["maxChars"]; !ok {
		t.Error("Expected 'maxChars' property")
	}
}

func TestNewWebFetchTool_DefaultMaxChars(t *testing.T) {
	tool := NewWebFetchTool(0)
	if tool.maxChars != 50000 {
		t.Errorf("maxChars = %d, want %d", tool.maxChars, 50000)
	}
}

func TestNewWebFetchTool_NegativeMaxChars(t *testing.T) {
	tool := NewWebFetchTool(-100)
	if tool.maxChars != 50000 {
		t.Errorf("maxChars = %d, want %d (default for negative)", tool.maxChars, 50000)
	}
}

// ========== WebFetchTool Execute Edge Cases ==========

func TestWebFetchTool_Execute_MaxCharsOverride(t *testing.T) {
	longContent := strings.Repeat("x", 500)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(longContent))
	}))
	defer server.Close()

	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"url":      server.URL,
		"maxChars": float64(200),
	})

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	var resultMap map[string]interface{}
	json.Unmarshal([]byte(result.ForUser), &resultMap)
	if text, ok := resultMap["text"].(string); ok {
		if len(text) > 250 {
			t.Errorf("Expected text truncated to ~200, got %d chars", len(text))
		}
	}
	if truncated, ok := resultMap["truncated"].(bool); !ok || !truncated {
		t.Errorf("Expected 'truncated' to be true")
	}
}

func TestWebFetchTool_Execute_MaxCharsBelow100(t *testing.T) {
	content := strings.Repeat("a", 300)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(content))
	}))
	defer server.Close()

	tool := NewWebFetchTool(10000)
	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"url":      server.URL,
		"maxChars": float64(50),
	})

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	var resultMap map[string]interface{}
	json.Unmarshal([]byte(result.ForUser), &resultMap)
	if truncated, ok := resultMap["truncated"].(bool); ok && truncated {
		t.Errorf("Expected not truncated (maxChars=50 ignored, default 10000 > 300)")
	}
}

func TestWebFetchTool_Execute_PlainTextContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("plain text content"))
	}))
	defer server.Close()

	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{"url": server.URL})

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	var resultMap map[string]interface{}
	json.Unmarshal([]byte(result.ForUser), &resultMap)
	if ext, ok := resultMap["extractor"].(string); !ok || ext != "raw" {
		t.Errorf("Expected extractor 'raw', got: %v", resultMap["extractor"])
	}
	if st, ok := resultMap["status"].(float64); !ok || int(st) != 200 {
		t.Errorf("Expected status 200, got: %v", resultMap["status"])
	}
}

func TestWebFetchTool_Execute_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("error body"))
	}))
	defer server.Close()

	tool := NewWebFetchTool(50000)
	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{"url": server.URL})

	if result.IsError {
		t.Errorf("Expected success (HTTP errors are not tool errors), got IsError=true")
	}

	var resultMap map[string]interface{}
	json.Unmarshal([]byte(result.ForUser), &resultMap)
	if st, ok := resultMap["status"].(float64); !ok || int(st) != 500 {
		t.Errorf("Expected status 500, got: %v", resultMap["status"])
	}
}
