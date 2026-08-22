// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
)

// newCovLoop builds an AgentLoop with a temp workspace and named provider.
func newCovLoop(t *testing.T) *AgentLoop {
	tmpDir := t.TempDir()
	cfg := buildCovConfig(tmpDir)
	return NewAgentLoop(cfg, bus.NewMessageBus())
}

func TestMessageProcessor_ProcessDirect(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	// SYSTEM_SPAWN path routes through ProcessDirectWithChannel since the
	// SYSTEM_SPAWN string is passed directly. ProcessDirect just wraps channel.
	out, err := mp.ProcessDirectWithChannel(context.Background(), "SYSTEM_SPAWN:\nTASK: do a thing", "ses1", "cli", "chat1")
	if err != nil {
		t.Logf("system spawn error (expected with no subagent manager): %v", err)
	} else {
		t.Logf("spawn result: %q", out)
	}
}

func TestMessageProcessor_ProcessDirect_Plain(t *testing.T) {
	al := newCovLoop(t)
	agent := al.registry.GetDefaultAgent()
	agent.Provider = &mockProvider{mockResponse: "hello reply"}
	mp := newMessageProcessor(al)
	// non-SYSTEM_SPAWN direct message
	_, err := mp.ProcessDirect(context.Background(), "hi there", "ses-default")
	if err != nil {
		t.Logf("direct error: %v", err)
	}
}

func TestMessageProcessor_ProcessHeartbeat(t *testing.T) {
	al := newCovLoop(t)
	agent := al.registry.GetDefaultAgent()
	agent.Provider = &mockProvider{mockResponse: "beat"}
	mp := newMessageProcessor(al)
	_, err := mp.ProcessHeartbeat(context.Background(), "ping", "cli", "chat1")
	if err != nil {
		t.Logf("heartbeat error: %v", err)
	}
}

func TestMessageProcessor_EstimateTokens(t *testing.T) {
	mp := &messageProcessorImpl{}
	msgs := []providers.Message{
		{Role: "user", Content: "hello world"},
		{Role: "user", Content: "excluded", ExcludeFromContext: true},
	}
	got := mp.estimateTokens(msgs)
	// only "hello world" = 11 chars -> 11*2/5 = 4
	if got != 4 {
		t.Errorf("estimateTokens = %d, want 4", got)
	}
	if mp.estimateTokens(nil) != 0 {
		t.Error("nil should be 0")
	}
}

func TestMessageProcessor_HandleNewCommand(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	agent := al.registry.GetDefaultAgent()
	got := mp.handleNewCommand(agent, "telegram:1")
	if !strings.Contains(got, "New conversation started") {
		t.Errorf("got %q", got)
	}
	// nil agent
	if got := mp.handleNewCommand(nil, "telegram:1"); got != "No default agent configured" {
		t.Errorf("nil agent: %q", got)
	}
}

func TestMessageProcessor_HandleToggleCommand(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	if got := mp.handleToggleCommand(nil); !strings.Contains(got, "Usage: /toggle") {
		t.Errorf("no args: %q", got)
	}
	got := mp.handleToggleCommand([]string{"ephemeral"})
	if !strings.Contains(got, "Ephemeral") {
		t.Errorf("ephemeral: %q", got)
	}
	if got := mp.handleToggleCommand([]string{"bogus"}); !strings.Contains(got, "Unknown toggle target") {
		t.Errorf("unknown: %q", got)
	}
}

func TestMessageProcessor_HandleVerboseCommand(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	if got := mp.handleVerboseCommand(""); !strings.Contains(got, "session context") {
		t.Errorf("empty key: %q", got)
	}
	// cycle: off -> basic
	got := mp.handleVerboseCommand("telegram:1")
	if !strings.Contains(got, "BASIC") {
		t.Errorf("cycle result: %q", got)
	}
	got = mp.handleVerboseCommand("telegram:1")
	if !strings.Contains(got, "FULL") {
		t.Errorf("cycle result 2: %q", got)
	}
	got = mp.handleVerboseCommand("telegram:1")
	if !strings.Contains(got, "OFF") {
		t.Errorf("cycle result 3: %q", got)
	}
}

func TestMessageProcessor_HandleModelCommand(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	agent := al.registry.GetDefaultAgent()
	// nil agent
	if got := mp.handleModelCommand(nil, "s", nil); got != "No default agent configured" {
		t.Errorf("nil agent: %q", got)
	}
	// no args
	got := mp.handleModelCommand(agent, "telegram:1", nil)
	if !strings.Contains(got, "Current model") {
		t.Errorf("no args: %q", got)
	}
	// empty session key
	got = mp.handleModelCommand(agent, "", []string{"gpt-4"})
	if !strings.Contains(got, "session context") {
		t.Errorf("empty session: %q", got)
	}
	// change model
	got = mp.handleModelCommand(agent, "telegram:1", []string{"m2"})
	if !strings.Contains(got, "Model changed") {
		t.Errorf("change model: %q", got)
	}
}

func TestMessageProcessor_HandleAgentCommand(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	if got := mp.handleAgentCommand("", nil); !strings.Contains(got, "session context") {
		t.Errorf("empty key: %q", got)
	}
	// list agents
	got := mp.handleAgentCommand("telegram:1", nil)
	if !strings.Contains(got, "Available agents") {
		t.Errorf("list: %q", got)
	}
	// not found
	got = mp.handleAgentCommand("telegram:1", []string{"nope"})
	if !strings.Contains(got, "Agent not found") {
		t.Errorf("not found: %q", got)
	}
	// switch to default agent
	got = mp.handleAgentCommand("telegram:1", []string{"main"})
	if !strings.Contains(got, "Agent changed to:") {
		t.Errorf("switch: %q", got)
	}
}

