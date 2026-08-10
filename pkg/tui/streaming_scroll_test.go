package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestStreamingToNonStreaming_ScrollsToBottom is a regression test for the bug
// where the TUI didn't scroll to (or display) the final assistant response
// after streaming completed.
//
// Root cause: countHistoryMessages() includes messages with Streaming=true in
// its count. When the session manager replaces the streaming message with the
// final version (Streaming: true → false), the count doesn't change. The
// rendered base cache (which had the streaming message SKIPPED) was not rebuilt
// because the cache-rebuild condition only checked message count changes.
//
// The fix adds renderedBaseLastStreaming tracking: if the last message was
// Streaming=true when the cache was built, and it's now Streaming=false, the
// cache is rebuilt even though the count is unchanged.
func TestStreamingToNonStreaming_ScrollsToBottom(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:stream-scroll-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	// Add a user message
	m.sessionMgr.AddMessage(key, "user", "Hello, what is Go?")

	// Simulate streaming: AppendAssistantChunk creates a message with Streaming=true
	m.sessionMgr.AppendAssistantChunk(key, "Go is a programming language")

	m.currentKey = key
	m.showWelcome = false
	m.processing = true
	m.currentStream = "Go is a programming language"
	m.forceGotoBottom = true
	m.reloadSessions()

	m.viewport.Width = 118
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Fatal("viewport should be at bottom after forceGotoBottom")
	}

	// The rendered base should NOT include the streaming assistant message
	// (it's skipped when m.processing && msg.Streaming && last message).
	baseWithStream := len(m.viewport.baseLines)

	// Simulate stream completion: AddMessage replaces the streaming
	// message with the final version (Streaming: false).
	m.sessionMgr.AddMessage(key, "assistant",
		"Go is a programming language designed at Google. It is a statically typed, compiled language.")

	m.processing = false
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""
	m.forceGotoBottom = true

	// updateViewport should detect the streaming→non-streaming transition
	// and rebuild the base, including the final assistant message.
	m.updateViewport()

	baseAfterComplete := len(m.viewport.baseLines)

	if baseAfterComplete <= baseWithStream {
		t.Errorf("BUG: rendered base did not grow after stream completion.\n"+
			"  base lines during stream: %d\n"+
			"  base lines after complete: %d\n"+
			"  renderedBaseMsgCount: %d, renderedBaseLastStreaming: %v",
			baseWithStream, baseAfterComplete,
			m.renderedBaseMsgCount, m.renderedBaseLastStreaming)
	}

	if !m.viewport.AtBottom() {
		t.Errorf("viewport should be at bottom after completion, got YOffset=%d maxYOffset=%d",
			m.viewport.YOffset, m.viewport.maxYOffset())
	}

	// Verify the final content is in the rendered base
	baseContent := strings.Join(m.viewport.baseLines, "\n")
	if !strings.Contains(baseContent, "designed at Google") {
		t.Error("final assistant response not visible in rendered base after stream completion")
	}
}

// TestStreamingCacheInvalidation_WithToolCalls verifies that the streaming
// cache invalidation works correctly when the assistant response follows
// tool calls (which add messages to history during streaming).
func TestStreamingCacheInvalidation_WithToolCalls(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:stream-tool-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	m.sessionMgr.AddMessage(key, "user", "Search the web for Go tutorials")

	m.currentKey = key
	m.showWelcome = false
	m.processing = true
	m.currentStream = ""
	m.currentToolAction = "web_search: Go tutorials"
	m.forceGotoBottom = true
	m.viewport.Width = 80
	m.reloadSessions()
	m.updateViewport()

	baseDuringTool := len(m.viewport.baseLines)

	// Simulate streaming of the final response after tool call
	m.sessionMgr.AppendAssistantChunk(key, "Here are the best Go tutorials I found...")
	m.currentStream = "Here are the best Go tutorials"
	m.currentToolAction = ""
	m.updateViewport()

	// Stream completes — final message replaces streaming message
	m.sessionMgr.AddMessage(key, "assistant",
		"Here are the best Go tutorials I found:\n1. Go by Example\n2. Tour of Go\n3. Effective Go")
	m.processing = false
	m.currentStream = ""
	m.forceGotoBottom = true
	m.updateViewport()

	baseAfterFinal := len(m.viewport.baseLines)

	if baseAfterFinal <= baseDuringTool {
		t.Errorf("final assistant message not rendered after stream+tool completion.\n"+
			"  during tool: %d base lines, after final: %d base lines",
			baseDuringTool, baseAfterFinal)
	}

	// Verify the final content is in the rendered base. Strip ANSI codes first
	// because glamour wraps individual words with color escape sequences.
	baseContent := stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
	if !strings.Contains(baseContent, "tutorials I found") {
		t.Errorf("final response content not visible in viewport base after tool call + streaming flow.\n"+
			"  base lines count: %d, renderedBaseMsgCount: %d, renderedBaseLastStreaming: %v",
			len(m.viewport.baseLines), m.renderedBaseMsgCount, m.renderedBaseLastStreaming)
	}
}

