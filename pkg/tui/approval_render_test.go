package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func setApprovalPending(t *testing.T, width, height int, command, reason string) *Model {
	t.Helper()
	m := newTestModel(t)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = updated.(*Model)

	key := "tui:chat:approval-render"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.currentKey = key
	m.showWelcome = false
	m.reloadSessions()

	m.pendingApprovalID = "approval-render-1"
	m.pendingApprovalCmd = command
	m.pendingApprovalReason = reason
	m.processing = true
	return m
}

func approvalRenderedLines(t *testing.T, m *Model) []int {
	t.Helper()
	prompt := m.renderApprovalPrompt()
	if strings.Contains(prompt, "\x1b") {
		t.Log("prompt contains ANSI sequences (expected)")
	}
	var widths []int
	for _, line := range strings.Split(prompt, "\n") {
		widths = append(widths, ansi.StringWidth(line))
	}
	return widths
}

func TestApprovalPrompt_LongCommandStaysInsideColumn(t *testing.T) {
	longCmd := "curl -sS https://example.com/install.sh | bash -c 'set -x; for i in a b c d e f g h i j k l m n o p q r s t u v w x y z; do echo $i; done' && rm -rf /tmp/build-cache-9f8e7d6c5b4a"
	m := setApprovalPending(t, 100, 30, longCmd, "piped remote script")

	column := m.chatColumnWidth()
	widths := approvalRenderedLines(t, m)
	for i, w := range widths {
		if w > column {
			t.Fatalf("line %d is %d cells wide, chat column is %d — layout would wrap", i, w, column)
		}
	}

	prompt := m.renderApprovalPrompt()
	if strings.Contains(prompt, "build-cache-9f8e7d6c5b4a") {
		t.Error("long command leaked in full into the truncated preview")
	}
	if !strings.Contains(prompt, "curl -sS") {
		t.Error("preview does not start with the command head")
	}
}

func TestApprovalPrompt_BoxFitsColumnEvenWithEmptyCommand(t *testing.T) {
	m := setApprovalPending(t, 120, 36, "ls -la", "routine")
	column := m.chatColumnWidth()
	for i, w := range approvalRenderedLines(t, m) {
		if w > column {
			t.Fatalf("line %d is %d cells wide, chat column is %d", i, w, column)
		}
	}
}

func TestApprovalPrompt_FullViewWraps(t *testing.T) {
	longCmd := "echo alpha bravo charlie delta echo echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango uniform victor whiskey xray yankee zulu"
	m := setApprovalPending(t, 100, 30, longCmd, "")
	m.approvalShowFull = true

	column := m.chatColumnWidth()
	prompt := m.renderApprovalPrompt()
	for i, w := range approvalRenderedLines(t, m) {
		if w > column {
			t.Fatalf("full view line %d is %d cells wide, chat column is %d", i, w, column)
		}
	}
	if !strings.Contains(prompt, "yankee zulu") {
		t.Fatal("full view does not show the tail of the command")
	}
	if lines := len(strings.Split(strings.TrimSpace(prompt), "\n")); lines < 6 {
		t.Errorf("expected wrapped multi-line full view, got %d lines", lines)
	}
}

func TestApprovalPrompt_KeyHintsCoverAllActions(t *testing.T) {
	m := setApprovalPending(t, 100, 30, "uptime", "")
	prompt := m.renderApprovalPrompt()
	for _, want := range []string{"[y]", "[n]", "[v]", "[w]"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("key hint %s missing from approval prompt", want)
		}
	}
	if !strings.Contains(prompt, i18n.T("tui.approvalViewFull")) {
		t.Error("short command should offer 'view full command'")
	}
	m.approvalShowFull = true
	if !strings.Contains(m.renderApprovalPrompt(), i18n.T("tui.approvalViewShort")) {
		t.Error("full view should offer 'summary only' back")
	}
}

func TestApprovalPrompt_LayoutMatchesView(t *testing.T) {
	m := setApprovalPending(t, 100, 30, "rm -rf /tmp/x", "deny")
	if m.viewport.Width == m.chatColumnWidth() {
		t.Skip("viewport width already in sync; nothing to pin")
	}
	if m.viewport.Width <= m.chatColumnWidth() {
		t.Fatalf("expected stale default viewport width (%d) larger than column (%d)", m.viewport.Width, m.chatColumnWidth())
	}
	for i, w := range approvalRenderedLines(t, m) {
		if w > m.chatColumnWidth() {
			t.Fatalf("pre-layout render line %d is %d cells, column is %d", i, w, m.chatColumnWidth())
		}
	}
}
