package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/keyring"
	"github.com/xilistudios/lele/pkg/providers"
)

// redactTestRecorder records messages added during a RunToolLoop run so tests
// can assert what actually reached session history / the LLM context.
type redactTestRecorder struct {
	mu       sync.Mutex
	messages []providers.Message
}

func (r *redactTestRecorder) AddFullMessage(sessionKey string, msg providers.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, msg)
}

func (r *redactTestRecorder) Save(string) error { return nil }

func (r *redactTestRecorder) toolContents() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, m := range r.messages {
		if m.Role == "tool" {
			out = append(out, m.Content)
		}
	}
	return out
}

// redactTool returns a tool whose ForLLM contains the given secret value.
func redactTool(t *testing.T, secret string) Tool {
	t.Helper()
	return &secretTool{t: t, secret: secret}
}

type secretTool struct {
	t      *testing.T
	secret string
}

func (s *secretTool) Name() string        { return "leaky" }
func (s *secretTool) Description() string { return "returns a secret" }
func (s *secretTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (s *secretTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: "result contains " + s.secret}
}

// runRedactLoop drives a single tool call and returns the tool messages the
// recorder captured.
func runRedactLoop(t *testing.T, cfg ToolLoopConfig, result string, sessionKey string) []string {
	t.Helper()

	// Provider: first call asks for the tool, second call returns a plain
	// answer terminating the loop.
	p := &redactScriptProvider{t: t, result: result}
	rec := &redactTestRecorder{}
	cfg.Provider = p
	cfg.Tools = NewToolRegistry()
	cfg.Tools.Register(redactTool(t, "sk-super-secret-value-123456"))
	cfg.MaxIterations = 5
	cfg.RetryWait = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time)
		close(ch)
		return ch
	}
	cfg.SessionRecorder = rec
	cfg.SessionKey = sessionKey

	messages := []providers.Message{{Role: "user", Content: "do it"}}
	if _, err := RunToolLoop(context.Background(), cfg, messages, "cli", "direct"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	return rec.toolContents()
}

// redactScriptProvider returns a tool call first, then a terminating answer.
type redactScriptProvider struct {
	t        *testing.T
	result   string
	toolName string
	mu       sync.Mutex
	calls    int
}

func (p *redactScriptProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls == 1 {
		toolName := p.toolName
		if toolName == "" {
			toolName = "leaky"
		}
		return &providers.LLMResponse{
			ToolCalls: []providers.ToolCall{{
				ID:   "call_1",
				Name: toolName,
				Arguments: map[string]interface{}{
					"type": "object",
				},
			}},
		}, nil
	}
	return &providers.LLMResponse{Content: p.result}, nil
}

func (p *redactScriptProvider) GetDefaultModel() string { return "test-model" }

// newRedactorTestService builds an opened keyring Service bound to a temp vault.
func newRedactorTestService(t *testing.T) *keyring.Service {
	t.Helper()
	// Reuse the same construction approach as the keyring tests: a file-backed
	// service over a temp dir so tests never touch the OS keychain.
	dir := t.TempDir()
	svc := keyring.NewService(keyring.ServiceConfig{
		Enabled:      true,
		VaultPath:    dir + "/keyring.enc",
		Backend:      keyring.BackendFile,
		AuditLogSize: 100,
		LeleDir:      dir,
	})
	if err := svc.EnsureOpen(); err != nil {
		t.Fatalf("open keyring service: %v", err)
	}
	return svc
}

