package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

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
	i18n.InitWithLanguage("en")
	seedRunningSubagents(m, 2)
	m.processing = false
	m.startTime = time.Time{}
	m.lastDuration = 0

	line := m.renderStatusLine(100)

	want := fmt.Sprintf(i18n.T("tui.subagentsRunning"), 2)
	if strings.TrimSpace(line) != want {
		t.Fatalf("status line = %q, want exactly %q", line, want)
	}
	if strings.Contains(line, m.getBouncingDots()) {
		t.Fatalf("status line = %q must not render the busy spinner", line)
	}
}

func TestSubagentsRunningText_ReportsOnlyBackgroundWork(t *testing.T) {
	composerHints := []string{"input", "free", "escrib", "escrev", "writing", "digitar", "escribir"}

	for _, lang := range i18n.AvailableLanguages() {
		i18n.SetLanguage(lang)
		text := fmt.Sprintf(i18n.T("tui.subagentsRunning"), 3)
		if !strings.HasPrefix(text, "3 ") {
			t.Fatalf("%s: tui.subagentsRunning = %q, expected it to open with the subagent count", lang, text)
		}
		if strings.ContainsAny(text, "—-→") {
			t.Errorf("%s: tui.subagentsRunning = %q must not carry a trailing hint after a separator", lang, text)
		}
		lower := strings.ToLower(text)
		for _, hint := range composerHints {
			if strings.Contains(lower, hint) {
				t.Errorf("%s: tui.subagentsRunning = %q must only report background work, found composer hint %q", lang, text, hint)
			}
		}
	}

	i18n.InitWithLanguage("en")
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
