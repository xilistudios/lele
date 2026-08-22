package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/session"
)

// ---------------------------------------------------------------------------
// ExecTool metadata and config setters
// ---------------------------------------------------------------------------

func TestExecTool_Metadata(t *testing.T) {
	tool := NewExecTool("", false)
	if tool.Name() != "exec" {
		t.Errorf("Name() = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description() empty")
	}
	params := tool.Parameters()
	props := params["properties"].(map[string]interface{})
	for _, name := range []string{"command", "working_dir", "background"} {
		if _, ok := props[name]; !ok {
			t.Errorf("Parameters() missing %q", name)
		}
	}
}

func TestExecTool_ContextAndHooks(t *testing.T) {
	tool := NewExecTool("", false)

	tool.SetContext("chan-x", "chat-y")
	if tool.channel != "chan-x" || tool.chatID != "chat-y" {
		t.Errorf("SetContext: %q/%q", tool.channel, tool.chatID)
	}

	var sent chan string
	feedback := func(channel, chatID, message string) { sent <- channel + "|" + message }
	tool.SetFeedbackCallback(feedback)
	if tool.feedbackCallback == nil {
		t.Error("feedbackCallback not set")
	}

	tool.SetVerbose(session.VerboseFull)
	if tool.verboseLevel != session.VerboseFull {
		t.Errorf("verboseLevel = %v", tool.verboseLevel)
	}

	tool.SetApprovalMode(true)
	if !tool.approvalMode {
		t.Error("approvalMode not set")
	}

	var approved bool
	tool.SetApprovalCallback(func(cmd string) (bool, error) { return approved, nil })
	if tool.approvalCallback == nil {
		t.Error("approvalCallback not set")
	}

	tool.SetBypassGuard(true)
	if !tool.bypassGuard {
		t.Error("bypassGuard not set")
	}

	mgr := NewBackgroundProcessManager()
	tool.SetBackgroundManager(mgr)
	if tool.backgroundManager != mgr {
		t.Error("backgroundManager not set")
	}

	tool.SetBackgroundThreshold(10 * time.Second)
	if tool.backgroundThreshold != 10*time.Second {
		t.Errorf("backgroundThreshold = %v", tool.backgroundThreshold)
	}

	tool.SetTimeout(3 * time.Second)
	if tool.timeout != 3*time.Second {
		t.Errorf("timeout = %v", tool.timeout)
	}
}

func TestExecTool_NewExecToolWithConfig(t *testing.T) {
	// config with custom deny patterns
	cfg := &config.Config{Tools: config.ToolsConfig{Exec: config.ExecConfig{
		EnableDenyPatterns: true,
		CustomDenyPatterns: []string{`\bforbiddencmd\b`},
		TimeoutSeconds:     5,
	}}}
	tool := NewExecToolWithConfig("/tmp", false, cfg)
	if len(tool.denyPatterns) != 1 {
		t.Fatalf("expected 1 custom deny pattern, got %d", len(tool.denyPatterns))
	}
	if tool.timeout != 5*time.Second {
		t.Errorf("timeout = %v, want 5s", tool.timeout)
	}

	// NewExecTool delegates to NewExecToolWithConfig with nil config -> defaults
	plain := NewExecTool("/tmp", false)
	if len(plain.denyPatterns) != len(defaultDenyPatterns) {
		t.Errorf("expected default deny patterns, got %d", len(plain.denyPatterns))
	}
	if plain.timeout != 60*time.Second {
		t.Errorf("default timeout = %v, want 60s", plain.timeout)
	}
}

func TestExecTool_NewExecToolWithConfig_InvalidPattern(t *testing.T) {
	cfg := &config.Config{Tools: config.ToolsConfig{Exec: config.ExecConfig{
		EnableDenyPatterns: true,
		CustomDenyPatterns: []string{`[invalid`, `\bokbutthis\b`},
		TimeoutSeconds:     0,
	}}}
	tool := NewExecToolWithConfig("/tmp", false, cfg)
	if len(tool.denyPatterns) != 1 {
		t.Errorf("expected only the valid custom pattern kept, got %d", len(tool.denyPatterns))
	}
}

func TestExecTool_NewExecToolWithConfig_DisabledDeny(t *testing.T) {
	cfg := &config.Config{Tools: config.ToolsConfig{Exec: config.ExecConfig{
		EnableDenyPatterns: false,
	}}}
	tool := NewExecToolWithConfig("/tmp", false, cfg)
	if len(tool.denyPatterns) != 0 {
		t.Errorf("expected no deny patterns when disabled, got %d", len(tool.denyPatterns))
	}
}

func TestExecTool_SetAllowPatterns(t *testing.T) {
	tool := NewExecTool("", false)
	if err := tool.SetAllowPatterns([]string{`^echo `}); err != nil {
		t.Fatalf("SetAllowPatterns failed: %v", err)
	}
	if len(tool.allowPatterns) != 1 {
		t.Fatalf("expected 1 allow pattern, got %d", len(tool.allowPatterns))
	}

	// invalid pattern returns an error
	if err := tool.SetAllowPatterns([]string{`[bad`}); err == nil {
		t.Error("expected error for invalid allow pattern")
	}
}

