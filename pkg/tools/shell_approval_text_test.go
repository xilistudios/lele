package tools

import (
	"context"
	"strings"
	"testing"
)

// TestGuardedCommand_RefusalTextIsTerminal pins the wording of the
// ApprovalRequired result ExecTool returns in approval mode.
//
// The interactive parent loop intercepts on ApprovalRequired before it ever
// reads ForLLM (pkg/agent/tool_executor.go), so this text is only consumed by
// paths that ignore the field — i.e. subagents, which execute through
// RunToolLoop and feed ForLLM straight back to the model. A phrasing that
// sounds like a pending, retryable state ("requires user approval") makes the
// model retry or rephrase until SubagentTimeoutMinutes kills the task, so the
// refusal must read as terminal and must tell the model what to do instead.
func TestGuardedCommand_RefusalTextIsTerminal(t *testing.T) {
	tool := newGuardedExecTool(t, nil)
	tool.SetApprovalMode(true)

	const command = "rm -rf /tmp/definitely-not-run"
	result := tool.Execute(context.Background(), map[string]interface{}{"command": command})
	if result == nil {
		t.Fatal("Execute returned nil")
	}

	// The parent path keys off this field and nothing else; if it goes away the
	// whole interactive approval workflow silently disappears. C0 must not
	// touch it.
	if result.ApprovalRequired == nil {
		t.Fatal("ApprovalRequired is nil: the interactive approval workflow would never trigger")
	}
	if result.ApprovalRequired.Command != command {
		t.Errorf("ApprovalRequired.Command = %q, want %q", result.ApprovalRequired.Command, command)
	}
	if result.ApprovalRequired.Reason == "" {
		t.Error("ApprovalRequired.Reason is empty")
	}

	for _, want := range []string{
		"NOT executed",                 // terminal, past tense
		"blocked by an approval guard", // names the cause
		"no interactive approver",      // the truth in a subagent session
		"Do not retry",                 // explicit anti-retry instruction
		"rephrase",                     // and anti-rephrase instruction
		"not a transient state",        // why retrying is pointless
		"STATUS: not_done",             // gives the model a way to finish
		"operator must run manually",   // what to tell the human
		result.ApprovalRequired.Reason, // the guard message is still surfaced
	} {
		if !strings.Contains(result.ForLLM, want) {
			t.Errorf("ForLLM does not contain %q:\n%s", want, result.ForLLM)
		}
	}

	// Regression guard: the old phrasing is exactly what a subagent misread as
	// "an approval is still on its way", so it must not come back.
	if strings.Contains(result.ForLLM, "requires user approval") {
		t.Errorf("ForLLM still uses the retryable-sounding wording:\n%s", result.ForLLM)
	}

	// Unchanged: the parent decides what to do based on ApprovalRequired, not
	// on the error flag.
	if result.IsError {
		t.Error("IsError = true, want false (unchanged: the parent intercepts on ApprovalRequired)")
	}
}
