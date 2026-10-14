package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/xilistudios/lele/pkg/auth"
	anthropicprovider "github.com/xilistudios/lele/pkg/providers/anthropic"
)

func TestClaudeProvider_ChatRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := map[string]interface{}{
			"id":          "msg_test",
			"type":        "message",
			"role":        "assistant",
			"model":       reqBody["model"],
			"stop_reason": "end_turn",
			"content": []map[string]interface{}{
				{"type": "text", "text": "Hello! How can I help you?"},
			},
			"usage": map[string]interface{}{
				"input_tokens":  15,
				"output_tokens": 8,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	delegate := anthropicprovider.NewProviderWithClient(createAnthropicTestClient(server.URL, "test-token"))
	provider := newClaudeProviderWithDelegate(delegate)

	messages := []Message{{Role: "user", Content: "Hello"}}
	resp, err := provider.Chat(t.Context(), messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "Hello! How can I help you?" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello! How can I help you?")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage.PromptTokens != 15 {
		t.Errorf("PromptTokens = %d, want 15", resp.Usage.PromptTokens)
	}
}

func TestClaudeProvider_GetDefaultModel(t *testing.T) {
	p := NewClaudeProvider("test-token")
	if got := p.GetDefaultModel(); got != "claude-sonnet-4-5-20250929" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "claude-sonnet-4-5-20250929")
	}
}

// TestClaudeProviderOAuthModeThreading pins that the oauth flag passed to the
// constructor reaches the anthropic delegate unchanged.
func TestClaudeProviderOAuthModeThreading(t *testing.T) {
	for _, oauth := range []bool{true, false} {
		name := "oauth_disabled"
		if oauth {
			name = "oauth_enabled"
		}
		t.Run(name, func(t *testing.T) {
			p := NewClaudeProviderWithTokenSourceBaseURLAndOAuthMode(
				"test-token",
				func() (string, error) { return "test-token", nil },
				"https://api.anthropic.com",
				oauth,
			)
			if got := p.delegate.OAuthMode(); got != oauth {
				t.Errorf("delegate.OAuthMode() = %v, want %v", got, oauth)
			}
		})
	}
}

func createAnthropicTestClient(baseURL, token string) *anthropic.Client {
	c := anthropic.NewClient(
		anthropicoption.WithAuthToken(token),
		anthropicoption.WithBaseURL(baseURL),
	)
	return &c
}

// --- createClaudeTokenSource -------------------------------------------------

// setupClaudeTokenSourceTest isolates the credential store used by the token
// source: a throwaway LELE_CONFIG_DIR plus UseStore(nil) selects the legacy
// JSON backend. UseStore(nil) is the only store seam exported to this package
// (the in-package SQLite helpers of pkg/auth are not importable), and the
// legacy backend is exactly what an isolated config dir makes safe to write to.
func setupClaudeTokenSourceTest(t *testing.T) {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	auth.UseStore(nil)
}

// claudeTokenServer is a fake Claude token endpoint that counts requests and
// rotates the refresh token, like the real one does.
type claudeTokenServer struct {
	*httptest.Server
	mu   sync.Mutex
	hits int
}

func (s *claudeTokenServer) HitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

