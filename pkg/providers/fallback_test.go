package providers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers/common"
)

func makeCandidate(provider, model string) FallbackCandidate {
	return FallbackCandidate{Provider: provider, Model: model}
}

func successRun(content string) func(ctx context.Context, provider, model string) (*LLMResponse, error) {
	return func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return &LLMResponse{Content: content, FinishReason: "stop"}, nil
	}
}

func failRun(err error) func(ctx context.Context, provider, model string) (*LLMResponse, error) {
	return func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return nil, err
	}
}

func TestFallback_SingleCandidate_Success(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}
	result, err := fc.Execute(context.Background(), candidates, successRun("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response.Content != "hello" {
		t.Errorf("content = %q, want hello", result.Response.Content)
	}
	if result.Provider != "openai" || result.Model != "gpt-4" {
		t.Errorf("provider/model = %s/%s, want openai/gpt-4", result.Provider, result.Model)
	}
}

func TestFallback_SecondCandidateSuccess(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(3, 100*time.Millisecond)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude-opus"),
	}

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if provider == "openai" {
			return nil, errors.New("rate limit exceeded")
		}
		return &LLMResponse{Content: "from claude", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}
	if result.Response.Content != "from claude" {
		t.Errorf("content = %q, want 'from claude'", result.Response.Content)
	}
}

func TestFallback_AllFail(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(2, 50*time.Millisecond)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
		makeCandidate("groq", "llama"),
	}

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return nil, errors.New("rate limit exceeded")
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error when all candidates fail")
	}
	var exhausted *FallbackExhaustedError
	if !errors.As(err, &exhausted) {
		t.Errorf("expected FallbackExhaustedError, got %T: %v", err, err)
	}
	if len(exhausted.Attempts) != 3 {
		t.Errorf("attempts = %d, want 3 (one per candidate)", len(exhausted.Attempts))
	}
}

func TestFallback_ContextCanceled(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	ctx, cancel := context.WithCancel(context.Background())
	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		if attempt == 1 {
			cancel()
			return nil, context.Canceled
		}
		t.Error("should not reach second candidate after cancel")
		return nil, nil
	}

	_, err := fc.Execute(ctx, candidates, run)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestFallback_NonRetriableError(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		return nil, errors.New("string should match pattern")
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error for non-retriable")
	}
	var fe *FailoverError
	if !errors.As(err, &fe) {
		t.Fatalf("expected FailoverError, got %T", err)
	}
	if fe.Reason != FailoverFormat {
		t.Errorf("reason = %q, want format", fe.Reason)
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1 (non-retriable should not retry or fallback)", attempt)
	}
}

func TestFallback_RetryWithBackoff(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(5, 50*time.Millisecond)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	callCount := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		callCount++
		if provider == "openai" && callCount <= 3 {
			return nil, errors.New("rate limit exceeded")
		}
		return &LLMResponse{Content: "success after retry", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response.Content != "success after retry" {
		t.Errorf("content = %q, want 'success after retry'", result.Response.Content)
	}
	if callCount != 4 {
		t.Errorf("callCount = %d, want 4 (3 retries + 1 success)", callCount)
	}
	if result.Provider != "openai" {
		t.Errorf("provider = %q, want openai (succeeded after retry)", result.Provider)
	}
}

func TestFallback_RetryExhaustedThenFallback(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(3, 50*time.Millisecond)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	openaiCalls := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if provider == "openai" {
			openaiCalls++
			return nil, errors.New("rate limit exceeded")
		}
		return &LLMResponse{Content: "from anthropic", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}
	if result.Response.Content != "from anthropic" {
		t.Errorf("content = %q, want 'from anthropic'", result.Response.Content)
	}
	if openaiCalls != 3 {
		t.Errorf("openaiCalls = %d, want 3 (maxRetryAttempts for this test)", openaiCalls)
	}
}

func TestFallback_CooldownSkip(t *testing.T) {
	now := time.Now()
	ct, _ := newTestTracker(now)
	fc := NewFallbackChain(ct)

	ct.MarkFailure("openai", FailoverRateLimit)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if provider == "openai" {
			t.Error("should not call openai (in cooldown)")
		}
		return &LLMResponse{Content: "claude response", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}
	skipped := 0
	for _, a := range result.Attempts {
		if a.Skipped {
			skipped++
		}
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
}

func TestFallback_AllInCooldown(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)
	var waits []time.Duration
	fc.sleepFn = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	ct.MarkFailure("openai", FailoverRateLimit)
	ct.MarkFailure("anthropic", FailoverBilling)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	_, err := fc.Execute(context.Background(), candidates,
		func(ctx context.Context, provider, model string) (*LLMResponse, error) {
			t.Error("should not call any provider (all in cooldown)")
			return nil, nil
		})

	if err == nil {
		t.Fatal("expected error when all in cooldown")
	}
	var exhausted *FallbackExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected FallbackExhaustedError, got %T", err)
	}
	if len(waits) != maxCooldownWaits {
		t.Fatalf("waits = %d, want %d (bounded all-cooldown waiting)", len(waits), maxCooldownWaits)
	}
	for i, w := range waits {
		if w <= 0 || w > fc.cooldownWaitCap() {
			t.Errorf("wait[%d] = %v, want (0, %v]", i, w, fc.cooldownWaitCap())
		}
	}
}

