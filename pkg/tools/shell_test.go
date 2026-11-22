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

func TestShellTool_Success(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "echo 'hello world'",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success, got IsError=true: %s", result.ForLLM)
	}

	if !strings.Contains(result.ForUser, "hello world") {
		t.Errorf("Expected ForUser to contain 'hello world', got: %s", result.ForUser)
	}

	if !strings.Contains(result.ForLLM, "hello world") {
		t.Errorf("Expected ForLLM to contain 'hello world', got: %s", result.ForLLM)
	}
}

func TestShellTool_Failure(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "ls /nonexistent_directory_12345",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error for failed command, got IsError=false")
	}

	if result.ForUser == "" {
		t.Errorf("Expected ForUser to contain error info, got empty string")
	}

	if !strings.Contains(result.ForLLM, "Exit code") && result.ForUser == "" {
		t.Errorf("Expected ForLLM to contain exit code or error, got: %s", result.ForLLM)
	}
}

func TestShellTool_Timeout(t *testing.T) {
	tool := NewExecTool("", false)
	tool.SetTimeout(100 * time.Millisecond)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "sleep 10",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error for timeout, got IsError=false")
	}

	if !strings.Contains(result.ForLLM, "timed out") && !strings.Contains(result.ForUser, "timed out") {
		t.Errorf("Expected timeout message, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

func TestShellTool_WorkingDir(t *testing.T) {
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

func TestShellTool_DangerousCommand(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "rm -rf /",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected dangerous command to be blocked (IsError=true)")
	}

	if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForUser, "blocked") {
		t.Errorf("Expected 'blocked' message, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

func TestShellTool_MissingCommand(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected error when command is missing")
	}
}

func TestShellTool_StderrCapture(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "sh -c 'echo stdout; echo stderr >&2'",
	}

	result := tool.Execute(ctx, args)

	if !strings.Contains(result.ForLLM, "stdout") {
		t.Errorf("Expected stdout in output, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "stderr") {
		t.Errorf("Expected stderr in output, got: %s", result.ForLLM)
	}
}

func TestShellTool_OutputTruncation(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "seq 1 200000",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Fatalf("command failed: %s", result.ForLLM)
	}

	const maxOutput = 64*1024 + 200
	if len(result.ForLLM) > maxOutput {
		t.Errorf("Expected output <= %d bytes, got %d", maxOutput, len(result.ForLLM))
	}

	if !strings.Contains(result.ForLLM, "bytes truncated") {
		t.Errorf("Expected truncation marker in output, got: %s", result.ForLLM[:200])
	}

	if !strings.HasPrefix(result.ForLLM, "1") {
		t.Errorf("Expected output to start with '1', got: %q", result.ForLLM[:50])
	}

	if !strings.Contains(result.ForLLM, "200000") {
		t.Errorf("Expected output to contain '200000' (tail preservation), got last 200 bytes: %q",
			result.ForLLM[len(result.ForLLM)-200:])
	}
}

func TestShellTool_TruncationKeepsValidUTF8(t *testing.T) {
	tool := NewExecTool("", false)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "seq 1 20000 | while read _; do echo café; done",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Fatalf("command failed: %s", result.ForLLM)
	}

	if !utf8.ValidString(result.ForLLM) {
		t.Errorf("ForLLM output is not valid UTF-8 after truncation (len=%d)", len(result.ForLLM))
	}
	if !utf8.ValidString(result.ForUser) {
		t.Errorf("ForUser output is not valid UTF-8 after truncation (len=%d)", len(result.ForUser))
	}
}

func TestShellTool_RestrictToWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	tool := NewExecTool(tmpDir, false)
	tool.SetRestrictToWorkspace(true)

	ctx := context.Background()
	args := map[string]interface{}{
		"command": "cat ../../etc/passwd",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected path traversal to be blocked with restrictToWorkspace=true")
	}

	if !strings.Contains(result.ForLLM, "blocked") && !strings.Contains(result.ForUser, "blocked") {
		t.Errorf("Expected 'blocked' message for path traversal, got ForLLM: %s, ForUser: %s", result.ForLLM, result.ForUser)
	}
}

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

func TestShellTool_SecretSubstitution_ScopeDenied(t *testing.T) {
	svc := newTestKeyring(t)
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

func TestShellTool_WorkingDirOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()

	tool := NewExecTool(workspace, true)
	tool.SetRestrictToWorkspace(true)

	ctx := context.Background()
	args := map[string]interface{}{
		"command":     "cat test.txt",
		"working_dir": outside,
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected working_dir outside workspace to be blocked")
	}
	if !strings.Contains(result.ForLLM, "access denied") {
		t.Errorf("Expected 'access denied' message, got: %s", result.ForLLM)
	}
}

func TestShellTool_WorkingDirInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	inner := filepath.Join(workspace, "sub")
	if err := os.MkdirAll(inner, 0755); err != nil {
		t.Fatal(err)
	}
	testFile := filepath.Join(inner, "test.txt")
	if err := os.WriteFile(testFile, []byte("inner content"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool(workspace, true)
	tool.SetRestrictToWorkspace(true)

	ctx := context.Background()
	args := map[string]interface{}{
		"command":     "cat test.txt",
		"working_dir": inner,
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected working_dir inside workspace to be allowed, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "inner content") {
		t.Errorf("Expected output from inner dir, got: %s", result.ForUser)
	}
}

func TestShellTool_WorkingDirTraversal(t *testing.T) {
	workspace := t.TempDir()

	tool := NewExecTool(workspace, true)
	tool.SetRestrictToWorkspace(true)

	ctx := context.Background()
	args := map[string]interface{}{
		"command":     "cat /etc/passwd",
		"working_dir": filepath.Join(workspace, "..", ".."),
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Errorf("Expected traversal working_dir to be blocked")
	}
}
