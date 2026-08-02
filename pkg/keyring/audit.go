package keyring

import (
	"sync"
	"time"
)

// AuditRing is a fixed-size ring buffer of access records. When full, the
// oldest records are overwritten. It is safe for concurrent use.
type AuditRing struct {
	buf  []AccessRecord
	size int
	pos  int
	full bool
	mu   sync.RWMutex
}

// NewAuditRing creates an AuditRing that retains up to size records.
// A non-positive size defaults to 1000.
func NewAuditRing(size int) *AuditRing {
	if size <= 0 {
		size = 1000
	}
	return &AuditRing{
		buf:  make([]AccessRecord, size),
		size: size,
	}
}

// Record appends an access record to the ring.
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

// Records returns all stored records in chronological order (oldest first).
func (r *AuditRing) Records() []AccessRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.full {
		out := make([]AccessRecord, r.pos)
		copy(out, r.buf[:r.pos])
		return out
	}

	out := make([]AccessRecord, 0, r.size)
	// Oldest entries start at r.pos (the next slot to be overwritten).
	out = append(out, r.buf[r.pos:]...)
	out = append(out, r.buf[:r.pos]...)
	return out
}

// Len returns the number of records currently stored.
func (r *AuditRing) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.full {
		return r.size
	}
	return r.pos
}
