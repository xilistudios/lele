package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

func TestExec_SecretNotInArgv(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("argv inspection reads /proc/<pid>/cmdline")
	}
	const secretValue = "argv-leak-9f3a1c"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "true {{SECRET:tok}}; tr '\\000' ' ' </proc/$$/cmdline",
	})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}

	argv := result.ForLLM
	if !strings.Contains(argv, "$LELE_SECRET_0") {
		t.Fatalf("child argv must carry the environment reference, got: %q", argv)
	}
	if strings.Contains(argv, secretValue) {
		t.Fatalf("SECURITY FAIL: resolved secret is visible in /proc/<pid>/cmdline: %q", argv)
	}
	if strings.Contains(argv, "{{SECRET:") {
		t.Errorf("executed command must be fully resolved, argv still has the placeholder: %q", argv)
	}
}
func TestExec_SecretEnvValueCorrect(t *testing.T) {
	const secretValue = "env-value-42"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "printf '%s' {{SECRET:tok}}",
	})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}
	if got := strings.TrimSpace(result.ForLLM); got != secretValue {
		t.Errorf("child read %q from the environment, want %q", got, secretValue)
	}
}

func TestExec_SingleQuoteContextFails(t *testing.T) {
	const secretValue = "single-quoted-secret"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo '{{SECRET:tok}}'",
	})

	if !result.IsError {
		t.Fatalf("expected an actionable error for a single-quoted placeholder, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "single quotes") {
		t.Errorf("error must explain the single-quote limitation, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "{{SECRET:tok}}") {
		t.Errorf("error must name the offending placeholder, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, secretValue) || strings.Contains(result.ForUser, secretValue) {
		t.Errorf("SECURITY FAIL: secret value leaked into the error, got: %s", result.ForLLM)
	}
}

func TestExec_DoubleQuoteContextExpands(t *testing.T) {
	const secretValue = "double quoted value"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": `printf '%s' "{{SECRET:tok}}"`,
	})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}
	if got := strings.TrimSpace(result.ForLLM); got != secretValue {
		t.Errorf("child read %q, want %q (double quotes must keep the value as one word)", got, secretValue)
	}
}

func TestExec_MultipleSecretsDistinctVars(t *testing.T) {
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
	resolved, err := tool.substituteSecrets(ctx, "echo {{SECRET:a}}:{{SECRET:b}}:{{SECRET:a}}")
	if err != nil {
		t.Fatalf("substituteSecrets failed: %v", err)
	}

	wantScript := "echo $LELE_SECRET_0:$LELE_SECRET_1:$LELE_SECRET_0"
	if resolved.script != wantScript {
		t.Errorf("resolved script = %q, want %q", resolved.script, wantScript)
	}
	wantEnv := []string{"LELE_SECRET_0=AAA", "LELE_SECRET_1=BBB"}
	if !slices.Equal(resolved.env, wantEnv) {
		t.Errorf("resolved env = %v, want %v", resolved.env, wantEnv)
	}

	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:a}}-{{SECRET:b}}",
	})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "AAA-BBB") {
		t.Errorf("expected 'AAA-BBB' from the child, got: %s", result.ForLLM)
	}
}

func TestExec_MetacharValueStillRejected(t *testing.T) {
	values := []string{
		"foo;touch /tmp/lele_metachar_marker",
		"$(id)",
		"`id`",
		"line1\nline2",
		"a|b",
		"a>b",
		"a'b",
		`a\b`,
		`a"b`,
	}

	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			svc := newTestKeyring(t)
			if err := svc.SetFromUI("tok", value, "", nil, nil, "tui"); err != nil {
				t.Fatal(err)
			}

			tool := NewExecTool("", false)
			tool.SetKeyringService(svc)

			ctx := WithAgentToolContext(context.Background(), "tester", "s")
			result := tool.Execute(ctx, map[string]interface{}{
				"command": "echo {{SECRET:tok}}",
			})

			if !result.IsError {
				t.Fatalf("expected metacharacter rejection for %q, got success: %s", value, result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "metacharacters") &&
				!strings.Contains(result.ForLLM, "substitution syntax") {
				t.Errorf("expected metacharacter rejection message, got: %s", result.ForLLM)
			}
			if strings.Contains(result.ForLLM, value) {
				t.Errorf("SECURITY FAIL: rejected value echoed back in the error: %s", result.ForLLM)
			}
		})
	}
}

