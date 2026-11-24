package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/config"
)

func TestExec_SecretValueWithCommandSubstitution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_marker")

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("evil", "x$(touch "+marker+")y", "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:evil}}",
	})

	if !result.IsError {
		t.Fatal("expected error for secret with $() command substitution, got success")
	}
	if !strings.Contains(result.ForLLM, "metacharacters") && !strings.Contains(result.ForLLM, "substitution syntax") {
		t.Errorf("expected metacharacter/substitution rejection message, got: %s", result.ForLLM)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("SECURITY FAIL: marker file was created — command substitution executed through secret injection")
	}
}

func TestExec_SecretValueWithBacktick(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_backtick")

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("evil", "x`touch "+marker+"`y", "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:evil}}",
	})

	if !result.IsError {
		t.Fatal("expected error for secret with backtick command substitution, got success")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("SECURITY FAIL: marker file was created via backtick injection")
	}
}

func TestExec_SecretValueWithSemicolonAndPipe(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_semi")

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("evil", "foo; touch "+marker, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:evil}}",
	})

	if !result.IsError {
		t.Fatal("expected error for secret with semicolon, got success")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("SECURITY FAIL: marker file was created via semicolon injection")
	}
}

func TestExec_SecretValueWithNewlineAndPipe(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_nl")

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("evil", "foo\nrm -rf /tmp/lele_should_not_run\n touch "+marker, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:evil}}",
	})

	if !result.IsError {
		t.Fatal("expected error for secret with newline and pipe, got success")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("SECURITY FAIL: marker file was created via newline injection")
	}
}

func TestExec_SecretValueLegitStillWorks(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"alphanumeric", "abc123"},
		{"github_pat", "ghp_1234567890abcdef1234567890abcdef12345678"},
		{"jwt_prefix", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abc123"},
		{"hyphen_dot_slash", "abc-123_def/456.789"},
		{"colon", "abc:def:123"},
		{"underscore", "sk-proj_abc123-def456"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newTestKeyring(t)
			if err := svc.SetFromUI("key", tc.value, "", nil, nil, "tui"); err != nil {
				t.Fatal(err)
			}

			tool := NewExecTool("", false)
			tool.SetKeyringService(svc)

			ctx := WithAgentToolContext(context.Background(), "tester", "s")
			result := tool.Execute(ctx, map[string]interface{}{
				"command": "echo {{SECRET:key}}",
			})

			if result.IsError {
				t.Fatalf("legitimate secret %q was rejected: %s", tc.value, result.ForLLM)
			}
			if !strings.Contains(result.ForUser, tc.value) {
				t.Errorf("expected substituted value %q in output, got: %s", tc.value, result.ForUser)
			}
			if strings.Contains(result.ForUser, "{{SECRET:") {
				t.Errorf("placeholder should have been substituted, got: %s", result.ForUser)
			}
		})
	}
}

func TestExec_PostSubstitutionGuardIgnoresWhitelist(t *testing.T) {
	svc := newTestKeyring(t)
	if err := svc.SetFromUI("x", "y; rm -rf /tmp/lele_ws_test", "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	tool.SetWhitelist([]string{"echo {{SECRET:x}}"})

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:x}}",
	})

	if !result.IsError {
		t.Fatal("expected error: injected value creates dangerous command even though placeholder-form is whitelisted")
	}
	if !strings.Contains(result.ForLLM, "metacharacters") &&
		!strings.Contains(result.ForLLM, "substitution syntax") &&
		!strings.Contains(result.ForLLM, "post-substitution safety guard") {
		t.Errorf("expected security rejection message, got: %s", result.ForLLM)
	}
}

func TestExec_PostSubstitutionGuardIgnoresContextBypass(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_bypass")

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("x", "y | touch "+marker, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	ctx := WithBypassGuard(
		WithAgentToolContext(context.Background(), "tester", "s"),
		"echo {{SECRET:x}}",
	)

	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:x}}",
	})

	if !result.IsError {
		t.Fatal("expected error: context bypass for placeholder-form must not authorize injected dangerous command")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("SECURITY FAIL: marker file was created despite bypass context applying only to placeholder form")
	}
}

func TestExec_PostSubstitutionGuardSkippedWhenNoSubstitution(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)

	ctx := context.Background()
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo hello",
	})

	if result.IsError {
		t.Fatalf("expected success for clean command with no placeholders, got error: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForUser, "hello") {
		t.Errorf("expected 'hello' in output, got: %s", result.ForUser)
	}
}

func waitForBackgroundOutput(t *testing.T, mgr *BackgroundProcessManager) string {
	t.Helper()

	procs := mgr.List()
	if len(procs) != 1 {
		t.Fatalf("expected exactly 1 registered background process, got %d", len(procs))
	}
	id := procs[0].ID

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p, ok := mgr.Get(id)
		if !ok {
			t.Fatalf("background process %s disappeared", id)
		}
		if p.Status != BgExecStatusRunning {
			return p.Output()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background process %s was still running after 5s", id)
	return ""
}

func TestExecBackgroundStoresPlaceholderCommand(t *testing.T) {
	const secretValue = "bg-s3cr3t-42"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	mgr := NewBackgroundProcessManager()
	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)
	tool.SetBackgroundManager(mgr)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command":    "echo {{SECRET:tok}}",
		"background": true,
	})
	if result.IsError {
		t.Fatalf("background exec failed: %s", result.ForLLM)
	}

	listed := NewListBackgroundExecsTool(mgr).Execute(ctx, map[string]interface{}{"include_completed": true}).ForLLM
	if !strings.Contains(listed, "{{SECRET:tok}}") {
		t.Errorf("background record must keep the placeholder form, got: %s", listed)
	}
	if strings.Contains(listed, secretValue) {
		t.Errorf("SECURITY FAIL: resolved secret leaked into the background record: %s", listed)
	}

	if out := waitForBackgroundOutput(t, mgr); !strings.Contains(out, secretValue) {
		t.Errorf("execution must still receive the resolved secret, got output: %q", out)
	}
}

func TestExecAutoBackgroundStoresPlaceholderCommand(t *testing.T) {
	const secretValue = "autobg-s3cr3t-42"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	mgr := NewBackgroundProcessManager()
	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)
	tool.SetBackgroundManager(mgr)
	tool.SetBackgroundThreshold(200 * time.Millisecond)
	tool.SetTimeout(30 * time.Second)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "sleep 5.5; echo {{SECRET:tok}}",
	})
	if result.IsError {
		t.Fatalf("auto-background exec failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Moved to background") {
		t.Fatalf("expected the command to be moved to the background, got: %s", result.ForLLM)
	}

	listed := NewListBackgroundExecsTool(mgr).Execute(ctx, map[string]interface{}{"include_completed": true}).ForLLM
	if !strings.Contains(listed, "{{SECRET:tok}}") {
		t.Errorf("auto-background record must keep the placeholder form, got: %s", listed)
	}
	if strings.Contains(listed, secretValue) {
		t.Errorf("SECURITY FAIL: resolved secret leaked into the auto-background record: %s", listed)
	}

	if out := waitForBackgroundOutput(t, mgr); !strings.Contains(out, secretValue) {
		t.Errorf("execution must still receive the resolved secret, got output: %q", out)
	}
}