func TestExecute_AllInCooldownWaitsAndSucceeds(t *testing.T) {
	now := time.Now()
	ct, current := newTestTracker(now)
	fc := NewFallbackChain(ct)

	ct.MarkFailure("openai", FailoverRateLimit)
	ct.MarkFailure("anthropic", FailoverRateLimit)

	fc.sleepFn = func(ctx context.Context, d time.Duration) error {
		*current = current.Add(d)
		return nil
	}
	fc.randFn = func() float64 { return 0 }

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	calls := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		calls++
		return &LLMResponse{Content: "back from cooldown", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("expected success after waiting out the cooldown, got %v", err)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}
	if result.Response.Content != "back from cooldown" {
		t.Errorf("content = %q", result.Response.Content)
	}
	for _, a := range result.Attempts {
		if a.Skipped {
			t.Errorf("unexpected skipped attempt after a successful wait: %+v", a)
		}
	}
}

func TestExecute_AllInCooldownRespectsContext(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ct.MarkFailure("openai", FailoverRateLimit)

	_, err := fc.Execute(ctx, []FallbackCandidate{makeCandidate("openai", "gpt-4")},
		successRun("never"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestFallback_NoCandidates(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	_, err := fc.Execute(context.Background(), nil, successRun("ok"))
	if err == nil {
		t.Error("expected error for empty candidates")
	}
}

func TestFallback_EmptyFallbacks(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}
	result, err := fc.Execute(context.Background(), candidates, successRun("ok"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response.Content != "ok" {
		t.Error("expected success with single candidate")
	}
}

func TestFallback_UnknownErrorFallsBackToNextCandidate(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(1, time.Millisecond)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	var providersTried []string
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		providersTried = append(providersTried, provider)
		if provider == "openai" {
			return nil, errors.New("completely unknown internal error")
		}
		return &LLMResponse{Content: "from claude", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic (unknown error must not abort the chain)", result.Provider)
	}
	if len(providersTried) != 2 {
		t.Errorf("providersTried = %v, want both candidates", providersTried)
	}
}

func TestFallback_WrappedContextCanceledAborts(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(3, time.Millisecond)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		return nil, fmt.Errorf("stream read: %w", context.Canceled)
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1 (cancellation must not retry or fallback)", attempt)
	}
}

func TestFallback_SuccessResetsCooldown(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		if attempt == 1 {
			ct.MarkFailure("openai", FailoverRateLimit)
		}
		return &LLMResponse{Content: "ok", FinishReason: "stop"}, nil
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ct.IsAvailable("openai") {
		t.Error("success should reset cooldown")
	}
}

func TestImageFallback_Success(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4o")}
	result, err := fc.ExecuteImage(context.Background(), candidates, successRun("image result"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Response.Content != "image result" {
		t.Error("expected image result")
	}
}

func TestImageFallback_DimensionError(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4o"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		return nil, errors.New("image dimensions exceed max 4096x4096")
	}

	_, err := fc.ExecuteImage(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error for image dimension error")
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1 (image dimension error should not retry)", attempt)
	}
}

func TestImageFallback_SizeError(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4o"),
		makeCandidate("anthropic", "claude"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		return nil, errors.New("image exceeds 20 mb")
	}

	_, err := fc.ExecuteImage(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error for image size error")
	}
	if attempt != 1 {
		t.Errorf("attempt = %d, want 1 (image size error should not retry)", attempt)
	}
}

func TestImageFallback_RetryOnOtherErrors(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4o"),
		makeCandidate("anthropic", "claude-sonnet"),
	}

	attempt := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		attempt++
		if attempt == 1 {
			return nil, errors.New("rate limit exceeded")
		}
		return &LLMResponse{Content: "image ok", FinishReason: "stop"}, nil
	}

	result, err := fc.ExecuteImage(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}
}

func TestImageFallback_NoCandidates(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct)

	_, err := fc.ExecuteImage(context.Background(), nil, successRun("ok"))
	if err == nil {
		t.Error("expected error for empty candidates")
	}
}

func TestResolveCandidates_Simple(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "gpt-4",
		Fallbacks: []string{"anthropic:claude-opus", "groq:llama-3"},
	}

	candidates := ResolveCandidates(cfg, "openai")
	if len(candidates) != 3 {
		t.Fatalf("candidates = %d, want 3", len(candidates))
	}

	if candidates[0].Provider != "openai" || candidates[0].Model != "gpt-4" {
		t.Errorf("candidate[0] = %s/%s, want openai/gpt-4", candidates[0].Provider, candidates[0].Model)
	}
	if candidates[1].Provider != "anthropic" || candidates[1].Model != "claude-opus" {
		t.Errorf("candidate[1] = %s/%s, want anthropic/claude-opus", candidates[1].Provider, candidates[1].Model)
	}
	if candidates[2].Provider != "groq" || candidates[2].Model != "llama-3" {
		t.Errorf("candidate[2] = %s/%s, want groq/llama-3", candidates[2].Provider, candidates[2].Model)
	}
}

