package keyring

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

func newRedactorTestService(t *testing.T) *Service {
	t.Helper()
	svc := newTestService(t)
	if err := svc.EnsureOpen(); err != nil {
		t.Fatalf("open service: %v", err)
	}
	return svc
}

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
	mustSet(t, svc, "short.key", "abc1234")
	r := NewRedactor(svc)

	out := r.Redact("value is abc1234")
	if !strings.Contains(out, "abc1234") {
		t.Fatalf("short value must not be replaced: %q", out)
	}
}

func TestRedact_ExactlyMinLength(t *testing.T) {
	svc := newRedactorTestService(t)
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

func TestRedact_EmptyCachePassesThrough(t *testing.T) {
	resetRedactorWarnings()
	svc := newRedactorTestService(t)
	r := NewRedactor(svc)

	content := "an empty vault is not an error: this message must reach the log unchanged"
	out := captureRedactorLogs(t, func() {
		if got := r.Redact(content); got != content {
			t.Fatalf("empty cache must pass the content through, got %q", got)
		}
	})
	if strings.Contains(out, "Secret redaction unavailable") {
		t.Fatalf("an empty vault must not warn about unavailable redaction, got %q", out)
	}
}

func resetRedactorWarnings() {
	redactorWarnLimiter.mu.Lock()
	redactorWarnLimiter.last = time.Time{}
	redactorWarnLimiter.mu.Unlock()
}

func captureRedactorLogs(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })

	fn()
	return buf.String()
}

func TestRedact_VaultClosed(t *testing.T) {
	resetRedactorWarnings()
	svc := aClosedService(t)
	r := NewRedactor(svc)

	content := "plain text with no secrets"
	if out := r.Redact(content); out != WithheldMarker {
		t.Fatalf("an unreadable vault must withhold the content, got %q", out)
	}
}

func TestRedact_ErrorPathWithholdsContent(t *testing.T) {
	secret := "sk-live-value-that-must-never-reach-a-log"
	content := "request failed with credential " + secret

	for _, tc := range []struct {
		name     string
		redactor func(*testing.T) *Redactor
	}{
		{"unreadable store", func(t *testing.T) *Redactor { return NewRedactor(aClosedService(t)) }},
		{"nil service", func(t *testing.T) *Redactor { return NewRedactor(nil) }},
		{"unreadable master key", func(t *testing.T) *Redactor {
			svc := newTestService(t)
			svc.keyProv = brokenKeyProvider{err: errors.New("master key unreadable")}
			return NewRedactor(svc)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetRedactorWarnings()
			r := tc.redactor(t)

			out := r.Redact(content)
			if out != WithheldMarker {
				t.Fatalf("redaction error must withhold the message, got %q", out)
			}
			if strings.Contains(out, secret) {
				t.Fatalf("secret leaked on the error path: %q", out)
			}
		})
	}
}

func TestRedact_ErrorPathWarnsOncePerInterval(t *testing.T) {
	resetRedactorWarnings()
	t.Cleanup(resetRedactorWarnings)

	r := NewRedactor(aClosedService(t))

	out := captureRedactorLogs(t, func() {
		for i := 0; i < 50; i++ {
			r.Redact(fmt.Sprintf("attempt %d with nothing secret", i))
		}
	})

	if got := strings.Count(out, "Secret redaction unavailable"); got != 1 {
		t.Fatalf("expected exactly one warning for 50 withheld messages, got %d: %q", got, out)
	}
}

func TestRedact_ErrorWarnIsRateLimitedNotSilenced(t *testing.T) {
	resetRedactorWarnings()
	t.Cleanup(resetRedactorWarnings)

	r := NewRedactor(nil)
	if out := captureRedactorLogs(t, func() { r.Redact("first") }); !strings.Contains(out, "Secret redaction unavailable") {
		t.Fatalf("first error path must warn, got %q", out)
	}

	redactorWarnLimiter.mu.Lock()
	redactorWarnLimiter.last = time.Now().Add(-2 * redactorWarnInterval)
	redactorWarnLimiter.mu.Unlock()

	if out := captureRedactorLogs(t, func() { r.Redact("second") }); !strings.Contains(out, "Secret redaction unavailable") {
		t.Fatalf("a later error outside the interval must warn again, got %q", out)
	}
}

func TestRedact_NilService(t *testing.T) {
	resetRedactorWarnings()
	content := "some input with nothing secret"

	var nilR *Redactor
	if out := nilR.Redact(content); out != content {
		t.Fatalf("a nil redactor means no redaction is configured, got %q", out)
	}
	if out := nilR.Redact(""); out != "" {
		t.Fatalf("empty content has nothing to withhold, got %q", out)
	}

	if out := NewRedactor(nil).Redact(content); out != WithheldMarker {
		t.Fatalf("a redactor without a keyring service must withhold, got %q", out)
	}
}

type brokenKeyProvider struct{ err error }

func (p brokenKeyProvider) GetKey() ([]byte, error) { return nil, p.err }
func (p brokenKeyProvider) Backend() string         { return "broken" }
func (p brokenKeyProvider) Available() bool         { return false }

func TestRedact_DisabledKeyringPassesThrough(t *testing.T) {
	resetRedactorWarnings()
	t.Cleanup(resetRedactorWarnings)

	svc := newTestService(t)
	svc.cfg.Enabled = false
	r := NewRedactor(svc)

	content := "tool output must still reach the model when the keyring is deliberately off"
	out := captureRedactorLogs(t, func() {
		for i := 0; i < 5; i++ {
			if got := r.Redact(content); got != content {
				t.Fatalf("keyring.enabled=false is an explicit opt-out, got %q", got)
			}
		}
	})
	if !strings.Contains(out, "disabled by config") {
		t.Fatalf("a disabled keyring must be announced in the log, got %q", out)
	}
	if got := strings.Count(out, "disabled by config"); got != 1 {
		t.Fatalf("the opt-out warning must be emitted once, got %d: %q", got, out)
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
	if strings.Contains(out, "{{SECRET:short.key}}-extra") {
		t.Fatalf("long placeholder mangled by short match: %q", out)
	}
	if strings.Contains(out, "sk-abcdef1234567890") {
		t.Fatalf("raw value leaked: %q", out)
	}
}

func TestRedact_RegexMetacharacters(t *testing.T) {
	svc := newRedactorTestService(t)
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
	if out := r.Redact(content); !strings.Contains(out, "second-secret-value-67890") {
		t.Fatalf("precondition: second value should leak before cache rebuild: %q", out)
	}

	mustSet(t, svc, "second.key", "second-secret-value-67890")
	out := r.Redact(content)
	if !strings.Contains(out, "{{SECRET:second.key}}") {
		t.Fatalf("new secret not redacted after cache invalidation: %q", out)
	}
	if strings.Contains(out, "second-secret-value-67890") {
		t.Fatalf("new value leaked after cache rebuild: %q", out)
	}

	mustSet(t, svc, "first.key", "changed-secret-value-99999")
	out = r.Redact("check: changed-secret-value-99999")
	if !strings.Contains(out, "{{SECRET:first.key}}") {
		t.Fatalf("updated value not redacted: %q", out)
	}

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
