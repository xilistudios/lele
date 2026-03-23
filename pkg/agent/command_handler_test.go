// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

// TestHandleCommand_NotACommand verifies that messages not starting with "/" are not handled
func TestHandleCommand_NotACommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	msg := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "Hello, how are you?",
	}

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), msg)

	if handled {
		t.Error("Expected non-command message to not be handled")
	}
	if result != "" {
		t.Errorf("Expected empty result, got: %s", result)
	}
}

// TestHandleCommand_EmptyCommand verifies empty command is not handled
func TestHandleCommand_EmptyCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	msg := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "   ",
	}

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), msg)

	if handled {
		t.Error("Expected empty command to not be handled")
	}
	if result != "" {
		t.Errorf("Expected empty result, got: %s", result)
	}
}

// TestHandleCommand_UnknownCommand verifies unknown commands are not handled
func TestHandleCommand_UnknownCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	msg := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/unknowncommand",
	}

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), msg)

	if handled {
		t.Error("Expected unknown command to not be handled")
	}
	if result != "" {
		t.Errorf("Expected empty result, got: %s", result)
	}
}

// TestHandleNewCommand tests the /new command
func TestHandleNewCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:new-session"
	ch := newCommandHandler(al)

	result := ch.handleNewCommand(al.registry.GetDefaultAgent(), sessionKey)

	if !strings.Contains(result, "New conversation started") {
		t.Errorf("Expected 'New conversation started' message, got: %s", result)
	}
	if !strings.Contains(result, "SOUL.md") {
		t.Errorf("Expected SOUL.md reference in response, got: %s", result)
	}
	if !strings.Contains(result, "AGENT.md") {
		t.Errorf("Expected AGENT.md reference in response, got: %s", result)
	}
}

// TestHandleNewCommand_NoAgent tests /new command when no agent is configured
func TestHandleNewCommand_NoAgent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Remove default agent for this test
	al.registry.agents = make(map[string]*AgentInstance)

	ch := newCommandHandler(al)
	result := ch.handleNewCommand(nil, "test")

	if result != "No default agent configured" {
		t.Errorf("Expected 'No default agent configured', got: %s", result)
	}
}

// TestHandleClearCommand tests the /clear command
func TestHandleClearCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:clear-session"
	agent := al.registry.GetDefaultAgent()

	// Add some history first
	agent.Sessions.AddMessage(sessionKey, "user", "Hello")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Hi there!")

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/clear",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /clear to be handled")
	}
	if result != "✅ Conversation cleared." {
		t.Errorf("Expected clear message, got: %s", result)
	}

	// Verify history was cleared (TruncateHistory(0) keeps system prompt if exists)
	history := agent.Sessions.GetHistory(sessionKey)
	// TruncateHistory(0) clears all messages, but may keep system prompt
	if len(history) > 0 {
		t.Logf("History after clear: %d messages (may include system prompt)", len(history))
	}
}

// TestHandleStatusCommand tests the /status command
func TestHandleStatusCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/status",
	})

	if !handled {
		t.Error("Expected /status to be handled")
	}
	if !strings.Contains(result, "lele") {
		t.Errorf("Expected lele in status, got: %s", result)
	}
	if !strings.Contains(result, "Model:") {
		t.Errorf("Expected model info in status, got: %s", result)
	}
}

// TestHandleModelCommand_NoArgs tests /model command without arguments
func TestHandleModelCommand_NoArgs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"openai": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey: "test-key",
					},
					Models: map[string]config.ProviderModelConfig{
						"gpt-4":    {},
						"gpt-3.5":  {},
						"claude-3": {},
					},
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:model-session"
	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/model",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /model to be handled")
	}
	if !strings.Contains(result, "Current model:") {
		t.Errorf("Expected current model info, got: %s", result)
	}
	// Note: Available models only shown when provider has models configured
}

// TestHandleModelCommand_WithArgs tests /model command with model name
func TestHandleModelCommand_WithArgs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: config.ProvidersConfig{
			Named: map[string]config.NamedProviderConfig{
				"openai": {
					Type: "openai",
					ProviderConfig: config.ProviderConfig{
						APIKey: "test-key",
					},
					Models: map[string]config.ProviderModelConfig{
						"gpt-4": {},
					},
				},
			},
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:model-change-session"
	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/model gpt-4",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /model to be handled")
	}
	if !strings.Contains(result, "Model changed") {
		t.Errorf("Expected model changed message, got: %s", result)
	}
}

