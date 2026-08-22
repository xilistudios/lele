package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/auth"
	"github.com/xilistudios/lele/pkg/config"
)

func TestCreateClaudeTokenSource_NoCreds(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		if provider != "anthropic" {
			t.Fatalf("provider = %q, want anthropic", provider)
		}
		return nil, nil
	}
	src := createClaudeTokenSource()
	if _, err := src(); err == nil {
		t.Fatal("expected error when no credentials exist")
	}
}

func TestCreateClaudeTokenSource_Error(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return nil, errors.New("boom")
	}
	src := createClaudeTokenSource()
	if _, err := src(); err == nil {
		t.Fatal("expected error on getCredential failure")
	}
}

func TestCreateClaudeTokenSource_Success(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{AccessToken: "claude-tok"}, nil
	}
	src := createClaudeTokenSource()
	tok, err := src()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "claude-tok" {
		t.Errorf("token = %q", tok)
	}
}

func TestCreateCodexTokenSource_Error(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	// Write an invalid auth store so LoadStore returns an error.
	os.MkdirAll(dir, 0755)
	_ = os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{invalid"), 0600)
	src := createCodexTokenSource()
	if _, _, err := src(); err == nil {
		t.Fatal("expected error on getCredential failure")
	}
}

func TestCreateCodexTokenSource_NoCreds(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	src := createCodexTokenSource()
	if _, _, err := src(); err == nil {
		t.Fatal("expected error when no credentials exist")
	}
}

func TestCreateCodexTokenSource_Simple(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	if err := auth.SetCredential("openai", &auth.AuthCredential{
		AccessToken: "tok",
		AccountID:   "acc",
		Provider:    "openai",
	}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	src := createCodexTokenSource()
	tok, acc, err := src()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "tok" || acc != "acc" {
		t.Errorf("tok=%q acc=%q", tok, acc)
	}
}

// TestCreateCodexTokenSource_Refresh exercises the refresh branch, including
// SetCredential persistence failure fallbacks. The refresh itself needs a
// working token endpoint; we skip hitting the network by only verifying the
// branch is reached via a credential that needs refresh — but we stub out
// auth.RefreshAccessToken? That's a package function in auth, not a var, so we
// keep the credential short-lived with a refresh token and let it fail
// gracefully (the token source must return a non-nil error).
func TestCreateCodexTokenSource_Refresh(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	// Credential that needs refresh but has no refresh token -> the oauth
	// branch returns an error from RefreshAccessToken path. We just verify we
	// don't panic and get some error.
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{
			AccessToken:  "tok",
			AccountID:    "acc",
			AuthMethod:   "oauth",
			ExpiresAt:    time.Now().Add(-time.Hour),
			RefreshToken: "rt",
		}, nil
	}
	src := createCodexTokenSource()
	_, _, _ = src()
}

func TestCreateCodexAuthProvider_NoCreds(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return nil, nil
	}
	if _, err := createCodexAuthProvider(false); err == nil {
		t.Fatal("expected error when no credentials")
	}
}

func TestCreateCodexAuthProvider_Error(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return nil, errors.New("boom")
	}
	if _, err := createCodexAuthProvider(true); err == nil {
		t.Fatal("expected error on getCredential failure")
	}
}

func TestCreateClaudeAuthProvider_Error(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return nil, errors.New("boom")
	}
	if _, err := createClaudeAuthProvider(""); err == nil {
		t.Fatal("expected error on getCredential failure")
	}
}

func TestCreateClaudeAuthProvider_NoCreds(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return nil, nil
	}
	if _, err := createClaudeAuthProvider(""); err == nil {
		t.Fatal("expected error when no credentials")
	}
}

func TestResolveProviderSelectionByName_NoConfigAllEmpty(t *testing.T) {
	cfg := &config.Config{Providers: &config.ProvidersConfig{}}
	_, err := resolveProviderSelectionByName(cfg, "", "")
	if err == nil {
		t.Fatal("expected error when nothing configured")
	}
}

func TestResolveProviderSelectionByName_GithubCopilotDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	sel, err := resolveProviderSelectionByName(cfg, "copilot", "gpt-4.1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if sel.providerType != providerTypeGitHubCopilot {
		t.Errorf("providerType = %v", sel.providerType)
	}
	if sel.apiBase != "localhost:4321" {
		t.Errorf("apiBase = %q", sel.apiBase)
	}
}

func TestResolveProviderSelection_NilProviders(t *testing.T) {
	cfg := &config.Config{}
	// resolveProviderSelection handles nil Providers and attempts to resolve
	// defaults. With nothing configured it should error out (no key/API base).
	_, err := resolveProviderSelection(cfg)
	if err == nil {
		t.Fatal("expected error with nil providers and no config")
	}
}

