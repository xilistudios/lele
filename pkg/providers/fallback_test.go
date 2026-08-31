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

	// Use retriable error - retry will exhaust then fallback to second candidate
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		if provider == "openai" {
			// Retriable error: will retry 3 times, then fallback
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
	// With retry logic, each candidate exhausts retries before moving to next
	// But we only record 1 attempt per candidate in the final error
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
			cancel() // cancel context
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
			// Fail first 3 calls with retriable error, succeed on 4th
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
	// Should succeed on 4th call (3 retries on first candidate)
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
	// After maxRetryAttempts on openai, should fallback to anthropic
	if result.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", result.Provider)
	}
	if result.Response.Content != "from anthropic" {
		t.Errorf("content = %q, want 'from anthropic'", result.Response.Content)
	}
	// OpenAI should be called maxRetryAttempts times before fallback
	if openaiCalls != 3 {
		t.Errorf("openaiCalls = %d, want 3 (maxRetryAttempts for this test)", openaiCalls)
	}
}

func TestFallback_CooldownSkip(t *testing.T) {
	now := time.Now()
	ct, _ := newTestTracker(now)
	fc := NewFallbackChain(ct)

	// Put openai in cooldown
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
	// Should have 1 skipped attempt
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
	// T3 made an all-cooldown chain wait instead of failing instantly, so the
	// real 90s cap would slow this test down. A no-op fake sleep keeps it
	// instant while still exercising the wait loop (the cooldown, driven by the
	// real clock, never expires here, so the chain must still give up).
	var waits []time.Duration
	fc.sleepFn = func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	// Put all providers in cooldown
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
	// It waited the maximum number of rounds before declaring the chain dead,
	// and each wait was bounded by maxCooldownWait.
	if len(waits) != maxCooldownWaits {
		t.Fatalf("waits = %d, want %d (bounded all-cooldown waiting)", len(waits), maxCooldownWaits)
	}
	for i, w := range waits {
		if w <= 0 || w > fc.cooldownWaitCap() {
			t.Errorf("wait[%d] = %v, want (0, %v]", i, w, fc.cooldownWaitCap())
		}
	}
}

// TestExecute_AllInCooldownWaitsAndSucceeds is defect C's fix: a chain whose
// candidates are all in cooldown must block until the earliest one frees up
// and then succeed, instead of returning FallbackExhaustedError for the caller
// to retry a millisecond later against the same wall.
func TestExecute_AllInCooldownWaitsAndSucceeds(t *testing.T) {
	now := time.Now()
	ct, current := newTestTracker(now)
	fc := NewFallbackChain(ct)

	// Both providers punished with the 1-minute first rung of the ladder.
	ct.MarkFailure("openai", FailoverRateLimit)
	ct.MarkFailure("anthropic", FailoverRateLimit)

	// The fake sleep advances the tracker's clock past the cooldown, which is
	// exactly what a real 60s wait does - without the test sleeping.
	fc.sleepFn = func(ctx context.Context, d time.Duration) error {
		*current = current.Add(d)
		return nil
	}
	fc.randFn = func() float64 { return 0 } // deterministic jitter

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
	// The skipped attempts must not be reported: the chain never gave up.
	for _, a := range result.Attempts {
		if a.Skipped {
			t.Errorf("unexpected skipped attempt after a successful wait: %+v", a)
		}
	}
}

// TestExecute_AllInCooldownRespectsContext: the wait is still tied to ctx, so a
// cancelled turn cannot be blocked by a cooldown wait.
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
	// Single primary, no fallbacks: should work like direct call
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

// TestFallback_UnknownErrorFallsBackToNextCandidate replaces the former
// TestFallback_UnclassifiedError, which asserted that an unrecognised error
// must abort the chain after a single call. That was the bug: transport
// failures (unexpected EOF, connection reset, ...) are unrecognised yet fully
// recoverable, so aborting killed sessions that a retry would have saved.
// Under default-to-transient an unknown error is retriable and the chain moves
// on to the next candidate.
func TestFallback_UnknownErrorFallsBackToNextCandidate(t *testing.T) {
	ct := NewCooldownTracker()
	// 1 retry per candidate + ~0s backoff: keeps the test fast while exercising
	// the (now-backing-off) unknown path.
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

// TestFallback_WrappedContextCanceledAborts is the counterpart: cancellation is
// the ONE case ClassifyError still refuses to classify, so the chain must stop
// without trying the next candidate.
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
			ct.MarkFailure("openai", FailoverRateLimit) // simulate failure tracked elsewhere
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

// --- Image Fallback Tests ---

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

// --- ResolveCandidates Tests ---

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
	// Bug fix: with a single candidate (no fallbacks), a retriable error
	// should NOT apply cooldown because there's no alternative to switch to.
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(1, 50*time.Millisecond)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}

	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		return nil, errors.New("rate limit exceeded") // retriable
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error when single candidate fails")
	}
	var exhausted *FallbackExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected FallbackExhaustedError, got %T: %v", err, err)
	}

	// CRITICAL: provider should still be available (no cooldown applied)
	if !ct.IsAvailable("openai") {
		t.Error("single candidate should NOT be put in cooldown on retriable error (no fallbacks to switch to)")
	}
	if ct.ErrorCount("openai") != 0 {
		t.Errorf("error count = %d, want 0 (no cooldown tracking for single candidate)", ct.ErrorCount("openai"))
	}
}

