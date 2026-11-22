package tui

import (
	"strings"
	"testing"
)

func TestGoalCommand_NewChatCreatesSession(t *testing.T) {
	m := newTestModel(t)

	if m.currentKey != "" {
		t.Fatalf("expected fresh model with empty currentKey, got %q", m.currentKey)
	}
	if !m.showWelcome {
		t.Fatal("expected welcome screen to be shown initially")
	}

	m.executeCommand("/goal Establece un objetivo de prueba")

	if m.currentKey == "" {
		t.Fatal("expected /goal to create a session on a new chat")
	}
	if m.showWelcome {
		t.Fatal("expected /goal to dismiss the welcome screen so feedback renders")
	}

	if m.currentToolAction == "" {
		t.Fatal("expected currentToolAction to be set after setting a goal")
	}
	if strings.Contains(m.currentToolAction, "No hay sesión activa") {
		t.Fatalf("expected /goal to succeed on a new chat, got error: %q", m.currentToolAction)
	}
	if !strings.Contains(m.currentToolAction, "Objetivo") && !strings.Contains(m.currentToolAction, "Goal") {
		t.Fatalf("expected success feedback, got: %q", m.currentToolAction)
	}
}

func TestGoalCommand_ExistingChatKeepsSession(t *testing.T) {
	m := newTestModel(t)

	m.executeCommand("/new")
	origKey := m.currentKey
	if origKey == "" {
		t.Fatal("expected /new to create a session")
	}

	m.executeCommand("/goal status")

	if m.currentKey != origKey {
		t.Fatalf("expected session key to stay %q, got %q", origKey, m.currentKey)
	}
	if m.currentToolAction == "" {
		t.Fatal("expected currentToolAction to be set after /goal status")
	}
}