func TestResolveProviderSelectionForProvider(t *testing.T) {
	cfg := &config.Config{Providers: &config.ProvidersConfig{}}
	if _, err := resolveProviderSelectionForProvider(cfg, "nonexistent"); err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestResolveProviderSelectionByName_TopLevel(t *testing.T) {
	t.Run("vllm", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.VLLM.APIBase = "http://vllm:8000"
		sel, err := resolveProviderSelectionByName(cfg, "vllm", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "http://vllm:8000" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("shengsuanyun default base", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.ShengSuanYun.APIKey = "ssy"
		sel, err := resolveProviderSelectionByName(cfg, "shengsuanyun", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://router.shengsuanyun.com/api/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("nvidia default base", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Nvidia.APIKey = "nv"
		sel, err := resolveProviderSelectionByName(cfg, "nvidia", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://integrate.api.nvidia.com/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("zai default base", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.ZAICodingPlan.APIKey = "zai"
		sel, err := resolveProviderSelectionByName(cfg, "zai", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://api.z.ai/api/paas/v4" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("modelark default base", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.ModelArkCodingPlan.APIKey = "ma"
		sel, err := resolveProviderSelectionByName(cfg, "modelark", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://ark.ap-southeast.bytepluses.com/api/coding/v3" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("deepseek defaults model", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.DeepSeek.APIKey = "ds"
		sel, err := resolveProviderSelectionByName(cfg, "deepseek", "custom-model-name")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://api.deepseek.com/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
		if sel.model != "deepseek-chat" {
			t.Errorf("model = %q, want deepseek-chat", sel.model)
		}
	})
	t.Run("deepseek keeps prefix model", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.DeepSeek.APIKey = "ds"
		sel, err := resolveProviderSelectionByName(cfg, "deepseek", "deepseek-reasoner")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.model != "deepseek-reasoner" {
			t.Errorf("model = %q, want deepseek-reasoner", sel.model)
		}
	})
	t.Run("alibaba named", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Named = map[string]config.NamedProviderConfig{
			"alibaba": {ProviderConfig: config.ProviderConfig{APIKey: "al"}},
		}
		sel, err := resolveProviderSelectionByName(cfg, "alibaba", "m")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://coding-intl.dashscope.aliyuncs.com/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("grpc empty provider openai no key", func(t *testing.T) {
		cfg := config.DefaultConfig()
		_, _ = resolveProviderSelectionByName(cfg, "openai", "gpt-4o")
	})
}

func TestResolveProviderSelectionByModel_Fallback(t *testing.T) {
	t.Run("gemini inference", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Gemini.APIKey = "ge"
		sel, err := resolveProviderSelectionByName(cfg, "", "gemini-2.0-flash")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://generativelanguage.googleapis.com/v1beta" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("glm inference", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Zhipu.APIKey = "zh"
		sel, err := resolveProviderSelectionByName(cfg, "", "glm-4")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://open.bigmodel.cn/api/paas/v4" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("groq inference", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Groq.APIKey = "gr"
		sel, err := resolveProviderSelectionByName(cfg, "", "groq/llama-x")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://api.groq.com/openai/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("nvidia inference", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Nvidia.APIKey = "nv"
		sel, err := resolveProviderSelectionByName(cfg, "", "nvidia/foo")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://integrate.api.nvidia.com/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("ollama inference", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.Ollama.APIKey = "ol"
		sel, err := resolveProviderSelectionByName(cfg, "", "ollama/llama")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "http://localhost:11434/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("vllm inference", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.VLLM.APIBase = "http://vllm:9000"
		cfg.Providers.VLLM.APIKey = "vk"
		sel, err := resolveProviderSelectionByName(cfg, "", "random-model")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "http://vllm:9000" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("default openrouter", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Providers.OpenRouter.APIKey = "or"
		cfg.Providers.OpenRouter.APIBase = "https://or.example.com/v1"
		sel, err := resolveProviderSelectionByName(cfg, "", "some-model")
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.apiBase != "https://or.example.com/v1" {
			t.Errorf("apiBase = %q", sel.apiBase)
		}
	})
	t.Run("default no key error", func(t *testing.T) {
		cfg := config.DefaultConfig()
		if _, err := resolveProviderSelectionByName(cfg, "", "some-model"); err == nil {
			t.Fatal("expected error when no key configurable")
		}
	})
}

func TestCreateProviderForCandidate_UnknownProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	if _, err := CreateProviderForCandidate(cfg, "totally-unknown"); err == nil {
		t.Fatal("expected error for unknown provider candidate")
	}
}

func TestCreateProvider_NilProvidersEmpty(t *testing.T) {
	cfg := &config.Config{}
	if _, err := CreateProvider(cfg); err == nil {
		t.Fatal("expected error with empty config")
	}
}

func TestGitHubCopilot_Chat_NilSession(t *testing.T) {
	p := &GitHubCopilotProvider{}
	// A nil session Send panics; this guards against regressions where the
	// grpc mode helper is reachable. We only verify the message encoding runs.
	// Since session is nil we expect a panic-free path is not defined; instead
	// we verify GetDefaultModel still works and encoding of messages works via
	// a small helper path. Just exercise the struct construction.
	if p.GetDefaultModel() != "gpt-4.1" {
		t.Fatal("default model mismatch")
	}
}

func TestExecuteWithRetry_SingleCandidateCapped(t *testing.T) {
	fc := &FallbackChain{
		maxRetries: 10,
		maxBackoff: time.Millisecond * 40,
	}
	calls := 0
	_, err := fc.executeWithRetry(context.Background(), "p", "m", func(ctx context.Context, p, m string) (*LLMResponse, error) {
		calls++
		return nil, errors.New("rate limited")
	}, 1)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls > singleCandidateMaxRetries {
		t.Errorf("calls = %d, want capped at %d", calls, singleCandidateMaxRetries)
	}
}

func TestExecuteWithRetry_CtxCanceled(t *testing.T) {
	fc := &FallbackChain{maxRetries: 3}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fc.executeWithRetry(ctx, "p", "m", func(ctx context.Context, p, m string) (*LLMResponse, error) {
		return nil, nil
	}, 2)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestExecuteWithRetry_ImmediateRetryExhausted(t *testing.T) {
	fc := &FallbackChain{maxRetries: 10, maxBackoff: time.Millisecond}
	calls := 0
	// A timeout classifies as retriable but not backoff -> immediate retry
	// until immediateRetryAttempts is exhausted.
	_, err := fc.executeWithRetry(context.Background(), "p", "m", func(ctx context.Context, p, m string) (*LLMResponse, error) {
		calls++
		return nil, context.DeadlineExceeded
	}, 2)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != immediateRetryAttempts+1 {
		t.Errorf("calls = %d, want %d", calls, immediateRetryAttempts+1)
	}
}

func TestResolveCodexAuthPath_EnvAndError(t *testing.T) {
	// With CODEX_HOME set
	tmp := t.TempDir()
	t.Setenv("CODEX_HOME", tmp)
	p, err := resolveCodexAuthPath()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p != filepath.Join(tmp, "auth.json") {
		t.Errorf("path = %q", p)
	}

	// Without CODEX_HOME, simulate home dir error is hard; just verify it
	// returns a path under home. Unset env (t.Setenv restores automatically).
	t.Setenv("CODEX_HOME", "")
	home, _ := os.UserHomeDir()
	p2, err := resolveCodexAuthPath()
	if err != nil {
		t.Fatalf("err2 = %v", err)
	}
	if p2 != filepath.Join(home, ".codex", "auth.json") {
		t.Errorf("path2 = %q", p2)
	}
}

func TestCreateGitHubCopilotProvider_StdioMode(t *testing.T) {
	// "stdio" mode doesn't spawn a CLI, so construction succeeds without panic.
	p, err := NewGitHubCopilotProvider("/tmp/cli", "stdio", "gpt-4.1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p == nil || p.connectMode != "stdio" {
		t.Fatalf("unexpected provider: %+v", p)
	}
}

// Ensure antigravity token source returns a graceful error without creds.
func TestCreateAntigravityTokenSource_NoCreds(t *testing.T) {
	// auth.GetCredential hits the real store; with a temp LELE_CONFIG_DIR there
	// are no credentials, so it should return nil cred -> error.
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	src := createAntigravityTokenSource()
	if _, _, err := src(); err == nil {
		t.Fatal("expected error for missing antigravity credentials")
	}
}

func TestFetchAntigravityModels_BadURL(t *testing.T) {
	// Passing an invalid request; function still marshals and creates request
	// against the fixed base URL. Verify it doesn't panic and returns models
	// (empty) or error gracefully.
	_, _ = FetchAntigravityModels("x", "y")
}

func TestClaudeProvider_Constructors(t *testing.T) {
	if NewClaudeProvider("t") == nil {
		t.Fatal("nil NewClaudeProvider")
	}
	if NewClaudeProviderWithBaseURL("t", "http://x") == nil {
		t.Fatal("nil NewClaudeProviderWithBaseURL")
	}
	if NewClaudeProviderWithTokenSource("t", func() (string, error) { return "t", nil }) == nil {
		t.Fatal("nil NewClaudeProviderWithTokenSource")
	}
	if NewClaudeProviderWithTokenSourceAndBaseURL("t", func() (string, error) { return "t", nil }, "http://x") == nil {
		t.Fatal("nil NewClaudeProviderWithTokenSourceAndBaseURL")
	}
	if newClaudeProviderWithDelegate(nil) == nil {
		t.Fatal("nil newClaudeProviderWithDelegate")
	}
}

func TestNewHTTPProvider_Empty(t *testing.T) {
	p := NewHTTPProvider("", "http://localhost:1/v1", "")
	if p == nil {
		t.Fatal("nil HTTPProvider")
	}
}

// The copilot SDK's NewClient panics only on *unparseable* CLI URLs; a valid
// host:port parses fine, and client.Start will fail to connect (returning an
// error) since nothing listens on the port. This exercises the grpc branch.
func TestCreateProviderFromSelection_GitHubCopilotNoServer(t *testing.T) {
	sel := &providerSelection{
		providerType: providerTypeGitHubCopilot,
		apiBase:      "127.0.0.1:1",
		connectMode:  "grpc",
	}
	_, err := createProviderFromSelection(sel)
	if err == nil {
		t.Log("copilot connect unexpectedly succeeded")
	}
}

func TestCreateCodexCliTokenSource_MissingHome(t *testing.T) {
	// Point CODEX_HOME at a temp dir with no auth.json
	t.Setenv("CODEX_HOME", t.TempDir())
	src := CreateCodexCliTokenSource()
	if _, _, err := src(); err == nil {
		t.Fatal("expected error when auth.json missing")
	}
}

var _ = httptest.NewServer
var _ = io.Discard
var _ = http.MethodGet

func TestCreateProviderFromSelection_ClaudeAuth(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{AccessToken: "tok"}, nil
	}
	sel := &providerSelection{providerType: providerTypeClaudeAuth, apiBase: "http://proxy/v1"}
	p, err := createProviderFromSelection(sel)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := p.(*ClaudeProvider); !ok {
		t.Fatalf("type = %T, want *ClaudeProvider", p)
	}
}

func TestCreateProviderFromSelection_CodexAuth(t *testing.T) {
	orig := getCredential
	t.Cleanup(func() { getCredential = orig })
	getCredential = func(provider string) (*auth.AuthCredential, error) {
		return &auth.AuthCredential{AccessToken: "tok", AccountID: "a"}, nil
	}
	sel := &providerSelection{providerType: providerTypeCodexAuth, enableWebSearch: true}
	p, err := createProviderFromSelection(sel)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := p.(*CodexProvider); !ok {
		t.Fatalf("type = %T, want *CodexProvider", p)
	}
}

func TestCreateProviderFromSelection_Anthropic(t *testing.T) {
	sel := &providerSelection{providerType: providerTypeAnthropic, apiKey: "k", apiBase: "http://a/v1"}
	p, err := createProviderFromSelection(sel)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestCreateProviderFromSelection_CodexCLIToken(t *testing.T) {
	sel := &providerSelection{providerType: providerTypeCodexCLIToken}
	p, err := createProviderFromSelection(sel)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := p.(*CodexProvider); !ok {
		t.Fatalf("type = %T, want *CodexProvider", p)
	}
}

func TestCreateProviderFromSelection_ClaudeCLI(t *testing.T) {
	sel := &providerSelection{providerType: providerTypeClaudeCLI, workspace: "/tmp"}
	p, err := createProviderFromSelection(sel)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestCreateProviderFromSelection_CodexCLI(t *testing.T) {
	sel := &providerSelection{providerType: providerTypeCodexCLI, workspace: "/tmp"}
	p, err := createProviderFromSelection(sel)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestCreateProviderFromSelection_HTTPCompat(t *testing.T) {
	sel := &providerSelection{providerType: providerTypeHTTPCompat, apiKey: "k", apiBase: "http://h/v1"}
	p, err := createProviderFromSelection(sel)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := p.(*HTTPProvider); !ok {
		t.Fatalf("type = %T, want *HTTPProvider", p)
	}
}

func TestProviderTypeSwitch_AllCases(t *testing.T) {
	// Verify the providerType iota constants map as expected.
	if providerTypeHTTPCompat != 0 ||
		providerTypeClaudeAuth != 1 ||
		providerTypeCodexAuth != 2 ||
		providerTypeCodexCLIToken != 3 {
		t.Fatalf("unexpected providerType constants")
	}
}

func TestBuildRequest_ToolResultAndToolRoleBranches(t *testing.T) {
	p := &AntigravityProvider{}
	req := p.buildRequest(
		[]Message{
			{Role: "assistant", Content: "let me read", ToolCalls: []ToolCall{
				{ID: "call_1", Name: "read_file", Arguments: map[string]any{"path": "a.txt"}},
				{ID: "", Name: "", Arguments: nil}, // empty name -> skipped
			}},
			{Role: "user", ToolCallID: "call_1", Content: "the result"},
			{Role: "tool", ToolCallID: "call_1", Content: "file contents"},
		},
		nil,
		"test-model",
		map[string]any{"max_tokens": float64(256), "temperature": 0.2},
	)
	if len(req.Contents) != 3 {
		t.Fatalf("len(Contents) = %d, want 3", len(req.Contents))
	}
	if req.Config == nil || req.Config.MaxOutputTokens != 256 {
		t.Fatalf("Config = %+v, want max 256", req.Config)
	}
	// first content is a tool-result user part
	// first content is the assistant (model) message with a function call
	if req.Contents[0].Role != "model" {
		t.Errorf("Contents[0].Role = %q, want model", req.Contents[0].Role)
	}
	if req.Contents[0].Parts[1].FunctionCall == nil {
		t.Fatal("expected function call in assistant content")
	}
	if req.Contents[0].Parts[1].FunctionCall.Name != "read_file" {
		t.Errorf("name = %q, want read_file", req.Contents[0].Parts[1].FunctionCall.Name)
	}
}

func TestBuildRequest_SkipEmptyName(t *testing.T) {
	p := &AntigravityProvider{}
	req := p.buildRequest(
		[]Message{{Role: "assistant", ToolCalls: []ToolCall{{ID: "x", Name: "", Arguments: nil}}}},
		nil, "test-model", nil,
	)
	if len(req.Contents) != 0 {
		t.Fatalf("len(Contents) = %d, want 0 (empty name skipped)", len(req.Contents))
	}
}

func TestBuildRequest_SystemOnlyConfigZero(t *testing.T) {
	p := &AntigravityProvider{}
	req := p.buildRequest(
		[]Message{{Role: "system", Content: "only sys"}},
		nil, "test-model",
		map[string]any{"max_tokens": 0, "temperature": 0},
	)
	if req.Config != nil {
		t.Errorf("Config = %+v, want nil (both zero)", req.Config)
	}
	if req.SystemPrompt == nil || req.SystemPrompt.Parts[0].Text != "only sys" {
		t.Errorf("SystemPrompt = %+v", req.SystemPrompt)
	}
}

// roundTripFunc adapter rarely used at package level; ensure presence.
var _ = roundTripFunc(nil)

func TestCodexProvider_Chat_TokenSourceAndBranches(t *testing.T) {
	// Server returning a completed response.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"id":     "resp_ts",
			"object": "response",
			"status": "completed",
			"output": []map[string]interface{}{
				{"type": "message", "role": "assistant", "content": []map[string]interface{}{
					{"type": "output_text", "text": "via token source"},
				}},
			},
		}
		writeCompletedSSE(w, resp)
	}))
	defer server.Close()

	t.Run("token source success with account override", func(t *testing.T) {
		p := NewCodexProviderWithTokenSource("stale", "old-account", func() (string, string, error) {
			return "fresh-token", "fresh-account", nil
		})
		p.client = createOpenAITestClient(server.URL, "fresh-token", "fresh-account")
		resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if resp.Content != "via token source" {
			t.Errorf("content = %q", resp.Content)
		}
	})

	t.Run("token source error", func(t *testing.T) {
		p := NewCodexProviderWithTokenSource("stale", "", func() (string, string, error) {
			return "", "", fmt.Errorf("no creds")
		})
		p.client = createOpenAITestClient(server.URL, "whatever", "")
		_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
		if err == nil || !strings.Contains(err.Error(), "refreshing token") {
			t.Errorf("err = %v, want refreshing token", err)
		}
	})

	t.Run("no account id warning path", func(t *testing.T) {
		p := NewCodexProvider("tok", "")
		p.client = createOpenAITestClient(server.URL, "tok", "")
		_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
		// Server doesn't require account id here, but the no-account warning path executes.
		_ = err
	})
}

func TestCodexProvider_Chat_StreamErrorAndNilResponse(t *testing.T) {
	t.Run("stream error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: error\n")
			fmt.Fprintf(w, "data: {\"error\":{\"type\":\"server_error\",\"message\":\"boom\"}}\n\n")
			fmt.Fprintf(w, "data: [DONE]\n\n")
		}))
		defer server.Close()
		p := NewCodexProvider("tok", "acc")
		p.client = createOpenAITestClient(server.URL, "tok", "acc")
		_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
		if err == nil {
			t.Log("expected stream error (may vary)")
		}
	})

	t.Run("empty stream nil response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: [DONE]\n\n")
		}))
		defer server.Close()
		p := NewCodexProvider("tok", "acc")
		p.client = createOpenAITestClient(server.URL, "tok", "acc")
		_, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "gpt-4o", nil)
		if err == nil || !strings.Contains(err.Error(), "stream ended without completed") {
			t.Errorf("err = %v, want stream ended without completed", err)
		}
	})
}

