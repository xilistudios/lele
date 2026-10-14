package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// B2 — running subagents are background work and must not gate input. These
// tests seed the subagent listing cache directly (no backend call, no 3 s TTL
// involved) so the busy predicate is exercised deterministically.

// seedRunningSubagents installs a fresh cache entry for the visible chat with
// the given number of running subagents. at = now keeps refreshSubagentsCache
// from replacing it during the next Update.
func seedRunningSubagents(m *Model, running int) {
	m.subagentsCache = subagentsCacheState{
		key:     m.subagentQueryKey(),
		running: running,
		at:      time.Now(),
	}
}

func TestIsSessionProcessing_IgnoresRunningSubagents(t *testing.T) {
	m := newQuietQueueModel(t, "tui:chat:subgate")
	seedRunningSubagents(m, 2)
	m.processing = false
	m.startTime = time.Time{}

	if m.isSessionProcessing() {
		t.Fatal("running subagents are background work and must not make the session look busy")
	}
}

func TestEnter_SendsWhileSubagentsRunning(t *testing.T) {
	m := newQuietQueueModel(t, "tui:chat:subgate")
	m.chatInput.Focus()
	seedRunningSubagents(m, 1)
	m.processing = false
	m.startTime = time.Time{}
	m.chatInput.SetValue("hello")

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.queueDepth(); got != 0 {
		t.Fatalf("Enter must not enqueue while only subagents run, queue depth = %d", got)
	}
	msg := drainInbound(t, m)
	if msg.Content != "hello" {
		t.Fatalf("inbound content = %q, want %q", msg.Content, "hello")
	}
}

func TestEnter_QueuesWhenBackendBusy(t *testing.T) {
	m := newQuietQueueModel(t, "tui:chat:subgate")
	m.chatInput.Focus()
	seedRunningSubagents(m, 1)
	// Local startup grace keeps isSessionProcessing() true even before the
	// backend reports busy.
	m.processing = true
	m.startTime = time.Now()
	m.chatInput.SetValue("hello")

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if got := m.queueDepth(); got != 1 {
		t.Fatalf("Enter must enqueue while the session is busy, queue depth = %d, want 1", got)
	}
	expectNoInbound(t, m)
}

func TestEsc_ArmsCancelWhenOnlySubagentsRun(t *testing.T) {
	m := newQuietQueueModel(t, "tui:chat:subgate")
	seedRunningSubagents(m, 1)
	m.processing = false
	m.startTime = time.Time{}
	m.escHint = false

	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if !m.escHint {
		t.Fatal("ESC must reach the cancel gate when only subagents are running")
	}
}

func TestStatusLine_ShowsSubagentsRunningWithoutSpinner(t *testing.T) {
	m := newQuietQueueModel(t, "tui:chat:subgate")
	// NewModel may apply the config's default language; pin English after it so
	// the assertion is locale-stable.
	i18n.InitWithLanguage("en")
	seedRunningSubagents(m, 2)
	m.processing = false
	m.startTime = time.Time{}
	m.lastDuration = 0

	line := m.renderStatusLine(100)

	if !strings.Contains(line, "2 subagent(s) running in background") {
		t.Fatalf("status line = %q, want the subagents-running text", line)
	}
	if strings.Contains(line, m.getBouncingDots()) {
		t.Fatalf("status line = %q must not render the busy spinner", line)
	}
}

func TestQueueFlushesImmediatelyWhenSubagentsRun(t *testing.T) {
	m := newQuietQueueModel(t, "tui:chat:subgate")
	seedRunningSubagents(m, 1)
	m.processing = false
	m.startTime = time.Time{}
	m.enqueueMessage("queued while a subagent runs")

	_ = m.maybeFlushQueue()

	if got := m.queueDepth(); got != 0 {
		t.Fatalf("queue depth after flush = %d, want 0: background subagents must not hold the queue closed", got)
	}
	msg := drainInbound(t, m)
	if msg.Content != "queued while a subagent runs" {
		t.Fatalf("inbound content = %q, want the queued message", msg.Content)
	}
}