func TestFallback_TwoCandidates_RetriableError_AppliesCooldown(t *testing.T) {
	// Control: with two candidates, a retriable error on the first SHOULD apply
	// cooldown because there is a fallback to switch to.
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
			return nil, errors.New("rate limit exceeded") // retriable
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

	// CRITICAL: openai SHOULD be in cooldown (there's a fallback candidate)
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

// ============================================================================
// Tests for IsRetriableError
// ============================================================================

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
		// Updated by T1: a chain where every candidate is in cooldown is not a
		// failure, it is a "wait, the cooldown will expire". The old `false`
		// made an all-skipped chain terminal and killed the turn.
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
		// The bug-catch case: an unrecognised error used to be reported as
		// non-retriable (false) and ended the agent's turn.
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
		// Raw transport errors from the non-streaming path are never wrapped in
		// a FailoverError. A single timeout must not abort the whole agent run.
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
		// Permanent failures must fail fast, never be hammered with retries.
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

// ============================================================================
// Tests for the per-candidate retry budget
// ============================================================================

// TestFallback_SingleCandidateGetsFullBudget is the anti-regression test for
// defect A. The old code capped a single-candidate chain at
// singleCandidateMaxRetries (3) with a 1s/2s ladder, so a 429 carrying
// `Retry-After: 30` was retried at t=0, 1s and 3s: the chain died in ~3
// seconds while the provider's quota window was still open. One candidate is
// exactly the case that needs MORE budget, not less, so the special case is
// gone: a lone provider now gets the same attempt count and the full
// wall-clock budget as the first candidate of a multi-provider chain.
func TestFallback_SingleCandidateGetsFullBudget(t *testing.T) {
	ct := NewCooldownTracker()
	fc := NewFallbackChain(ct).WithRetryConfig(10, time.Millisecond)

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}

	callCount := 0
	run := func(ctx context.Context, provider, model string) (*LLMResponse, error) {
		callCount++
		return nil, errors.New("rate limit exceeded") // retriable
	}

	_, err := fc.Execute(context.Background(), candidates, run)
	if err == nil {
		t.Fatal("expected error when single candidate fails")
	}

	// The single candidate is no longer truncated to 3 attempts.
	if callCount != 10 {
		t.Errorf("callCount = %d, want 10 (full retry budget, single candidate)", callCount)
	}
}

// TestExecute_SingleCandidateHonorsRetryAfterFloor proves the Retry-After hint
// is actually consumed: the waits the chain performs must be at least the
// server's ask, not the 1s/2s exponential ladder. A fake clock/sleep keeps the
// test instant while still exercising the real budget arithmetic.
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
		clock = clock.Add(d) // the fake clock advances with every wait
		return nil
	}

	candidates := []FallbackCandidate{makeCandidate("openai", "gpt-4")}
	// The realistic shape: providers surface 429 as *common.APIError, which is
	// what carries the hint through ClassifyError's structured path.
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
	// The first waits are the server's ask; only the tail may be clamped to
	// whatever budget was left over.
	honored := 0
	for _, w := range waits {
		if w >= 30*time.Second {
			honored++
		}
	}
	if honored < 2 {
		t.Errorf("waits = %v, want at least 2 waits >= 30s (server Retry-After floor)", waits)
	}
	// Total elapsed must be well beyond the ~3s the old code gave up in.
	if elapsed := clock.Sub(start); elapsed <= 3*time.Second {
		t.Errorf("chain gave up after %v, want > 3s (defect A regression)", elapsed)
	}
}

func TestFallback_MultipleCandidatesNoReduction(t *testing.T) {
	// With two candidates, the first should use the full retry budget (10)
	// before falling back to the second.
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

	// With 2 candidates the full retry budget is used.
	if openaiCalls != 10 {
		t.Errorf("openaiCalls = %d, want 10 (full retry budget with fallback)", openaiCalls)
	}
}

// ============================================================================
// IsRetriableError: default-to-transient blacklist (T1)
// ============================================================================

func TestIsRetriableError_AllSkippedCooldownIsTransient(t *testing.T) {
	// Every candidate in cooldown: nothing was actually tried, and cooldowns
	// expire, so the caller must keep retrying instead of aborting the turn.
	err := &FallbackExhaustedError{Attempts: []FallbackAttempt{
		{Provider: "openai", Model: "gpt-4", Skipped: true, Reason: FailoverRateLimit},
		{Provider: "anthropic", Model: "claude", Skipped: true, Reason: FailoverRateLimit},
	}}
	if !IsRetriableError(err) {
		t.Error("all-skipped chain must be retriable (cooldown is transient)")
	}
}

func TestIsRetriableError_TransportErrorsAreTransient(t *testing.T) {
	// Real strings seen in production logs; each one killed a session before T1.
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
	// The ONLY non-retriable inputs: cancellation plus explicitly terminal
	// reasons. Everything else is transient by default.
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

	// A bare FailoverError with an auth/billing reason is terminal for THAT
	// provider but still candidate-swappable (FailoverError.IsRetriable), so it
	// reports true here by design: the chain may still have work left. Only a
	// chain whose every attempt is terminal is a dead end (covered above).
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
