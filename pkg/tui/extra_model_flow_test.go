package tui

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// extra_model_flow_test.go covers the remaining low-coverage branches in
// model.go streaming/reload helpers and a few pure utils.

func TestCleanupStreamingIfCompleteWithHistory_EmptyStream(t *testing.T) {
	m := &Model{currentStream: "", currentThinking: ""}
	m.cleanupStreamingIfCompleteWithHistory(nil)
	if m.currentStream != "" {
		t.Error("expected stream unchanged when empty")
	}
}

func TestCleanupStreamingIfCompleteWithHistory_NoKey(t *testing.T) {
	m := &Model{currentStream: "s", currentKey: ""}
	m.cleanupStreamingIfCompleteWithHistory(nil)
	if m.currentStream != "s" {
		t.Error("expected stream unchanged when key empty")
	}
}

func TestCleanupStreamingIfCompleteWithHistory_MatchesAndClears(t *testing.T) {
	m := &Model{currentStream: "hello", currentThinking: "think", currentKey: "k"}
	history := []providers.Message{
		{Role: "assistant", Content: "prefix hello", ReasoningContent: "x think y", Streaming: false},
	}
	m.cleanupStreamingIfCompleteWithHistory(history)
	if m.currentStream != "" || m.currentThinking != "" {
		t.Errorf("expected streaming cleared, got %q/%q", m.currentStream, m.currentThinking)
	}
	if m.currentAssistantMsgID != "" {
		t.Error("expected assistant msg id cleared")
	}
}

func TestCleanupStreamingIfCompleteWithHistory_StillStreaming(t *testing.T) {
	m := &Model{currentStream: "hello", currentThinking: "think", currentKey: "k"}
	history := []providers.Message{
		{Role: "assistant", Content: "hello", ReasoningContent: "think", Streaming: true},
	}
	m.cleanupStreamingIfCompleteWithHistory(history)
	if m.currentStream != "hello" || m.currentThinking != "think" {
		t.Errorf("expected streaming preserved while still streaming, got %q/%q", m.currentStream, m.currentThinking)
	}
}

func TestCleanupStreamingIfCompleteWithHistory_NoMatchKeeps(t *testing.T) {
	m := &Model{currentStream: "hello", currentThinking: "think", currentKey: "k"}
	history := []providers.Message{
		{Role: "assistant", Content: "totally different", ReasoningContent: "x", Streaming: false},
	}
	m.cleanupStreamingIfCompleteWithHistory(history)
	if m.currentStream != "hello" || m.currentThinking != "think" {
		t.Errorf("expected streaming kept on no match")
	}
}

func TestCleanupStreamingIfCompleteWithHistory_NoAssistant(t *testing.T) {
	m := &Model{currentStream: "hello", currentThinking: "think", currentKey: "k"}
	history := []providers.Message{
		{Role: "user", Content: "hello", Streaming: false},
	}
	m.cleanupStreamingIfCompleteWithHistory(history)
	if m.currentStream != "hello" {
		t.Error("expected streaming kept when no assistant message")
	}
}

func TestShouldSkipViewportUpdate(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:skip"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddMessage(key, "user", "u")
	m.sessionMgr.AddMessage(key, "assistant", "a")
	m.currentKey = key
	m.showWelcome = false
	// A valid rendered base enables the skip shortcut on identical state.
	m.renderedBaseValid = true
	if m.shouldSkipViewportUpdate() {
		t.Error("first update should not be skipped")
	}
	// Same state again: cache hit -> true.
	if !m.shouldSkipViewportUpdate() {
		t.Error("second identical update should be skipped")
	}
	// Change something visible: no longer skipped.
	m.pendingUserMessage = "changed"
	if m.shouldSkipViewportUpdate() {
		t.Error("changed state should not be skipped")
	}
}

func TestShouldSkipViewportUpdate_Guards(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:skipg"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddMessage(key, "user", "u")
	m.currentKey = key
	m.showWelcome = true
	// showWelcome guard forces false.
	if m.shouldSkipViewportUpdate() {
		t.Error("should not skip when showWelcome true")
	}
	m.showWelcome = false
	// empty currentKey guard.
	m.currentKey = ""
	if m.shouldSkipViewportUpdate() {
		t.Error("should not skip when currentKey empty")
	}
	// modal open guard.
	m.currentKey = key
	m.modalMode = ModalSessions
	if m.shouldSkipViewportUpdate() {
		t.Error("should not skip when modal open")
	}
	// subagent parent guard.
	m.modalMode = ModalNone
	m.parentSessionKey = "tui:chat:parent"
	if m.shouldSkipViewportUpdate() {
		t.Error("should not skip when viewing subagent")
	}
}

