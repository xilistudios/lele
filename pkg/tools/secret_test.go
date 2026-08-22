package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/keyring"
)

func TestSecretTool_Metadata(t *testing.T) {
	tool := NewSecretTool(nil)
	if tool.Name() != "secret" {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("expected description")
	}
	props := tool.Parameters()["properties"].(map[string]interface{})
	for _, k := range []string{"action", "name"} {
		if _, ok := props[k]; !ok {
			t.Errorf("missing param %q", k)
		}
	}
}

// newTestKeyring builds a file-backed keyring service isolated in a temp dir.
func newSecretTestKeyring(t *testing.T) *keyring.Service {
	t.Helper()
	dir := t.TempDir()
	return keyring.NewService(keyring.ServiceConfig{
		Enabled:      true,
		VaultPath:    filepath.Join(dir, "vault.enc"),
		Backend:      keyring.BackendFile,
		AuditLogSize: 100,
		LeleDir:      dir,
	})
}

// TestSecretTool_Execute_NilService verifies the not-available error.
func TestSecretTool_Execute_NilService(t *testing.T) {
	tool := NewSecretTool(nil)
	res := tool.Execute(context.Background(), map[string]interface{}{
		"action": "list",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for nil service")
	}
	if !strings.Contains(res.ForLLM, "not available") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSecretTool_Execute_UnknownAction verifies default action error.
func TestSecretTool_Execute_UnknownAction(t *testing.T) {
	tool := NewSecretTool(newSecretTestKeyring(t))
	res := tool.Execute(context.Background(), map[string]interface{}{
		"action": "bogus",
	})
	if res == nil || !res.IsError {
		t.Fatal("expected error for unknown action")
	}
	if !strings.Contains(res.ForLLM, "unknown action") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSecretTool_Execute_ListEmpty verifies the no-secrets path.
func TestSecretTool_Execute_ListEmpty(t *testing.T) {
	tool := NewSecretTool(newSecretTestKeyring(t))
	res := tool.Execute(context.Background(), map[string]interface{}{
		"action": "list",
	})
	if res == nil || res.IsError {
		t.Fatalf("expected success, got %+v", res)
	}
	if !strings.Contains(res.ForLLM, "No secrets") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSecretTool_Execute_ListAndGet verifies list and get flows with an agent context.
func TestSecretTool_Execute_ListAndGet(t *testing.T) {
	svc := newSecretTestKeyring(t)
	if err := svc.SetFromUI("openai.api_key", "sk-secret-value", "OpenAI key", nil, []string{"coder"}, "tui"); err != nil {
		t.Fatalf("SetFromUI: %v", err)
	}
	if err := svc.SetFromUI("emptykey", "ab", "Tiny", nil, []string{"coder"}, "tui"); err != nil {
		t.Fatalf("SetFromUI: %v", err)
	}

	tool := NewSecretTool(svc)
	ctx := WithAgentToolContext(context.Background(), "coder", "session-1")

	// List shows both secrets.
	res := tool.Execute(ctx, map[string]interface{}{"action": "list"})
	if res == nil || res.IsError {
		t.Fatalf("list failed: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "openai.api_key") {
		t.Fatalf("list ForLLM = %q", res.ForLLM)
	}
	if res.Metadata["sensitive"] != "true" {
		t.Fatalf("expected sensitive metadata, got %v", res.Metadata)
	}

	// Get returns the masked value.
	res = tool.Execute(ctx, map[string]interface{}{"action": "get", "name": "openai.api_key"})
	if res == nil || res.IsError {
		t.Fatalf("get failed: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "sk-****") {
		t.Fatalf("get ForLLM = %q (expected masked value)", res.ForLLM)
	}
	if res.Metadata["sensitive"] != "true" {
		t.Fatalf("expected sensitive metadata on get")
	}
	if res.Metadata["secret_name"] != "openai.api_key" {
		t.Fatalf("secret_name = %q", res.Metadata["secret_name"])
	}
}

// TestSecretTool_Execute_GetShortSecret verifies short-secret mask ("****").
func TestSecretTool_Execute_GetShortSecret(t *testing.T) {
	svc := newSecretTestKeyring(t)
	if err := svc.SetFromUI("ab", "xy", "short", nil, []string{"coder"}, "tui"); err != nil {
		t.Fatalf("SetFromUI: %v", err)
	}
	tool := NewSecretTool(svc)
	ctx := WithAgentToolContext(context.Background(), "coder", "s")
	res := tool.Execute(ctx, map[string]interface{}{"action": "get", "name": "ab"})
	if res == nil || res.IsError {
		t.Fatalf("get failed: %+v", res)
	}
	if !strings.Contains(res.ForLLM, "****") {
		t.Fatalf("ForLLM = %q (expected full mask)", res.ForLLM)
	}
}

// TestSecretTool_Execute_GetMissingName verifies get requires a name.
func TestSecretTool_Execute_GetMissingName(t *testing.T) {
	tool := NewSecretTool(newSecretTestKeyring(t))
	ctx := WithAgentToolContext(context.Background(), "coder", "s")
	res := tool.Execute(ctx, map[string]interface{}{"action": "get"})
	if res == nil || !res.IsError {
		t.Fatal("expected error for get without name")
	}
	if !strings.Contains(res.ForLLM, "name") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSecretTool_Execute_GetNotFound verifies the not-found error.
func TestSecretTool_Execute_GetNotFound(t *testing.T) {
	tool := NewSecretTool(newSecretTestKeyring(t))
	ctx := WithAgentToolContext(context.Background(), "coder", "s")
	res := tool.Execute(ctx, map[string]interface{}{"action": "get", "name": "nope"})
	if res == nil || !res.IsError {
		t.Fatal("expected not-found error")
	}
	if !strings.Contains(res.ForLLM, "failed to get secret") {
		t.Fatalf("ForLLM = %q", res.ForLLM)
	}
}

// TestSecretTool_Execute_GetAccessDenied verifies scope restriction error.
func TestSecretTool_Execute_GetAccessDenied(t *testing.T) {
	svc := newSecretTestKeyring(t)
	if err := svc.SetFromUI("scoped", "value", "d", nil, []string{"other"}, "tui"); err != nil {
		t.Fatalf("SetFromUI: %v", err)
	}
	tool := NewSecretTool(svc)
	ctx := WithAgentToolContext(context.Background(), "coder", "s")
	res := tool.Execute(ctx, map[string]interface{}{"action": "get", "name": "scoped"})
	if res == nil || !res.IsError {
		t.Fatal("expected access denied error")
	}
}

// TestMaskSecretValue verifies the masking helper directly.
func TestMaskSecretValue(t *testing.T) {
	if got := maskSecretValue("abcdef"); got != "abc****" {
		t.Fatalf("maskSecretValue(abcdef) = %q", got)
	}
	if got := maskSecretValue("abc"); got != "****" {
		t.Fatalf("maskSecretValue(abc) = %q", got)
	}
	if got := maskSecretValue("a"); got != "****" {
		t.Fatalf("maskSecretValue(a) = %q", got)
	}
	if got := maskSecretValue(""); got != "****" {
		t.Fatalf("maskSecretValue('') = %q", got)
	}
}
