package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/xilistudios/lele/pkg/keyring"
)

// TestShellTool_Success verifies successful command execution
func TestShellTool_Success(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "echo 'hello world'",
	}

	result := tool.Execute(ctx, args)

	// Success should not be an error
	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	// ForUser should contain command output
	if !strings.Contains(result.ForUser, "hello world") {
		t.Errorf("Expected ForUser to contain 'hello world', got: %s", result.ForUser)
	}

	// ForLLM should contain full output
	if !strings.Contains(result.ForLLM, "hello world") {
		t.Errorf("Expected ForLLM to contain 'hello world', got: %s", result.ForLLM)
	}
}

// TestShellTool_Failure verifies failed command execution
func TestShellTool_Failure(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "ls /nonexistent_directory_12345",
	}

	result := tool.Execute(ctx, args)

	// Failure should be marked as error
	if !result.IsError {
		t.Errorf("Expected error for failed command, got IsError=false")
	}

	// ForUser should contain error information
	if result.ForUser == "" {
		t.Errorf("Expected ForUser to contain error info, got empty string")
	}

	// ForLLM should contain exit code or error
	if !strings.Contains(result.ForLLM, "Exit code") && result.ForUser == "" {
		t.Errorf("Expected ForLLM to contain exit code or error, got: %s", result.ForLLM)
	}
}

// TestShellTool_Timeout verifies command timeout handling
func TestShellTool_Timeout(t *testing.T) {
	tool := NewExecTool("", false)
	tool.SetTimeout(100 * time.Millisecond)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "sleep 10",
	}

	result := tool.Execute(ctx, args)

	// Timeout should be marked as error
	if !result.IsError {
		t.Errorf("Expected error for timeout, got IsError=false")
	}

	// Should mention timeout
	if !strings.Contains(result.ForLLM, "timed out") && !strings.Contains(result.ForUser, "timed out") {
		t.Errorf("Expected timeout message, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

// TestShellTool_WorkingDir verifies custom working directory
func TestShellTool_WorkingDir(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test content"), 0644)

	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command":     "cat test.txt",
		"working_dir": tmpDir,
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success in custom working dir, got error: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForUser, "test content") {
		t.Errorf("Expected output from custom dir, got: %s", result.ForUser)
	}
}

// TestShellTool_DangerousCommand verifies safety guard blocks dangerous commands
func TestShellTool_DangerousCommand(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "rm -rf /",
	}

	result := tool.Execute(ctx, args)

	// Dangerous command should be blocked
	if !result.IsError {
		t.Errorf("Expected dangerous command to be blocked (IsError=true)")
	}

	if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForUser, "blocked") {
		t.Errorf("Expected 'blocked' message, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

// TestShellTool_MissingCommand verifies error handling for missing command
func TestShellTool_MissingCommand(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{}

	result := tool.Execute(ctx, args)

	// Should return error result
	if !result.IsError {
		t.Errorf("Expected error when command is missing")
	}
}

// TestShellTool_StderrCapture verifies stderr is captured and included
func TestShellTool_StderrCapture(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "sh -c 'echo stdout; echo stderr >&2'",
	}

	result := tool.Execute(ctx, args)

	// Both stdout and stderr should be in output
	if !strings.Contains(result.ForLLM, "stdout") {
		t.Errorf("Expected stdout in output, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "stderr") {
		t.Errorf("Expected stderr in output, got: %s", result.ForLLM)
	}
}

// TestShellTool_OutputTruncation verifies long output is truncated
func TestShellTool_OutputTruncation(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	// Generate long output (>10000 chars)
	args := map[string]interface{}{
		"command": "python3 -c \"print('x' * 20000)\" || echo " + strings.Repeat("x", 20000),
	}

	result := tool.Execute(ctx, args)

	// Should have truncation message or be truncated
	if len(result.ForLLM) > 15000 {
		t.Errorf("Expected output to be truncated, got length: %d", len(result.ForLLM))
	}
}

// TestShellTool_TruncationKeepsValidUTF8 ensures truncation backs off to a UTF-8
// rune boundary so multi-byte characters are never split in the middle.
func TestShellTool_TruncationKeepsValidUTF8(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	// Emit >10000 bytes of multibyte UTF-8 ("café" = 5 bytes each). The cut at
	// byte 10000 will likely land mid-rune; the fix must back off to a boundary.
	args := map[string]interface{}{
		"command": "yes 'café' | head -c 20000",
	}

	result := tool.Execute(ctx, args)

	// Ensure truncation actually happened (as a sanity check of the harness).
	if len(result.ForLLM) <= 10000 {
		t.Errorf("Expected output to be truncated (>10000 bytes), got length: %d", len(result.ForLLM))
	}

	// ForLLM and ForUser should both be valid UTF-8 after truncation.
	if !utf8.ValidString(result.ForLLM) {
		t.Errorf("ForLLM output is not valid UTF-8 after truncation: %q", result.ForLLM)
	}
	if !utf8.ValidString(result.ForUser) {
		t.Errorf("ForUser output is not valid UTF-8 after truncation: %q", result.ForUser)
	}
}

// TestShellTool_RestrictToWorkspace verifies workspace restriction
func TestShellTool_RestrictToWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewExecTool(tmpDir, false)
	tool.SetRestrictToWorkspace(true)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "cat ../../etc/passwd",
	}

	result := tool.Execute(ctx, args)

	// Path traversal should be blocked
	if !result.IsError {
		t.Errorf("Expected path traversal to be blocked with restrictToWorkspace=true")
	}

	if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForUser, "blocked") {
		t.Errorf("Expected 'blocked' message for path traversal, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

// newTestKeyring builds an in-memory (file-backed, temp dir) keyring service for tests.
func newTestKeyring(t *testing.T) *keyring.Service {
	t.Helper()
	dir := t.TempDir()
	return keyring.NewService(keyring.ServiceConfig{
		Enabled:      true,
		VaultPath:    filepath.Join(dir, "keyring.enc"),
		Backend:      keyring.BackendFile,
		AuditLogSize: 100,
		LeleDir:      dir,
	})
}

// TestShellTool_SecretSubstitution verifies {{SECRET:name}} is replaced with the
// keyring value at run time.
func TestShellTool_SecretSubstitution(t *testing.T) {
	svc := newTestKeyring(t)
	if err := svc.SetFromUI("test.token", "s3cr3t-value", "test", nil, nil, "tui"); err != nil {
		t.Fatalf("failed to set secret: %v", err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "session-1")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:test.token}}",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "s3cr3t-value") {
		t.Errorf("expected substituted value in output, got: %s", result.ForUser)
	}
	if strings.Contains(result.ForUser, "{{SECRET:") {
		t.Errorf("placeholder should have been substituted, got: %s", result.ForUser)
	}
}