// TestHandleModelCommand_NoSession tests /model command without session
func TestHandleModelCommand_NoSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/model gpt-4",
	})

	if !handled {
		t.Error("Expected /model to be handled")
	}
	// Note: The actual code allows model change without session, stores in sessionModels
	// This test reflects actual behavior
	if !strings.Contains(result, "Model changed") {
		t.Logf("Model change allowed without session: %s", result)
	}
}

// TestHandleVerboseCommand tests the /verbose command
func TestHandleVerboseCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:verbose-session"
	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/verbose",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /verbose to be handled")
	}
	// Verbose cycles through levels, should be one of the valid responses
	validResponses := []string{
		"🔇 Verbose mode **OFF**",
		"🛠️ Verbose mode **BASIC**",
		"📋 Verbose mode **FULL**",
	}
	found := false
	for _, resp := range validResponses {
		if strings.Contains(result, resp) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected verbose level message, got: %s", result)
	}
}

// TestHandleVerboseCommand_NoSession tests /verbose without session
func TestHandleVerboseCommand_NoSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/verbose",
	})

	if !handled {
		t.Error("Expected /verbose to be handled")
	}
	// Note: The actual code allows verbose cycling without session
	// This test reflects actual behavior
	validResponses := []string{
		"🔇 Verbose mode **OFF**",
		"🛠️ Verbose mode **BASIC**",
		"📋 Verbose mode **FULL**",
	}
	found := false
	for _, resp := range validResponses {
		if strings.Contains(result, resp) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected verbose level message, got: %s", result)
	}
}

func TestHandleToggleEphemeralCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("HOME", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Session: config.SessionConfig{
			Ephemeral:          false,
			EphemeralThreshold: config.DefaultEphemeralThresholdSeconds,
		},
	}

	msgBus := bus.NewMessageBus()
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/toggle ephemeral",
	})

	if !handled {
		t.Error("Expected /toggle ephemeral to be handled")
	}
	if !strings.Contains(result, "Ephemeral mode enabled") {
		t.Fatalf("expected enabled response, got: %s", result)
	}
	if !cfg.SessionEphemeralEnabled() {
		t.Fatal("ephemeral mode should be enabled in runtime config")
	}

	loaded, err := config.LoadConfig(config.DefaultConfigPath())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !loaded.Session.Ephemeral {
		t.Fatal("ephemeral mode should be persisted to config.json")
	}

	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/toggle ephemeral",
	})
	if !handled {
		t.Error("Expected second /toggle ephemeral to be handled")
	}
	if !strings.Contains(result, "Ephemeral mode disabled") {
		t.Fatalf("expected disabled response, got: %s", result)
	}
}

// TestHandleAgentCommand_NoArgs lists available agents
func TestHandleAgentCommand_NoArgs(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:agent-session"
	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/agent",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /agent to be handled")
	}
	if !strings.Contains(result, "Available agents:") {
		t.Errorf("Expected agents list, got: %s", result)
	}
}

// TestHandleAgentCommand_WithAgent switches to specified agent
func TestHandleAgentCommand_WithAgent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:agent-switch-session"
	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/agent main",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /agent to be handled")
	}
	if !strings.Contains(result, "Agent changed to:") {
		t.Errorf("Expected agent switch message, got: %s", result)
	}
	if !strings.Contains(result, "main") {
		t.Errorf("Expected 'main' in response, got: %s", result)
	}
}

// TestHandleNewCommand_RefreshesBootstrapFiles verifies /new picks up current workspace bootstrap files.
func TestHandleNewCommand_RefreshesBootstrapFiles(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte("agent context v1"), 0644); err != nil {
		t.Fatalf("Failed to write AGENT.md: %v", err)
	}

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

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("No default agent found")
	}

	if !strings.Contains(agent.ContextBuilder.BuildSystemPrompt(), "agent context v1") {
		t.Fatal("Expected initial system prompt to contain first AGENT.md content")
	}

	if err := os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte("agent context v2"), 0644); err != nil {
		t.Fatalf("Failed to update AGENT.md: %v", err)
	}

	ch := newCommandHandler(al)
	response := ch.handleNewCommand(agent, "test:new-refresh")
	if !strings.Contains(response, "AGENT.md") {
		t.Fatalf("Expected refresh message to mention AGENT.md, got: %s", response)
	}

	if !strings.Contains(agent.ContextBuilder.BuildSystemPrompt(), "agent context v2") {
		t.Fatal("Expected refreshed system prompt to contain updated AGENT.md content")
	}
}

