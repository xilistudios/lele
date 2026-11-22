package keyring

import (
	"sync"
	"time"
)

type AuditRing struct {
	buf  []AccessRecord
	size int
	pos  int
	full bool
	mu   sync.RWMutex
}

func NewAuditRing(size int) *AuditRing {
	if size <= 0 {
		size = 1000
	}
	return &AuditRing{
		buf:  make([]AccessRecord, size),
		size: size,
	}
}

func (r *AuditRing) Record(rec AccessRecord) {
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf[r.pos] = rec
	r.pos++
	if r.pos >= r.size {
		r.pos = 0
		r.full = true
	}
}

func (r *AuditRing) Records() []AccessRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.full {
		out := make([]AccessRecord, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}

	out := make([]AccessRecord, 0, r.size)
	out = append(out, r.buf[r.pos:]...)
	out = append(out, r.buf[:r.pos]...)
	return out
}

func (r *AuditRing) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.full {
		return r.size
	}
	return r.pos
}