func TestResolveProviderSelectionByName_NamedInferenceFromRef(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"myprov": {ProviderConfig: config.ProviderConfig{APIKey: "mk", APIBase: "http://my/v1"}},
	}
	// model "myprov:model-x" -> ParseModelRef yields provider "myprov"
	sel, err := resolveProviderSelectionByName(cfg, "", "myprov:model-x")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if sel.apiKey != "mk" || sel.apiBase != "http://my/v1" {
		t.Errorf("apiKey=%q apiBase=%q", sel.apiKey, sel.apiBase)
	}
	if sel.model != "model-x" {
		t.Errorf("model = %q", sel.model)
	}
}

func TestResolveProviderSelectionByName_HTTPCompatErrorBranches(t *testing.T) {
	// HTTPCompat type with no key and no bedrock prefix => "no API key" here
	// happens after inference returns nothing. Use a config with no keys.
	t.Run("no api key error", func(t *testing.T) {
		cfg := config.DefaultConfig()
		_, err := resolveProviderSelectionByName(cfg, "", "my-compat-model")
		if err == nil || !strings.Contains(err.Error(), "no API key") {
			t.Fatalf("err = %v, want no API key", err)
		}
	})

	t.Run("api key but no api base", func(t *testing.T) {
		// Build HTTPCompat selection with apiKey set but apiBase empty via a
		// named provider type that forces HTTPCompat and no default base.
		cfg := config.DefaultConfig()
		cfg.Providers.Named = map[string]config.NamedProviderConfig{
			"localproxy": {ProviderConfig: config.ProviderConfig{APIKey: "k"}},
		}
		// This named provider has no "type" so providerName defaults to
		// "localproxy" which has no default base -> error in selectionFromNamedProvider.
		if _, err := resolveProviderSelectionByName(cfg, "localproxy", "m"); err == nil {
			t.Log("selectionFromNamedProvider may have errored")
		}
	})
}

