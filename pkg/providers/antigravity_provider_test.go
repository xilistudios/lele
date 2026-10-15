package providers

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/auth"
)

func TestBuildRequestUsesFunctionFieldsWhenToolCallNameMissing(t *testing.T) {
	p := &AntigravityProvider{}

	messages := []Message{
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID: "call_read_file_123",
				Function: &FunctionCall{
					Name:      "read_file",
					Arguments: `{"path":"README.md"}`,
				},
			}},
		},
		{
			Role:       "tool",
			ToolCallID: "call_read_file_123",
			Content:    "ok",
		},
	}

	req := p.buildRequest(messages, nil, "", nil)
	if len(req.Contents) != 2 {
		t.Fatalf("expected 2 contents, got %d", len(req.Contents))
	}

	modelPart := req.Contents[0].Parts[0]
	if modelPart.FunctionCall == nil {
		t.Fatal("expected functionCall in assistant message")
	}
	if modelPart.FunctionCall.Name != "read_file" {
		t.Fatalf("expected functionCall name read_file, got %q", modelPart.FunctionCall.Name)
	}
	if got := modelPart.FunctionCall.Args["path"]; got != "README.md" {
		t.Fatalf("expected functionCall args[path] to be README.md, got %v", got)
	}

	toolPart := req.Contents[1].Parts[0]
	if toolPart.FunctionResponse == nil {
		t.Fatal("expected functionResponse in tool message")
	}
	if toolPart.FunctionResponse.Name != "read_file" {
		t.Fatalf("expected functionResponse name read_file, got %q", toolPart.FunctionResponse.Name)
	}
}

func TestResolveToolResponseNameInfersNameFromGeneratedCallID(t *testing.T) {
	got := resolveToolResponseName("call_search_docs_999", map[string]string{})
	if got != "search_docs" {
		t.Fatalf("expected inferred tool name search_docs, got %q", got)
	}
}

// setupAntigravityTokenSourceTest isolates the credential store used by the
// token source, mirroring setupClaudeTokenSourceTest: a throwaway
// LELE_CONFIG_DIR plus UseStore(nil) selects the legacy JSON backend. UseStore
// is the only store seam exported to this package (the in-package SQLite
// helpers of pkg/auth are not importable), and the legacy backend is exactly
// what an isolated config dir makes safe to write to.
//
// UseStore(nil) resets pkg/auth's package-global store and cannot restore it:
// pkg/auth exposes no getter for the active repository (only the unexported
// currentRepo), so there is no prior value to hand back to a t.Cleanup. This
// test must therefore stay serial — no t.Parallel — and must not depend on a
// store a previous test installed.
func setupAntigravityTokenSourceTest(t *testing.T) {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	auth.UseStore(nil)
}

// TestCreateAntigravityTokenSourceLoginHints pins the exact user-facing login
// hints produced by createAntigravityTokenSource(). These two hints used to
// carry a stale binary name ("picoclaw auth login") while every sibling
// provider hint was already pinned by a test; this guards the regression.
//
// Both paths are network-free: with nothing stored, EnsureFreshCredential
// returns (nil, nil) at refresh_coordinator.go:78 before any I/O, and for an
// expired credential with an EMPTY refresh token it returns the stored
// credential as-is at refresh_coordinator.go:95, also before any I/O.
func TestCreateAntigravityTokenSourceLoginHints(t *testing.T) {
	t.Run("without credentials", func(t *testing.T) {
		setupAntigravityTokenSourceTest(t)

		token, project, err := createAntigravityTokenSource()()
		if err == nil {
			t.Fatal("createAntigravityTokenSource() error = nil, want non-nil")
		}
		const want = "no credentials for google-antigravity. Run: lele auth login --provider google-antigravity"
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
		if token != "" {
			t.Errorf("token = %q, want empty", token)
		}
		if project != "" {
			t.Errorf("project = %q, want empty", project)
		}
	})

	t.Run("expired credential", func(t *testing.T) {
		setupAntigravityTokenSourceTest(t)

		// Empty RefreshToken keeps this offline: EnsureFreshCredential hands
		// the stored credential back untouched instead of refreshing it.
		expired := &auth.AuthCredential{
			AccessToken: "stale-access",
			ExpiresAt:   time.Now().Add(-time.Hour),
			Provider:    "google-antigravity",
			AuthMethod:  "oauth",
		}
		if err := auth.SetCredential("google-antigravity", expired); err != nil {
			t.Fatalf("SetCredential() error: %v", err)
		}

		token, project, err := createAntigravityTokenSource()()
		if err == nil {
			t.Fatal("createAntigravityTokenSource() error = nil, want non-nil")
		}
		const want = "antigravity credentials expired. Run: lele auth login --provider google-antigravity"
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
		if token != "" {
			t.Errorf("token = %q, want empty", token)
		}
		if project != "" {
			t.Errorf("project = %q, want empty", project)
		}

		// Nothing may have refreshed or rewritten the stored credential.
		stored, err := auth.GetCredential("google-antigravity")
		if err != nil {
			t.Fatalf("GetCredential() error: %v", err)
		}
		if stored == nil {
			t.Fatal("GetCredential() = nil, want the seeded credential")
		}
		if stored.AccessToken != "stale-access" {
			t.Errorf("stored AccessToken = %q, want %q (credential was mutated)", stored.AccessToken, "stale-access")
		}
	})
}
