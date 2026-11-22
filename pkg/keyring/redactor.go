package keyring

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

const MinRedactLength = 8

type Redactor struct {
	svc *Service

	cacheMu     sync.Mutex
	values      map[string]string
	fingerprint string
}

func NewRedactor(svc *Service) *Redactor {
	return &Redactor{svc: svc}
}

func (r *Redactor) Redact(content string) string {
	if r == nil || r.svc == nil || content == "" {
		return content
	}
	if err := r.svc.EnsureOpen(); err != nil {
		return content
	}

	r.cacheMu.Lock()
	defer r.cacheMu.Unlock()

	r.svc.mu.RLock()
	metas, err := r.svc.store.List()
	r.svc.mu.RUnlock()
	if err != nil {
		return content
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
	for _, m := range metas {
		sec, err := r.svc.store.Get(m.Name)
		if err != nil || sec == nil || len(sec.Value) < MinRedactLength {
			continue
		}
		vals[m.Name] = sec.Value
	}
	r.svc.mu.RUnlock()
	r.values = vals
}

func fingerprint(metas []SecretMeta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "n=%d;", len(metas))
	for _, m := range metas {
		fmt.Fprintf(&b, "%s:%d;", m.Name, m.UpdatedAt.UnixNano())
	}
	return b.String()
}
