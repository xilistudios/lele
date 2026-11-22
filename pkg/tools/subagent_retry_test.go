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

func TestIsTransientFailureDelegatesToProviderPolicy(t *testing.T) {
	assertDelegatesToProviderPolicy(t, "isTransientFailure", isTransientFailure)
}

func TestIsRetryableErrorDelegatesToProviderPolicy(t *testing.T) {
	assertDelegatesToProviderPolicy(t, "isRetryableError", isRetryableError)
}

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

func transportErr() error {
	return &url.Error{Op: "Post", URL: "https://api.example.com/v1/chat", Err: io.ErrUnexpectedEOF}
}

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

func TestSubagentTransientFailureRetries(t *testing.T) {
	withNoRetrySleep(t)

	provider := &flakySubagentProvider{
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
	manager.mu.Lock()
	lastErr := task.lastErr
	manager.mu.Unlock()
	if lastErr != nil {
		t.Errorf("lastErr = %v, want nil after success", lastErr)
	}
}

func TestSubagentTransientFailureRetriesWithRegisteredCancel(t *testing.T) {
	withNoRetrySleep(t)

	innerMax := DefaultRetryConfig().MaxRetries
	provider := &flakySubagentProvider{
		failAt:  innerMax + 1,
		failErr: transportErr(),
		final:   "STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted after retry",
	}
	manager := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)
	manager.SetDefaultMaxRetries(2)

	taskCtx, taskCancel := context.WithCancel(context.Background())
	defer taskCancel()

	task := &SubagentTask{
		ID:               "subagent-retry-cancel-1",
		Task:             "do the thing",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusPending,
		MaxRetries:       2,
	}

	manager.AddTaskForTest(task, taskCancel)

	manager.runTask(taskCtx, task, nil)

	snap := task.Snapshot()
	if snap.Status != SubagentStatusCompleted {
		t.Fatalf("AGT-01 BUG REPRODUCED: status = %q, want %q (result=%q). "+
			"runTaskImpl's defer cancelled taskCtx, so the retry select saw ctx.Done() "+
			"and gave up instead of retrying.",
			snap.Status, SubagentStatusCompleted, snap.Result)
	}
	if snap.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", snap.RetryCount)
	}
	if calls := provider.callsCount(); calls < innerMax+2 {
		t.Errorf("provider calls = %d, want >= %d (retry must re-run LLM)", calls, innerMax+2)
	}
}

func TestSubagentExplicitCancelDuringBackoff(t *testing.T) {
	innerMax := DefaultRetryConfig().MaxRetries

	unblock := make(chan struct{})
	prev := retrySleep
	retrySleep = func(d time.Duration) <-chan time.Time {
		<-unblock
		ch := make(chan time.Time, 1)
		ch <- time.Time{}
		return ch
	}
	t.Cleanup(func() { retrySleep = prev })

	provider := &flakySubagentProvider{
		failAt:  innerMax + 1,
		failErr: transportErr(),
		final:   "STATUS: completed\nSUMMARY: Should not reach\nDETAILS:\n-",
	}
	manager := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)
	manager.SetDefaultMaxRetries(2)

	taskCtx, taskCancel := context.WithCancel(context.Background())
	defer taskCancel()

	task := &SubagentTask{
		ID:               "subagent-cancel-backoff-1",
		Task:             "do the thing",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusPending,
		MaxRetries:       2,
	}

	manager.AddTaskForTest(task, taskCancel)

	done := make(chan struct{})
	go func() {
		manager.runTask(taskCtx, task, nil)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	taskCancel()

	close(unblock)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runTask did not return after cancellation")
	}

	snap := task.Snapshot()
	if snap.Status != SubagentStatusFailed {
		t.Errorf("status = %q, want %q (terminal failure, no retry)", snap.Status, SubagentStatusFailed)
	}
	if snap.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0 (cancelled task must not retry)", snap.RetryCount)
	}
	if !strings.Contains(snap.Summary, "cancelled") && !strings.Contains(snap.Summary, "failed") {
		t.Errorf("summary = %q, want it to mention cancellation or failure", snap.Summary)
	}
}

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
	if !strings.HasPrefix(snap.Result, "Error [format]:") {
		t.Errorf("Result = %q, want prefix %q", snap.Result, "Error [format]:")
	}
	if !strings.Contains(snap.Summary, "Subagent execution failed [format]") {
		t.Errorf("Summary = %q, want it to name the classified reason", snap.Summary)
	}
}
