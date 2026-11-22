package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func TestStatusCommand_NewChatCreatesSession(t *testing.T) {
	m := newTestModel(t)

	if m.currentKey != "" {
		t.Fatalf("expected fresh model with empty currentKey, got %q", m.currentKey)
	}
	if !m.showWelcome {
		t.Fatal("expected welcome screen to be shown initially")
	}

	if cmd := m.executeCommand("/status"); cmd != nil {
		t.Fatalf("expected /status to return nil, got %v", cmd)
	}

	if m.currentKey == "" {
		t.Fatal("expected /status to create a session on the welcome screen")
	}
	if m.showWelcome {
		t.Fatal("expected /status to dismiss the welcome screen so the report renders")
	}
}

func TestStatusCommand_PopulatesFeedback(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")

	m.compactFeedback = "STALE"

	m.executeCommand("/status")

	if m.statusFeedback == "" {
		t.Fatal("expected statusFeedback to be non-empty after /status")
	}

	report := m.statusFeedback

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

	if m.compactFeedback != "" {
		t.Errorf("expected compactFeedback cleared, got %q", m.compactFeedback)
	}
	if strings.Contains(report, "STALE") {
		t.Error("statusFeedback should not contain stale compact feedback")
	}
}

func TestStatusCommand_ClearedOnNextMessage(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.executeCommand("/status")

	if m.statusFeedback == "" {
		t.Fatal("expected statusFeedback non-empty after /status")
	}

	m.publishUserMessage("hola")

	if m.statusFeedback != "" {
		t.Errorf("expected statusFeedback cleared after publishUserMessage, got %q", m.statusFeedback)
	}
}

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

func TestBuildStatusReport_NoWindowDoesNotPanic(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")

	report := m.buildStatusReport()
	if report == "" {
		t.Fatal("expected buildStatusReport to return a non-empty string")
	}
}
