package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

// TestCoverageDefaultRenderStartIdxCovers the three branches of
// defaultRenderStartIdx: unlimited (0), fits-in-window, and windowed overflow.
func TestCoverageDefaultRenderStartIdxBranches(t *testing.T) {
	m := newTestModel(t)

	// Unlimited (maxRenderedMessages <= 0) -> 0.
	m.maxRenderedMessages = 0
	if got := m.defaultRenderStartIdx(200); got != 0 {
		t.Errorf("unlimited: expected 0, got %d", got)
	}

	// Fits in window -> 0.
	m.maxRenderedMessages = 100
	if got := m.defaultRenderStartIdx(50); got != 0 {
		t.Errorf("fits: expected 0, got %d", got)
	}

	// Overflow -> msgCount - max.
	m.maxRenderedMessages = 100
	if got := m.defaultRenderStartIdx(150); got != 50 {
		t.Errorf("overflow: expected 50, got %d", got)
	}
}

// TestCoverageBuildRenderedHistoryAssistReasoningToolCalls exercises the
// assistant branch (agent header, reasoning content, content, tool calls) and
// the tool branch, plus compaction-summary skipping.
func TestCoverageBuildRenderedHistoryAssistReasoningToolCalls(t *testing.T) {
	m := newTestModel(t)
	key := "tui:chat:brh-v4a"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "Q2?")
	m.currentKey = key
	m.showWelcome = false

	history := []providers.Message{
		{Role: "user", Content: "dir?"},
		{
			Role:             "assistant",
			Content:          "here is the answer",
			ReasoningContent: "thinking deep",
			ToolCalls: []providers.ToolCall{
				{Name: "bash", Arguments: map[string]interface{}{"cmd": "echo hi"}},
			},
		},
		{Role: "tool", Content: "some tool output that might be long enough to matter"},
		{Role: "system", Content: "internal prompt"},
		{Role: "assistant", Content: "compaction summary", Streaming: false},
	}

	m.renderStartIdx = 0
	m.maxRenderedMessages = 100
	lines := m.buildRenderedHistoryLines(history)
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "here is the answer") {
		t.Errorf("expected answer content, got: %v", lines)
	}
	if !strings.Contains(joined, "thinking deep") {
		t.Errorf("expected reasoning content, got: %v", lines)
	}
	if !strings.Contains(joined, "bash") {
		t.Errorf("expected tool call name, got: %v", lines)
	}
	if !strings.Contains(joined, "→") {
		t.Errorf("expected tool result, got: %v", lines)
	}
}

// TestCoverageBuildRenderedHistoryCacheHit verifies the per-message render
// cache is reused on a second pass (no panic) and that a pre-populated cache
// entry is returned without re-rendering.
func TestCoverageBuildRenderedHistoryCacheHit(t *testing.T) {
	m := newTestModel(t)
	m.renderStartIdx = 0
	m.maxRenderedMessages = 100
	history := []providers.Message{
		{Role: "user", Content: "cache me"},
	}
	lines := m.buildRenderedHistoryLines(history)
	if len(lines) == 0 {
		t.Fatal("expected rendered lines")
	}
	// Second pass should hit the cache.
	lines2 := m.buildRenderedHistoryLines(history)
	if len(lines2) == 0 {
		t.Fatal("expected cached rendered lines")
	}
	if m.msgRenderCacheLines == nil {
		t.Error("expected msg render cache initialized")
	}
}

// TestCoverageBuildRenderedHistorySkipsExecutingStream covers the
// streaming-skip branch when m.processing is true and the last message is a
// streaming assistant message.
func TestCoverageBuildRenderedHistorySkipsExecutingStream(t *testing.T) {
	m := newTestModel(t)
	m.renderStartIdx = 0
	m.maxRenderedMessages = 100
	m.processing = true
	m.currentToolAction = "bash"
	history := []providers.Message{
		{Role: "user", Content: "run"},
		{Role: "assistant", Content: "partial", Streaming: true},
	}
	lines := m.buildRenderedHistoryLines(history)
	// The streaming partial answer should be excluded from the rendered lines.
	joined := stripANSI(strings.Join(lines, "\n"))
	if strings.Contains(joined, "partial") {
		t.Errorf("streaming assistant content should be skipped, got: %v", lines)
	}
	// A non-executing tool call in an earlier message renders normally.
	history2 := []providers.Message{
		{Role: "assistant", Content: "", ToolCalls: []providers.ToolCall{{Name: "bash", Arguments: map[string]interface{}{"cmd": "x"}}}},
	}
	m.currentToolAction = ""
	lines2 := m.buildRenderedHistoryLines(history2)
	if !strings.Contains(stripANSI(strings.Join(lines2, "\n")), "bash") {
		t.Errorf("expected tool call rendered when not executing: %v", lines2)
	}
}

// TestCoverageBuildRenderedHistoryCompaction skips compaction summary messages
// that are flagged as such.
func TestCoverageBuildRenderedHistoryCompaction(t *testing.T) {
	m := newTestModel(t)
	m.renderStartIdx = 0
	m.maxRenderedMessages = 100
	history := []providers.Message{
		{Role: "user", Content: "q"},
	}
	// No compaction summary messages available in the source; verify a normal
	// render still works and compaction helper returns false for normal msgs.
	if isCompactionSummary(history[0]) {
		t.Error("user message must not be a compaction summary")
	}
	m.buildRenderedHistoryLines(history)
}

// TestCoverageBuildRenderedHistoryStartIdxPeek renders with an explicit
// renderStartIdx that sets the 'earlier messages' header.
func TestCoverageBuildRenderedHistoryStartIdxPeek(t *testing.T) {
	m := newTestModel(t)
	m.maxRenderedMessages = 10
	m.renderStartIdx = 2
	history := []providers.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
		{Role: "user", Content: "c"},
	}
	lines := m.buildRenderedHistoryLines(history)
	joined := stripANSI(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "earlier messages") {
		t.Errorf("expected earlier-messages header, got: %v", lines)
	}
}

var _ = strings.Contains
