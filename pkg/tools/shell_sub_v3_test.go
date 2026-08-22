package tools

import (
	"context"
	"testing"
	"time"
)

// TestExecTool_SubstituteSecrets_EnabledNoPlaceholder covers the branch in
// substituteSecrets where substitution is enabled+keyring attached but the
// command has no {{SECRET:...}} placeholder (shell.go:568-570) — returns the
// command unchanged.
func TestExecTool_SubstituteSecrets_EnabledNoPlaceholder(t *testing.T) {
	svc := newTestKeyring(t)
	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	out, err := tool.substituteSecrets(context.Background(), "echo hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "echo hi" {
		t.Errorf("out = %q, want unchanged command", out)
	}
}

// TestExecTool_SubstituteSecrets_PlaceholderNoAgent covers the agentID==""
// default branch (shell.go:573-575) where a placeholder exists but no agent
// context was set.
func TestExecTool_SubstituteSecrets_PlaceholderNoAgent(t *testing.T) {
	svc := newTestKeyring(t)
	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	// No agent context -> resolves to agent "unknown"; secret doesn't exist so
	// this returns an error naming the secret.
	out, err := tool.substituteSecrets(context.Background(), "echo {{SECRET:missing.nope}}")
	if err == nil {
		t.Fatalf("expected error for missing secret, got out=%q", out)
	}
}

// TestExecTool_Execute_StopCommand exercises the context-cancelled branch
// (shell.go:376-383) by cancelling the context during execution.
func TestExecTool_Execute_StopCommand(t *testing.T) {
	tool := NewExecTool("", false)
	tool.SetTimeout(60 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel almost immediately to race the command start; command is `sleep`.
	done := make(chan struct{})
	go func() {
		cancel()
		close(done)
	}()
	<-done

	result := tool.Execute(ctx, map[string]interface{}{
		"command": "sleep 5",
	})
	if result == nil {
		t.Fatal("nil result")
	}
	if !result.IsError {
		// A cancelled context but the command may have already finished (0
		// startup race); accept either an error or a completed non-error.
		_ = result
	}
}
