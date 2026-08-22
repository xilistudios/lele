// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

// ============================================================================
// loop.go accessors
// ============================================================================

func TestLoopAccessors(t *testing.T) {
	al := newTestAgentLoop(t)

	// MessageBus returns the bus.
	if al.MessageBus() == nil {
		t.Error("expected non-nil MessageBus")
	}
	// GetDefaultAgent is non-nil when registry has a default.
	if al.GetDefaultAgent() == nil {
		t.Error("expected default agent")
	}
	// KeyringService is non-nil (keyring module disabled by default but svc exists).
	if al.KeyringService() == nil {
		t.Error("expected non-nil keyring service")
	}
	// SessionManager is non-nil.
	if al.SessionManager() == nil {
		t.Error("expected non-nil session manager")
	}
	// GroupManager is non-nil.
	if al.GroupManager() == nil {
		t.Error("expected non-nil group manager")
	}
	// SkillInstaller tied to default agent workspace.
	if al.SkillInstaller() == nil {
		t.Error("expected non-nil skill installer")
	}
	// Store may be nil; just ensure no panic.
	_ = al.Store()
	// AllGroupSnapshots returns something (possibly empty).
	if al.AllGroupSnapshots() == nil {
		t.Error("expected non-nil group snapshots")
	}
	// GetProvidable.
	if al.GetProvidable() == nil {
		t.Error("expected non-nil providable")
	}
	// GetSubagents non-nil.
	if al.GetSubagents() == nil {
		t.Error("expected non-nil subagents map")
	}
	// GetStartupInfo.
	if al.GetStartupInfo() == nil {
		t.Error("expected non-nil startup info")
	}
}

func TestLoopAccessors_NoRegistry(t *testing.T) {
	al := newTestAgentLoop(t)
	al.registry.agents = nil
	if al.GetDefaultAgent() != nil {
		t.Error("expected nil default agent")
	}
	if al.getDefaultAgentID() != "main" {
		t.Error("expected fallback 'main' agent id")
	}
	if al.SkillsLoader() != nil {
		t.Error("expected nil skills loader without default agent")
	}
	if al.SkillInstaller() != nil {
		t.Error("expected nil skill installer without default agent")
	}
}

func TestLoopGetSubagentParentSessionKey(t *testing.T) {
	al := newTestAgentLoop(t)
	// bare subagent prefix -> no task found in coordinator
	if got := al.GetSubagentParentSessionKey("subagent:abc"); got != "" {
		t.Errorf("bare subagent key = %q, want empty", got)
	}
	// session-key form with subagent- pattern but unknown task -> empty
	if got := al.GetSubagentParentSessionKey("native:parent:subagent-77"); got != "native:parent" {
		t.Errorf("session-key fallback = %q, want native:parent", got)
	}
	if got := al.GetSubagentParentSessionKey("not-a-subagent"); got != "" {
		t.Errorf("non-subagent = %q, want empty", got)
	}
}

func TestLoopChannelManagerSetters(t *testing.T) {
	al := newTestAgentLoop(t)
	am := channels.NewApprovalManager()
	al.SetApprovalManager(am)
	if al.GetApprovalManager() != am {
		t.Error("expected approval manager to be set/returned")
	}
	al.SetChannelManager(nil) // should not panic
}

func TestLoopSessionProcessing(t *testing.T) {
	al := newTestAgentLoop(t)
	if al.isSessionProcessing("native:proc") {
		t.Error("expected not processing for empty key path")
	}
	// Pre-seed an alias to test ResolveSessionKey path.
	al.sessionAliases.Store("native:proc", "native:proc:chat:1")
	if al.isSessionProcessing("native:proc") {
		t.Error("expected not processing for unresolved alias")
	}
}

func TestLoopMessageBusGetDefault(t *testing.T) {
	// default cfg (no agents) -> registry has a main agent.
	cfg := config.DefaultConfig()
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	if al.GetDefaultAgent() == nil {
		t.Error("default config should produce a main agent")
	}
}

func TestToolLineRegex(t *testing.T) {
	if !toolLineRegex.MatchString("🛠️ Exec: ls") {
		t.Error("expected tool line regex match")
	}
}

// ============================================================================
// subagent_helpers.go — publishSubagentAsyncResult
// ============================================================================

func TestPublishSubagentAsyncResult_NilGuard(t *testing.T) {
	publishSubagentAsyncResult(nil, "s", "c", "chat", "task", nil)
	al := newTestAgentLoop(t)
	publishSubagentAsyncResult(al, "s", "c", "chat", "task", nil)                             // result nil
	publishSubagentAsyncResult(al, "s", "c", "chat", "task", &tools.ToolResult{ForLLM: "  "}) // empty content
}

func TestPublishSubagentAsyncResult_WithContent(t *testing.T) {
	al := newTestAgentLoop(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen := make(chan bus.InboundMessage, 10)
	go func() {
		for {
			m, ok := al.bus.ConsumeInbound(ctx)
			if !ok {
				return
			}
			seen <- m
		}
	}()
	result := &tools.ToolResult{ForLLM: "the subagent result"}
	publishSubagentAsyncResult(al, "native:p", "native", "chat1", "task-1", result)
	select {
	case m := <-seen:
		if m.SenderID != "subagent" || m.Channel != "system" {
			t.Errorf("unexpected inbound message: %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for inbound message")
	}
}

func TestPublishSubagentAsyncResult_VerboseBasic(t *testing.T) {
	al := newTestAgentLoop(t)
	al.verboseManager.SetLevel("native:v", session.VerboseBasic)
	result := &tools.ToolResult{ForLLM: "STATUS: completed\n🛠️ Exec: ls\nSUMMARY: ok"}
	publishSubagentAsyncResult(al, "native:v", "native", "chat1", "task-2", result)
}

func TestPublishSubagentAsyncResult_ErrorOnly(t *testing.T) {
	al := newTestAgentLoop(t)
	result := &tools.ToolResult{ForLLM: "", Err: &errStringError{"boom"}}
	publishSubagentAsyncResult(al, "native:e", "native", "chat1", "task-3", result)
}

// errStringError is a minimal error so ToolResult.Err path is exercised.
type errStringError struct{ msg string }

func (e *errStringError) Error() string { return e.msg }