func TestMessageProcessor_FormatStatusResponse(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	agent := al.registry.GetDefaultAgent()
	got := mp.formatStatusResponse(agent, "telegram:1", "telegram")
	if !strings.Contains(got, "lele") || !strings.Contains(got, "Model:") {
		t.Errorf("status: %q", got)
	}
	// nil agent
	if got := mp.formatStatusResponse(nil, "s", "cli"); got != "No default agent configured" {
		t.Errorf("nil agent status: %q", got)
	}
}

func TestMessageProcessor_ProcessSystemMessage_NonSystemChannel(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	_, err := mp.processSystemMessage(context.Background(), bus.InboundMessage{Channel: "telegram", Content: "/status"})
	if err == nil {
		t.Error("expected error for non-system channel")
	}
}

func TestMessageProcessor_ProcessSystemMessage_Commands(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)

	commands := []string{"/status", "/new", "/toggle", "/clear", "/model", "/verbose", "/agent"}
	for _, cmd := range commands {
		msg := bus.InboundMessage{
			Channel:    "system",
			Content:    cmd,
			ChatID:     "cli:chat1",
			SessionKey: "telegram:1",
			Metadata:   map[string]string{"message_id": "m1"},
		}
		_, _ = mp.processSystemMessage(context.Background(), msg)
	}
	// /stop with empty session subagents
	_, _ = mp.processSystemMessage(context.Background(), bus.InboundMessage{
		Channel: "system", Content: "/stop", ChatID: "cli:chat1",
	})
	// /compact with few messages
	_, _ = mp.processSystemMessage(context.Background(), bus.InboundMessage{
		Channel: "system", Content: "/compact", ChatID: "cli:chat1", SessionKey: "telegram:comp",
	})
	// /goal
	_, _ = mp.processSystemMessage(context.Background(), bus.InboundMessage{
		Channel: "system", Content: "/goal", ChatID: "cli:chat1", SessionKey: "telegram:goal",
	})
}

func TestMessageProcessor_ProcessSystemMessage_NoParts(t *testing.T) {
	al := newCovLoop(t)
	mp := newMessageProcessor(al)
	// chan with no content in a 'system' channel yields empty parts
	_, err := mp.processSystemMessage(context.Background(), bus.InboundMessage{
		Channel: "system", Content: "   ", ChatID: "cli:chat1",
	})
	if err != nil {
		t.Errorf("expected no error for empty system cmd, got %v", err)
	}
}

func TestSessionManager_AddTokenCounts_NilDefaultAgent(t *testing.T) {
	al := newCovLoop(t)
	sm := newSessionManager(al)
	// make default agent nil & registry empty to hit the default-agent nil branch
	al.registry.agents = make(map[string]*AgentInstance)
	sm.AddTokenCounts("telegram:noagent", 1, 2)
}

func TestSessionManager_AddTokenCounts_WithOverride(t *testing.T) {
	al := newCovLoop(t)
	sm := newSessionManager(al)
	key := "telegram:override"
	al.sessionAgents.Store(key, "main")
	sm.AddTokenCounts(key, 3, 4)
}

func TestSessionManager_RegisterAndCancelSession(t *testing.T) {
	al := newCovLoop(t)
	sm := newSessionManager(al)

	// nil cancel
	cleanup := sm.RegisterSessionCancel("key1", nil)
	cleanup()

	// empty key
	sm.RegisterSessionCancel("", func() {})

	_, cancel := context.WithCancel(context.Background())
	cleanup = sm.RegisterSessionCancel("key2", cancel)

	if !sm.IsSessionProcessing("key2") {
		t.Error("expected key2 processing")
	}
	if n := sm.CancelSession("key2"); n != 1 {
		t.Errorf("CancelSession = %d, want 1", n)
	}
	if sm.IsSessionProcessing("key2") {
		t.Error("expected key2 not processing after cancel")
	}
	// Cancel again -> 0
	if n := sm.CancelSession("key2"); n != 0 {
		t.Errorf("second CancelSession = %d, want 0", n)
	}
	// empty key
	if n := sm.CancelSession(""); n != 0 {
		t.Errorf("CancelSession empty = %d, want 0", n)
	}

	// Register + cleanup
	_, cancel2 := context.WithCancel(context.Background())
	cleanup2 := sm.RegisterSessionCancel("key3", cancel2)
	if !sm.IsSessionProcessing("key3") {
		t.Error("expected key3 processing")
	}
	cleanup2()
}

func TestSessionManager_RegisterSessionCancel_ContextValue(t *testing.T) {
	al := newCovLoop(t)
	sm := newSessionManager(al)

	// Register twice on same key -> group
	_, cancelA := context.WithCancel(context.Background())
	_, cancelB := context.WithCancel(context.Background())
	cleanupA := sm.RegisterSessionCancel("dup", cancelA)
	cleanupB := sm.RegisterSessionCancel("dup", cancelB)

	// cancel group -> returns 2
	if n := sm.CancelSession("dup"); n != 2 {
		t.Errorf("CancelSession dup = %d, want 2", n)
	}
	_ = cleanupA
	_ = cleanupB
}

func TestVerboseManager_Levels(t *testing.T) {
	if string(session.VerboseOff) != "off" {
		t.Error("verbose off string mismatch")
	}
}
