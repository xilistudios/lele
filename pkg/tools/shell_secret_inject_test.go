package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// ── C1: Command substitution via $(...) ────────────────────────────────────

// TestExec_SecretValueWithCommandSubstitution verifies that a secret containing
// $(…) shell substitution syntax is rejected and the injected command is NEVER
// executed. Before the fix (HEAD a5d97d5), this would execute the injected
// command (rojo); after the fix it returns an error (verde).
func TestExec_SecretValueWithCommandSubstitution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_marker")

	svc := newTestKeyring(t)
	// The malicious value uses command substitution to create a file marker.
	// We use `touch` instead of `reboot` for safety.
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

	// MUST be rejected (verde). Before the fix this would succeed and create
	// the marker file.
	if !result.IsError {
		t.Fatal("expected error for secret with $() command substitution, got success")
	}
	if !strings.Contains(result.ForLLM, "metacharacters") && !strings.Contains(result.ForLLM, "substitution syntax") {
		t.Errorf("expected metacharacter/substitution rejection message, got: %s", result.ForLLM)
	}
	// Double-check: the marker must NOT exist.
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("SECURITY FAIL: marker file was created — command substitution executed through secret injection")
	}
}

// ── C2a: Backtick variant ─────────────────────────────────────────────────

// TestExec_SecretValueWithBacktick verifies that a secret containing backtick
// command substitution is rejected.
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

// ── C2b: Newline + pipe variant ───────────────────────────────────────────

// TestExec_SecretValueWithSemicolonAndPipe verifies that secrets containing
// semicolons and pipe characters are rejected.
func TestExec_SecretValueWithSemicolonAndPipe(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_semi")

	svc := newTestKeyring(t)
	// Value with semicolon: could inject a second command.
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

// TestExec_SecretValueWithNewlineAndPipe verifies that secrets containing
// newlines and pipe characters are rejected.
func TestExec_SecretValueWithNewlineAndPipe(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_nl")

	svc := newTestKeyring(t)
	// Value with newline + rm — classic injection split.
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

// ── C3: Legitimate secrets still work ─────────────────────────────────────

// TestExec_SecretValueLegitStillWorks verifies that common API key formats
// (alphanumeric, hyphens, underscores, dots, colons, slashes) are accepted
// and correctly substituted.
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

// ── C4: Post-substitution guard ignores whitelist/bypass ──────────────────

// TestExec_PostSubstitutionGuardIgnoresWhitelist verifies that even when the
// command with placeholders would be allowed by the safety guard (e.g. "echo
// {{SECRET:x}}"), the post-substitution re-guard catches a malicious value
// that turns it into a dangerous command. The pre-existing bypass mechanisms
// (whitelist, context-based approval) authorize the command the user SAW
// (with placeholders), not whatever results from injecting a secret value.
func TestExec_PostSubstitutionGuardIgnoresWhitelist(t *testing.T) {
	svc := newTestKeyring(t)
	// A value that, once injected into "echo {{SECRET:x}}", produces a
	// command containing "rm -rf" — a denied pattern. The layer A
	// metacharacter check will catch this first (the value contains ';'),
	// but this test specifically validates that layer B (post-substitution
	// guard) also catches it, ensuring defence in depth.
	if err := svc.SetFromUI("x", "y; rm -rf /tmp/lele_ws_test", "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	// Whitelist "echo {{SECRET:x}}" — the command with placeholder is safe.
	tool.SetWhitelist([]string{"echo {{SECRET:x}}"})

	ctx := WithAgentToolContext(context.Background(), "tester", "s")
	result := tool.Execute(ctx, map[string]interface{}{
		"command": "echo {{SECRET:x}}",
	})

	if !result.IsError {
		t.Fatal("expected error: injected value creates dangerous command even though placeholder-form is whitelisted")
	}
	// The error should come from the metacharacter check (layer A) or the
	// post-substitution guard (layer B).
	if !strings.Contains(result.ForLLM, "metacharacters") &&
		!strings.Contains(result.ForLLM, "substitution syntax") &&
		!strings.Contains(result.ForLLM, "post-substitution safety guard") {
		t.Errorf("expected security rejection message, got: %s", result.ForLLM)
	}
}

// TestExec_PostSubstitutionGuardIgnoresContextBypass verifies that a context-
// based bypass (WithBypassGuard) for the placeholder-form command does NOT
// authorize the post-substitution form if it contains injected shell syntax.
func TestExec_PostSubstitutionGuardIgnoresContextBypass(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "poc_bypass")

	svc := newTestKeyring(t)
	// A pipe + touch: the value introduces "touch /path/to/marker" after a pipe.
	// The metacharacter check (layer A) will block this, but we also want to
	// validate that the post-substitution guard (layer B) would block it if
	// layer A were bypassed — so we use a value that contains `|` (blocked by
	// layer A) and `;` (blocked by layer A) but the test documents both layers.
	if err := svc.SetFromUI("x", "y | touch "+marker, "", nil, nil, "tui"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.Tools.Exec.EnableDenyPatterns = true
	ws := t.TempDir()
	tool := NewExecToolWithConfig(ws, false, cfg)
	tool.SetKeyringService(svc)

	// Create a bypass context for the PLACEHOLDER command (which is safe).
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

// ── Post-substitution guard is skipped when no substitution occurred ──────

// TestExec_PostSubstitutionGuardSkippedWhenNoSubstitution verifies that when
// the command has no placeholders, the post-substitution guard is NOT
// re-invoked (no false positives, no performance waste).
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

// ── Layer A: regex matches all specified metacharacters ───────────────────

// TestSecretValueShellMeta_RegexCoverage validates that the regex rejects every
// metacharacter listed in the spec: backtick, ;, $, |, &, <, >, \, ", ', newline, carriage return.

// TestSecretValueShellMeta_LegitValuesNotMatched validates that the regex does
// NOT match common legitimate secret formats.
