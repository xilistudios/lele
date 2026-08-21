package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// --- Additional executeCommand coverage ---

// addTestProvider wires a named provider with a model into the model's config
// so listProviders / listProviderModels / /providers / /models work.
func addTestProvider(t *testing.T, m *Model) {
	t.Helper()
	if m.cfg == nil {
		t.Fatal("expected cfg")
	}
	if m.cfg.Providers == nil {
		m.cfg.Providers = &config.ProvidersConfig{}
	}
	m.cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {
			Type:           "openai",
			ProviderConfig: config.ProviderConfig{APIKey: "sk-xxx"},
			Models:         map[string]config.ProviderModelConfig{"gpt-4o": {Model: "gpt-4o"}},
		},
	}
}

// TestExecCmdModelsPreselect exercises /models model preselection branch when
// the config snapshot has providers and the session model matches an item.
func TestExecCmdModelsPreselect(t *testing.T) {
	m := newTestModel(t)
	addTestProvider(t, m)
	m.executeCommand("/new")
	m.agentLoop.GetProvidable().SetSessionModel(m.currentKey, "openai:gpt-4o")
	m.executeCommand("/models")
	if m.modalMode != ModalModel {
		t.Fatalf("expected ModalModel, got %v", m.modalMode)
	}
	found := false
	for _, item := range m.modalItems {
		if strings.Contains(item, "gpt-4o") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected gpt-4o in models list, got %v", m.modalItems)
	}
}

// TestExecCmdClearWithPendingModel covers the /clear pendingModel branch.
func TestExecCmdClearWithPendingModel(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.pendingModel = "openai:gpt-4o"
	m.executeCommand("/clear")
	if m.modalMode != ModalNone {
		t.Errorf("expected no modal after /clear, got %v", m.modalMode)
	}
}

// TestExecCmdGoalTurnsFlag covers the --turns strip branch in /goal.
func TestExecCmdGoalTurnsFlag(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/goal Write code --turns 5")
	if !strings.Contains(m.currentToolAction, "Goal") {
		t.Errorf("expected goal tool action, got %q", m.currentToolAction)
	}
	if strings.Contains(m.currentToolAction, "--turns") {
		t.Errorf("expected --turns stripped from display, got %q", m.currentToolAction)
	}
}

// TestExecCmdProvidersWithProviders covers /providers when providers exist.
func TestExecCmdProvidersWithProviders(t *testing.T) {
	m := newTestModel(t)
	addTestProvider(t, m)
	m.executeCommand("/providers")
	if m.modalMode != ModalProviders {
		t.Fatalf("expected ModalProviders, got %v", m.modalMode)
	}
	if len(m.providerModalKeys) == 0 {
		t.Error("expected at least one provider key")
	}
}

// TestExecCmdSubagentsWithTasks covers /subagents with non-empty tasks via the
// native provider path.
func TestExecCmdSubagentsWithTasks(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/new")
	m.executeCommand("/subagents")
	if m.modalMode != ModalSubagents {
		t.Fatalf("expected ModalSubagents, got %v", m.modalMode)
	}
}

// TestExecCmdBgNoProcesses verifies the no-processes branch of /bg.
func TestExecCmdBgNoProcesses(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/bg")
	if m.modalMode != ModalBackgroundExecs {
		t.Fatalf("expected ModalBackgroundExecs, got %v", m.modalMode)
	}
	if len(m.modalItems) == 0 {
		t.Error("expected at least the no-processes message in /bg items")
	}
}