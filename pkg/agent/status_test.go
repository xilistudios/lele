package agent

import (
	"context"
	"os"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func TestStatusCommand_SessionKeyWithoutAgentPrefix(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "status-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := NewAgentLoop(cfg, msgBus)

	sessionKey := "telegram:12345"

	defaultAgent := al.registry.GetDefaultAgent()
	defaultAgent.Sessions.AddTokenCounts(sessionKey, 100, 50)

	ch := newCommandHandler(al)
	response, handled := ch.handleCommand(context.TODO(), bus.InboundMessage{
		Channel:    "telegram",
		ChatID:     "12345",
		SessionKey: sessionKey,
		Content:    "/status",
	})

	if !handled {
		t.Fatal("Expected /status to be handled")
	}

	if !contains(response, "~100 in / ~50 out") {
		t.Errorf("Expected token counts in status response, got: %s", response)
	}
}

func contains(s, substr string) bool {
	return len(substr) == 0 || len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}
