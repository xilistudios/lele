package tools

import (
	"context"
	"strings"
	"testing"
)

func TestGuardedCommand_RefusalTextIsTerminal(t *testing.T) {
	tool := newGuardedExecTool(t, nil)
	tool.SetApprovalMode(true)

	const command = "rm -rf /tmp/definitely-not-run"
	result := tool.Execute(context.Background(), map[string]interface{}{"command": command})
	if result == nil {
		t.Fatal("Execute returned nil")
	}

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
		"NOT executed",
		"blocked by an approval guard",
		"no interactive approver",
		"Do not retry",
		"rephrase",
		"not a transient state",
		"STATUS: not_done",
		"operator must run manually",
		result.ApprovalRequired.Reason,
	} {
		if !strings.Contains(result.ForLLM, want) {
			t.Errorf("ForLLM does not contain %q:\n%s", want, result.ForLLM)
		}
	}

	if strings.Contains(result.ForLLM, "requires user approval") {
		t.Errorf("ForLLM still uses the retryable-sounding wording:\n%s", result.ForLLM)
	}

	if result.IsError {
		t.Error("IsError = true, want false (unchanged: the parent intercepts on ApprovalRequired)")
	}
}
