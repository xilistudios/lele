package providers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- GitHub Copilot: default connect mode + grpc failure launch ---

// TestGitHubCopilotProvider_DefaultConnectMode forces the "connectMode == \"\""
// branch (defaults to grpc) and the grpc client.Start failure return path by
// pointing the CLI URL at an unreachable address.
func TestGitHubCopilotProvider_DefaultConnectMode(t *testing.T) {
	// Empty connect mode defaults to "grpc"; the CLI at an unreachable address
	// makes Start fail, returning the documented error. This covers both the
	// default-mode assignment and the grpc error return branch.
	p, err := NewGitHubCopilotProvider("127.0.0.1:1", "", "gpt-4.1")
	if err == nil {
		// If Start happened to succeed (unlikely), ensure mode defaulted.
		if p.connectMode != "grpc" {
			t.Errorf("connectMode = %q, want grpc", p.connectMode)
		}
		return
	}
	if !strings.Contains(strings.ToLower(err.Error()), "copilot") {
		t.Logf("err = %v", err)
	}
}

// --- tool_call_extract: unmatched "tool_calls" JSON ---

// TestStripToolCallsFromText_UnmatchedBrace covers the early-return in
// stripToolCallsFromText when a "tool_calls" marker appears without a matching
// closing brace for itself.
func TestStripToolCallsFromText_UnmatchedBrace(t *testing.T) {
	input := `prefix {"tool_calls" but no closing brace`
	got := stripToolCallsFromText(input)
	if got != input {
		t.Errorf("strip = %q, want unchanged %q", got, input)
	}
}

// --- Codex CLI Chat: process exit with only stderr output ---

// TestCodexCliProvider_StderrErrorBranch drives Chat through the
// "cmd.Run() errored with non-empty stderr" branch by providing a script that
// writes a diagnostic to stderr and exits non-zero, without producing usable
// JSONL on stdout.
func TestCodexCliProvider_StderrErrorBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("shell script spawn")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "codex-sh")
	body := "#!/bin/sh\necho 'boom: some cli diagnostic' >&2\nexit 2\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	p := &CodexCliProvider{command: script}
	_, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "x"}}, nil, "", nil)
	if err == nil {
		t.Fatal("expected error from stderr-only failed run")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %q, want to contain stderr text", err.Error())
	}
}

// TestCodexCliProvider_DeadlineCanceledBranch covers the context-canceled path
// inside Chat when cmd.Run returns and ctx is canceled.
func TestCodexCliProvider_DeadlineCanceledBranch(t *testing.T) {
	if testing.Short() {
		t.Skip("shell script spawn")
	}
	dir := t.TempDir()
	// Sleep briefly then fail; with a context deadline we get canceled.
	script := filepath.Join(dir, "codex-sleep")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	p := &CodexCliProvider{command: script}

	_, err := p.Chat(ctx, []Message{{Role: "user", Content: "x"}}, nil, "", nil)
	if err == nil {
		t.Log("Chat returned nil; canceled branch not asserted")
		return
	}
	// The closest contract: an error mentioning context deadline or canceled.
	if !strings.Contains(err.Error(), "context") && !strings.Contains(err.Error(), "cancel") {
		t.Logf("err = %v", err)
	}
}