func TestGetHistoryMessageCount(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:cnt"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddMessage(key, "user", "u")
	m.sessionMgr.AddMessage(key, "assistant", "a")
	m.sessionMgr.AddMessage(key, "system", "s")
	m.currentKey = key
	if got := m.getHistoryMessageCount(); got != 2 {
		t.Fatalf("getHistoryMessageCount = %d, want 2 (user+assistant)", got)
	}
	// Empty key.
	if got := (&Model{}).getHistoryMessageCount(); got != 0 {
		t.Fatalf("empty key count = %d, want 0", got)
	}
}

func TestFormatToolCallArgs_HasUnknownFallback(t *testing.T) {
	// formatToolCallArgs with raw non-JSON function arguments returns the raw
	// sanitized string.
	tc := providers.ToolCall{Function: &providers.FunctionCall{Arguments: "not json at all"}}
	out := formatToolCallArgs(tc)
	if out != "not json at all" {
		t.Fatalf("formatToolCallArgs(raw) = %q", out)
	}
	// Empty tool call.
	if got := formatToolCallArgs(providers.ToolCall{}); got != "" {
		t.Fatalf("empty tool call = %q, want ''", got)
	}
	// Structured arguments map.
	tc2 := providers.ToolCall{Arguments: map[string]interface{}{"file": "a.go"}}
	if out := formatToolCallArgs(tc2); out != "file: a.go" {
		t.Fatalf("formatToolCallArgs(structured) = %q", out)
	}
}

func TestFormatToolCallArgsCompactFallback(t *testing.T) {
	// Non-JSON function arguments fall back to raw truncated string.
	tc := providers.ToolCall{Function: &providers.FunctionCall{Arguments: "raw value here"}}
	out := formatToolCallArgsCompact(tc)
	if out != "raw value here" {
		t.Fatalf("compact fallback = %q", out)
	}
	// Empty.
	if got := formatToolCallArgsCompact(providers.ToolCall{}); got != "" {
		t.Fatalf("empty compact = %q", got)
	}
}

func TestFormatToolCallArgsCompact_FlatNewlines(t *testing.T) {
	tc := providers.ToolCall{Function: &providers.FunctionCall{Arguments: `{"cmd":"echo a\nb","note":"x"}`}}
	out := formatToolCallArgsCompact(tc)
	if out == "" {
		t.Fatal("expected compact output")
	}
}

func TestGetMarkdownRenderer_WidthChangeCreatesNew(t *testing.T) {
	m := newTestModel(t)
	r1 := m.getMarkdownRenderer(60)
	if r1 == nil {
		t.Fatal("expected renderer for width 60")
	}
	// Same width returns cached instance.
	if m.getMarkdownRenderer(60) != r1 {
		t.Error("expected cached renderer for same width")
	}
	// Different width creates a new one.
	if m.getMarkdownRenderer(100) == r1 {
		t.Error("expected a new renderer when width changes")
	}
	if m.cachedRendererWidth != 100 {
		t.Errorf("cachedRendererWidth = %d, want 100", m.cachedRendererWidth)
	}
}

func TestRenderMarkdown_UsesGlamour(t *testing.T) {
	m := newTestModel(t)
	out := m.renderMarkdown("# Title\n\nBody text", 60)
	if out == "" {
		t.Fatal("expected markdown rendered output")
	}
	if !containsAny(out, "Title", "title") {
		t.Errorf("expected title content, got %q", out)
	}
}

func TestFormStepNames(t *testing.T) {
	m := &Model{modalMode: ModalAddProvider}
	steps := m.formStepNames()
	if len(steps) != 10 {
		t.Fatalf("AddProvider steps = %d, want 10", len(steps))
	}
	m.modalMode = ModalAddModel
	if got := m.formStepNames(); len(got) != 5 {
		t.Fatalf("AddModel steps = %d, want 5", len(got))
	}
	m.modalMode = ModalAddSecret
	if got := m.formStepNames(); len(got) != 5 {
		t.Fatalf("AddSecret steps = %d, want 5", len(got))
	}
	m.modalMode = ModalNone
	if got := m.formStepNames(); got != nil {
		t.Fatalf("default steps = %v, want nil", got)
	}
}
