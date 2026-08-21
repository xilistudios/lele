package tui

import (
	"strings"
	"testing"
)

func TestExecuteCommandEmptyNil(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.executeCommand("   "); cmd != nil {
		t.Error("expected nil cmd for empty input")
	}
	if cmd := m.executeCommand(""); cmd != nil {
		t.Error("expected nil cmd for empty input")
	}
}

func TestExecuteCommandUnknown(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.executeCommand("/wibble"); cmd != nil {
		t.Error("expected nil cmd for unknown command")
	}
}

func TestExecuteCommandSessions(t *testing.T) {
	m := newTestModel(t)
	m.currentMode = ModeAgent
	m.executeCommand("/new")
	if m.currentKey == "" {
		t.Fatal("expected session after /new")
	}
	m.executeCommand("/sessions")
	if m.modalMode != ModalSessions {
		t.Errorf("expected ModalSessions, got %v", m.modalMode)
	}
}

func TestExecuteCommandNew(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	if m.currentKey == "" {
		t.Fatal("expected /new to create a session")
	}
	if !m.showWelcome {
		t.Error("expected showWelcome true after /new")
	}
}

func TestExecuteCommandAgents(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/agents")
	if m.modalMode != ModalAgent {
		t.Errorf("expected ModalAgent, got %v", m.modalMode)
	}
}

func TestExecuteCommandModels(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/models")
	if m.modalMode != ModalModel {
		t.Errorf("expected ModalModel, got %v", m.modalMode)
	}
}

func TestExecuteCommandClear(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.processing = false
	m.executeCommand("/clear")
}

func TestExecuteCommandThink(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/think")
	if m.modalMode != ModalThink {
		t.Errorf("expected ModalThink, got %v", m.modalMode)
	}
	if len(m.modalItems) != 4 {
		t.Errorf("expected 4 think levels, got %v", m.modalItems)
	}
}

func TestExecuteCommandLang(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/lang")
	if m.modalMode != ModalLang {
		t.Errorf("expected ModalLang, got %v", m.modalMode)
	}
	if len(m.modalItems) != 3 {
		t.Errorf("expected 3 languages, got %v", m.modalItems)
	}
}

func TestExecuteCommandSubagents(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.executeCommand("/subagents")
	if m.modalMode != ModalSubagents {
		t.Errorf("expected ModalSubagents, got %v", m.modalMode)
	}
}

func TestExecuteCommandBg(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/bg")
	if m.modalMode != ModalBackgroundExecs {
		t.Errorf("expected ModalBackgroundExecs, got %v", m.modalMode)
	}
}

func TestExecuteCommandCron(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/cron")
	if m.modalMode != ModalCron {
		t.Errorf("expected ModalCron, got %v", m.modalMode)
	}
}

func TestExecuteCommandSecrets(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/secrets")
	if m.modalMode != ModalSecrets {
		t.Errorf("expected ModalSecrets, got %v", m.modalMode)
	}
}

func TestExecuteCommandSkills(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/skills")
	if m.modalMode != ModalSkills {
		t.Errorf("expected ModalSkills, got %v", m.modalMode)
	}
}

func TestExecuteCommandSettings(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/settings")
	if m.modalMode != ModalSettings {
		t.Errorf("expected ModalSettings, got %v", m.modalMode)
	}
	if len(m.modalItems) != 3 {
		t.Errorf("expected 3 settings items, got %v", m.modalItems)
	}
}

func TestExecuteCommandProviders(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/providers")
	if m.modalMode != ModalProviders {
		t.Errorf("expected ModalProviders, got %v", m.modalMode)
	}
}

func TestExecuteCommandConnect(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/connect")
	if m.modalMode != ModalAddProvider {
		t.Errorf("expected ModalAddProvider, got %v", m.modalMode)
	}
}

func TestExecuteCommandAddModel(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/add-model")
	if m.modalMode != ModalAddModel {
		t.Errorf("expected ModalAddModel, got %v", m.modalMode)
	}
}

func TestExecuteCommandCompact(t *testing.T) {
	m := newTestModel(t)
	// No current key → no-op nil cmd.
	if cmd := m.executeCommand("/compact"); cmd != nil && m.currentKey == "" {
		t.Error("expected no compact cmd without session")
	}
	m.executeCommand("/new")
	cmd := m.executeCommand("/compact")
	_ = cmd
}

func TestExecuteCommandGoalSetsProcessing(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/goal Write a summary")
	if m.currentKey == "" {
		t.Fatal("expected /goal to create session")
	}
	if !m.processing {
		t.Error("expected processing true for goal set")
	}
	if !strings.Contains(m.currentToolAction, "Goal") {
		t.Errorf("expected goal tool action, got %q", m.currentToolAction)
	}
}

func TestExecuteCommandGoalStatus(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.processing = false
	// status/pause/resume/clear shouldn't trigger processing kickoff.
	m.executeCommand("/goal status")
	if m.currentToolAction == "" {
		t.Error("expected tool action for status")
	}
}

func TestExecuteCommandQuit(t *testing.T) {
	m := newTestModel(t)
	cmd := m.executeCommand("/quit")
	if cmd == nil {
		t.Fatal("expected quit cmd")
	}
}

func TestIsGoalSetCommand(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"status"}, false},
		{[]string{"pause"}, false},
		{[]string{"resume"}, false},
		{[]string{"clear"}, false},
		{[]string{"write", "code"}, true},
		{[]string{"summarize"}, true},
	}
	for _, tt := range tests {
		if got := isGoalSetCommand(tt.args); got != tt.want {
			t.Errorf("isGoalSetCommand(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestExecuteCommandBGVithProcesses(t *testing.T) {
	// Ensure bg modal handles process entries if present. Hard to inject into
	// agent loop without a running bg process; just ensure base path works.
	m := newTestModel(t)
	m.executeCommand("/bg")
	if m.modalMode != ModalBackgroundExecs {
		t.Errorf("expected ModalBackgroundExecs, got %v", m.modalMode)
	}
}