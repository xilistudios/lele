package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newTestGroupHandler(cfg *config.Config) (*commandHandlerImpl, *AgentLoop) {
	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)
	ch := newCommandHandler(al)
	return ch, al
}

func TestGroupCommand_NoArgs_ShowsUsage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group",
	})

	if !handled {
		t.Fatal("Expected /group to be handled")
	}
	if !strings.Contains(result, "Uso:") {
		t.Errorf("Expected usage message, got: %s", result)
	}
	if !strings.Contains(result, "/group list") {
		t.Errorf("Expected /group list in usage, got: %s", result)
	}
}

func TestGroupCommand_Help_ShowsUsage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group help",
	})

	if !handled {
		t.Fatal("Expected /group help to be handled")
	}
	if !strings.Contains(result, "Uso:") {
		t.Errorf("Expected usage message, got: %s", result)
	}
}

func TestGroupCommand_List_NoGroups(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group list",
	})

	if !handled {
		t.Fatal("Expected /group list to be handled")
	}
	if !strings.Contains(result, "No hay grupos activos") {
		t.Errorf("Expected no active groups message, got: %s", result)
	}
}

func TestGroupCommand_Status_NoGroups(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group status",
	})

	if !handled {
		t.Fatal("Expected /group status to be handled")
	}
	if !strings.Contains(result, "No hay grupos activos") {
		t.Errorf("Expected no active groups, got: %s", result)
	}
}

func TestGroupCommand_Status_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group status nonexistent",
	})

	if !handled {
		t.Fatal("Expected /group status to be handled")
	}
	if !strings.Contains(result, "Grupo no encontrado") {
		t.Errorf("Expected group not found message, got: %s", result)
	}
	if !strings.Contains(result, "nonexistent") {
		t.Errorf("Expected group ID in error, got: %s", result)
	}
}

func TestGroupCommand_Stop_NoID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group stop",
	})

	if !handled {
		t.Fatal("Expected /group stop to be handled")
	}
	if !strings.Contains(result, "Uso:") {
		t.Errorf("Expected usage message, got: %s", result)
	}
}

func TestGroupCommand_Stop_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group stop nonexistent",
	})

	if !handled {
		t.Fatal("Expected /group stop to be handled")
	}
	if !strings.Contains(result, "Grupo no encontrado") {
		t.Errorf("Expected group not found message, got: %s", result)
	}
}

func TestGroupCommand_Start_NoArgs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group start",
	})

	if !handled {
		t.Fatal("Expected /group start to be handled")
	}
	if !strings.Contains(result, "Uso:") {
		t.Errorf("Expected usage message, got: %s", result)
	}
}

func TestGroupCommand_Start_ProfileNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group start nonexistent-profile some task here",
	})

	if !handled {
		t.Fatal("Expected /group start to be handled")
	}
	if !strings.Contains(result, "Perfil de grupo no encontrado") {
		t.Errorf("Expected profile not found message, got: %s", result)
	}
	if !strings.Contains(result, "nonexistent-profile") {
		t.Errorf("Expected profile ID in error, got: %s", result)
	}
	if !strings.Contains(result, "--agents") {
		t.Errorf("Expected ad-hoc hint in error, got: %s", result)
	}
}

func TestGroupCommand_Start_ProfileMissingTask(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group start myprofile",
	})

	if !handled {
		t.Fatal("Expected /group start to be handled")
	}
	if !strings.Contains(result, "❌") {
		t.Errorf("Expected error message, got: %s", result)
	}
}

func TestGroupCommand_Start_AdHoc_MissingAgents(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group start --strategy moa some task",
	})

	if !handled {
		t.Fatal("Expected /group start to be handled")
	}
	if !strings.Contains(result, "--agents") {
		t.Errorf("Expected --agents required message, got: %s", result)
	}
}

func TestGroupCommand_Start_AdHoc_MissingTask(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group start --agents main",
	})

	if !handled {
		t.Fatal("Expected /group start to be handled")
	}
	if !strings.Contains(result, "tarea") {
		t.Errorf("Expected missing task message, got: %s", result)
	}
}

