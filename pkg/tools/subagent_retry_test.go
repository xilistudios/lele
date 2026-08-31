package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// ============================================================================
// T8: the subagent path must decide "transient or not?" with exactly the same
// policy the parent agent loop uses (providers.IsRetriableError), instead of
// hand-rolled string whitelists. Both predicates below are thin delegates, so
// every case is checked twice: against an explicit expected value (documents
// the taxonomy) and against providers.IsRetriableError itself (proves the
// delegation, i.e. parent and subagent can never disagree again).
// ============================================================================

// transientPolicyCases returns the shared table used by both delegation tests.
//
// Note on *FailoverError{Auth}: a bare auth error is terminal for THAT provider
// but still candidate-swappable for the chain (see FailoverError.IsRetriable),
// so the provider policy reports it as retriable. That is intentional and is
// the whole point of delegating: the subagent inherits the parent's answer,
// whatever it is, instead of voting with its own string list.
func transientPolicyCases() []struct {
	name string
	err  error
	want bool
} {
	return []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "url.Error wrapping io.ErrUnexpectedEOF (the regression)",
			err:  &url.Error{Op: "Post", URL: "https://api.example.com/v1/chat", Err: io.ErrUnexpectedEOF},
			want: true,
		},
		{
			name: "url.Error connection reset by peer",
			err:  &url.Error{Op: "Post", URL: "https://api.example.com/v1/chat", Err: errors.New("read tcp: connection reset by peer")},
			want: true,
		},
		{
			name: "FallbackExhaustedError with one skipped (cooldown) attempt",
			err: &providers.FallbackExhaustedError{Attempts: []providers.FallbackAttempt{
				{Provider: "openrouter", Model: "m", Skipped: true},
			}},
			want: true,
		},
		{
			name: "FallbackExhaustedError with all format attempts",
			err: &providers.FallbackExhaustedError{Attempts: []providers.FallbackAttempt{
				{Provider: "openrouter", Model: "m", Reason: providers.FailoverFormat},
				{Provider: "anthropic", Model: "m", Reason: providers.FailoverFormat},
			}},
			want: false,
		},
		{
			name: "wrapped context.Canceled (user abort)",
			err:  fmt.Errorf("request: %w", context.Canceled),
			want: false,
		},
		{
			name: "bare FailoverError{Auth} (candidate-swappable)",
			err:  &providers.FailoverError{Reason: providers.FailoverAuth, Wrapped: errors.New("invalid api key")},
			want: true,
		},
		{
			name: "bare FailoverError{RateLimit}",
			err:  &providers.FailoverError{Reason: providers.FailoverRateLimit, Wrapped: errors.New("429")},
			want: true,
		},
		{
			name: "bare FailoverError{Format} (terminal)",
			err:  &providers.FailoverError{Reason: providers.FailoverFormat, Wrapped: errors.New("invalid request format")},
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}
}

func assertDelegatesToProviderPolicy(t *testing.T, name string, fn func(error) bool) {
	t.Helper()
	for _, tt := range transientPolicyCases() {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := fn(tt.err)
			if got != tt.want {
				t.Errorf("%s(%v) = %v, want %v", name, tt.err, got, tt.want)
			}
			if want := providers.IsRetriableError(tt.err); got != want {
				t.Errorf("%s(%v) = %v but providers.IsRetriableError = %v: predicate must delegate", name, tt.err, got, want)
			}
		})
	}
}

// TestIsTransientFailureDelegatesToProviderPolicy covers the subagent retry
// decision (pkg/tools/subagent_runner.go).
func TestIsTransientFailureDelegatesToProviderPolicy(t *testing.T) {
	assertDelegatesToProviderPolicy(t, "isTransientFailure", isTransientFailure)
}

// TestIsRetryableErrorDelegatesToProviderPolicy covers the per-LLM-call retry
// decision (pkg/tools/retry.go).
func TestIsRetryableErrorDelegatesToProviderPolicy(t *testing.T) {
	assertDelegatesToProviderPolicy(t, "isRetryableError", isRetryableError)
}

// ============================================================================
// End-to-end retry behaviour through runTask
// ============================================================================

// flakySubagentProvider fails the first `failures` calls with err and then
// returns `final`, so a test can assert that the retry loops actually re-ran
// the task. It implements only the non-streaming LLMProvider interface, so
// providers.ChatIdle calls Chat() directly.
type flakySubagentProvider struct {
	mu      sync.Mutex
	failAt  int
	failErr error
	final   string
	calls   int
}

func (p *flakySubagentProvider) next() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.failErr != nil && p.calls <= p.failAt {
		return "", p.failErr
	}
	return p.final, nil
}

func (p *flakySubagentProvider) callsCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *flakySubagentProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	content, err := p.next()
	if err != nil {
		return nil, err
	}
	return &providers.LLMResponse{Content: content}, nil
}

