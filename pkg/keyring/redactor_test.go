package keyring

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// newRedactorTestService builds an opened Service bound to a temp FileStore,
// mirroring the pattern used in keyring_test.go.
func newRedactorTestService(t *testing.T) *Service {
	t.Helper()
	svc := newTestService(t)
	if err := svc.EnsureOpen(); err != nil {
		t.Fatalf("open service: %v", err)
	}
	return svc
}

// aClosedService returns an *opened* Service whose store has been Closed to
// simulate an unavailable/closed vault.
func aClosedService(t *testing.T) *Service {
	t.Helper()
	svc := newRedactorTestService(t)
	if err := svc.EnsureOpen(); err != nil {
		t.Fatalf("open service: %v", err)
	}
	if err := svc.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return svc
}

func mustSet(t *testing.T, svc *Service, name, value string) {
	t.Helper()
	if err := svc.SetFromUI(name, value, "desc", nil, nil, "tui"); err != nil {
		t.Fatalf("set %q: %v", name, err)
	}
}

func TestRedact_BasicReplacement(t *testing.T) {
	svc := newRedactorTestService(t)
	mustSet(t, svc, "openai.api_key", "sk-verysecret123456")
	r := NewRedactor(svc)

	out := r.Redact("my key is sk-verysecret123456 here")
	if !strings.Contains(out, "{{SECRET:openai.api_key}}") {
		t.Fatalf("expected placeholder in output, got %q", out)
	}
	if strings.Contains(out, "sk-verysecret123456") {
		t.Fatalf("raw value leaked: %q", out)
	}
	if !strings.Contains(out, "my key is") {
		t.Fatalf("unrelated text damaged: %q", out)
	}
}

func TestRedact_MultipleOccurrences(t *testing.T) {
	svc := newRedactorTestService(t)
	mustSet(t, svc, "gh.token", "ghp_abcdefghijkl")
	r := NewRedactor(svc)

	content := "a=ghp_abcdefghijkl b=ghp_abcdefghijkl c=ghp_abcdefghijkl"
	out := r.Redact(content)
	if got := strings.Count(out, "{{SECRET:gh.token}}"); got != 3 {
		t.Fatalf("expected 3 placeholders, got %d: %q", got, out)
	}
	if strings.Contains(out, "ghp_abcdefghijkl") {
		t.Fatalf("raw value leaked: %q", out)
	}
}

func TestRedact_MultipleSecrets(t *testing.T) {
	svc := newRedactorTestService(t)
	mustSet(t, svc, "a.key", "alpha-secret-value-123")
	mustSet(t, svc, "b.key", "beta-secret-value-4567")
	r := NewRedactor(svc)

	content := "A:alpha-secret-value-123 B:beta-secret-value-4567"
	out := r.Redact(content)
	for _, want := range []string{
		"{{SECRET:a.key}}",
		"{{SECRET:b.key}}",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %q", want, out)
		}
	}
	for _, leak := range []string{"alpha-secret-value-123", "beta-secret-value-4567"} {
		if strings.Contains(out, leak) {
			t.Fatalf("raw value %q leaked: %q", leak, out)
		}
	}
}

func TestRedact_ShortSecretSkipped(t *testing.T) {
	svc := newRedactorTestService(t)
	// 7 chars < MinRedactLength (8).
	mustSet(t, svc, "short.key", "abc1234")
	r := NewRedactor(svc)

	out := r.Redact("value is abc1234")
	if !strings.Contains(out, "abc1234") {
		t.Fatalf("short value must not be replaced: %q", out)
	}
}

func TestRedact_ExactlyMinLength(t *testing.T) {
	svc := newRedactorTestService(t)
	// Exactly 8 chars.
	mustSet(t, svc, "eight.key", "abcdefgh")
	r := NewRedactor(svc)

	out := r.Redact("token=abcdefgh")
	if !strings.Contains(out, "{{SECRET:eight.key}}") {
		t.Fatalf("8-char value should be replaced: %q", out)
	}
}

func TestRedact_EmptyContent(t *testing.T) {
	svc := newRedactorTestService(t)
	mustSet(t, svc, "a.key", "super-secret-value-123456")
	r := NewRedactor(svc)

	// Empty content must be returned as-is without touching the store.
	if got := r.Redact(""); got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
}

func TestRedact_NoSecrets(t *testing.T) {
	svc := newRedactorTestService(t)
	r := NewRedactor(svc)

	content := "nothing to hide here"
	if out := r.Redact(content); out != content {
		t.Fatalf("expected unchanged output, got %q", out)
	}
}

func TestRedact_VaultClosed(t *testing.T) {
	svc := aClosedService(t)
	r := NewRedactor(svc)

	content := "plain text with no secrets"
	if out := r.Redact(content); out != content {
		t.Fatalf("closed vault must return content unchanged, got %q", out)
	}
}

