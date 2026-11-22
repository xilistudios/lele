package providers

import (
	"sync"
	"testing"
	"time"
)

func newTestTracker(now time.Time) (*CooldownTracker, *time.Time) {
	current := now
	ct := NewCooldownTracker()
	ct.nowFunc = func() time.Time { return current }
	return ct, &current
}

func TestCooldown_InitiallyAvailable(t *testing.T) {
	ct := NewCooldownTracker()
	if !ct.IsAvailable("openai") {
		t.Error("new provider should be available")
	}
	if ct.ErrorCount("openai") != 0 {
		t.Error("new provider should have 0 errors")
	}
}

func TestCooldown_StandardEscalation(t *testing.T) {
	now := time.Now()
	ct, current := newTestTracker(now)

	ct.MarkFailure("openai", FailoverRateLimit)
	if ct.IsAvailable("openai") {
		t.Error("should be in cooldown after 1st error")
	}

	*current = now.Add(61 * time.Second)
	if !ct.IsAvailable("openai") {
		t.Error("should be available after 1 min cooldown")
	}

	ct.MarkFailure("openai", FailoverRateLimit)
	*current = now.Add(61*time.Second + 4*time.Minute)
	if ct.IsAvailable("openai") {
		t.Error("should be in cooldown (5 min) after 2nd error")
	}
	*current = now.Add(61*time.Second + 6*time.Minute)
	if !ct.IsAvailable("openai") {
		t.Error("should be available after 5 min cooldown")
	}
}

func TestCooldown_StandardCap(t *testing.T) {
	expected := []time.Duration{
		1 * time.Minute,
		5 * time.Minute,
		25 * time.Minute,
		1 * time.Hour,
		1 * time.Hour,
	}

	for i, want := range expected {
		got := calculateStandardCooldown(i + 1)
		if got != want {
			t.Errorf("calculateStandardCooldown(%d) = %v, want %v", i+1, got, want)
		}
	}
}

func TestCooldown_BillingEscalation(t *testing.T) {
	now := time.Now()
	ct, current := newTestTracker(now)

	ct.MarkFailure("openai", FailoverBilling)
	if ct.IsAvailable("openai") {
		t.Error("should be disabled after billing error")
	}

	*current = now.Add(4 * time.Hour)
	if ct.IsAvailable("openai") {
		t.Error("should still be disabled (5h cooldown)")
	}

	*current = now.Add(5*time.Hour + 1*time.Second)
	if !ct.IsAvailable("openai") {
		t.Error("should be available after 5h billing cooldown")
	}
}

func TestCooldown_BillingCap(t *testing.T) {
	expected := []time.Duration{
		5 * time.Hour,
		10 * time.Hour,
		20 * time.Hour,
		24 * time.Hour,
		24 * time.Hour,
	}

	for i, want := range expected {
		got := calculateBillingCooldown(i + 1)
		if got != want {
			t.Errorf("calculateBillingCooldown(%d) = %v, want %v", i+1, got, want)
		}
	}
}

func TestCooldown_SuccessReset(t *testing.T) {
	ct := NewCooldownTracker()

	ct.MarkFailure("openai", FailoverRateLimit)
	ct.MarkFailure("openai", FailoverBilling)
	if ct.ErrorCount("openai") != 2 {
		t.Errorf("error count = %d, want 2", ct.ErrorCount("openai"))
	}

	ct.MarkSuccess("openai")
	if ct.ErrorCount("openai") != 0 {
		t.Errorf("error count after success = %d, want 0", ct.ErrorCount("openai"))
	}
	if !ct.IsAvailable("openai") {
		t.Error("should be available after success")
	}
	if ct.FailureCount("openai", FailoverRateLimit) != 0 {
		t.Error("failure counts should be reset after success")
	}
	if ct.FailureCount("openai", FailoverBilling) != 0 {
		t.Error("billing failure count should be reset after success")
	}
}

