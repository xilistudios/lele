package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// retryProvider is a fake LLMProvider that returns a scripted sequence of
// (response, error) outcomes for each Chat call.
type retryProvider struct {
	mu            chan struct{}
	results       []*providers.LLMResponse
	errs          []error
	calls         int
	lastModel     string
	lastOptions   map[string]interface{}
	lastMessages  []providers.Message
	lastTools     []providers.ToolDefinition
}

func newRetryProvider() *retryProvider {
	return &retryProvider{mu: make(chan struct{}, 1)}
}

func (p *retryProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	p.mu <- struct{}{}
	defer func() { <-p.mu }()

	p.calls++
	p.lastModel = model
	p.lastOptions = options
	p.lastMessages = messages
	p.lastTools = tools

	// Return error if scripted for this call.
	if p.calls-1 < len(p.errs) && p.errs[p.calls-1] != nil {
		return nil, p.errs[p.calls-1]
	}
	if p.calls-1 < len(p.results) {
		return p.results[p.calls-1], nil
	}
	return &providers.LLMResponse{Content: "default"}, nil
}

func (p *retryProvider) GetDefaultModel() string { return "retry-model" }

type retryLLMResponse = providers.LLMResponse

// TestChatWithRetry_successNoRetry verifies a single successful call.
func TestChatWithRetry_successNoRetry(t *testing.T) {
	p := newRetryProvider()
	p.results = []*providers.LLMResponse{{Content: "hello"}}

	resp, err := ChatWithRetry(context.Background(), p, nil, nil, "m", nil, DefaultRetryConfig())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("content = %q, want hello", resp.Content)
	}
	if p.calls != 1 {
		t.Fatalf("calls = %d, want 1", p.calls)
	}
}

// TestChatWithRetry_retriesRetryableThenSucceeds verifies a retryable error is
// retried then succeeds. Uses a tiny BaseDelay to keep it fast.
func TestChatWithRetry_retriesRetryableThenSucceeds(t *testing.T) {
	p := newRetryProvider()
	p.errs = []error{errors.New("connection refused")}
	p.results = []*retryLLMResponse{nil, {Content: "after retry"}}

	cfg := DefaultRetryConfig()
	cfg.BaseDelay = time.Millisecond
	cfg.MaxDelay = time.Millisecond

	resp, err := ChatWithRetry(context.Background(), p, nil, nil, "m", nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "after retry" {
		t.Fatalf("content = %q", resp.Content)
	}
	if p.calls != 2 {
		t.Fatalf("calls = %d, want 2", p.calls)
	}
}

// TestChatWithRetry_retryOnAll verifies RetryOnAll retries non-retryable errors.
func TestChatWithRetry_retryOnAll(t *testing.T) {
	p := newRetryProvider()
	p.errs = []error{errors.New("400 bad request")}
	p.results = []*retryLLMResponse{nil, {Content: "ok"}}

	cfg := DefaultRetryConfig()
	cfg.RetryOnAll = true
	cfg.BaseDelay = time.Millisecond
	cfg.MaxDelay = time.Millisecond

	resp, err := ChatWithRetry(context.Background(), p, nil, nil, "m", nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q", resp.Content)
	}
	if p.calls != 2 {
		t.Fatalf("calls = %d, want 2", p.calls)
	}
}

// TestChatWithRetry_nonRetryableNotRetried verifies non-retryable errors with
// RetryOnAll=false are returned immediately.
func TestChatWithRetry_nonRetryableNotRetried(t *testing.T) {
	p := newRetryProvider()
	p.errs = []error{errors.New("401 unauthorized")}

	cfg := DefaultRetryConfig()
	cfg.BaseDelay = time.Millisecond
	cfg.MaxDelay = time.Millisecond

	_, err := ChatWithRetry(context.Background(), p, nil, nil, "m", nil, cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "401 unauthorized" {
		t.Fatalf("err = %v", err)
	}
	if p.calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry for non-retryable)", p.calls)
	}
}

// TestChatWithRetry_exhaustsRetries verifies the last error is returned after
// MaxRetries attempts.
func TestChatWithRetry_exhaustsRetries(t *testing.T) {
	p := newRetryProvider()
	retryErr := errors.New("service unavailable")
	p.errs = []error{retryErr, retryErr, retryErr, retryErr}

	cfg := DefaultRetryConfig()
	cfg.MaxRetries = 3
	cfg.BaseDelay = time.Millisecond
	cfg.MaxDelay = time.Millisecond

	_, err := ChatWithRetry(context.Background(), p, nil, nil, "m", nil, cfg)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if err != retryErr {
		t.Fatalf("err = %v, want lastErr", err)
	}
	// 4 calls: attempt 0,1,2,3
	if p.calls != 4 {
		t.Fatalf("calls = %d, want 4", p.calls)
	}
}

// TestChatWithRetry_noRetries verifies MaxRetries=0 means a single attempt with
// no retry even for retryable errors.
func TestChatWithRetry_noRetries(t *testing.T) {
	p := newRetryProvider()
	p.errs = []error{errors.New("timeout")}

	cfg := DefaultRetryConfig()
	cfg.MaxRetries = 0

	_, err := ChatWithRetry(context.Background(), p, nil, nil, "m", nil, cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if p.calls != 1 {
		t.Fatalf("calls = %d, want 1", p.calls)
	}
}

// TestChatWithRetry_cancelledContext verifies a cancelled context returns early.
func TestChatWithRetry_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := newRetryProvider()
	p.errs = []error{errors.New("connection refused")}

	cfg := DefaultRetryConfig()
	cfg.BaseDelay = time.Millisecond

	_, err := ChatWithRetry(ctx, p, nil, nil, "m", nil, cfg)
	if err == nil {
		t.Fatal("expected context error")
	}
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if p.calls != 0 {
		t.Fatalf("calls = %d, want 0 (context cancelled before attempt)", p.calls)
	}
}

// TestCalculateDelay_capsAtMax verifies cap and the max-multiplier path once more.
func TestCalculateDelay_capsAtMax(t *testing.T) {
	cfg := RetryConfig{BaseDelay: time.Second, MaxDelay: 3 * time.Second, Multiplier: 100}
	// attempt=1: 1s * 100^1 = 100s, capped to 3s
	if got := calculateDelay(cfg, 1); got != 3*time.Second {
		t.Fatalf("delay = %v, want capped 3s", got)
	}
}

// TestIsRetryableError_additional verifies a few extra retryable strings.
func TestIsRetryableError_additional(t *testing.T) {
	msgs := []string{
		"context deadline exceeded",
		"i/o timeout",
		"broken pipe",
		"unexpected end of JSON input",
		"bad gateway",
		"gateway timeout",
	}
	for _, sm := range msgs {
		if !isRetryableError(errors.New(sm)) {
			t.Errorf("expected retryable for %q", sm)
		}
	}
}