func TestRedact_NilService(t *testing.T) {
	r := NewRedactor(nil)
	content := "some input with nothing secret"
	if out := r.Redact(content); out != content {
		t.Fatalf("nil service must be a no-op, got %q", out)
	}

	// A nil *Redactor itself must also not panic.
	var nilR *Redactor
	if out := nilR.Redact(content); out != content {
		t.Fatalf("nil redactor must be a no-op, got %q", out)
	}
}

func TestRedact_SubstringLongestWins(t *testing.T) {
	svc := newRedactorTestService(t)
	mustSet(t, svc, "short.key", "sk-abcdef1234567890")
	mustSet(t, svc, "long.key", "sk-abcdef1234567890-extra")
	r := NewRedactor(svc)

	content := "long: sk-abcdef1234567890-extra short: sk-abcdef1234567890"
	out := r.Redact(content)

	if !strings.Contains(out, "{{SECRET:long.key}}") {
		t.Fatalf("long secret not replaced: %q", out)
	}
	if !strings.Contains(out, "{{SECRET:short.key}}") {
		t.Fatalf("short secret not replaced: %q", out)
	}
	// No sub-part of the long value should be spliced into the long placeholder.
	if strings.Contains(out, "{{SECRET:short.key}}-extra") {
		t.Fatalf("long placeholder mangled by short match: %q", out)
	}
	if strings.Contains(out, "sk-abcdef1234567890") {
		t.Fatalf("raw value leaked: %q", out)
	}
}

func TestRedact_RegexMetacharacters(t *testing.T) {
	svc := newRedactorTestService(t)
	// Value contains regex metacharacters that must be matched literally.
	mustSet(t, svc, "meta.key", "a$b+c.d/")
	r := NewRedactor(svc)

	content := "here: a$b+c.d/ end"
	out := r.Redact(content)
	if !strings.Contains(out, "{{SECRET:meta.key}}") {
		t.Fatalf("metachar value not replaced: %q", out)
	}
	if strings.Contains(out, "a$b+c.d/") {
		t.Fatalf("raw metachar value leaked: %q", out)
	}
}

func TestRedact_CacheInvalidation(t *testing.T) {
	svc := newRedactorTestService(t)
	mustSet(t, svc, "first.key", "first-secret-value-12345")
	r := NewRedactor(svc)

	content := "now: second-secret-value-67890"
	// First redaction sees only the first secret; the second value leaks.
	if out := r.Redact(content); !strings.Contains(out, "second-secret-value-67890") {
		t.Fatalf("precondition: second value should leak before cache rebuild: %q", out)
	}

	// Add a new secret, then redact again => fingerprint changed => rebuild.
	mustSet(t, svc, "second.key", "second-secret-value-67890")
	out := r.Redact(content)
	if !strings.Contains(out, "{{SECRET:second.key}}") {
		t.Fatalf("new secret not redacted after cache invalidation: %q", out)
	}
	if strings.Contains(out, "second-secret-value-67890") {
		t.Fatalf("new value leaked after cache rebuild: %q", out)
	}

	// Updating an existing value must also trigger a rebuild.
	mustSet(t, svc, "first.key", "changed-secret-value-99999")
	out = r.Redact("check: changed-secret-value-99999")
	if !strings.Contains(out, "{{SECRET:first.key}}") {
		t.Fatalf("updated value not redacted: %q", out)
	}

	// Deleting a secret must drop its placeholder.
	if err := svc.DeleteFromUI("second.key", "tui"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	out = r.Redact("again: second-secret-value-67890")
	if strings.Contains(out, "{{SECRET:second.key}}") {
		t.Fatalf("deleted secret still redacted: %q", out)
	}
}

func TestRedact_Concurrent(t *testing.T) {
	var svc = newRedactorTestService(t)
	mustSet(t, svc, "cc.a", "cc-alpha-secret-value-1111")
	mustSet(t, svc, "cc.b", "cc-beta-secret-value-2222")
	r := NewRedactor(svc)

	content := strings.Repeat("A:cc-alpha-secret-value-1111 B:cc-beta-secret-value-2222 ", 50)

	var wg sync.WaitGroup
	errCh := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := r.Redact(content)
			if strings.Contains(out, "cc-alpha-secret-value-1111") ||
				strings.Contains(out, "cc-beta-secret-value-2222") {
				errCh <- fmt.Sprintf("value leaked: %q", out)
				return
			}
			if !strings.Contains(out, "{{SECRET:cc.a}}") || !strings.Contains(out, "{{SECRET:cc.b}}") {
				errCh <- fmt.Sprintf("placeholder missing: %q", out)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}
