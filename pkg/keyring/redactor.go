package keyring

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// MinRedactLength is the minimum secret value length that the Redactor will
// consider for substitution. Shorter values are skipped to avoid false
// positives on common short tokens.
const MinRedactLength = 8

// Redactor replaces raw secret values in text with {{SECRET:name}} placeholders.
// Safe for concurrent use. A nil *Redactor or a Redactor whose service is nil
// or whose vault is closed is a no-op (returns input unchanged).
type Redactor struct {
	svc *Service

	// cacheMu guards values and fingerprint below.
	cacheMu sync.Mutex
	// values maps secret name -> plaintext value for every eligible secret
	// (value length >= MinRedactLength and non-empty).
	values map[string]string
	// fingerprint is a cheap checksum of store.List()'s (name, UpdatedAt)
	// pairs + count. When it changes the cache is rebuilt.
	fingerprint string
}

// NewRedactor creates a Redactor bound to the given keyring service.
// Passing nil returns a usable no-op Redactor.
func NewRedactor(svc *Service) *Redactor {
	return &Redactor{svc: svc}
}

// Redact returns content with every known secret value replaced by its
// {{SECRET:name}} placeholder. Secrets with values shorter than
// MinRedactLength (8) characters are skipped to avoid false positives.
// Returns content unchanged if the vault is closed or there are no secrets.
func (r *Redactor) Redact(content string) string {
	if r == nil || r.svc == nil || content == "" {
		return content
	}
	if err := r.svc.EnsureOpen(); err != nil {
		// Vault unavailable (disabled or failed to open): leave content as-is.
		return content
	}

	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	// Snapshot the secret metadata under the service read lock.
	r.svc.mu.RLock()
	metas := r.svc.store.List()
	r.svc.mu.RUnlock()

	if fp := fingerprint(metas); fp != r.fingerprint {
		r.rebuild(metas)
		r.fingerprint = fp
	}
	if len(r.values) == 0 {
		return content
	}

	// Apply longest values first so a secret that is a substring of another
	// secret is handled correctly (longest match wins).
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

// rebuild collects the plaintext values for every eligible secret from the
// store and rebuilds the cache map. It must be called while cacheMu is held.
func (r *Redactor) rebuild(metas []SecretMeta) {
	r.svc.mu.RLock()
	vals := make(map[string]string, len(metas))
	for _, m := range metas {
		sec, ok := r.svc.store.Get(m.Name)
		if !ok || sec == nil || len(sec.Value) < MinRedactLength {
			continue
		}
		vals[m.Name] = sec.Value
	}
	r.svc.mu.RUnlock()
	r.values = vals
}

// fingerprint returns a cheap checksum over the secret metadata list: the
// count plus the concatenation of each (name, UpdatedAt.UnixNano()) pair.
// List() is sorted by name, so the result is deterministic. It deliberately
// contains no secret values — used only for cache invalidation.
func fingerprint(metas []SecretMeta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "n=%d;", len(metas))
	for _, m := range metas {
		fmt.Fprintf(&b, "%s:%d;", m.Name, m.UpdatedAt.UnixNano())
	}
	return b.String()
}