// stripAnsi removes ANSI escape sequences from s.
func stripAnsi(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}


// TestAutoScroll_NewMessageNoOverlay is a regression test for the bug where
// the viewport didn't auto-scroll to bottom when a new message arrived but
// there was no overlay content (no streaming, no pending messages, no
// approvals). The fast path in updateViewport() returned early before the
// GotoBottom() logic at the end of the function.
//
// Scenario: user sends a message, agent completes, the final assistant
// message is saved in history. When updateViewport() runs, there's no
// overlay (processing=false, no stream), so the fast path is taken.
// The viewport should still scroll to bottom to show the new message.
func TestAutoScroll_NewMessageNoOverlay(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:autoscroll-no-overlay"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	// Add enough messages to overflow the viewport
	for i := 0; i < 20; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Question %d", i))
		m.sessionMgr.AddMessage(key, "assistant",
			fmt.Sprintf("Answer %d with enough text to fill multiple lines "+
				"line two line three line four line five line six.", i))
	}

	m.currentKey = key
	m.showWelcome = false
	m.processing = false
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""
	m.forceGotoBottom = true
	m.reloadSessions()
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Fatalf("viewport should be at bottom after initial load with forceGotoBottom, "+
			"YOffset=%d maxYOffset=%d", m.viewport.YOffset, m.viewport.maxYOffset())
	}

	// Now scroll up to simulate the user reading older messages
	m.viewport.YOffset = 0
	if m.viewport.AtBottom() {
		t.Fatal("viewport should NOT be at bottom after scrolling up")
	}

	// A new message arrives — but there's no overlay (processing already done)
	m.sessionMgr.AddMessage(key, "user", "New question after scrolling up")
	m.sessionMgr.AddMessage(key, "assistant",
		"New answer that is long enough to fill several lines in the viewport "+
			"so the content overflows and scrolling is needed to see it all.")

	// Simulate the state after completion: no overlay, forceGotoBottom set
	m.forceGotoBottom = true
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Errorf("BUG: viewport did not auto-scroll to bottom when new message arrived "+
			"with no overlay. YOffset=%d maxYOffset=%d totalLines=%d",
			m.viewport.YOffset, m.viewport.maxYOffset(), m.viewport.totalLines())
	}

	// Verify the new content is visible in the rendered base
	baseContent := stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
	if !strings.Contains(baseContent, "New answer") {
		t.Error("new assistant message not found in rendered base after auto-scroll")
	}
}

// TestAutoScroll_FastPathPreservesUserScroll verifies that when the user has
// scrolled up and no forceGotoBottom is set, the fast path does NOT scroll
// to bottom — preserving the user's scroll position.
func TestAutoScroll_FastPathPreservesUserScroll(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:autoscroll-preserve"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	for i := 0; i < 20; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("Question %d", i))
		m.sessionMgr.AddMessage(key, "assistant",
			fmt.Sprintf("Answer %d line two line three line four line five.", i))
	}

	m.currentKey = key
	m.showWelcome = false
	m.processing = false
	m.forceGotoBottom = true
	m.reloadSessions()
	m.updateViewport()

	// Scroll up
	m.viewport.YOffset = 0
	m.forceGotoBottom = false // no forced scroll

	// Re-render with no changes — should preserve scroll position
	m.updateViewport()

	if m.viewport.AtBottom() {
		t.Errorf("BUG: viewport scrolled to bottom in fast path when user had scrolled up "+
			"and forceGotoBottom was false. YOffset=%d maxYOffset=%d",
			m.viewport.YOffset, m.viewport.maxYOffset())
	}
}