// newClaudeTokenServer starts the fake endpoint. The response must carry an
// inference scope (or no scope at all) because the Claude preset enables the
// RequireInferenceScope gate; a scope-less fresh token would be rejected.
func newClaudeTokenServer(t *testing.T) *claudeTokenServer {
	t.Helper()
	ts := &claudeTokenServer{}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.mu.Lock()
		ts.hits++
		ts.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fresh-access","refresh_token":"rotated-refresh","expires_in":3600,"scope":"user:inference"}`))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// pointClaudeOAuthConfigAt redirects the Claude token endpoint to the fake
// server by overriding the package-level seam, restoring its original value
// when the test ends.
func pointClaudeOAuthConfigAt(t *testing.T, srv *httptest.Server) {
	t.Helper()
	original := claudeOAuthConfig
	claudeOAuthConfig = func() auth.OAuthProviderConfig {
		cfg := auth.ClaudeOAuthConfig()
		cfg.TokenURL = srv.URL
		cfg.HTTPClient = srv.Client()
		return cfg
	}
	t.Cleanup(func() { claudeOAuthConfig = original })
}

// storedClaudeCredential builds an expired credential that NeedsRefresh() and
// carries the given refresh token.
func storedClaudeCredential(refreshToken, authMethod string) *auth.AuthCredential {
	return &auth.AuthCredential{
		AccessToken:  "stale-access",
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(-time.Hour),
		Provider:     "anthropic",
		AuthMethod:   authMethod,
	}
}

// TestCreateClaudeTokenSourceUsesCoordinator is the mandatory mutation #4
// guard: two concurrent callers of the token source must collapse into exactly
// ONE token-endpoint request (single-flight) and both must observe the fresh
// access token, with the rotated refresh token persisted.
func TestCreateClaudeTokenSourceUsesCoordinator(t *testing.T) {
	setupClaudeTokenSourceTest(t)
	srv := newClaudeTokenServer(t)
	pointClaudeOAuthConfigAt(t, srv.Server)

	if err := auth.SetCredential("anthropic", storedClaudeCredential("old-refresh", "oauth")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	tokenSource := createClaudeTokenSource()

	const callers = 2
	var wg sync.WaitGroup
	tokens := make([]string, callers)
	errs := make([]error, callers)
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = tokenSource()
		}(i)
	}
	wg.Wait()

	if got := srv.HitCount(); got != 1 {
		t.Errorf("token endpoint hits = %d, want exactly 1 (single-flight)", got)
	}
	for i := range errs {
		if errs[i] != nil {
			t.Errorf("caller %d error: %v", i, errs[i])
		}
		if tokens[i] != "fresh-access" {
			t.Errorf("caller %d token = %q, want %q", i, tokens[i], "fresh-access")
		}
	}

	stored, err := auth.GetCredential("anthropic")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if stored == nil {
		t.Fatal("GetCredential() = nil, want the rotated credential")
	}
	if stored.RefreshToken != "rotated-refresh" {
		t.Errorf("stored RefreshToken = %q, want %q", stored.RefreshToken, "rotated-refresh")
	}
	if stored.AccessToken != "fresh-access" {
		t.Errorf("stored AccessToken = %q, want %q", stored.AccessToken, "fresh-access")
	}
}

// TestCreateClaudeTokenSourceWithoutCredentials pins the exact user-facing
// message shown when nothing is stored for anthropic.
func TestCreateClaudeTokenSourceWithoutCredentials(t *testing.T) {
	setupClaudeTokenSourceTest(t)
	srv := newClaudeTokenServer(t)
	pointClaudeOAuthConfigAt(t, srv.Server)

	token, err := createClaudeTokenSource()()
	if err == nil {
		t.Fatal("createClaudeTokenSource() error = nil, want non-nil")
	}
	const want = "no credentials for anthropic. Run: lele auth login --provider anthropic"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
	if got := srv.HitCount(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0", got)
	}
}

// TestCreateClaudeTokenSourceSkipsRefreshForTokenCredential covers the
// RequireOAuthMethod policy: an expired paste-token credential carrying a
// refresh token must be returned as-is, never refreshed.
func TestCreateClaudeTokenSourceSkipsRefreshForTokenCredential(t *testing.T) {
	setupClaudeTokenSourceTest(t)
	srv := newClaudeTokenServer(t)
	pointClaudeOAuthConfigAt(t, srv.Server)

	if err := auth.SetCredential("anthropic", storedClaudeCredential("token-refresh", "token")); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	token, err := createClaudeTokenSource()()
	if err != nil {
		t.Fatalf("createClaudeTokenSource() error: %v", err)
	}
	if token != "stale-access" {
		t.Errorf("token = %q, want the stored %q", token, "stale-access")
	}
	if got := srv.HitCount(); got != 0 {
		t.Errorf("token endpoint hits = %d, want 0 (RequireOAuthMethod policy)", got)
	}
}
