package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// newModeSwitchTestModel builds a TUI Model backed by a real agent loop whose
// registry is populated from cfg.Agents.List, so TAB cycling has real agents to
// walk. Groups are enabled/disabled per test.
func newModeSwitchTestModel(t *testing.T, agentIDs []string, groupsEnabled bool) *Model {
	t.Helper()
	cfg := testModelConfig(t)
	cfg.Groups.Enabled = groupsEnabled
	cfg.Agents.List = make([]config.AgentConfig, 0, len(agentIDs))
	for i, id := range agentIDs {
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
			ID:      id,
			Name:    strings.ToUpper(id),
			Default: i == 0,
		})
	}
	m := newTestModelWithConfig(t, cfg, true)
	m.showWelcome = false
	return m
}

// pressTab sends a TAB key through Update, exactly like the terminal does.
func pressTab(t *testing.T, m *Model) *Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	return updated.(*Model)
}

// pressCommand runs a slash command through Update (the same entry point as
// typing it in the composer).
func pressCommand(t *testing.T, m *Model, cmd string) *Model {
	t.Helper()
	m.chatInput.SetValue(cmd)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return updated.(*Model)
}

func TestTabCyclesAgentsCircularly(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta", "gamma"}, false)
	m.createNewChat()

	prov := m.agentLoop.GetProvidable()
	if got := prov.GetSessionAgent(m.currentKey); got != "alpha" {
		t.Fatalf("initial agent = %q, want alpha", got)
	}

	// Sorted order is alpha → beta → gamma → alpha (wrap).
	want := []string{"beta", "gamma", "alpha"}
	for _, expected := range want {
		m = pressTab(t, m)
		if got := prov.GetSessionAgent(m.currentKey); got != expected {
			t.Fatalf("after TAB agent = %q, want %q", got, expected)
		}
		if m.pendingAgent != expected {
			t.Errorf("pendingAgent = %q, want %q", m.pendingAgent, expected)
		}
	}
}

func TestTabCycleLeavesModeUntouched(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, true)
	m.createNewChat()
	m.currentMode = ModeChat

	m = pressTab(t, m)

	if m.currentMode != ModeChat {
		t.Errorf("TAB changed the mode: got %v, want ModeChat", m.currentMode)
	}
	if got := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey); got != "beta" {
		t.Errorf("agent = %q, want beta", got)
	}
}

func TestTabWithSingleAgentIsNoop(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"solo"}, false)
	m.createNewChat()

	m = pressTab(t, m)

	if got := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey); got != "solo" {
		t.Errorf("agent = %q, want solo", got)
	}
	if m.cycleAgent() {
		t.Error("cycleAgent() = true with a single agent, want false")
	}
}

func TestTabOnWelcomeScreenCyclesPendingAgent(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, false)
	m.currentKey = ""
	m.showWelcome = true
	m.pendingAgent = ""

	// No session: the cycle resolves the default agent and moves past it.
	m = pressTab(t, m)
	if m.pendingAgent != "beta" {
		t.Fatalf("pendingAgent = %q, want beta", m.pendingAgent)
	}
	if m.currentKey != "" {
		t.Errorf("TAB created a session (%q); it must only select the agent", m.currentKey)
	}

	// A chat created from here starts on the selected agent.
	m.createNewChat()
	if got := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey); got != "beta" {
		t.Errorf("new chat agent = %q, want beta", got)
	}
}

func TestTabDoesNotCycleUnderModal(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, false)
	m.createNewChat()
	m.resetModal(ModalAgent)

	m = pressTab(t, m)

	if got := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey); got != "alpha" {
		t.Errorf("agent = %q, want alpha (TAB belongs to the modal)", got)
	}
	if m.modalMode != ModalAgent {
		t.Errorf("modalMode = %v, want ModalAgent", m.modalMode)
	}
}

func TestModeCommandCyclesWhenGroupsDisabled(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, false)
	m.createNewChat()

	steps := []chatMode{ModeChat, ModeAgent, ModeChat}
	for _, want := range steps {
		m = pressCommand(t, m, "/mode")
		if m.currentMode != want {
			t.Fatalf("/mode → %v, want %v", m.currentMode, want)
		}
	}
}