func TestGroupCommand_Start_AdHoc_InvalidStrategy(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group start --agents main --strategy invalid hello task",
	})

	if !handled {
		t.Fatal("Expected /group start to be handled")
	}
	if !strings.Contains(result, "Estrategia inválida") {
		t.Errorf("Expected invalid strategy message, got: %s", result)
	}
}

func TestGroupCommand_Start_AdHoc_Success(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, al := newTestGroupHandler(cfg)
	defer func() {
		for _, g := range al.GroupManager().List() {
			al.GroupManager().Stop(g.ID)
			_, _ = al.GroupManager().Wait(g.ID)
		}
	}()

	defaultAgent := al.registry.GetDefaultAgent()
	if defaultAgent == nil {
		t.Fatal("Expected default agent to exist")
	}

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group start --agents main --strategy round_robin analiza el código",
	})

	if !handled {
		t.Fatal("Expected /group start to be handled")
	}
	if !strings.Contains(result, "✅ Grupo iniciado") {
		t.Errorf("Expected success message, got: %s", result)
	}
	if !strings.Contains(result, "round_robin") {
		t.Errorf("Expected strategy in response, got: %s", result)
	}
	if !strings.Contains(result, "group:adhoc-") {
		t.Errorf("Expected group ID in response, got: %s", result)
	}

	groups := al.GroupManager().List()
	if len(groups) != 1 {
		t.Fatalf("Expected 1 active group, got %d", len(groups))
	}
	if groups[0].OriginSenderID != "user1" {
		t.Errorf("Expected the triggering sender as group origin, got %q", groups[0].OriginSenderID)
	}
}

func TestGroupCommand_Start_Profile_Success(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{
			Enabled: true,
			List: []config.GroupProfile{
				{
					ID:           "test-group",
					Participants: []string{"main"},
					Strategy:     config.StrategyRoundRobin,
					Rounds:       2,
				},
			},
		},
	}

	ch, al := newTestGroupHandler(cfg)
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
		Content:  "/group start test-group analiza el repositorio",
	})

	if !handled {
		t.Fatal("Expected /group start to be handled")
	}
	if !strings.Contains(result, "✅ Grupo iniciado") {
		t.Errorf("Expected success message, got: %s", result)
	}
	if !strings.Contains(result, "test-group") {
		t.Errorf("Expected profile ID in group ID, got: %s", result)
	}

	groups := al.GroupManager().List()
	if len(groups) != 1 {
		t.Fatalf("Expected 1 active group, got %d", len(groups))
	}
	if groups[0].Strategy != config.StrategyRoundRobin {
		t.Errorf("Expected strategy round_robin, got %s", groups[0].Strategy)
	}
	if groups[0].OriginSenderID != "user1" {
		t.Errorf("Expected the triggering sender as group origin, got %q", groups[0].OriginSenderID)
	}
}

func TestGroupCommand_UnknownSubcommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
		Groups: config.GroupsConfig{Enabled: true},
	}

	ch, _ := newTestGroupHandler(cfg)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/group foobar",
	})

	if !handled {
		t.Fatal("Expected /group to be handled")
	}
	if !strings.Contains(result, "Subcomando desconocido") {
		t.Errorf("Expected unknown subcommand message, got: %s", result)
	}
	if !strings.Contains(result, "foobar") {
		t.Errorf("Expected subcommand name in error, got: %s", result)
	}
}

func TestValidStrategy(t *testing.T) {
	tests := []struct {
		strategy string
		valid    bool
	}{
		{"round_robin", true},
		{"moa", true},
		{"moderator", true},
		{"pipeline", true},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		got := config.ValidStrategy(tt.strategy)
		if got != tt.valid {
			t.Errorf("ValidStrategy(%q) = %v, want %v", tt.strategy, got, tt.valid)
		}
	}
}
