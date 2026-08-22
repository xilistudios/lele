package tui

import (
	"strings"
	"testing"
)

// --- markdown.go coverage ---

func TestSimpleMarkdownRenderHeaders(t *testing.T) {
	out := simpleMarkdownRender("# Big\n## Mid\n### Small", 60)
	if !strings.Contains(out, "Big") || !strings.Contains(out, "Mid") || !strings.Contains(out, "Small") {
		t.Errorf("expected headers to render, got %q", out)
	}
}

func TestSimpleMarkdownRenderWrap(t *testing.T) {
	long := strings.Repeat("word ", 50)
	out := simpleMarkdownRender(long, 20)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	// Should wrap into multiple lines.
	if len(strings.Split(out, "\n")) < 2 {
		t.Errorf("expected wrapping, got single line:\n%s", out)
	}
}

func TestSimpleMarkdownRenderShort(t *testing.T) {
	out := simpleMarkdownRender("plain text line", 200)
	if !strings.Contains(out, "plain text line") {
		t.Errorf("expected plain text preserved, got %q", out)
	}
}

func TestGetRenderedStreamEmpty(t *testing.T) {
	m := &Model{}
	if got := m.getRenderedStream(80); got != "" {
		t.Errorf("expected empty for empty stream, got %q", got)
	}
}

func TestGetRenderedStream(t *testing.T) {
	m := &Model{currentStream: "hello\nworld"}
	out := m.getRenderedStream(80)
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Errorf("expected both lines, got %q", out)
	}
	// Second call should use cache.
	out2 := m.getRenderedStream(80)
	if out != out2 {
		t.Errorf("cached render differs: %q vs %q", out, out2)
	}
}

func TestGetRenderedStreamHeaders(t *testing.T) {
	m := &Model{currentStream: "# Title"}
	out := m.getRenderedStream(80)
	if !strings.Contains(out, "Title") {
		t.Errorf("expected header rendered, got %q", out)
	}
}

func TestGetRenderedThinking(t *testing.T) {
	m := &Model{currentThinking: "step one\nstep two"}
	out := m.getRenderedThinking(80)
	if !strings.Contains(out, "step one") || !strings.Contains(out, "step two") {
		t.Errorf("expected thinking lines, got %q", out)
	}
	out2 := m.getRenderedThinking(80)
	if out != out2 {
		t.Errorf("cached thinking render differs")
	}
}

func TestGetRenderedThinkingEmpty(t *testing.T) {
	m := &Model{}
	if got := m.getRenderedThinking(80); got != "" {
		t.Errorf("expected empty for empty thinking, got %q", got)
	}
}

func TestRenderSingleLine(t *testing.T) {
	if got := renderSingleLine("plain", 80); got != "plain" {
		t.Errorf("renderSingleLine plain = %q", got)
	}
	if got := renderSingleLine("# Head", 80); !strings.Contains(got, "Head") {
		t.Errorf("renderSingleLine header = %q", got)
	}
	if got := renderSingleLine("## Two", 80); !strings.Contains(got, "Two") {
		t.Errorf("renderSingleLine h2 = %q", got)
	}
	if got := renderSingleLine("### Three", 80); !strings.Contains(got, "Three") {
		t.Errorf("renderSingleLine h3 = %q", got)
	}
	// Long line wraps.
	long := strings.Repeat("ab ", 50)
	got := renderSingleLine(long, 20)
	if len(strings.Split(got, "\n")) < 2 {
		t.Errorf("expected wrap on long line, got %q", got)
	}
}
