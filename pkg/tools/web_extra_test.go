package tools

import (
	"strings"
	"testing"
)

// TestDuckDuckGoExtractResults verifies DDG HTML result extraction.
func TestDuckDuckGoExtractResults(t *testing.T) {
	html := `<html><a class="result__a" href="https://example.com">Example Site</a>
	<a class="result__snippet" href="#">A snippet about example.</a>
	<a class="result__a" href="https://example.org">Second</a></html>`

	p := &DuckDuckGoSearchProvider{}
	out, err := p.extractResults(html, 2, "test query")
	if err != nil {
		t.Fatalf("extractResults: %v", err)
	}
	if !strings.Contains(out, "Example Site") {
		t.Fatalf("out = %q (expected title)", out)
	}
	if !strings.Contains(out, "DuckDuckGo") {
		t.Fatalf("out = %q (expected provider marker)", out)
	}
	if !strings.Contains(out, "snippet") {
		t.Fatalf("out = %q (expected snippet)", out)
	}
}

// TestDuckDuckGoExtractResults_NoMatches verifies the no-results branch.
func TestDuckDuckGoExtractResults_NoMatches(t *testing.T) {
	p := &DuckDuckGoSearchProvider{}
	out, err := p.extractResults("<html><p>nothing here</p></html>", 3, "q")
	if err != nil {
		t.Fatalf("extractResults: %v", err)
	}
	if !strings.Contains(out, "No results found") {
		t.Fatalf("out = %q", out)
	}
}

// TestDuckDuckGoExtractResults_UDDG verifies the uddg= decoding path.
func TestDuckDuckGoExtractResults_UDDG(t *testing.T) {
	encoded := `https://duckduckgo.com/l/?uddg=` + "%66%6f%6f" // "foo"
	html := `<a class="result__a" href="` + encoded + `">Link</a>`
	p := &DuckDuckGoSearchProvider{}
	out, err := p.extractResults(html, 1, "q")
	if err != nil {
		t.Fatalf("extractResults: %v", err)
	}
	if !strings.Contains(out, "foo") {
		t.Fatalf("out = %q (expected decoded foo)", out)
	}
}

// TestWebFetch_extractText verifies HTML-to-text conversion.
func TestWebFetch_extractText(t *testing.T) {
	tool := NewWebFetchTool(50000)
	html := "<html><script>var x=1;</script><style>.a{}</style>\n\n\n  <p>Hello\nworld</p>   <p>Second line</p>  </html>"
	text := tool.extractText(html)
	if !strings.Contains(text, "Hello") || !strings.Contains(text, "world") {
		t.Fatalf("text = %q", text)
	}
	if strings.Contains(text, "<") {
		t.Fatalf("text still contains tags: %q", text)
	}
	if strings.Contains(text, "var x") {
		t.Fatalf("script content not removed: %q", text)
	}
	if strings.Contains(text, ".a{}") {
		t.Fatalf("style content not removed: %q", text)
	}
}

// TestWebFetch_execute_JSON verifies the JSON extractor path via a body-only check
// (non-HTTP branch is covered by existing httptest tests; this exercises extractText
// with multi-line normalization).
func TestWebFetch_extractText_WhitespaceNormalization(t *testing.T) {
	tool := NewWebFetchTool(50000)
	html := "  spaced \t text  \n\n\n\nend"
	text := tool.extractText(html)
	if strings.Contains(text, "\n\n\n\n") {
		t.Fatalf("excessive newlines not collapsed: %q", text)
	}
	if segments := strings.Split(text, "\n"); len(segments) == 0 || segments[0] != "spaced text" {
		t.Fatalf("expected trimmed first line, got %q", text)
	}
}