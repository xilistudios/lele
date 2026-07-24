package group

import (
	"context"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

// TestDepsCallbackAssignment verifies that the callback type aliases are
// assignable from plain functions, ensuring they can be implemented by
// pkg/agent without issues.
func TestDepsCallbackAssignment(t *testing.T) {
	// TurnExecutor
	var _ TurnExecutor = func(_ context.Context, _ TurnRequest) (string, int, error) {
		return "", 0, nil
	}

	// ResolveAgentFunc
	var _ ResolveAgentFunc = func(_ string) (AgentContext, bool) {
		return AgentContext{}, false
	}

	// Publisher
	var _ Publisher = func(_ bus.OutboundMessage) {}

	// If this compiles and runs, all callback types are usable.
}

func TestAgentContextFields(t *testing.T) {
	ctx := AgentContext{
		AgentID:       "test-agent",
		Name:          "Test Agent",
		Workspace:     "/tmp/test",
		SystemPrompt:  "You are a test.",
		ContextWindow: 8192,
		MaxTokens:     4096,
	}
	if ctx.AgentID != "test-agent" {
		t.Errorf("unexpected AgentID: %s", ctx.AgentID)
	}
	if ctx.MaxTokens != 4096 {
		t.Errorf("unexpected MaxTokens: %d", ctx.MaxTokens)
	}
}

func TestTurnRequestFields(t *testing.T) {
	req := TurnRequest{
		GroupID:      "group:1",
		Speaker:      "alice",
		SystemPrompt: "You are Alice.",
		Transcript:   "[Alice]: hello",
		Instruction:  "Propose a solution",
		MaxTokens:    2048,
		EnableTools:  true,
	}
	if req.GroupID != "group:1" {
		t.Errorf("unexpected GroupID: %s", req.GroupID)
	}
	if !req.EnableTools {
		t.Error("expected EnableTools=true")
	}
}
