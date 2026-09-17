package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// TestStatusCommand_NewChatCreatesSession verifies that /status on the welcome
// screen (currentKey == "") creates a new session and dismisses the welcome
// screen so the report is visible.
func TestStatusCommand_NewChatCreatesSession(t *testing.T) {
	m := newTestModel(t)

	// Fresh TUI on the welcome screen: no active session yet.
	if m.currentKey != "" {
		t.Fatalf("expected fresh model with empty currentKey, got %q", m.currentKey)
	}
	if !m.showWelcome {
		t.Fatal("expected welcome screen to be shown initially")
	}

	// Execute /status on the welcome screen. It is a purely local command, so
	// it must not schedule any follow-up work.
	if cmd := m.executeCommand("/status"); cmd != nil {
		t.Fatalf("expected /status to return nil, got %v", cmd)
	}

	// A session must have been created and the welcome screen dismissed.
	if m.currentKey == "" {
		t.Fatal("expected /status to create a session on the welcome screen")
	}
	if m.showWelcome {
		t.Fatal("expected /status to dismiss the welcome screen so the report renders")
	}
}

// TestStatusCommand_PopulatesFeedback verifies that /status fills
// statusFeedback with the expected context-usage report fields and clears any
// stale compactFeedback.
func TestStatusCommand_PopulatesFeedback(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")

	// Pre-set stale compact feedback to verify /status clears it.
	m.compactFeedback = "STALE"

	m.executeCommand("/status")

	if m.statusFeedback == "" {
		t.Fatal("expected statusFeedback to be non-empty after /status")
	}

	report := m.statusFeedback

	// The report must contain each expected i18n key.
	mustContain := []string{
		i18n.T("tui.currentContext"),
		i18n.T("tui.contextWindow"),
		i18n.T("tui.compactions"),
		i18n.T("tui.totalSent"),
		i18n.T("tui.context"),
	}
	for _, want := range mustContain {
		if !strings.Contains(report, want) {
			t.Errorf("statusFeedback missing %q; report:\n%s", want, report)
		}
	}

	// Stale compact feedback must have been cleared.
	if m.compactFeedback != "" {
		t.Errorf("expected compactFeedback cleared, got %q", m.compactFeedback)
	}
	if strings.Contains(report, "STALE") {
		t.Error("statusFeedback should not contain stale compact feedback")
	}
}

// TestStatusCommand_ClearedOnNextMessage verifies that statusFeedback is
// cleared when the user sends a new message (publishUserMessage path).
func TestStatusCommand_ClearedOnNextMessage(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.executeCommand("/status")

	if m.statusFeedback == "" {
		t.Fatal("expected statusFeedback non-empty after /status")
	}

	// Sending a message must clear the status overlay.
	m.publishUserMessage("hola")

	if m.statusFeedback != "" {
		t.Errorf("expected statusFeedback cleared after publishUserMessage, got %q", m.statusFeedback)
	}
}

// TestStatusCommand_ClearedOnSessionSwitch verifies that statusFeedback is
// cleared by clearStreamingState (the same reset used on session switch).
func TestStatusCommand_ClearedOnSessionSwitch(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.executeCommand("/status")

	if m.statusFeedback == "" {
		t.Fatal("expected statusFeedback non-empty after /status")
	}

	m.clearStreamingState()

	if m.statusFeedback != "" {
		t.Errorf("expected statusFeedback cleared after clearStreamingState, got %q", m.statusFeedback)
	}
}

// TestStatusCommand_Registered verifies that /status appears in the global
// allCommands slice.
func TestStatusCommand_Registered(t *testing.T) {
	found := false
	for _, c := range allCommands {
		if c.name == "/status" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected /status to be registered in allCommands")
	}
}

// TestBuildStatusReport_NoWindowDoesNotPanic verifies that buildStatusReport
// does not panic even when the session has no provider/window configured
// (window == 0), which could trigger a divide-by-zero in the percentage calc.
func TestBuildStatusReport_NoWindowDoesNotPanic(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")

	// buildStatusReport may encounter window == 0 in test config (no real
	// provider configured). It must not panic and must return a non-empty
	// string.
	report := m.buildStatusReport()
	if report == "" {
		t.Fatal("expected buildStatusReport to return a non-empty string")
	}
}