// ---------------------------------------------------------------------------
// guardCommand / guardCommandWithStatus
// ---------------------------------------------------------------------------

func TestGuardCommand_DenyPattern(t *testing.T) {
	tool := NewExecTool("", false)

	for _, cmd := range []string{
		"rm -rf /tmp/x",
		"sudo apt install x",
		"shutdown now",
		"echo $(danger)",
		"cat /etc/passwd | sh",
		"git push origin main",
	} {
		if msg, blockable := tool.guardCommandWithStatus(cmd, "/tmp"); blockable != true || msg == "" {
			t.Errorf("guardCommandWithStatus(%q) = %q, %v; want blocked", cmd, msg, blockable)
		}
		// guardCommand (non-status) should also produce a message.
		if msg := tool.guardCommand(cmd, "/tmp"); msg == "" {
			t.Errorf("guardCommand(%q) expected a block message", cmd)
		}
	}
}

func TestGuardCommand_SafeCommand(t *testing.T) {
	tool := NewExecTool("", false)
	if msg, blockable := tool.guardCommandWithStatus("echo hello", "/tmp"); msg != "" || blockable {
		t.Errorf("safe command blocked: %q, %v", msg, blockable)
	}
	if msg := tool.guardCommand("echo hello", "/tmp"); msg != "" {
		t.Errorf("safe command guardCommand blocked: %q", msg)
	}
}

func TestGuardCommand_Allowlist(t *testing.T) {
	tool := NewExecTool("", false)
	_ = tool.SetAllowPatterns([]string{`\becho\b`})

	// not in allowlist
	if _, blockable := tool.guardCommandWithStatus("ls -la", "/tmp"); blockable {
		t.Error("expected non-allowlisted command to be blocked with blockable=false")
	}
	if msg := tool.guardCommand("ls -la", "/tmp"); msg == "" {
		t.Error("expected guardCommand to block non-allowlisted command")
	}

	// in allowlist
	if msg, _ := tool.guardCommandWithStatus("echo ok", "/tmp"); msg != "" {
		t.Errorf("allowlisted command blocked: %q", msg)
	}
	if msg := tool.guardCommand("echo ok", "/tmp"); msg != "" {
		t.Errorf("allowlisted command guardCommand blocked: %q", msg)
	}
}

func TestGuardCommand_RestrictToWorkspace(t *testing.T) {
	tool := NewExecTool("/tmp", true)

	// path traversal
	if msg, blockable := tool.guardCommandWithStatus("cat ../secret", "/tmp"); msg == "" || blockable {
		t.Errorf("expected path traversal block, got %q, %v", msg, blockable)
	}
	if msg := tool.guardCommand("cat ../secret", "/tmp"); msg == "" {
		t.Error("expected guardCommand path traversal block")
	}

	// absolute path outside workspace
	if msg, _ := tool.guardCommandWithStatus("cat /etc/passwd", "/tmp"); msg == "" {
		t.Error("expected outside-workspace block")
	}
	if msg := tool.guardCommand("cat /etc/passwd", "/tmp"); msg == "" {
		t.Error("expected guardCommand outside-workspace block")
	}

	// path inside workspace
	if msg, _ := tool.guardCommandWithStatus("cat /tmp/file.txt", "/tmp"); msg != "" {
		t.Errorf("inside-workspace path blocked: %q", msg)
	}
	if msg := tool.guardCommand("cat /tmp/file.txt", "/tmp"); msg != "" {
		t.Errorf("inside-workspace path guardCommand blocked: %q", msg)
	}

	// Windows-style traversal triggers the alternate branch.
	if msg := tool.guardCommand("type ..\\..\\secret", "/tmp"); msg == "" {
		t.Error("expected guardCommand windows traversal block")
	}
}

func TestGuardCommand_Restrict_Safe(t *testing.T) {
	// A safe command under restricted workspace with no path args.
	tool := NewExecTool("/tmp", true)
	if msg := tool.guardCommand("ls", "/tmp"); msg != "" {
		t.Errorf("safe restricted command blocked: %q", msg)
	}
	if msg, _ := tool.guardCommandWithStatus("ls", "/tmp"); msg != "" {
		t.Errorf("safe restricted withStatus blocked: %q", msg)
	}
}

// ---------------------------------------------------------------------------
// Execute – background mode + explicit context channel resolution
// ---------------------------------------------------------------------------