func TestRunToolLoop_RedactsSecretFromToolResult(t *testing.T) {
	const secretVal = "sk-super-secret-value-123456" // >= 8 chars
	svc := newRedactorTestService(t)
	if err := svc.SetFromUI("gh.token", secretVal, "token desc", nil, nil, "tui"); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	redactor := keyring.NewRedactor(svc)

	contents := runRedactLoop(t, ToolLoopConfig{Redactor: redactor}, "done", "redact-session")
	if len(contents) == 0 {
		t.Fatal("expected at least one tool message")
	}
	got := strings.Join(contents, "\n")
	if !strings.Contains(got, "{{SECRET:gh.token}}") {
		t.Fatalf("expected placeholder in tool result, got %q", got)
	}
	if strings.Contains(got, secretVal) {
		t.Fatalf("raw secret value leaked into context: %q", got)
	}
}

func TestRunToolLoop_NilRedactorPassesThrough(t *testing.T) {
	const secretVal = "sk-super-secret-value-123456"
	// No Redactor set -> content must pass through unchanged (backward compat).
	contents := runRedactLoop(t, ToolLoopConfig{}, "done", "nil-redact-session")
	if len(contents) == 0 {
		t.Fatal("expected at least one tool message")
	}
	got := strings.Join(contents, "\n")
	if !strings.Contains(got, secretVal) {
		t.Fatalf("nil redactor must leave content unchanged, got %q", got)
	}
	if strings.Contains(got, "{{SECRET:") {
		t.Fatalf("nil redactor must not inject placeholders, got %q", got)
	}
}

func TestRunToolLoop_RedactsBeforeTruncation(t *testing.T) {
	const secretVal = "sk-super-secret-value-123456" // >= 8 chars, near the start
	svc := newRedactorTestService(t)
	if err := svc.SetFromUI("github.token", secretVal, "token desc", nil, nil, "tui"); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	redactor := keyring.NewRedactor(svc)

	// A tool result far longer than maxToolResultChars (50000). The secret sits
	// at the very beginning, which proves that (a) redaction ran before the
	// truncation path and (b) the {{SECRET:name}} placeholder survives
	// truncation intact in the retained prefix.
	result := secretVal + strings.Repeat("x", 60000)
	// Override the tool's ForLLM via scripted result content.
	contents := runRedactLoopWithResult(t, result, "done", "trunc-redact-session", redactor)
	if len(contents) == 0 {
		t.Fatal("expected at least one tool message")
	}
	got := contents[0]
	if !strings.Contains(got, "{{SECRET:github.token}}") {
		t.Fatalf("expected placeholder in truncated result, got %q (len %d)", got, len(got))
	}
	if strings.Contains(got, secretVal) {
		t.Fatalf("raw secret leaked beyond truncation: %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

// runRedactLoopWithResult is like runRedactLoop but lets the caller control the
// ForLLM content exactly (used for the truncation test).
func runRedactLoopWithResult(t *testing.T, forLLM, result, sessionKey string, redactor *keyring.Redactor) []string {
	t.Helper()
	p := &redactScriptProvider{t: t, result: result, toolName: "bigout"}
	rec := &redactTestRecorder{}
	cfg := ToolLoopConfig{
		Provider:        p,
		Tools:           NewToolRegistry(),
		MaxIterations:   5,
		SessionRecorder: rec,
		SessionKey:      sessionKey,
		Redactor:        redactor,
	}
	cfg.Tools.Register(&fixedResultTool{result: forLLM})
	cfg.RetryWait = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time)
		close(ch)
		return ch
	}

	messages := []providers.Message{{Role: "user", Content: "do it"}}
	if _, err := RunToolLoop(context.Background(), cfg, messages, "cli", "direct"); err != nil {
		t.Fatalf("RunToolLoop: %v", err)
	}
	return rec.toolContents()
}

// fixedResultTool returns a fixed ForLLM payload, useful for truncation tests.
type fixedResultTool struct {
	result string
}

func (f *fixedResultTool) Name() string        { return "bigout" }
func (f *fixedResultTool) Description() string { return "returns a big payload" }
func (f *fixedResultTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object"}
}
func (f *fixedResultTool) Execute(_ context.Context, _ map[string]interface{}) *ToolResult {
	return &ToolResult{ForLLM: f.result}
}