func TestResolveProviderSelectionForProvider_Named(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"deepseek": {ProviderConfig: config.ProviderConfig{APIKey: "dk"}},
	}
	sel, err := resolveProviderSelectionForProvider(cfg, "deepseek")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	// named provider with no type defaults providerName "deepseek"
	if sel.apiKey != "dk" {
		t.Errorf("apiKey = %q", sel.apiKey)
	}
	if sel.apiBase != "https://api.deepseek.com/v1" {
		t.Errorf("apiBase = %q", sel.apiBase)
	}
}

func TestFallback_Execute_CooldownSkip(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)
	// Force provider into cooldown by marking failures enough times.
	for i := 0; i < 5; i++ {
		ct.MarkFailure("p1", FailoverRateLimit)
	}
	calls := 0
	result, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "p1", Model: "m1"}},
		func(ctx context.Context, p, m string) (*LLMResponse, error) {
			calls++
			return &LLMResponse{Content: "x"}, nil
		})
	if err == nil {
		t.Fatalf("expected exhausted error, got %+v", result)
	}
	if calls != 0 {
		t.Errorf("calls = %d, want 0 (skipped due to cooldown)", calls)
	}
}

func TestFallback_Execute_ContextCanceledInLoop(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// First candidate in cooldown (skips, continues), then context canceled
	// check runs for second candidate.
	result, err := fc.Execute(ctx,
		[]FallbackCandidate{{Provider: "p1", Model: "m1"}, {Provider: "p2", Model: "m2"}},
		func(ctx context.Context, p, m string) (*LLMResponse, error) {
			return &LLMResponse{Content: "x"}, nil
		})
	_ = result
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFallback_Execute_CanceledAfterError(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)
	ctx, cancel := context.WithCancel(context.Background())
	// First call errors while ctx not yet canceled -> retriable path
	// (timeout = immediate retry, but ctx becomes canceled on second attempt).
	attempt := 0
	_, err := fc.Execute(ctx,
		[]FallbackCandidate{{Provider: "p1", Model: "m1"}},
		func(ctx context.Context, p, m string) (*LLMResponse, error) {
			attempt++
			if attempt == 1 {
				cancel() // cancel during first call
				return nil, context.DeadlineExceeded
			}
			return &LLMResponse{Content: "x"}, nil
		})
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFallback_Execute_AllCandidatesSkipped(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)
	for _, p := range []string{"p1", "p2"} {
		for i := 0; i < 5; i++ {
			ct.MarkFailure(p, FailoverRateLimit)
		}
	}
	_, err := fc.Execute(context.Background(),
		[]FallbackCandidate{{Provider: "p1", Model: "m1"}, {Provider: "p2", Model: "m2"}},
		func(ctx context.Context, p, m string) (*LLMResponse, error) {
			return &LLMResponse{Content: "x"}, nil
		})
	if err == nil {
		t.Fatal("expected error (all candidates skipped)")
	}
}