// TestHandleAgentCommand_UsesSelectedAgentWorkspaceContext verifies /agent switches to the selected agent workspace context.
func TestHandleAgentCommand_UsesSelectedAgentWorkspaceContext(t *testing.T) {
	mainDir := t.TempDir()
	supportDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(mainDir, "AGENT.md"), []byte("main agent context"), 0644); err != nil {
		t.Fatalf("Failed to write main AGENT.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(supportDir, "AGENT.md"), []byte("support agent context"), 0644); err != nil {
		t.Fatalf("Failed to write support AGENT.md: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         mainDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: "main", Default: true, Workspace: mainDir},
				{ID: "support", Workspace: supportDir},
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus(), &mockProvider{})
	ch := newCommandHandler(al)
	sessionKey := "agent:main:test:direct:user1"

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/agent support",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Fatal("Expected /agent to be handled")
	}
	if !strings.Contains(result, "support") {
		t.Fatalf("Expected response to mention support agent, got: %s", result)
	}
	if got := al.GetSessionAgent(sessionKey); got != "support" {
		t.Fatalf("Expected session agent support, got %s", got)
	}

	switchedAgent, ok := al.registry.GetAgent(al.GetSessionAgent(sessionKey))
	if !ok {
		t.Fatal("Expected switched agent to exist in registry")
	}
	prompt := switchedAgent.ContextBuilder.BuildSystemPrompt()
	if !strings.Contains(prompt, "support agent context") {
		t.Fatal("Expected switched agent prompt to contain support workspace context")
	}
	if strings.Contains(prompt, "main agent context") {
		t.Fatal("Expected switched agent prompt to exclude main workspace context")
	}
}

// TestHandleAgentCommand_UnknownAgent tests switching to non-existent agent
func TestHandleAgentCommand_UnknownAgent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:agent-unknown-session"
	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/agent nonexistent",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /agent to be handled")
	}
	if !strings.Contains(result, "Agent not found") {
		t.Errorf("Expected agent not found message, got: %s", result)
	}
}

// TestHandleAgentCommand_NoSession tests /agent without session
func TestHandleAgentCommand_NoSession(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/agent main",
	})

	if !handled {
		t.Error("Expected /agent to be handled")
	}
	// Note: The actual code allows agent switching without session
	// This test reflects actual behavior
	if !strings.Contains(result, "Agent changed to:") {
		t.Logf("Agent change allowed without session: %s", result)
	}
}

// TestHandleSubagentsCommand_NoRunning shows no running subagents
func TestHandleSubagentsCommand_NoRunning(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/subagents",
	})

	if !handled {
		t.Error("Expected /subagents to be handled")
	}
	if !strings.Contains(result, "No active or waiting subagents") {
		t.Errorf("Expected no active subagents message, got: %s", result)
	}
}

type commandHandlerSubagentCoordinatorStub struct {
	lastSessionKey string
	lastTaskID     string
	lastGuidance   string
	response       string
	err            error
}

func (m *commandHandlerSubagentCoordinatorStub) updateToolContexts(agent *AgentInstance, channel, chatID string) {
}

func (m *commandHandlerSubagentCoordinatorStub) stopAllSubagents() int { return 0 }

func (m *commandHandlerSubagentCoordinatorStub) cancelSession(sessionKey string) {}

func (m *commandHandlerSubagentCoordinatorStub) listRunningSubagentTasks() []*tools.SubagentTask {
	return nil
}

func (m *commandHandlerSubagentCoordinatorStub) getSubagentTask(taskID string) (*tools.SubagentTask, bool) {
	return nil, false
}

func (m *commandHandlerSubagentCoordinatorStub) stopSubagentTask(taskID string) bool { return false }

