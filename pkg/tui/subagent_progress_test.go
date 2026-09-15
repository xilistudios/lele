package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/bus"
)

// TUI-M4: subagentProgress (taskID → latest action) was written on subagent
// tool.executing / message.stream events but never rendered and never
// cleared on session switch. These tests cover the render path, the
// session-switch lifecycle, completion-driven deletion, and the map cap.

// setupSubagentProgressModel builds a model viewing a parent chat session,
// sized so updateViewport produces a real frame. The sidebar is visible by
// default (showWelcome=false, onboardingActive=false, modalMode=ModalNone).
func setupSubagentProgressModel(t *testing.T) (*Model, string) {
	t.Helper()
	m := newTestModelWithDenyPatterns(t)
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = up.(*Model)

	key := "tui:chat:subprog"
	m.sessionMgr.GetOrCreate(key)
	m.setCurrentChatKey(key)
	m.showWelcome = false
	m.viewport.Width = 118
	return m, key
}

// publishAndDrain publishes one outbound event and feeds it through the
// model's outbound listener (same drain pattern as approval_test.go).
func publishAndDrain(t *testing.T, m *Model, ev bus.OutboundMessage) {
	t.Helper()
	m.agentLoop.MessageBus().PublishOutbound(ev)
	cmd := m.startOutboundListener()
	if cmd == nil {
		t.Fatal("outbound listener returned nil cmd")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("outbound listener returned nil msg")
	}
	om, ok := msg.(outboundMsg)
	if !ok {
		t.Fatalf("expected outboundMsg, got %T", msg)
	}
	up, _ := m.Update(om)
	*m = *up.(*Model)
}

// overlayContent calls updateViewport and returns the joined overlay lines.
func overlayContent(m *Model) string {
	m.updateViewport()
	return strings.Join(m.viewport.overlayLines, "\n")
}

func TestSubagentProgressRenderedInOverlay(t *testing.T) {
	m, key := setupSubagentProgressModel(t)
	m.processing = true
	// Hide sidebar so the inline progress block renders.
	m.showWelcome = true

	publishAndDrain(t, m, bus.OutboundMessage{
		Channel: "native",
		ChatID:  "native:" + key + ":subagent-1",
		Event:   "tool.executing",
		Metadata: map[string]string{
			"tool":   "exec",
			"action": "running go test ./pkg/tui/",
		},
	})

	got, ok := m.subagentProgress["subagent-1"]
	if !ok {
		t.Fatal("tool.executing for subagent session was not recorded in subagentProgress")
	}
	if got != "running go test ./pkg/tui/" {
		t.Errorf("progress action = %q, want the metadata action", got)
	}

	overlay := overlayContent(m)
	if !strings.Contains(overlay, "running go test ./pkg/tui/") {
		t.Error("overlay does not render the subagent progress action line")
	}
	if !strings.Contains(overlay, "subagents") {
		t.Error("overlay does not render the subagent progress header")
	}
}

// TestSubagentProgressRenderedWithoutStreaming verifies the overlay also shows
// when the parent has nothing streaming (e.g. waiting on wait_for_subagent) —
// the hasOverlay gate must include the progress map, not just hasStreaming.
func TestSubagentProgressRenderedWithoutStreaming(t *testing.T) {
	m, _ := setupSubagentProgressModel(t)
	m.processing = false
	m.currentStream = ""
	m.currentToolAction = ""
	// Hide sidebar so the inline progress block renders.
	m.showWelcome = true

	m.subagentProgress = map[string]string{"subagent-2": "scanning files"}
	overlay := overlayContent(m)
	if !strings.Contains(overlay, "scanning files") {
		t.Error("progress line must render even with no parent stream active")
	}
}

func TestSubagentProgressClearedOnSessionSwitch(t *testing.T) {
	m, _ := setupSubagentProgressModel(t)
	m.subagentProgress = map[string]string{"subagent-1": "old parent progress"}
	m.setCurrentChatKey("tui:chat:other")

	if len(m.subagentProgress) != 0 {
		t.Errorf("subagentProgress must be empty after session switch, got %d entries", len(m.subagentProgress))
	}
}

