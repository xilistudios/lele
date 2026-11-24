package providers

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/providers/common"
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

func setupAntigravityTokenSourceTest(t *testing.T) {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	auth.UseStore(nil)
}

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

func TestParseSSEResponseTooLongLine(t *testing.T) {
	p := &AntigravityProvider{}
	body := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hello\"}]}}]}}\n" +
		"data: {\"pad\":\"" + strings.Repeat("x", 2*1024*1024) + "\"}\n"

	resp, err := p.parseSSEResponse(body)
	if err == nil {
		t.Fatal("expected an error for an over-long SSE line, got nil")
	}
	if !errors.Is(err, common.ErrStreamTruncated) {
		t.Fatalf("error = %v, want it to wrap common.ErrStreamTruncated", err)
	}
	if resp == nil {
		t.Fatal("expected the partial response alongside the error")
	}
	if resp.Content != "hello" {
		t.Fatalf("partial content = %q, want %q", resp.Content, "hello")
	}
}