func TestResolveCandidates_Deduplication(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "openai:gpt-4",
		Fallbacks: []string{"openai:gpt-4", "anthropic:claude"},
	}

	candidates := ResolveCandidates(cfg, "default")
	if len(candidates) != 2 {
		t.Errorf("candidates = %d, want 2 (duplicate removed)", len(candidates))
	}
}

func TestResolveCandidates_EmptyFallbacks(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "gpt-4",
		Fallbacks: nil,
	}

	candidates := ResolveCandidates(cfg, "openai")
	if len(candidates) != 1 {
		t.Errorf("candidates = %d, want 1", len(candidates))
	}
}

func TestResolveCandidates_EmptyPrimary(t *testing.T) {
	cfg := ModelConfig{
		Primary:   "",
		Fallbacks: []string{"anthropic:claude"},
	}

	candidates := ResolveCandidates(cfg, "openai")
	if len(candidates) != 1 {
		t.Errorf("candidates = %d, want 1", len(candidates))
	}
}

func TestFallback_SingleCandidate_RetriableError_NoCooldown(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(1, 50*time.Millisecond)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return nil, errors.New("rate limit exceeded")
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error when single candidate fails")
	}
	var exhausted *FallbackExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected FallbackExhaustedError, got %T: %v", err, err)
	}

	if !ct.IsAvailable("openai") {
		t.Error("single candidate should NOT be put in cooldown on retriable error (no fallbacks to switch to)")
	}
	if ct.ErrorCount("openai") != 0 {
		t.Errorf("error count = %d, want 0 (no cooldown tracking for single candidate)", ct.ErrorCount("openai"))
	}
}

func TestFallback_TwoCandidates_RetriableError_AppliesCooldown(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(1, 50*time.Millisecond)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	openaiCalls := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if provider == "openai" {
			openaiCalls++
			return nil, errors.New("rate limit exceeded")
		}
		return &LLMResponse{Content: "from claude", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}

	if ct.IsAvailable("openai") {
		t.Error("first provider should be in cooldown when fallback candidates exist")
	}
	if ct.ErrorCount("openai") == 0 {
		t.Error("error count should be > 0 for first provider with fallbacks")
	}
}

func TestFallbackExhaustedError_Message(t *testing.T) {
	e := &FallbackExhaustedError{
		Attempts: []FallbackAttempt{
			{Provider: "openai", Model: "gpt-4", Error: errors.New("rate limited"), Reason: FailoverRateLimit, Duration: 500 * time.Millisecond},
			{Provider: "anthropic", Model: "claude", Skipped: true},
		},
	}
	msg := e.Error()
	if msg == "" {
		t.Error("expected non-empty error message")
	}
}

func TestIsRetriableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"context.Canceled", context.Canceled, false},
		{"wrapped context.Canceled", fmt.Errorf("wrapper: %w", context.Canceled), false},
		{
			"FallbackExhaustedError with retriable reason (timeout)",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{{Reason: FailoverTimeout}}},
			true,
		},
		{
			"FallbackExhaustedError with retriable reason (rate_limit)",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{{Reason: FailoverRateLimit}}},
			true,
		},
		{
			"FallbackExhaustedError with non-retriable reason (format)",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{{Reason: FailoverFormat}}},
			false,
		},
		{
			"FallbackExhaustedError with only skipped attempts (all in cooldown)",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{{Skipped: true, Reason: FailoverRateLimit}}},
			true,
		},
		{
			"FallbackExhaustedError all skipped with unknown reason",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{
				{Skipped: true},
				{Skipped: true, Reason: FailoverUnknown},
			}},
			true,
		},
		{
			"FallbackExhaustedError with unknown reason (transient by default)",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{{Reason: FailoverUnknown}}},
			true,
		},
		{
			"FallbackExhaustedError with unclassified attempt (empty reason)",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{{Reason: ""}}},
			true,
		},
		{
			"FallbackExhaustedError mixed: terminal + unknown",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{
				{Reason: FailoverAuth},
				{Reason: FailoverUnknown},
			}},
			true,
		},
		{
			"FallbackExhaustedError mixed: skipped + retriable",
			&FallbackExhaustedError{Attempts: []FallbackAttempt{
				{Skipped: true, Reason: FailoverRateLimit},
				{Reason: FailoverTimeout},
			}},
			true,
		},
		{
			"FailoverError retriable (rate_limit)",
			&FailoverError{Reason: FailoverRateLimit},
			true,
		},
		{
			"FailoverError retriable (timeout)",
			&FailoverError{Reason: FailoverTimeout},
			true,
		},
		{
			"FailoverError non-retriable (format)",
			&FailoverError{Reason: FailoverFormat},
			false,
		},
		{
			"generic error is transient by default",
			errors.New("weird sdk failure xyz"),
			true,
		},
		{
			"generic error boom",
			errors.New("boom"),
			true,
		},
		{
			"wrapped generic error",
			fmt.Errorf("call failed: %w", errors.New("totally novel provider failure")),
			true,
		},
		{
			"wrapped FallbackExhaustedError (errors.As)",
			fmt.Errorf("outer: %w", &FallbackExhaustedError{Attempts: []FallbackAttempt{{Reason: FailoverRateLimit}}}),
			true,
		},
		{
			"raw deadline exceeded (no fallback chain)",
			errors.New("failed to send request: context deadline exceeded"),
			true,
		},
		{
			"raw timeout awaiting response headers",
			errors.New("failed to send request: timeout awaiting response headers"),
			true,
		},
		{
			"raw TLS handshake timeout",
			errors.New("failed to send request: net/http: TLS handshake timeout"),
			true,
		},
		{
			"transient upstream 5xx body",
			errors.New("API request failed:\n  Status: 502\n  Body: bad gateway"),
			true,
		},
		{
			"raw auth error is not retried",
			errors.New("API request failed:\n  Status: 401\n  Body: invalid api key"),
			false,
		},
		{
			"raw billing error is not retried",
			errors.New("API request failed:\n  Status: 402\n  Body: insufficient credits"),
			false,
		},
		{
			"raw format error is not retried",
			errors.New("API request failed:\n  Status: 400\n  Body: invalid request format"),
			false,
		},
		{
			"cancelled context is never retried",
			fmt.Errorf("stream read: %w", context.Canceled),
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRetriableError(tt.err)
			if got != tt.want {
				t.Errorf("IsRetriableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestFallback_SingleCandidateGetsFullBudget(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(10, time.Millisecond)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}

	callCount := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		callCount++
		return nil, errors.New("rate limit exceeded")
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error when single candidate fails")
	}

	if callCount != 10 {
		t.Errorf("callCount = %d, want 10 (full retry budget, single candidate)", callCount)
	}
}

func TestExecute_SingleCandidateHonorsRetryAfterFloor(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryBudget(10, time.Millisecond, time.Minute)

	var clock time.Time
	start := clock
	now := func() time.Time { return clock }
	var waits []time.Duration
	fc.nowFn = now
	fc.randFn = func() float64 { return 0 }
	fc.sleepFn = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		clock = clock.Add(d)
		return nil
	}

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return nil, &common.APIError{StatusCode: 429, RetryAfter: 30 * time.Second, Body: "slow down"}
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error when the provider keeps rate limiting")
	}
	if len(waits) < 2 {
		t.Fatalf("waits = %v, want at least 2 backoff sleeps", waits)
	}
	honored := 0
	for _, w := range waits {
		if w >= 30*time.Second {
			honored++
		}
	}
	if honored < 2 {
		t.Errorf("waits = %v, want at least 2 waits >= 30s (server Retry-After floor)", waits)
	}
	if elapsed := clock.Sub(start); elapsed <= 3*time.Second {
		t.Errorf("chain gave up after %v, want > 3s (defect A regression)", elapsed)
	}
}