func TestExec_TranscriptKeepsPlaceholder(t *testing.T) {
	const secretValue = "transcript-s3cr3t-77"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	mgr := NewBackgroundProcessManager()
	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)
	tool.SetBackgroundManager(mgr)

	args := map[string]interface{}{
		"command":    "echo {{SECRET:tok}}",
		"background": true,
	}
	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, args)
	if result.IsError {
		t.Fatalf("background exec failed: %s", result.ForLLM)
	}

	if got, _ := args["command"].(string); got != "echo {{SECRET:tok}}" {
		t.Errorf("caller args were mutated to %q; the transcript form must keep the placeholder", got)
	}

	listed := NewListBackgroundExecsTool(mgr).Execute(ctx, map[string]interface{}{"include_completed": true}).ForLLM
	if !strings.Contains(listed, "{{SECRET:tok}}") {
		t.Errorf("stored command must keep the placeholder form, got: %s", listed)
	}
	if strings.Contains(listed, secretValue) {
		t.Errorf("SECURITY FAIL: resolved secret leaked into the stored command: %s", listed)
	}

	if out := waitForBackgroundOutput(t, mgr); !strings.Contains(out, secretValue) {
		t.Errorf("execution must still receive the resolved secret, got output: %q", out)
	}
}

func TestExec_PowerShellEnvRef(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires PowerShell as the exec shell (windows)")
	}
	const secretValue = "pwsh-env-ref-value"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "Write-Output {{SECRET:tok}}",
	})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}
	if got := strings.TrimSpace(result.ForLLM); got != secretValue {
		t.Errorf("PowerShell read %q, want %q (must use $env:LELE_SECRET_n)", got, secretValue)
	}
}

func TestExec_SecretEnvReferenceForm(t *testing.T) {
	if got, want := secretEnvReference(0, false), "$LELE_SECRET_0"; got != want {
		t.Errorf("POSIX reference = %q, want %q", got, want)
	}
	if got, want := secretEnvReference(3, true), "$env:LELE_SECRET_3"; got != want {
		t.Errorf("PowerShell reference = %q, want %q", got, want)
	}
}

func TestExec_EscapedPlaceholderStaysLiteral(t *testing.T) {
	const secretValue = "escaped-placeholder-secret"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	resolved, err := tool.substituteSecrets(ctx, `echo \{{SECRET:tok}}`)
	if err != nil {
		t.Fatalf("substituteSecrets failed: %v", err)
	}
	if resolved.script != `echo \{{SECRET:tok}}` {
		t.Errorf("backslash-escaped placeholder must stay literal, got %q", resolved.script)
	}
	if len(resolved.env) != 0 {
		t.Errorf("escaped placeholder must not resolve an env var, got %v", resolved.env)
	}

	result := tool.Execute(ctx, map[string]interface{}{"command": `echo \{{SECRET:tok}}`})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "{{SECRET:tok}}") {
		t.Errorf("expected the literal placeholder in the output, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, secretValue) {
		t.Errorf("SECURITY FAIL: escaped placeholder was resolved to the value: %s", result.ForLLM)
	}
}

func TestExec_SecretEnvShadowsInheritedVariable(t *testing.T) {
	const secretValue = "shadowing-secret"

	t.Setenv("LELE_SECRET_0", "stale-inherited-value")

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "printf '%s' {{SECRET:tok}}",
	})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}
	if got := strings.TrimSpace(result.ForLLM); got != secretValue {
		t.Errorf("child read %q, want %q: an inherited LELE_SECRET_0 must not shadow the injected secret", got, secretValue)
	}
}

func TestExec_BuildExecEnvKeepsOneEntryPerSecret(t *testing.T) {
	t.Setenv("LELE_SECRET_0", "stale-inherited-value")
	t.Setenv("UNRELATED_INHERITED_VAR", "kept")

	env := buildExecEnv([]string{"LELE_SECRET_0=fresh-value"})

	var secretEntries []string
	keptUnrelated := false
	for _, entry := range env {
		if strings.HasPrefix(entry, "LELE_SECRET_0=") {
			secretEntries = append(secretEntries, entry)
		}
		if entry == "UNRELATED_INHERITED_VAR=kept" {
			keptUnrelated = true
		}
	}

	if len(secretEntries) != 1 {
		t.Fatalf("expected exactly one LELE_SECRET_0 entry, got %v", secretEntries)
	}
	if secretEntries[0] != "LELE_SECRET_0=fresh-value" {
		t.Errorf("injected entry = %q, want the resolved value", secretEntries[0])
	}
	if !keptUnrelated {
		t.Error("unrelated inherited environment variables must be preserved")
	}
}