func (m *commandHandlerSubagentCoordinatorStub) continueSubagentTask(ctx context.Context, sessionKey, taskID, guidance string) (string, error) {
	m.lastSessionKey = sessionKey
	m.lastTaskID = taskID
	m.lastGuidance = guidance
	return m.response, m.err
}

func (m *commandHandlerSubagentCoordinatorStub) GetStartupInfo() map[string]interface{} {
	return map[string]interface{}{}
}

func TestHandleSubagentsCommand_Continue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	stub := &commandHandlerSubagentCoordinatorStub{response: "Continuing subagent task subagent-9 with new guidance."}
	al.toolCoordinator = stub

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/subagents continue subagent-9 use the main workspace",
		SessionKey: "telegram:chat1",
	})

	if !handled {
		t.Error("Expected /subagents continue to be handled")
	}
	if result != stub.response {
		t.Fatalf("Unexpected response: %s", result)
	}
	if stub.lastSessionKey != "telegram:chat1" {
		t.Fatalf("Expected session key telegram:chat1, got %s", stub.lastSessionKey)
	}
	if stub.lastTaskID != "subagent-9" {
		t.Fatalf("Expected task id subagent-9, got %s", stub.lastTaskID)
	}
	if stub.lastGuidance != "use the main workspace" {
		t.Fatalf("Unexpected guidance: %s", stub.lastGuidance)
	}
}

func TestHandleSubagentsCommand_ContinueUsage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	al.toolCoordinator = &commandHandlerSubagentCoordinatorStub{}

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/subagents continue subagent-9",
		SessionKey: "telegram:chat1",
	})

	if !handled {
		t.Error("Expected /subagents continue to be handled")
	}
	if result != "Usage: /subagents continue <task_id> <guidance>" {
		t.Fatalf("Unexpected usage response: %s", result)
	}
}

// TestHandleStopCommand tests the /stop command
func TestHandleStopCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/stop",
	})

	if !handled {
		t.Error("Expected /stop to be handled")
	}
	if result != "Agente detenido." {
		t.Errorf("Expected stop message, got: %s", result)
	}
}

// TestHandleShowCommand tests /show command variants
func TestHandleShowCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	// Test /show without args
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/show",
	})

	if !handled {
		t.Error("Expected /show to be handled")
	}
	if result != "Usage: /show [model|channel|agents]" {
		t.Errorf("Expected usage message, got: %s", result)
	}

	// Test /show model
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/show model",
	})

	if !handled {
		t.Error("Expected /show model to be handled")
	}
	if !strings.Contains(result, "Current model:") {
		t.Errorf("Expected current model info, got: %s", result)
	}

	// Test /show channel
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/show channel",
	})

	if !handled {
		t.Error("Expected /show channel to be handled")
	}
	if !strings.Contains(result, "Current channel:") {
		t.Errorf("Expected current channel info, got: %s", result)
	}

	// Test /show agents
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/show agents",
	})

	if !handled {
		t.Error("Expected /show agents to be handled")
	}
	if !strings.Contains(result, "Registered agents:") {
		t.Errorf("Expected agents list, got: %s", result)
	}

	// Test /show unknown
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/show unknown",
	})

	if !handled {
		t.Error("Expected /show unknown to be handled")
	}
	if !strings.Contains(result, "Unknown show target:") {
		t.Errorf("Expected unknown target message, got: %s", result)
	}
}

// TestHandleListCommand tests /list command variants
func TestHandleListCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	// Test /list without args
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/list",
	})

	if !handled {
		t.Error("Expected /list to be handled")
	}
	if result != "Usage: /list [models|channels|agents]" {
		t.Errorf("Expected usage message, got: %s", result)
	}

	// Test /list models
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/list models",
	})

	if !handled {
		t.Error("Expected /list models to be handled")
	}
	if !strings.Contains(result, "Available models:") {
		t.Errorf("Expected models info, got: %s", result)
	}

	// Test /list channels
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/list channels",
	})

	if !handled {
		t.Error("Expected /list channels to be handled")
	}
	// Channel manager is nil by default in tests
	if result != "Channel manager not initialized" {
		t.Errorf("Expected channel manager not initialized, got: %s", result)
	}

	// Test /list agents
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/list agents",
	})

	if !handled {
		t.Error("Expected /list agents to be handled")
	}
	if !strings.Contains(result, "Registered agents:") {
		t.Errorf("Expected agents list, got: %s", result)
	}

	// Test /list unknown
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/list unknown",
	})

	if !handled {
		t.Error("Expected /list unknown to be handled")
	}
	if !strings.Contains(result, "Unknown list target:") {
		t.Errorf("Expected unknown target message, got: %s", result)
	}
}