func TestFallback_MultipleCandidatesNoReduction(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(10, time.Millisecond)

	candidates := []FallbackCandidate{
		makeCandidate("openai", "gpt-4"),
		makeCandidate("anthropic", "claude"),
	}

	openaiCalls := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if provider == "openai" {
			openaiCalls++
			return nil, errors.New("rate limit exceeded")
		}
		return &LLMResponse{Content: "from claude", FinishReason: "stop"}, nil
	}

	result, err := fc.Execute(context.Background(), candidates, run)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}

	if openaiCalls != 10 {
		t.Errorf("openaiCalls = %d, want 10 (full retry budget with fallback)", openaiCalls)
	}
}

func TestIsRetriableError_AllSkippedCooldownIsTransient(t *testing.T) {
	err := &FallbackExhaustedError{Attempts: []FallbackAttempt{
		{Provider: "openai", Model: "gpt-4", Skipped: true, Reason: FailoverRateLimit},
		{Provider: "anthropic", Model: "claude", Skipped: true, Reason: FailoverRateLimit},
	}}
	if !IsRetriableError(err) {
		t.Error("all-skipped chain must be retriable (cooldown is transient)")
	}
}

func TestIsRetriableError_TransportErrorsAreTransient(t *testing.T) {
	msgs := []string{
		"unexpected EOF",
		"read tcp 10.0.0.5:443->1.2.3.4:443: connection reset by peer",
		"dial tcp: lookup api.openai.com: no such host",
		"net/http: TLS handshake timeout",
		`malformed HTTP response "\x00\x12"`,
		"stream disconnected before completion",
		"EOF",
		"openai: go stream error",
	}
	for _, msg := range msgs {
		err := fmt.Errorf("provider request failed: %w", errors.New(msg))
		if !IsRetriableError(err) {
			t.Errorf("IsRetriableError(%q) = false, want true", msg)
		}
	}
}

func TestIsRetriableError_TerminalBlacklist(t *testing.T) {
	terminal := []error{
		context.Canceled,
		fmt.Errorf("wrap: %w", context.Canceled),
		errors.New("context canceled"),
		&FailoverError{Reason: FailoverFormat},
		fmt.Errorf("outer: %w", &FailoverError{Reason: FailoverFormat}),
		&FallbackExhaustedError{Attempts: []FallbackAttempt{
			{Reason: FailoverFormat},
			{Reason: FailoverAuth},
			{Reason: FailoverBilling},
		}},
		errors.New("API request failed:\n  Status: 401\n  Body: invalid api key"),
		errors.New("API request failed:\n  Status: 402\n  Body: insufficient credits"),
		errors.New("API request failed:\n  Status: 400\n  Body: invalid request format"),
	}
	for _, err := range terminal {
		if IsRetriableError(err) {
			t.Errorf("IsRetriableError(%v) = true, want false (terminal)", err)
		}
	}

	for _, r := range []FailoverReason{FailoverAuth, FailoverBilling} {
		if !IsRetriableError(&FailoverError{Reason: r}) {
			t.Errorf("FailoverError(%s) must be retriable for the chain (next candidate)", r)
		}
	}
}

func TestIsRetriableError_NilIsFalse(t *testing.T) {
	if IsRetriableError(nil) {
		t.Error("nil error must not be retriable")
	}
}
