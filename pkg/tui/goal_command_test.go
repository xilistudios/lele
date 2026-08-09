package tui

import (
	"strings"
	"testing"
)

func TestGoalCommand_NewChatCreatesSession(t *testing.T) {
	m := newTestModel(t)

	// Fresh TUI on the welcome screen: no active session yet.
	if m.currentKey != "" {
		t.Fatalf("expected fresh model with empty currentKey, got %q", m.currentKey)
	}
	if !m.showWelcome {
		t.Fatal("expected welcome screen to be shown initially")
	}

	// Execute /goal on the welcome screen (no session).
	m.executeCommand("/goal Establece un objetivo de prueba")

	// A session must have been created and the welcome screen dismissed so
	// the feedback is actually rendered.
	if m.currentKey == "" {
		t.Fatal("expected /goal to create a session on a new chat")
	}
	if m.showWelcome {
		t.Fatal("expected /goal to dismiss the welcome screen so feedback renders")
	}

	// The feedback should reflect the goal being set, not the "no active
	// session" error.
	if m.goalFeedback == "" {
		t.Fatal("expected goalFeedback to be set")
	}
	if strings.Contains(m.goalFeedback, "No hay sesión activa") {
		t.Fatalf("expected /goal to succeed on a new chat, got error feedback: %q", m.goalFeedback)
	}
	if !strings.Contains(m.goalFeedback, "Objetivo establecido") {
		t.Fatalf("expected success feedback, got: %q", m.goalFeedback)
	}
}

func TestGoalCommand_ExistingChatKeepsSession(t *testing.T) {
	m := newTestModel(t)

	// Simulate an existing chat by creating one first.
	m.executeCommand("/new")
	origKey := m.currentKey
	if origKey == "" {
		t.Fatal("expected /new to create a session")
	}

	m.executeCommand("/goal status")

	if m.currentKey != origKey {
		t.Fatalf("expected session key to stay %q, got %q", origKey, m.currentKey)
	}
	if m.goalFeedback == "" {
		t.Fatal("expected goalFeedback to be set")
	}
}