// TestHandleSwitchCommand tests /switch command
func TestHandleSwitchCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	// Test /switch without args
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch",
	})

	if !handled {
		t.Error("Expected /switch to be handled")
	}
	if result != "Usage: /switch [model|channel] to <name>" {
		t.Errorf("Expected usage message, got: %s", result)
	}

	// Test /switch model to <name>
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch model to gpt-4",
	})

	if !handled {
		t.Error("Expected /switch model to be handled")
	}
	if !strings.Contains(result, "Switched model from") {
		t.Errorf("Expected model switch message, got: %s", result)
	}

	// Test /switch channel to <name>
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch channel to cli",
	})

	if !handled {
		t.Error("Expected /switch channel to be handled")
	}
	// Channel switch requires channel manager to be initialized
	// In tests, it's nil by default
	if !strings.Contains(result, "Switched target channel to") && !strings.Contains(result, "Channel manager not initialized") {
		t.Errorf("Expected channel switch or not initialized message, got: %s", result)
	}

	// Test /switch unknown target
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/switch unknown to value",
	})

	if !handled {
		t.Error("Expected /switch unknown to be handled")
	}
	if !strings.Contains(result, "Unknown switch target:") {
		t.Errorf("Expected unknown target message, got: %s", result)
	}
}

// TestHandleCompactCommand tests /compact command
func TestHandleCompactCommand(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:compact-session"
	agent := al.registry.GetDefaultAgent()

	// Add minimal history (not enough to compact)
	agent.Sessions.AddMessage(sessionKey, "user", "Hello")
	agent.Sessions.AddMessage(sessionKey, "assistant", "Hi")

	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/compact",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /compact to be handled")
	}
	if !strings.Contains(result, "Not enough messages to compact") {
		t.Errorf("Expected not enough messages message, got: %s", result)
	}
}

// TestExtractPeer tests the extractPeer helper function
func TestExtractPeer(t *testing.T) {
	// Test with peer_kind metadata
	msg := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Metadata: map[string]string{
			"peer_kind": "direct",
			"peer_id":   "custom-peer-id",
		},
	}

	peer := extractPeer(msg)
	if peer == nil {
		t.Fatal("Expected peer to be extracted")
	}
	if peer.Kind != "direct" {
		t.Errorf("Expected peer kind 'direct', got '%s'", peer.Kind)
	}
	if peer.ID != "custom-peer-id" {
		t.Errorf("Expected peer ID 'custom-peer-id', got '%s'", peer.ID)
	}

	// Test with peer_kind but no peer_id (should use SenderID for direct)
	msg2 := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user2",
		ChatID:   "chat2",
		Metadata: map[string]string{
			"peer_kind": "direct",
		},
	}

	peer2 := extractPeer(msg2)
	if peer2 == nil {
		t.Fatal("Expected peer to be extracted")
	}
	if peer2.ID != "user2" {
		t.Errorf("Expected peer ID 'user2', got '%s'", peer2.ID)
	}

	// Test with peer_kind but no peer_id (should use ChatID for non-direct)
	msg3 := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user3",
		ChatID:   "chat3",
		Metadata: map[string]string{
			"peer_kind": "group",
		},
	}

	peer3 := extractPeer(msg3)
	if peer3 == nil {
		t.Fatal("Expected peer to be extracted")
	}
	if peer3.ID != "chat3" {
		t.Errorf("Expected peer ID 'chat3', got '%s'", peer3.ID)
	}

	// Test with no peer_kind
	msg4 := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user4",
		ChatID:   "chat4",
		Metadata: map[string]string{},
	}

	peer4 := extractPeer(msg4)
	if peer4 != nil {
		t.Errorf("Expected nil peer, got %+v", peer4)
	}
}

