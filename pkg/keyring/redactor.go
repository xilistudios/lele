package keyring

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

const (
	MinRedactLength = 8

	WithheldMarker = "[log message withheld: secret redaction unavailable]"

	redactorWarnInterval = time.Minute
)

type warnLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	last     time.Time
}

func (l *warnLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.last.IsZero() && now.Sub(l.last) < l.interval {
		return false
	}
	l.last = now
	return true
}

var redactorWarnLimiter = &warnLimiter{interval: redactorWarnInterval}

var redactorUnreadableWarnLimiter = &warnLimiter{interval: redactorWarnInterval}

func withholdContent(reason string) string {
	if redactorWarnLimiter.allow(time.Now()) {
		logger.WarnCF("keyring", "Secret redaction unavailable; withholding log message content",
			map[string]interface{}{"reason": reason})
	}
	return WithheldMarker
}

type Redactor struct {
	svc *Service

	cacheMu     sync.Mutex
	values      map[string]string
	fingerprint string

	disabledOnce sync.Once
}

func (r *Redactor) warnDisabledOnce() {
	r.disabledOnce.Do(func() {
		logger.WarnCF("keyring", "Secret redaction is disabled by config (keyring.enabled=false); tool output is not redacted",
			nil)
	})
}

func NewRedactor(svc *Service) *Redactor {
	return &Redactor{svc: svc}
}

func (r *Redactor) Redact(content string) string {
	if content == "" || r == nil {
		return content
	}
	if r.svc == nil {
		return withholdContent("no keyring service")
	}
	if err := r.svc.EnsureOpen(); err != nil {
		if errors.Is(err, ErrDisabled) {
			r.warnDisabledOnce()
			return content
		}
		return withholdContent("keyring unavailable: " + err.Error())
	}

	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	r.svc.mu.RLock()
	metas, err := r.svc.store.List()
	r.svc.mu.RUnlock()
	if err != nil {
		return withholdContent("secret list failed: " + err.Error())
	}

	if fp := fingerprint(metas); fp != r.fingerprint {
		r.rebuild(metas)
		r.fingerprint = fp
	}
	if len(r.values) == 0 {
		return content
	}

	names := make([]string, 0, len(r.values))
	for name := range r.values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		return len(r.values[names[i]]) > len(r.values[names[j]])
	})

	for _, name := range names {
		content = strings.ReplaceAll(content, r.values[name], "{{SECRET:"+name+"}}")
	}
	return content
}

func (r *Redactor) rebuild(metas []SecretMeta) {
	r.svc.mu.RLock()
	vals := make(map[string]string, len(metas))
	var unreadable []string
	for _, m := range metas {
		sec, err := r.svc.store.Get(m.Name)
		if err != nil || sec == nil {
			unreadable = append(unreadable, m.Name)
			continue
		}
		if len(sec.Value) < MinRedactLength {
			continue
		}
		vals[m.Name] = sec.Value
	}
	r.svc.mu.RUnlock()
	r.values = vals

	if len(unreadable) > 0 {
		warnUnreadableSecrets(unreadable)
	}
}

// warnUnreadableSecrets reports the secrets a rebuild could not read. They are
// skipped, so their values reach the model unredacted while every other secret
// is still replaced: a narrower fail-open than the ones that withhold the whole
// message, and one that has to be visible rather than silent. Only names, never
// values, and at most once a minute.
func warnUnreadableSecrets(names []string) {
	if !redactorUnreadableWarnLimiter.allow(time.Now()) {
		return
	}
	sort.Strings(names)
	logger.WarnCF("keyring", "Secret redaction is incomplete: stored secrets could not be read and are not redacted",
		map[string]interface{}{"secrets": strings.Join(names, ",")})
}

func fingerprint(metas []SecretMeta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "n=%d;", len(metas))
	for _, m := range metas {
		fmt.Fprintf(&b, "%s:%d;", m.Name, m.UpdatedAt.UnixNano())
	}
	return b.String()
}
