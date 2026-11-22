package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
)

func gatingCfg(tmpDir string, groupsEnabled bool) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Groups: config.GroupsConfig{Enabled: groupsEnabled},
	}
}

func TestGroupsFeatureEnabled_ConfigHelper(t *testing.T) {
	var nilCfg *config.Config
	if nilCfg.GroupsFeatureEnabled() {
		t.Error("nil config must report groups disabled")
	}

	off := &config.Config{}
	if off.GroupsFeatureEnabled() {
		t.Error("zero-value config must report groups disabled (default off)")
	}

	on := &config.Config{Groups: config.GroupsConfig{Enabled: true}}
	if !on.GroupsFeatureEnabled() {
		t.Error("Groups.Enabled=true must report groups enabled")
	}
}

func TestAgentLoop_GroupsEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	off := NewAgentLoop(gatingCfg(tmpDir, false), bus.NewMessageBus())
	if off.GroupsEnabled() {
		t.Error("AgentLoop.GroupsEnabled must be false when cfg.Groups.Enabled=false")
	}

	on := NewAgentLoop(gatingCfg(tmpDir, true), bus.NewMessageBus())
	if !on.GroupsEnabled() {
		t.Error("AgentLoop.GroupsEnabled must be true when cfg.Groups.Enabled=true")
	}
}

func TestGroupChatTool_RegisteredOnlyWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	off := NewAgentLoop(gatingCfg(tmpDir, false), bus.NewMessageBus())
	for _, id := range off.registry.ListAgentIDs() {
		agent, ok := off.registry.GetAgent(id)
		if !ok || agent.Tools == nil {
			continue
		}
		if _, exists := agent.Tools.Get("group_chat"); exists {
			t.Errorf("groups OFF: agent %q must not have group_chat registered", id)
		}
	}
	if sm, ok := off.GetSubagents()["main"]; ok {
		if _, exists := sm.GetToolRegistry().Get("group_chat"); exists {
			t.Error("groups OFF: subagent toolset must not include group_chat")
		}
	}

	on := NewAgentLoop(gatingCfg(tmpDir, true), bus.NewMessageBus())
	agent, ok := on.registry.GetAgent("main")
	if !ok || agent == nil {
		t.Fatal("expected default agent 'main' to exist")
	}
	if _, exists := agent.Tools.Get("group_chat"); !exists {
		t.Error("groups ON: agent 'main' should have group_chat registered")
	}
}

func TestSyncGroupChatTool(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := gatingCfg(tmpDir, true)
	registry := NewAgentRegistry(cfg)
	agent, ok := registry.GetAgent("main")
	if !ok || agent == nil {
		t.Fatal("expected implicit 'main' agent")
	}
	agent.Tools.Register(&llmRunnerMockCustomTool{name: "exec"})
	gm := group.NewGroupManager(func(string) (group.AgentContext, bool) {
		return group.AgentContext{AgentID: "main"}, true
	}, nil, nil)

	syncGroupChatTool(agent, cfg, registry, "main", gm)
	if _, exists := agent.Tools.Get("group_chat"); !exists {
		t.Fatal("groups ON: syncGroupChatTool should register group_chat")
	}

	cfgOff := gatingCfg(tmpDir, false)
	syncGroupChatTool(agent, cfgOff, registry, "main", gm)
	if _, exists := agent.Tools.Get("group_chat"); exists {
		t.Error("groups OFF: syncGroupChatTool should unregister group_chat")
	}
	if _, exists := agent.Tools.Get("exec"); !exists {
		t.Error("syncGroupChatTool must not remove unrelated tools")
	}

	agent.Tools.Register(&llmRunnerMockCustomTool{name: "group_chat"})
	syncGroupChatTool(agent, cfg, registry, "main", nil)
	if _, exists := agent.Tools.Get("group_chat"); exists {
		t.Error("nil group manager must not leave group_chat registered")
	}
}

func TestLoopGroupManager_StartRejectedWhenDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	al := NewAgentLoop(gatingCfg(tmpDir, false), bus.NewMessageBus())
	gm := al.GroupManager()
	if gm == nil {
		t.Fatal("GroupManager should still be constructed (list/status paths)")
	}

	_, err := gm.Start(context.Background(), "gate-loop-off", "", "task", "round_robin",
		[]group.Participant{{AgentID: "main"}}, group.GroupOptions{Rounds: 1}, "test", "chat1")
	if !errors.Is(err, group.ErrGroupsDisabled) {
		t.Fatalf("production loop must inject the gate into Start, got %v", err)
	}
	if len(gm.List()) != 0 {
		t.Errorf("no group state should exist after rejected Start, got %d", len(gm.List()))
	}
}

func TestLoopGroupManager_StartAllowedWhenEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	al := NewAgentLoop(gatingCfg(tmpDir, true), bus.NewMessageBus())
	defer func() {
		for _, g := range al.GroupManager().List() {
			al.GroupManager().Stop(g.ID)
			_, _ = al.GroupManager().Wait(g.ID)
		}
	}()

	_, err := al.GroupManager().Start(context.Background(), "gate-loop-on", "", "task", "round_robin",
		[]group.Participant{{AgentID: "main"}}, group.GroupOptions{Rounds: 1}, "test", "chat1")
	if err != nil {
		t.Fatalf("groups ON: Start should be allowed, got %v", err)
	}
}

func TestGroupCommand_DisabledFeature(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := gatingCfg(tmpDir, false)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	ch := newCommandHandler(al)

	subcommands := []string{
		"/group",
		"/group help",
		"/group list",
		"/group status",
		"/group stop anything",
		"/group start --agents main --strategy round_robin do the thing",
	}

	for _, content := range subcommands {
		result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
			Channel:  "test",
			SenderID: "user1",
			ChatID:   "chat1",
			Content:  content,
		})
		if !handled {
			t.Fatalf("%s: expected command to be handled", content)
		}
		if !strings.Contains(result, "groups are disabled") {
			t.Errorf("%s: expected disabled notice, got: %s", content, result)
		}
		if !strings.Contains(result, "groups.enabled = true") {
			t.Errorf("%s: notice should tell the user how to enable, got: %s", content, result)
		}
	}

	if groups := al.GroupManager().List(); len(groups) != 0 {
		t.Errorf("disabled /group must not start anything, got %d group(s)", len(groups))
	}
}

func TestGroupCommand_EnabledFeature_StillWorks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := gatingCfg(tmpDir, true)
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	ch := newCommandHandler(al)
	defer func() {
		for _, g := range al.GroupManager().List() {
			al.GroupManager().Stop(g.ID)
			_, _ = al.GroupManager().Wait(g.ID)
		}
	}()

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group",
	})
	if !handled {
		t.Fatal("expected /group to be handled")
	}
	if strings.Contains(result, "groups are disabled") {
		t.Errorf("groups ON: /group must not report disabled, got: %s", result)
	}
	if !strings.Contains(result, "Uso:") {
		t.Errorf("groups ON: expected usage message, got: %s", result)
	}
}