func TestCooldown_FailureWindowReset(t *testing.T) {
	now := time.Now()
	ct, current := newTestTracker(now)

	for i := 0; i < 4; i++ {
		ct.MarkFailure("openai", FailoverRateLimit)
		*current = current.Add(2 * time.Second)
	}
	if ct.ErrorCount("openai") != 4 {
		t.Errorf("error count = %d, want 4", ct.ErrorCount("openai"))
	}

	*current = now.Add(25 * time.Hour)

	ct.MarkFailure("openai", FailoverRateLimit)
	if ct.ErrorCount("openai") != 1 {
		t.Errorf("error count after window reset = %d, want 1 (reset + 1)", ct.ErrorCount("openai"))
	}
}

func TestCooldown_PerReasonTracking(t *testing.T) {
	ct := NewCooldownTracker()

	ct.MarkFailure("openai", FailoverRateLimit)
	ct.MarkFailure("openai", FailoverRateLimit)
	ct.MarkFailure("openai", FailoverBilling)
	ct.MarkFailure("openai", FailoverAuth)

	if ct.FailureCount("openai", FailoverRateLimit) != 2 {
		t.Errorf("rate_limit count = %d, want 2", ct.FailureCount("openai", FailoverRateLimit))
	}
	if ct.FailureCount("openai", FailoverBilling) != 1 {
		t.Errorf("billing count = %d, want 1", ct.FailureCount("openai", FailoverBilling))
	}
	if ct.FailureCount("openai", FailoverAuth) != 1 {
		t.Errorf("auth count = %d, want 1", ct.FailureCount("openai", FailoverAuth))
	}
	if ct.ErrorCount("openai") != 4 {
		t.Errorf("total error count = %d, want 4", ct.ErrorCount("openai"))
	}
}

func TestCooldown_BillingTakesPrecedence(t *testing.T) {
	now := time.Now()
	ct, current := newTestTracker(now)

	ct.MarkFailure("openai", FailoverRateLimit)
	ct.MarkFailure("openai", FailoverBilling)

	*current = now.Add(2 * time.Minute)
	if ct.IsAvailable("openai") {
		t.Error("billing disable should take precedence over standard cooldown")
	}

	*current = now.Add(5*time.Hour + 1*time.Second)
	if !ct.IsAvailable("openai") {
		t.Error("should be available after all cooldowns expire")
	}
}

func TestCooldown_CooldownRemaining(t *testing.T) {
	now := time.Now()
	ct, current := newTestTracker(now)

	if ct.CooldownRemaining("openai") != 0 {
		t.Error("expected 0 remaining for new provider")
	}

	ct.MarkFailure("openai", FailoverRateLimit)

	*current = now.Add(30 * time.Second)
	remaining := ct.CooldownRemaining("openai")
	if remaining <= 0 || remaining > 1*time.Minute {
		t.Errorf("remaining = %v, expected ~30s", remaining)
	}
}

func TestCooldown_SuccessOnUnknownProvider(t *testing.T) {
	ct := NewCooldownTracker()
	ct.MarkSuccess("nonexistent")
	if !ct.IsAvailable("nonexistent") {
		t.Error("nonexistent provider should be available")
	}
}

func TestCooldown_ConcurrentAccess(t *testing.T) {
	ct := NewCooldownTracker()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			ct.MarkFailure("openai", FailoverRateLimit)
		}()
		go func() {
			defer wg.Done()
			ct.IsAvailable("openai")
		}()
		go func() {
			defer wg.Done()
			ct.MarkSuccess("openai")
		}()
	}

	wg.Wait()
}

func TestCooldown_MultipleProviders(t *testing.T) {
	ct := NewCooldownTracker()

	ct.MarkFailure("openai", FailoverRateLimit)
	ct.MarkFailure("anthropic", FailoverBilling)

	if ct.IsAvailable("openai") {
		t.Error("openai should be in cooldown")
	}
	if ct.IsAvailable("anthropic") {
		t.Error("anthropic should be in cooldown")
	}
	if !ct.IsAvailable("groq") {
		t.Error("groq should be available")
	}
}