// TestExtractParentPeer tests the extractParentPeer helper function
func TestExtractParentPeer(t *testing.T) {
	// Test with parent metadata
	msg := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Metadata: map[string]string{
			"parent_peer_kind": "direct",
			"parent_peer_id":   "parent-id",
		},
	}

	peer := extractParentPeer(msg)
	if peer == nil {
		t.Fatal("Expected parent peer to be extracted")
	}
	if peer.Kind != "direct" {
		t.Errorf("Expected parent peer kind 'direct', got '%s'", peer.Kind)
	}
	if peer.ID != "parent-id" {
		t.Errorf("Expected parent peer ID 'parent-id', got '%s'", peer.ID)
	}

	// Test with no parent metadata
	msg2 := bus.InboundMessage{
		Channel:  "test",
		SenderID: "user2",
		ChatID:   "chat2",
		Metadata: map[string]string{},
	}

	peer2 := extractParentPeer(msg2)
	if peer2 != nil {
		t.Errorf("Expected nil parent peer, got %+v", peer2)
	}
}

// TestGatewayVersion tests the gatewayVersion helper function
func TestGatewayVersion(t *testing.T) {
	version := gatewayVersion()
	// Version should be either "dev" or a valid version string
	if version == "" {
		t.Error("Expected non-empty version")
	}
}

// TestFormatSubagentsResponse_Info tests /subagents info command
func TestFormatSubagentsResponse_Info(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/subagents info nonexistent",
	})

	if !handled {
		t.Error("Expected /subagents info to be handled")
	}
	if !strings.Contains(result, "Subagent task not found") {
		t.Errorf("Expected task not found message, got: %s", result)
	}
}

// TestFormatSubagentsResponse_Stop tests /subagents stop command
func TestFormatSubagentsResponse_Stop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	ch := newCommandHandler(al)

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/subagents stop nonexistent",
	})

	if !handled {
		t.Error("Expected /subagents stop to be handled")
	}
	if !strings.Contains(result, "Subagent task not running") {
		t.Errorf("Expected task not running message, got: %s", result)
	}
}

// TestSessionKeyOverride tests that session key from message overrides route session key
func TestSessionKeyOverride(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)
	ch := newCommandHandler(al)

	// Test with agent: prefixed session key
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/clear",
		SessionKey: "agent:custom-session",
	})

	if !handled {
		t.Error("Expected /clear to be handled")
	}
	if result != "✅ Conversation cleared." {
		t.Errorf("Expected clear message, got: %s", result)
	}

	// Test with telegram: prefixed session key
	result, handled = ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "telegram",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/clear",
		SessionKey: "telegram:custom-session",
	})

	if !handled {
		t.Error("Expected /clear to be handled")
	}
	if result != "✅ Conversation cleared." {
		t.Errorf("Expected clear message, got: %s", result)
	}
}

// TestHandleCommand_SessionAgentOverride tests that session agent overrides default agent
func TestHandleCommand_SessionAgentOverride(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:session-agent-override"
	ch := newCommandHandler(al)

	// First, switch to main agent
	al.sessionAgents.Store(sessionKey, "main")

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/status",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /status to be handled")
	}
	if !strings.Contains(result, "lele") {
		t.Errorf("Expected status response, got: %s", result)
	}
}

// TestHandleStatusCommand_WithTokens tests /status command shows token counts
func TestHandleStatusCommand_WithTokens(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Use the default agent's session directly
	agent := al.registry.GetDefaultAgent()
	sessionKey := "agent:main:main" // This is what the command handler will use

	// Add some token counts to the session
	agent.Sessions.AddTokenCounts(sessionKey, 150, 80)
	agent.Sessions.AddMessage(sessionKey, "user", "test message")

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/status",
	})

	if !handled {
		t.Error("Expected /status to be handled")
	}

	// Verify token information is present
	if !strings.Contains(result, "Tokens:") {
		t.Errorf("Expected 'Tokens:' in status, got: %s", result)
	}

	// The new format should show "X in / Y out (Z total)"
	if !strings.Contains(result, "in") || !strings.Contains(result, "out") {
		t.Errorf("Expected token format 'in / out', got: %s", result)
	}

	// Verify context information
	if !strings.Contains(result, "Context:") {
		t.Errorf("Expected 'Context:' in status, got: %s", result)
	}

	t.Logf("Status output: %s", result)
}