// TestShellTool_SecretSubstitution_Multiple verifies several placeholders in one command.
func TestShellTool_SecretSubstitution_Multiple(t *testing.T) {
	svc := newTestKeyring(t)
	if err := svc.SetFromUI("a", "AAA", "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetFromUI("b", "BBB", "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:a}}-{{SECRET:b}}",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "AAA-BBB") {
		t.Errorf("expected 'AAA-BBB' in output, got: %s", result.ForUser)
	}
}

// TestShellTool_SecretSubstitution_Unknown verifies an unknown secret aborts the command.
func TestShellTool_SecretSubstitution_Unknown(t *testing.T) {
	svc := newTestKeyring(t)

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:does.not.exist}}",
	})

	if !result.IsError {
		t.Fatalf("expected error for unknown secret, got success: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "does.not.exist") {
		t.Errorf("expected error to mention secret name, got: %s", result.ForLLM)
	}
}

// TestShellTool_SecretSubstitution_Disabled verifies substitution can be turned off,
// leaving the placeholder literal.
func TestShellTool_SecretSubstitution_Disabled(t *testing.T) {
	svc := newTestKeyring(t)
	if err := svc.SetFromUI("test.token", "s3cr3t-value", "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)
	tool.SetSecretSubstitution(false)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo '{{SECRET:test.token}}'",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "{{SECRET:test.token}}") {
		t.Errorf("expected literal placeholder when disabled, got: %s", result.ForUser)
	}
	if strings.Contains(result.ForUser, "s3cr3t-value") {
		t.Errorf("value should NOT be substituted when disabled, got: %s", result.ForUser)
	}
}

// TestShellTool_SecretSubstitution_NoKeyring verifies commands without a keyring
// attached run unchanged (placeholders left literal, no crash).
func TestShellTool_SecretSubstitution_NoKeyring(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo '{{SECRET:x}}'",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "{{SECRET:x}}") {
		t.Errorf("expected literal placeholder with no keyring, got: %s", result.ForUser)
	}
}

// TestShellTool_SecretSubstitution_ScopeDenied verifies a secret scoped to another
// agent cannot be resolved by this agent.
func TestShellTool_SecretSubstitution_ScopeDenied(t *testing.T) {
	svc := newTestKeyring(t)
	// Scoped to "other-agent" only.
	if err := svc.SetFromUI("restricted", "hidden", "", nil, []string{"other-agent"}, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:restricted}}",
	})

	if !result.IsError {
		t.Fatalf("expected access-denied error, got success: %s", result.ForLLM)
	}
	if strings.Contains(result.ForUser, "hidden") {
		t.Errorf("secret value must not leak on denied access, got: %s", result.ForUser)
	}
}
