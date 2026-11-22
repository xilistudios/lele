package tui

import (
	"strings"
	"testing"
)

func TestGetRenderedStreamWidthChangeInvalidatesCache(t *testing.T) {
	m := &Model{currentStream: "hello world\nsecond line"}

	m.getRenderedStream(100)
	if len(m.streamRenderedLines) == 0 {
		t.Fatalf("expected stream cache to be populated after first render")
	}
	if m.streamRenderCacheWidth != 100 {
		t.Fatalf("expected streamRenderCacheWidth=100, got %d", m.streamRenderCacheWidth)
	}
	cachedLines := len(m.streamRenderedLines)
	cachedJoined := m.streamRenderedJoined

	same := m.getRenderedStream(100)
	if len(m.streamRenderedLines) != cachedLines || m.streamRenderedJoined != cachedJoined {
		t.Fatalf("expected cache to be reused at same width")
	}
	if !strings.Contains(same, "hello world") {
		t.Fatalf("unexpected render output at same width: %q", same)
	}

	narrow := m.getRenderedStream(5)
	if m.streamRenderCacheWidth != 5 {
		t.Fatalf("expected streamRenderCacheWidth=5 after width change, got %d", m.streamRenderCacheWidth)
	}
	if len(m.streamRenderedLines) == 0 {
		t.Fatalf("expected stream cache to be re-populated at new width")
	}
	if !strings.Contains(narrow, "hello\nworld") {
		t.Fatalf("expected wrapped output at width 5, got %q", narrow)
	}
}

func TestGetRenderedThinkingWidthChangeInvalidatesCache(t *testing.T) {
	m := &Model{currentThinking: "thinking line one\nthinking line two"}

	m.getRenderedThinking(100)
	if len(m.thinkingRenderedLines) == 0 {
		t.Fatalf("expected thinking cache to be populated after first render")
	}
	if m.thinkingRenderCacheWidth != 100 {
		t.Fatalf("expected thinkingRenderCacheWidth=100, got %d", m.thinkingRenderCacheWidth)
	}

	m.getRenderedThinking(4)
	if m.thinkingRenderCacheWidth != 4 {
		t.Fatalf("expected thinkingRenderCacheWidth=4 after width change, got %d", m.thinkingRenderCacheWidth)
	}
	out := m.getRenderedThinking(4)
	if !strings.Contains(out, "line\none") {
		t.Fatalf("expected wrapped output at width 4, got %q", out)
	}
}

func TestInvalidateRenderCacheClearsStreams(t *testing.T) {
	m := &Model{currentStream: "stream content\nmore stream", currentThinking: "thinking a\nthinking b"}

	m.getRenderedStream(80)
	m.getRenderedThinking(80)
	if len(m.streamRenderedLines) != 1 || len(m.thinkingRenderedLines) != 1 {
		t.Fatalf("expected both caches to be populated (1 completed line each) before invalidation, got stream=%d thinking=%d",
			len(m.streamRenderedLines), len(m.thinkingRenderedLines))
	}
	if m.streamRenderCacheWidth != 80 || m.thinkingRenderCacheWidth != 80 {
		t.Fatalf("expected cache widths to be 80 before invalidation")
	}

	m.invalidateRenderCache()

	if m.streamRenderedLines != nil {
		t.Errorf("streamRenderedLines should be nil after invalidateRenderCache")
	}
	if m.thinkingRenderedLines != nil {
		t.Errorf("thinkingRenderedLines should be nil after invalidateRenderCache")
	}
	if m.streamRenderedJoined != "" {
		t.Errorf("streamRenderedJoined should be empty after invalidateRenderCache")
	}
	if m.thinkingRenderedJoined != "" {
		t.Errorf("thinkingRenderedJoined should be empty after invalidateRenderCache")
	}
	if m.streamRenderCacheWidth != 0 {
		t.Errorf("streamRenderCacheWidth should be 0 after invalidateRenderCache, got %d", m.streamRenderCacheWidth)
	}
	if m.thinkingRenderCacheWidth != 0 {
		t.Errorf("thinkingRenderCacheWidth should be 0 after invalidateRenderCache, got %d", m.thinkingRenderCacheWidth)
	}
}

func TestSimpleMarkdownRenderMultibyteWidth(t *testing.T) {
	cjkWide := "\u4e16\u4e16 \u4e16\u4e16 \u4e16\u4e16 \u4e16\u4e16 \u4e16\u4e16"
	got := simpleMarkdownRender(cjkWide, 10)
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected CJK line of visual width 14 to wrap at width 10, got: %q", got)
	}

	cjk10 := strings.Repeat("\u4e16", 5)
	got = simpleMarkdownRender(cjk10, 10)
	if strings.Contains(got, "\n") {
		t.Fatalf("expected CJK line of visual width 10 to fit in width 10, got wrapped output: %q", got)
	}

	accents := "áá áá áá"
	got = simpleMarkdownRender(accents, 10)
	if strings.Contains(got, "\n") {
		t.Fatalf("expected accented line of visual width 8 to fit in width 10, got wrapped output: %q", got)
	}

	ascii := strings.Repeat("ab ", 10)
	got = simpleMarkdownRender(ascii, 10)
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected long ASCII line to wrap at width 10, got: %q", got)
	}
}