func TestModeCommandCyclesThroughGroupWhenEnabled(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, true)
	m.createNewChat()

	steps := []chatMode{ModeChat, ModeGroup, ModeAgent}
	for _, want := range steps {
		m = pressCommand(t, m, "/mode")
		if m.currentMode != want {
			t.Fatalf("/mode → %v, want %v", m.currentMode, want)
		}
	}
}

func TestModeCommandSwitchesToNamedMode(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, true)
	m.createNewChat()

	tests := []struct {
		command string
		want    chatMode
	}{
		{"/mode chat", ModeChat},
		{"/mode agent", ModeAgent},
		{"/mode group", ModeGroup},
		{"/mode CHAT", ModeChat}, // case-insensitive
	}
	for _, tt := range tests {
		m = pressCommand(t, m, tt.command)
		if m.currentMode != tt.want {
			t.Errorf("%s → %v, want %v", tt.command, m.currentMode, tt.want)
		}
		if m.statusFeedback != "" {
			t.Errorf("%s set feedback %q, want none", tt.command, m.statusFeedback)
		}
	}
}

func TestModeCommandDoesNotTouchAgent(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, true)
	m.createNewChat()

	m = pressCommand(t, m, "/mode chat")

	if got := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey); got != "alpha" {
		t.Errorf("agent = %q, want alpha (unchanged by /mode)", got)
	}
}

func TestModeCommandRejectsGroupWhenDisabled(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, false)
	m.createNewChat()

	m = pressCommand(t, m, "/mode group")

	if m.currentMode != ModeAgent {
		t.Errorf("mode = %v, want ModeAgent (group is disabled)", m.currentMode)
	}
	if !strings.Contains(m.statusFeedback, "agent, chat") {
		t.Errorf("statusFeedback = %q, want the usage hint", m.statusFeedback)
	}
	if strings.Contains(m.statusFeedback, "group") {
		t.Errorf("statusFeedback = %q must not offer group while it is disabled", m.statusFeedback)
	}
}

func TestModeCommandRejectsUnknownMode(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, true)
	m.createNewChat()
	m.currentMode = ModeChat

	m = pressCommand(t, m, "/mode nonsense")

	if m.currentMode != ModeChat {
		t.Errorf("mode = %v, want ModeChat (unknown argument must not switch)", m.currentMode)
	}
	if !strings.Contains(m.statusFeedback, "agent, chat, group") {
		t.Errorf("statusFeedback = %q, want the usage hint listing every mode", m.statusFeedback)
	}
}

func TestSetModeUnknownLeavesFeedbackAlone(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha"}, false)

	if m.setMode("nope") {
		t.Error("setMode(\"nope\") = true, want false")
	}
	if m.currentMode != ModeAgent {
		t.Errorf("mode = %v, want ModeAgent", m.currentMode)
	}
}
func TestWelcomeScreenAdvertisesSwitchingShortcuts(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, true)
	m.showWelcome = true
	m.width = 100
	m.height = 40

	out := m.View()

	// The welcome screen is where new users land: it must name both shortcuts
	// (TAB cycles agents, /mode switches the chat/agent/group filter).
	if !strings.Contains(out, i18n.T("tui.tabHint")) {
		t.Errorf("welcome view missing the shortcut hint %q", i18n.T("tui.tabHint"))
	}
}

func TestCycledAgentCarriesIntoNextChat(t *testing.T) {
	m := newModeSwitchTestModel(t, []string{"alpha", "beta"}, false)
	m.createNewChat()
	firstKey := m.currentKey

	m = pressTab(t, m) // alpha → beta

	m.createNewChat()
	if m.currentKey == firstKey {
		t.Fatal("/new reused the previous session key")
	}
	if got := m.agentLoop.GetProvidable().GetSessionAgent(m.currentKey); got != "beta" {
		t.Errorf("new chat agent = %q, want beta (the agent picked with TAB)", got)
	}
}