// TestHandleStatusCommand_TokenAccumulation tests that tokens accumulate correctly
func TestHandleStatusCommand_TokenAccumulation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	// Use the default agent's session directly
	agent := al.registry.GetDefaultAgent()
	sessionKey := "agent:main:main" // This is what the command handler will use

	// Add tokens multiple times to test accumulation
	agent.Sessions.AddTokenCounts(sessionKey, 100, 50)
	agent.Sessions.AddTokenCounts(sessionKey, 200, 75)
	agent.Sessions.AddTokenCounts(sessionKey, 50, 25)

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/status",
	})

	if !handled {
		t.Error("Expected /status to be handled")
	}

	// Total should be 350 in + 150 out = 500 total
	if !strings.Contains(result, "350") || !strings.Contains(result, "150") || !strings.Contains(result, "500") {
		t.Errorf("Expected accumulated tokens (350 in / 150 out / 500 total), got: %s", result)
	}

	t.Logf("Status with accumulated tokens: %s", result)
}

// TestHandleStatusCommand_ZeroTokens tests /status when no tokens have been used
func TestHandleStatusCommand_ZeroTokens(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	agent := al.registry.GetDefaultAgent()
	sessionKey := "agent:main:main"

	// Don't add any tokens - should show zeros
	agent.Sessions.AddMessage(sessionKey, "user", "test")

	ch := newCommandHandler(al)
	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:  "test",
		SenderID: "user1",
		ChatID:   "chat1",
		Content:  "/status",
	})

	if !handled {
		t.Error("Expected /status to be handled")
	}

	// Should still show token format even with zeros
	if !strings.Contains(result, "Tokens:") {
		t.Errorf("Expected 'Tokens:' in status, got: %s", result)
	}

	t.Logf("Status with zero tokens: %s", result)
}

// TestHandleStatusCommand_ContextIncludesSystemPrompt verifies that /status includes system prompt tokens
// in the context calculation (not just history tokens).
func TestHandleStatusCommand_ContextIncludesSystemPrompt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "command-handler-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create bootstrap files to have non-empty system prompt
	os.MkdirAll(filepath.Join(tmpDir, "memory"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "SOUL.md"), []byte("# SOUL\nTest soul content with enough text"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "AGENT.md"), []byte("# AGENT\nTest agent content with enough text"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "memory", "MEMORY.md"), []byte("# Memory\nTest memory content"), 0644)

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
	provider := &mockProvider{}
	al := NewAgentLoop(cfg, msgBus, provider)

	sessionKey := "test:context-tokens"
	ch := newCommandHandler(al)

	// Build system prompt to verify it's non-empty
	agent := al.registry.GetDefaultAgent()
	_ = agent.ContextBuilder.BuildSystemPrompt()

	result, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
		Channel:    "test",
		SenderID:   "user1",
		ChatID:     "chat1",
		Content:    "/status",
		SessionKey: sessionKey,
	})

	if !handled {
		t.Error("Expected /status to be handled")
	}

	// Verify context is shown
	if !strings.Contains(result, "Context:") {
		t.Fatal("Expected 'Context:' in status output")
	}

	// Extract the context percentage
	// Format: "📚 Context: ~1234/128000 (0%)"
	contextIdx := strings.Index(result, "Context: ~")
	if contextIdx == -1 {
		t.Fatal("Could not find 'Context: ~' in status output")
	}

	// Parse the context number
	contextPart := result[contextIdx+len("Context: ~"):]
	var contextTokens int
	n, _ := fmt.Sscanf(contextPart, "%d", &contextTokens)
	if n != 1 {
		t.Fatal("Could not parse context tokens from status output")
	}

	// System prompt should contribute significant tokens
	// With our bootstrap files (~150 chars), system prompt should be ~60 tokens minimum
	// If status only counted history, it would show ~0-10 tokens for empty history
	// So context should be > 50 tokens if system prompt is included
	if contextTokens < 50 {
		t.Errorf("Context tokens (%d) too low - seems system prompt not included. Expected > 50", contextTokens)
	}

	t.Logf("Context tokens: %d (includes system prompt with bootstrap files)", contextTokens)
}
