package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStreamingToNonStreaming_ScrollsToBottom(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:stream-scroll-test"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	m.sessionMgr.AddMessage(key, "user", "Hello, what is Go?")

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

	baseWithStream := len(m.viewport.baseLines)

	m.sessionMgr.AddMessage(key, "assistant",
		"Go is a programming language designed at Google. It is a statically typed, compiled language.")

	m.processing = false
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""
	m.forceGotoBottom = true

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

	baseContent := strings.Join(m.viewport.baseLines, "\n")
	if !strings.Contains(baseContent, "designed at Google") {
		t.Error("final assistant response not visible in rendered base after stream completion")
	}
}

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

	m.sessionMgr.AppendAssistantChunk(key, "Here are the best Go tutorials I found...")
	m.currentStream = "Here are the best Go tutorials"
	m.currentToolAction = ""
	m.updateViewport()

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

	baseContent := stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
	if !strings.Contains(baseContent, "tutorials I found") {
		t.Errorf("final response content not visible in viewport base after tool call + streaming flow.\n"+
			"  base lines count: %d, renderedBaseMsgCount: %d, renderedBaseLastStreaming: %v",
			len(m.viewport.baseLines), m.renderedBaseMsgCount, m.renderedBaseLastStreaming)
	}
}

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

func TestAutoScroll_NewMessageNoOverlay(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:autoscroll-no-overlay"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

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

	m.viewport.YOffset = 0
	if m.viewport.AtBottom() {
		t.Fatal("viewport should NOT be at bottom after scrolling up")
	}

	m.sessionMgr.AddMessage(key, "user", "New question after scrolling up")
	m.sessionMgr.AddMessage(key, "assistant",
		"New answer that is long enough to fill several lines in the viewport "+
			"so the content overflows and scrolling is needed to see it all.")

	m.forceGotoBottom = true
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Errorf("BUG: viewport did not auto-scroll to bottom when new message arrived "+
			"with no overlay. YOffset=%d maxYOffset=%d totalLines=%d",
			m.viewport.YOffset, m.viewport.maxYOffset(), m.viewport.totalLines())
	}

	baseContent := stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
	if !strings.Contains(baseContent, "New answer") {
		t.Error("new assistant message not found in rendered base after auto-scroll")
	}
}

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

	m.viewport.YOffset = 0
	m.forceGotoBottom = false

	m.updateViewport()

	if m.viewport.AtBottom() {
		t.Errorf("BUG: viewport scrolled to bottom in fast path when user had scrolled up "+
			"and forceGotoBottom was false. YOffset=%d maxYOffset=%d",
			m.viewport.YOffset, m.viewport.maxYOffset())
	}
}

func TestAutoScroll_AtBottomNewMessageNoForceGotoBottom(t *testing.T) {
	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = updated.(*Model)

	key := "tui:chat:autoscroll-atbottom-no-force"
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
	m.currentStream = ""
	m.currentThinking = ""
	m.currentToolAction = ""
	m.forceGotoBottom = true
	m.reloadSessions()
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Fatalf("precondition: viewport should be at bottom, YOffset=%d maxYOffset=%d",
			m.viewport.YOffset, m.viewport.maxYOffset())
	}

	m.forceGotoBottom = false

	m.sessionMgr.AddMessage(key, "user", "A brand new question after being at the bottom")
	m.sessionMgr.AddMessage(key, "assistant",
		"A brand new answer that is long enough to fill several lines in the viewport "+
			"so that the content overflows and scrolling is required to see it.")

	m.reloadSessions()
	m.updateViewport()

	if !m.viewport.AtBottom() {
		t.Errorf("BUG: viewport did not auto-scroll to bottom when a new message arrived "+
			"while at the bottom and forceGotoBottom was false. YOffset=%d maxYOffset=%d totalLines=%d",
			m.viewport.YOffset, m.viewport.maxYOffset(), m.viewport.totalLines())
	}

	baseContent := stripAnsi(strings.Join(m.viewport.baseLines, "\n"))
	if !strings.Contains(baseContent, "A brand new answer") {
		t.Error("new assistant message not found in rendered base after auto-scroll")
	}
}