func (p *flakySubagentProvider) GetDefaultModel() string { return "test-model" }
func (p *flakySubagentProvider) SupportsTools() bool     { return false }
func (p *flakySubagentProvider) GetContextWindow() int   { return 4096 }

// transportErr is a realistic "the wire broke" error: exactly the shape that
// the old string whitelists missed and that killed sessions.
func transportErr() error {
	return &url.Error{Op: "Post", URL: "https://api.example.com/v1/chat", Err: io.ErrUnexpectedEOF}
}

// withNoRetrySleep replaces the package's backoff sleep seam (retrySleep in
// retry.go) with an instantly-ready channel so retry paths run in tests without
// wall-clock waits. Production always uses time.After; both the per-call retry
// loop and the subagent task retry select on it alongside ctx.Done(), so
// cancellation behaviour is identical. pkg/tools tests do not use
// t.Parallel, so swapping the package-level var is safe.
func withNoRetrySleep(t *testing.T) {
	t.Helper()
	prev := retrySleep
	retrySleep = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	t.Cleanup(func() { retrySleep = prev })
}

// TestSubagentTransientFailureRetries proves Change 2 + Change 4: a transport
// failure must be classified transient from the real error (not from the
// rendered result string) and must actually re-run the task.
func TestSubagentTransientFailureRetries(t *testing.T) {
	withNoRetrySleep(t)

	provider := &flakySubagentProvider{
		// The inner ChatWithRetry loop gets the first look at the transport
		// error and burns its whole budget (MaxRetries attempts + 1), so the
		// outer runTask retry is what actually recovers the task.
		failAt:  DefaultRetryConfig().MaxRetries + 1,
		failErr: transportErr(),
		final:   "STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted after retry",
	}
	manager := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)
	manager.SetDefaultMaxRetries(2)

	task := &SubagentTask{
		ID:               "subagent-retry-1",
		Task:             "do the thing",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusPending,
		MaxRetries:       2,
	}

	manager.runTask(context.Background(), task, nil)

	snap := task.Snapshot()
	if snap.Status != SubagentStatusCompleted {
		t.Fatalf("status = %q, want %q (result=%q)", snap.Status, SubagentStatusCompleted, snap.Result)
	}
	if snap.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", snap.RetryCount)
	}
	if calls := provider.callsCount(); calls < 2 {
		t.Errorf("provider calls = %d, want >= 2 (the retry must re-run the LLM)", calls)
	}
	// A successful run must not leave a stale error behind for the next one.
	manager.mu.Lock()
	lastErr := task.lastErr
	manager.mu.Unlock()
	if lastErr != nil {
		t.Errorf("lastErr = %v, want nil after success", lastErr)
	}
}

// TestSubagentTerminalFailureDoesNotRetry proves the blacklist side: a terminal
// failure is not retried. Uses a chain whose every attempt is a format error,
// which providers.IsRetriableError rejects outright (a bare auth FailoverError
// is deliberately retriable for the chain, see FailoverError.IsRetriable).
func TestSubagentTerminalFailureDoesNotRetry(t *testing.T) {
	withNoRetrySleep(t)

	provider := &flakySubagentProvider{
		failAt: 99,
		failErr: &providers.FailoverError{
			Reason:   providers.FailoverFormat,
			Provider: "test",
			Wrapped:  errors.New("invalid request format"),
		},
		final: "STATUS: completed\nSUMMARY: Should not be reached\nDETAILS:\n-",
	}
	manager := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)
	manager.SetDefaultMaxRetries(2)

	task := &SubagentTask{
		ID:               "subagent-retry-2",
		Task:             "do the thing",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusPending,
		MaxRetries:       2,
	}

	manager.runTask(context.Background(), task, nil)

	snap := task.Snapshot()
	if snap.Status != SubagentStatusFailed {
		t.Fatalf("status = %q, want %q", snap.Status, SubagentStatusFailed)
	}
	if snap.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0 (terminal failure must not retry)", snap.RetryCount)
	}
	if calls := provider.callsCount(); calls != 1 {
		t.Errorf("provider calls = %d, want 1 (no re-run)", calls)
	}
	// Change 3: the rendered error type is the provider layer's reason, not the
	// old local taxonomy. "format" replaces what used to be reported as
	// "http_timeout"/"rate_limited"/"connection_error"/"server_error"/"unknown".
	if !strings.HasPrefix(snap.Result, "Error [format]:") {
		t.Errorf("Result = %q, want prefix %q", snap.Result, "Error [format]:")
	}
	if !strings.Contains(snap.Summary, "Subagent execution failed [format]") {
		t.Errorf("Summary = %q, want it to name the classified reason", snap.Summary)
	}
}