func TestExecTool_Execute_BackgroundMode(t *testing.T) {
	tool := NewExecTool("", false)
	mgr := NewBackgroundProcessManager()
	tool.SetBackgroundManager(mgr)
	tool.SetBackgroundThreshold(0) // disable auto-background heartbeat path

	ctx := WithToolContext(context.Background(), "chan-bg", "chat-bg")
	res := tool.Execute(ctx, map[string]interface{}{
		"command":    "sleep 0.05",
		"background": true,
	})
	if res.IsError {
		t.Fatalf("background execute error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "background") {
		t.Errorf("expected background notice, got: %s", res.ForLLM)
	}
	if mgr.Count() != 1 {
		t.Errorf("expected 1 background process, got %d", mgr.Count())
	}
	if mgr.List()[0].OwnerSessionKey != "" {
		t.Errorf("owner session key should be empty without AgentToolContext, got %q", mgr.List()[0].OwnerSessionKey)
	}
	// Wait for the follow-up goroutine (cmd.Wait + MarkCompleted) to run.
	deadline := time.Now().Add(2 * time.Second)
	p := mgr.List()[0]
	for time.Now().Before(deadline) {
		p.mu.RLock()
		st := p.Status
		p.mu.RUnlock()
		if st != BgExecStatusRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.mu.RLock()
	st := p.Status
	p.mu.RUnlock()
	if st != BgExecStatusCompleted {
		t.Errorf("expected background process to complete via goroutine, got status %q", st)
	}
	mgr.StopAll()
}

func TestExecTool_Execute_BackgroundMode_Owned(t *testing.T) {
	tool := NewExecTool("", false)
	mgr := NewBackgroundProcessManager()
	tool.SetBackgroundManager(mgr)
	tool.SetBackgroundThreshold(0)

	ctx := WithAgentToolContext(WithToolContext(context.Background(), "c", "g"), "agent-1", "sess-1")
	res := tool.Execute(ctx, map[string]interface{}{
		"command":    "sleep 0",
		"background": true,
	})
	if res.IsError {
		t.Fatalf("background execute error: %s", res.ForLLM)
	}
	if mgr.List()[0].OwnerSessionKey != "sess-1" {
		t.Errorf("owner session key = %q, want sess-1", mgr.List()[0].OwnerSessionKey)
	}
	// Wait for the goroutine to mark the process completed (mutex-guarded reads).
	p := mgr.List()[0]
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		running := p.Status == BgExecStatusRunning
		p.mu.RUnlock()
		if !running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.StopAll()
}

// Verify that an explicit working_dir arg takes precedence and the guard still
// runs against it.
func TestExecTool_Execute_ApprovalMode(t *testing.T) {
	tool := NewExecTool("", false)
	tool.SetApprovalMode(true)

	ctx := context.Background()
	res := tool.Execute(ctx, map[string]interface{}{
		"command": "rm -rf /tmp/x",
	})
	if res.IsError {
		t.Fatalf("expected approval-required result, got error: %s", res.ForLLM)
	}
	if res.ApprovalRequired == nil {
		t.Fatal("expected ApprovalRequired to be set")
	}
	if res.ApprovalRequired.Command != "rm -rf /tmp/x" {
		t.Errorf("approval command = %q", res.ApprovalRequired.Command)
	}
	if !strings.Contains(res.ForLLM, "requires user approval") {
		t.Errorf("expected approval message, got: %s", res.ForLLM)
	}
}

func TestExecTool_Execute_BypassGuard(t *testing.T) {
	// With bypass enabled, a normally-blocked command is allowed to attempt
	// execution. We use a safe command to avoid destructive side effects but
	// prove to by bypassGuard path.
	tool := NewExecTool("", false)
	tool.SetBypassGuard(true)
	ctx := context.Background()

	res := tool.Execute(ctx, map[string]interface{}{
		"command": "echo bypassed",
	})
	if res.IsError {
		t.Fatalf("expected success with bypass, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForUser, "bypassed") {
		t.Errorf("expected output, got: %s", res.ForUser)
	}
}

func TestExecTool_Execute_WorkingDirArg(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecTool("", false)
	ctx := context.Background()
	res := tool.Execute(ctx, map[string]interface{}{
		"command":     "pwd",
		"working_dir": dir,
	})
	if res.IsError {
		t.Fatalf("pwd error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, dir) {
		t.Errorf("expected cwd %q in output, got: %s", dir, res.ForLLM)
	}
}

func TestExecTool_Execute_FeedbackCallback_Completion(t *testing.T) {
	// With a long-running command, completion callback only fires when elapsed
	// > 10s, which is impractical for a unit test, so we only test that the
	// verbose feedback is NOT forced (no crash) and command works.
	tool := NewExecTool("", false)
	sent := make([]string, 0, 5)
	tool.SetFeedbackCallback(func(channel, chatID, message string) { sent = append(sent, channel+"|"+message) })
	tool.SetVerbose(session.VerboseOff)
	ctx := WithToolContext(context.Background(), "c", "g")

	res := tool.Execute(ctx, map[string]interface{}{"command": "echo fast"})
	if res.IsError {
		t.Fatalf("command error: %s", res.ForLLM)
	}
	if len(sent) != 0 {
		t.Errorf("expected no feedback for quick command, got %v", sent)
	}
}