func TestSubagentProgressDeletedOnSubagentResult(t *testing.T) {
	m, key := setupSubagentProgressModel(t)
	m.processing = true

	m.subagentProgress = map[string]string{
		"subagent-1": "finalizing…",
		"subagent-2": "still working",
	}

	publishAndDrain(t, m, bus.OutboundMessage{
		Channel: "native",
		ChatID:  key,
		Event:   "subagent.result",
		Metadata: map[string]string{
			"task_id": "subagent-1",
			"result":  "done",
		},
	})

	if _, ok := m.subagentProgress["subagent-1"]; ok {
		t.Error("completed task entry must be deleted from subagentProgress on subagent.result")
	}
	if m.subagentProgress["subagent-2"] != "still working" {
		t.Error("unrelated task entry must be preserved on subagent.result")
	}
}

func TestSubagentProgressCappedAt16(t *testing.T) {
	m, _ := setupSubagentProgressModel(t)

	for i := 1; i <= 20; i++ {
		m.recordSubagentProgress("subagent-"+string(rune('0'+i/10))+string(rune('0'+i%10)), "working")
	}

	if len(m.subagentProgress) != subagentProgressCap {
		t.Fatalf("subagentProgress should be capped at %d, got %d", subagentProgressCap, len(m.subagentProgress))
	}
	// FIFO eviction: the lowest-numbered (oldest) tasks are gone; 20 inserts
	// against a cap of 16 evict exactly subagent-01..subagent-04.
	if _, ok := m.subagentProgress["subagent-01"]; ok {
		t.Error("oldest entry subagent-01 should have been evicted")
	}
	if _, ok := m.subagentProgress["subagent-04"]; ok {
		t.Error("subagent-04 should have been evicted")
	}
	if _, ok := m.subagentProgress["subagent-05"]; !ok {
		t.Error("subagent-05 is inside the cap and must survive eviction")
	}
	if _, ok := m.subagentProgress["subagent-20"]; !ok {
		t.Error("newest entry subagent-20 must survive eviction")
	}
}

func TestSubagentProgressShowsPlusNMore(t *testing.T) {
	m, _ := setupSubagentProgressModel(t)
	// Hide sidebar so the inline progress block renders.
	m.showWelcome = true

	for i := 1; i <= 5; i++ {
		m.recordSubagentProgress("subagent-"+string(rune('0'+i)), "working")
	}
	m.processing = true
	overlay := overlayContent(m)
	for _, id := range []string{"1", "2", "3"} {
		if !strings.Contains(overlay, id+" working") {
			t.Errorf("overlay should show subagent-%s line", id)
		}
	}
	if strings.Contains(overlay, "4 working") {
		t.Error("overlay should show at most 3 progress lines, saw subagent-4")
	}
	if !strings.Contains(overlay, "+2 more") {
		t.Error("overlay should summarize remaining entries as \"+2 more\"")
	}
}

// TestSubagentProgressHiddenWhenSidebarVisible verifies that the inline
// "⏳ subagents" overlay block is NOT rendered when the chat sidebar is
// visible (normal chat layout).
func TestSubagentProgressHiddenWhenSidebarVisible(t *testing.T) {
	m, _ := setupSubagentProgressModel(t)
	// Sidebar is visible: showWelcome=false (default from setup), no modal.
	if !m.isChatSidebarVisible() {
		t.Fatal("precondition: sidebar should be visible in default setup")
	}
	m.subagentProgress = map[string]string{"subagent-1": "working"}

	overlay := overlayContent(m)
	if strings.Contains(overlay, "⏳ subagents") {
		t.Error("overlay must NOT contain the subagent header when sidebar is visible")
	}
	if strings.Contains(overlay, "working") {
		t.Error("overlay must NOT contain subagent progress text when sidebar is visible")
	}
}

// TestSubagentProgressShownWhenSidebarHidden verifies that renderSubagentProgress
// returns a non-empty string containing expected content when the sidebar is not
// visible (e.g. welcome screen).
func TestSubagentProgressShownWhenSidebarHidden(t *testing.T) {
	m, _ := setupSubagentProgressModel(t)
	m.showWelcome = true
	if m.isChatSidebarVisible() {
		t.Fatal("precondition: sidebar should be hidden when showWelcome=true")
	}
	m.subagentProgress = map[string]string{"subagent-1": "working"}

	result := m.renderSubagentProgress()
	if result == "" {
		t.Fatal("renderSubagentProgress should return non-empty when sidebar is hidden")
	}
	if !strings.Contains(result, "subagents") {
		t.Error("renderSubagentProgress should contain the subagent header")
	}
	if !strings.Contains(result, "1 working") {
		t.Error("renderSubagentProgress should contain the task progress line")
	}
}
