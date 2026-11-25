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
	const dangerousValue = "rm -rf /tmp/lele_guard_whitelist_probe"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("x", dangerousValue, "", nil, nil, "tui"); err != nil {
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
	if !strings.Contains(result.ForLLM, "post-substitution safety guard") {
		t.Errorf("expected the post-substitution guard to block the resolved value, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, dangerousValue) {
		t.Errorf("SECURITY FAIL: rejected value echoed back in the error: %s", result.ForLLM)
	}
}

func TestExec_PostSubstitutionGuardIgnoresContextBypass(t *testing.T) {
	const dangerousValue = "rm -rf /tmp/lele_guard_bypass_probe"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("x", dangerousValue, "", nil, nil, "tui"); err != nil {
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
	if !strings.Contains(result.ForLLM, "post-substitution safety guard") {
		t.Errorf("expected the post-substitution guard to block the resolved value, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, dangerousValue) {
		t.Errorf("SECURITY FAIL: rejected value echoed back in the error: %s", result.ForLLM)
	}
}

func TestExec_PostSubstitutionGuardBlocksResolvedValue(t *testing.T) {
	const dangerousValue = "rm -rf /tmp/lele_guard_probe_463"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", dangerousValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	if msg, _ := tool.guardCommandWithStatus("echo "+dangerousValue, ws); msg == "" {
		t.Fatalf("probe precondition failed: %q is not a deny-pattern match, so this cell cannot pin the guard", dangerousValue)
	}

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	resolved, err := tool.substituteSecrets(ctx, "echo {{SECRET:tok}}")
	if err != nil {
		t.Fatalf("substituteSecrets failed: %v", err)
	}
	if want := "echo ${LELE_SECRET_0}"; resolved.script != want {
		t.Errorf("executed script = %q, want the environment reference %q", resolved.script, want)
	}
	if want := "echo " + dangerousValue; resolved.expanded != want {
		t.Errorf("guard view = %q, want the value-inlined form %q", resolved.expanded, want)
	}

	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:tok}}",
	})
	if !result.IsError {
		t.Fatalf("expected the resolved value to be blocked by the post-substitution guard, got success: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "post-substitution safety guard") {
		t.Errorf("expected the post-substitution guard to be the rejection, got: %s", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "metacharacters") {
		t.Errorf("value %q has no metacharacters, so the guard (not the metacharacter screen) must reject it: %s", dangerousValue, result.ForLLM)
	}
	if strings.Contains(result.ForLLM, dangerousValue) {
		t.Errorf("SECURITY FAIL: rejected value echoed back in the error: %s", result.ForLLM)
	}
}

func TestGuardCommand_SecretEnvReferenceExemptionIsNarrow(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	tool := NewExecToolWithConfig(t.TempDir(), false, cfg)

	allowed := []string{
		"echo ${LELE_SECRET_0}",
		"echo ${lele_secret_12}",
		"echo ${env:LELE_SECRET_3}",
		"curl -H 'X-Key: '${LELE_SECRET_0} https://example.test",
	}
	for _, command := range allowed {
		if msg := tool.guardCommand(command, ""); msg != "" {
			t.Errorf("emitted environment reference %q must not trip the guard, got: %s", command, msg)
		}
	}

	denied := []string{
		"echo ${HOME}",
		"echo ${PATH}",
		"echo ${env:HOME}",
		"echo ${SECRET}",
		"echo ${LELE_SECRET_}",
		"echo ${LELE_SECRET_0:-$(id)}",
		"echo ${LELE_SECRET_0}${HOME}",
		"echo ${OTHER_SECRET_0}",
	}
	for _, command := range denied {
		if msg := tool.guardCommand(command, ""); msg == "" {
			t.Errorf("guard must keep rejecting parameter expansion %q; only ${LELE_SECRET_<digits>} is exempt", command)
		}
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
	if !strings.Contains(argv, "${LELE_SECRET_0}") {
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

	wantScript := "echo ${LELE_SECRET_0}:${LELE_SECRET_1}:${LELE_SECRET_0}"
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
	if got, want := secretEnvReference(0, false), "${LELE_SECRET_0}"; got != want {
		t.Errorf("POSIX reference = %q, want %q", got, want)
	}
	if got, want := secretEnvReference(3, true), "${env:LELE_SECRET_3}"; got != want {
		t.Errorf("PowerShell reference = %q, want %q", got, want)
	}
	if got, want := secretEnvReference(12, false), "${LELE_SECRET_12}"; got != want {
		t.Errorf("POSIX reference for a multi-digit index = %q, want %q", got, want)
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
func TestExec_SecretReferenceSuffixIntact(t *testing.T) {
	const secretValue = "suffix-value-42"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")

	cases := []struct {
		name    string
		command string
		want    string
	}{
		{"unquoted", `printf '[%s]' a{{SECRET:tok}}b`, "[a" + secretValue + "b]"},
		{"double_quoted", `printf '[%s]' "a{{SECRET:tok}}b"`, "[a" + secretValue + "b]"},
		{"unquoted_suffix", `printf '[%s]' {{SECRET:tok}}_suffix`, "[" + secretValue + "_suffix]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := tool.Execute(ctx, map[string]interface{}{"command": tc.command})
			if result.IsError {
				t.Fatalf("exec failed: %s", result.ForLLM)
			}
			if got := strings.TrimSpace(result.ForLLM); got != tc.want {
				t.Errorf("child printed %q, want %q: a word character after the reference must not extend the variable name", got, tc.want)
			}
		})
	}
}

func TestExec_QuotedHeredocPlaceholderFails(t *testing.T) {
	const secretValue = "heredoc-quoted-secret"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		command string
	}{
		{"single_quoted_delimiter", "cat > out.txt <<'EOF'\nvalue={{SECRET:tok}}\nEOF"},
		{"double_quoted_delimiter", "cat > out.txt <<\"EOF\"\nvalue={{SECRET:tok}}\nEOF"},
		{"escaped_delimiter", "cat > out.txt <<\\EOF\nvalue={{SECRET:tok}}\nEOF"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tool := NewExecTool(dir, false)
			tool.SetKeyringService(svc)

			ctx := WithAgentToolContext(context.Background(), "tester", "s")
			result := tool.Execute(ctx, map[string]interface{}{"command": tc.command})

			if !result.IsError {
				t.Fatalf("expected an actionable error for a placeholder inside a quoted heredoc, got success: %s", result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "heredoc") {
				t.Errorf("error must explain the quoted heredoc limitation, got: %s", result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, "{{SECRET:tok}}") {
				t.Errorf("error must name the offending placeholder, got: %s", result.ForLLM)
			}
			if strings.Contains(result.ForLLM, secretValue) || strings.Contains(result.ForUser, secretValue) {
				t.Errorf("SECURITY FAIL: secret value leaked into the error, got: %s", result.ForLLM)
			}

			if _, err := os.Stat(filepath.Join(dir, "out.txt")); err == nil {
				t.Fatalf("nothing must be written when the placeholder cannot be expanded")
			}
		})
	}
}

func TestExec_PlainHeredocExpands(t *testing.T) {
	const secretValue = "plain-heredoc-value"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	tool := NewExecTool(dir, false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	command := "cat > out.txt <<EOT\nvalue={{SECRET:tok}}\nEOT\ncat out.txt"

	resolved, err := tool.substituteSecrets(ctx, command)
	if err != nil {
		t.Fatalf("substituteSecrets failed: %v", err)
	}
	if !strings.Contains(resolved.script, "${LELE_SECRET_0}") {
		t.Errorf("an unquoted heredoc body must carry the environment reference, got: %q", resolved.script)
	}
	if strings.Contains(resolved.script, secretValue) {
		t.Errorf("SECURITY FAIL: resolved script carries the value: %q", resolved.script)
	}

	result := tool.Execute(ctx, map[string]interface{}{"command": command})
	if result.IsError {
		t.Fatalf("exec failed: %s", result.ForLLM)
	}

	content, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	if err != nil {
		t.Fatalf("reading heredoc output failed: %v", err)
	}
	if want := "value=" + secretValue + "\n"; string(content) != want {
		t.Errorf("heredoc body = %q, want %q: an unquoted delimiter expands variables", string(content), want)
	}
}

func TestExec_CommentApostropheIsNotSingleQuotes(t *testing.T) {
	const secretValue = "comment-secret-value"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo a # don't expand anything here\nprintf '%s' {{SECRET:tok}}",
	})
	if result.IsError {
		t.Fatalf("an apostrophe inside a comment must not be treated as a quote, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, secretValue) {
		t.Errorf("expected the secret value from the second line, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "a") {
		t.Errorf("expected the first line output, got: %s", result.ForLLM)
	}
}

func TestExec_PowerShellScannerHasNoPOSIXBackslashEscape(t *testing.T) {
	const secretValue = "pwsh-scanner-value"

	svc := newTestKeyring(t)
	if err := svc.SetFromUI("tok", secretValue, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	tool := NewExecTool("", false)
	tool.SetKeyringService(svc)

	ctx := WithAgentToolContext(context.Background(), "tester", "s")

	posix, err := tool.substituteSecretsForShell(ctx, `echo \{{SECRET:tok}}`, false)
	if err != nil {
		t.Fatalf("substituteSecretsForShell(posix) failed: %v", err)
	}
	if want := `echo \{{SECRET:tok}}`; posix.script != want {
		t.Errorf("POSIX script = %q, want the escaped literal %q", posix.script, want)
	}
	if len(posix.env) != 0 {
		t.Errorf("a backslash-escaped placeholder must not export an environment variable, got %v", posix.env)
	}

	pwsh, err := tool.substituteSecretsForShell(ctx, `Write-Output \{{SECRET:tok}}`, true)
	if err != nil {
		t.Fatalf("substituteSecretsForShell(powershell) failed: %v", err)
	}
	if want := `Write-Output \${env:LELE_SECRET_0}`; pwsh.script != want {
		t.Errorf("PowerShell script = %q, want %q: a backslash is not an escape character in PowerShell", pwsh.script, want)
	}
	if !slices.Equal(pwsh.env, []string{"LELE_SECRET_0=" + secretValue}) {
		t.Errorf("PowerShell env = %v, want the resolved secret", pwsh.env)
	}
}

func TestExec_BuildExecEnvDropsAllInheritedSecretVariables(t *testing.T) {
	t.Setenv("LELE_SECRET_1", "stale-inherited")
	t.Setenv("lele_secret_2", "stale-lowercase")
	t.Setenv("UNRELATED_INHERITED_VAR", "kept")

	env := buildExecEnv([]string{"LELE_SECRET_0=fresh-value"})

	var secretEntries []string
	keptUnrelated := false
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && strings.HasPrefix(strings.ToUpper(name), "LELE_SECRET_") {
			secretEntries = append(secretEntries, entry)
		}
		if entry == "UNRELATED_INHERITED_VAR=kept" {
			keptUnrelated = true
		}
	}

	if !slices.Equal(secretEntries, []string{"LELE_SECRET_0=fresh-value"}) {
		t.Errorf("child env secret entries = %v, want only the injected one: every inherited LELE_SECRET_* must be dropped", secretEntries)
	}
	if !keptUnrelated {
		t.Error("unrelated inherited environment variables must be preserved")
	}
}