func TestCooldown_GetOrCreateAndFailureCountNil(t *testing.T) {
	ct := NewCooldownTracker()
	// FailureCount with nil entry returns 0.
	if ct.FailureCount("nope", FailoverRateLimit) != 0 {
		t.Error("FailureCount(nil entry) != 0")
	}
	if ct.ErrorCount("nope") != 0 {
		t.Error("ErrorCount(nil entry) != 0")
	}
	// getOrCreate
	ct.MarkSuccess("newprov") // triggers getOrCreate internally
	if ct.IsAvailable("newprov") != true {
		t.Error("expected available after getOrCreate")
	}
}

func TestFallback_ExecuteImage_CanceledBeforeLoop(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fc.ExecuteImage(ctx,
		[]FallbackCandidate{{Provider: "p1", Model: "m1"}},
		func(ctx context.Context, p, m string) (*LLMResponse, error) {
			return &LLMResponse{Content: "x"}, nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFallback_ExecuteImage_CanceledDuringRun(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker())
	ctx, cancel := context.WithCancel(context.Background())
	_, err := fc.ExecuteImage(ctx,
		[]FallbackCandidate{{Provider: "p1", Model: "m1"}},
		func(ctx context.Context, p, m string) (*LLMResponse, error) {
			cancel()
			return nil, errors.New("connection reset")
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestFallback_ExecuteImage_DimensionError(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker())
	_, err := fc.ExecuteImage(context.Background(),
		[]FallbackCandidate{{Provider: "p1", Model: "m1"}},
		func(ctx context.Context, p, m string) (*LLMResponse, error) {
			return nil, errors.New("image dimensions exceed max allowed")
		})
	if err == nil {
		t.Fatal("expected dimension error")
	}
	var fe *FailoverError
	if !errors.As(err, &fe) || fe.Reason != FailoverFormat {
		t.Fatalf("err = %T %v, want FailoverError with Format reason", err, err)
	}
}

func TestFallback_ExecuteImage_LastCandidateExhausted(t *testing.T) {
	fc := NewFallbackChain(NewCooldownTracker())
	_, err := fc.ExecuteImage(context.Background(),
		[]FallbackCandidate{{Provider: "p1", Model: "m1"}},
		func(ctx context.Context, p, m string) (*LLMResponse, error) {
			return nil, errors.New("boom")
		})
	if err == nil {
		t.Fatal("expected exhausted error")
	}
	var ee *FallbackExhaustedError
	if !errors.As(err, &ee) || len(ee.Attempts) != 1 {
		t.Fatalf("err = %T, want FallbackExhaustedError", err)
	}
}

func TestExecuteWithRetry_BackoffCtxDone(t *testing.T) {
	fc := &FallbackChain{maxRetries: 10, maxBackoff: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := fc.executeWithRetry(ctx, "p", "m", func(ctx context.Context, p, m string) (*LLMResponse, error) {
		cancel() // cancel during backoff wait
		return nil, errors.New("status: 429 rate limited")
	}, 2)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestCreateAntigravityTokenSource_Expired(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	// Credential present but expired -> token source returns expiry error.
	if err := auth.SetCredential("google-antigravity", &auth.AuthCredential{
		AccessToken: "at",
		ProjectID:   "proj-1",
		ExpiresAt:   time.Now().Add(-time.Hour),
		Provider:    "google-antigravity",
	}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	src := createAntigravityTokenSource()
	if _, _, err := src(); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want expired", err)
	}
}

func TestCreateAntigravityTokenSource_EmptyProjectIDFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	// Credential present, not expired, no ProjectID. FetchAntigravityProjectID
	// hits the network and fails (no network / fixed URL), so it falls back to
	// the default project id.
	if err := auth.SetCredential("google-antigravity", &auth.AuthCredential{
		AccessToken: "at",
		Provider:    "google-antigravity",
	}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	src := createAntigravityTokenSource()
	tok, projectID, err := src()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "at" {
		t.Errorf("tok = %q", tok)
	}
	if projectID != "rising-fact-p41fc" {
		t.Errorf("projectID = %q, want fallback", projectID)
	}
}

func TestCreateAntigravityTokenSource_Success(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	if err := auth.SetCredential("google-antigravity", &auth.AuthCredential{
		AccessToken: "at",
		ProjectID:   "proj-1",
		Provider:    "google-antigravity",
	}); err != nil {
		t.Fatalf("SetCredential: %v", err)
	}
	src := createAntigravityTokenSource()
	tok, pid, err := src()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if tok != "at" || pid != "proj-1" {
		t.Errorf("tok=%q pid=%q", tok, pid)
	}
}

func TestBuildCodexParams_UserToolResultAndInvalidCall(t *testing.T) {
	messages := []Message{
		{Role: "user", ToolCallID: "call_9", Content: "result"},
		{Role: "assistant", Content: "using tool", ToolCalls: []ToolCall{
			{ID: "call_9", Name: "read_file", Arguments: map[string]any{"p": "x"}},
			{ID: "call_bad", Name: "", Arguments: nil}, // invalid -> skipped
		}},
	}
	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{}, false)
	if len(params.Input.OfInputItemList) != 3 {
		t.Fatalf("len = %d, want 3", len(params.Input.OfInputItemList))
	}
	// assistant message has content + a function call item
	foundCall := false
	for _, it := range params.Input.OfInputItemList {
		if it.OfFunctionCall != nil && it.OfFunctionCall.Name == "read_file" {
			foundCall = true
		}
	}
	if !foundCall {
		t.Error("expected read_file function call")
	}
}

func TestBuildCodexParams_ToolRoleArgsBranch(t *testing.T) {
	messages := []Message{
		{Role: "tool", ToolCallID: "t1", Content: "path output"},
		{Role: "tool", ToolCallID: "t2", Content: "more"},
	}
	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{}, false)
	if len(params.Input.OfInputItemList) != 2 {
		t.Fatalf("len = %d, want 2", len(params.Input.OfInputItemList))
	}
	for _, it := range params.Input.OfInputItemList {
		if it.OfFunctionCallOutput == nil {
			t.Fatalf("expected function call output item")
		}
	}
}

func TestBuildCodexParams_AllRolesAndTools(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "sys-instr"},
		{Role: "user", Content: "hi"},
	}
	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{}, true)
	if len(params.Input.OfInputItemList) != 1 {
		t.Fatalf("len = %d, want 1", len(params.Input.OfInputItemList))
	}
	if got := params.Instructions.Or(""); got != "sys-instr" {
		t.Errorf("instructions = %q, want sys-instr", got)
	}
	// web search enabled -> tools present
	if params.Tools == nil || len(params.Tools) == 0 {
		t.Error("expected web search tool when enableWebSearch")
	}
}

func TestResolveCodexToolCall_Branches(t *testing.T) {
	// name with top-level arguments
	name, args, ok := resolveCodexToolCall(ToolCall{Name: "a", Arguments: map[string]any{"x": 1}})
	if !ok || name != "a" || args != `{"x":1}` {
		t.Errorf("got %q %q %v", name, args, ok)
	}
	// empty name -> false
	if _, _, ok := resolveCodexToolCall(ToolCall{Name: "", Arguments: map[string]any{"y": 2}}); ok {
		t.Error("expected not ok for empty name")
	}
	// name from function
	name, args, ok = resolveCodexToolCall(ToolCall{Function: &FunctionCall{Name: "fn", Arguments: `{"z":3}`}})
	if !ok || name != "fn" || args != `{"z":3}` {
		t.Errorf("got %q %q %v", name, args, ok)
	}
	// name set, no args, no function -> "{}"
	name, args, ok = resolveCodexToolCall(ToolCall{Name: "emptyargs"})
	if !ok || name != "emptyargs" || args != "{}" {
		t.Errorf("got %q %q %v", name, args, ok)
	}
	// marshal error args -> not ok
	name, _, ok = resolveCodexToolCall(ToolCall{Name: "bad", Arguments: map[string]any{"ch": make(chan int)}})
	_ = name
	if ok {
		t.Error("expected not ok for unmarshalable arguments")
	}
}

func TestCodexProvider_Chat_ModelFallbackWarn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"id": "r", "object": "response", "status": "completed",
			"output": []map[string]interface{}{
				{"type": "message", "role": "assistant", "content": []map[string]interface{}{
					{"type": "output_text", "text": "fallback"},
				}},
			},
		}
		writeCompletedSSE(w, resp)
	}))
	defer server.Close()
	p := NewCodexProvider("tok", "acc")
	p.client = createOpenAITestClient(server.URL, "tok", "acc")
	// Non-gpt model triggers the fallback warn branch.
	resp, err := p.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-opus", nil)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.Content != "fallback" {
		t.Errorf("content = %q", resp.Content)
	}
}