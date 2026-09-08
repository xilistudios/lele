package session

// Paged access to messages that are no longer in memory.
//
// LoadMessagesWindow reads a seq-bounded window of a session's persisted rows
// so the UI can scroll through evicted history without loading the whole
// session back into memory.

import (
	"encoding/json"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// EvictedMessagesPage is a page of a session's persisted messages that are NOT
// resident in memory (evicted history / out-of-context messages). Frontends
// use it to render the full transcript on demand without ever re-injecting
// these messages into the agent's context.
type EvictedMessagesPage struct {
	// Messages are in chronological order (ascending seq).
	Messages []providers.Message
	// FirstSeq/LastSeq are the SQLite seqs of the first/last message.
	FirstSeq int
	LastSeq  int
	// Seqs holds the SQLite seq of each message in Messages (same order).
	// Exposed because the persisted region may contain gaps: PruneExcluded
	// physically deletes the oldest rows, so seqs are not guaranteed to be
	// contiguous and consumers must not derive them from slice indices.
	Seqs []int
	// HasOlder is true when out-of-memory persisted rows exist before FirstSeq.
	HasOlder bool
	// HasNewer is true when out-of-memory persisted rows exist after LastSeq.
	HasNewer bool
	// EvictedCount is the number of persisted messages not resident in memory.
	EvictedCount int
	// TotalCount is the session's total message count (memory + evicted).
	TotalCount int
}

// maxMessagesWindowLimit caps the number of messages a single
// LoadMessagesWindow call may return, so a misbehaving client cannot make the
// gateway deserialize a whole multi-thousand-message transcript in one call.
const maxMessagesWindowLimit = 200

// LoadMessagesWindow returns a page of the session's out-of-memory messages
// from SQLite. It is read-only: it never loads the session into memory, never
// touches the LRU list, and never changes context membership.
//
// Boundaries:
//   - For a resident session, the out-of-memory region is seq <
//     session.firstInMemorySeq (the evicted prefix).
//   - For a non-resident (fully evicted) session, every persisted row is
//     out-of-memory, so the window covers the whole transcript.
//
// Paging:
//   - before >= 0: return up to limit rows with seq < before (scroll-up /
//     paging deeper into history), clamped to the out-of-memory region.
//     before = -1 means "no cursor".
//   - after > 0: return up to limit rows with after < seq (gap fill /
//     scroll-down), clamped to the out-of-memory region.
//   - neither: return the newest limit rows of the out-of-memory region.
//
// Returns nil when the session does not exist or has no out-of-memory rows
// for the requested page.
func (sm *SessionManager) LoadMessagesWindow(sessionKey string, before, after, limit int) *EvictedMessagesPage {
	if sm.store == nil || limit <= 0 {
		return nil
	}
	if limit > maxMessagesWindowLimit {
		limit = maxMessagesWindowLimit
	}

	sm.ensureLoaded()

	// Snapshot the memory boundary under the read lock (store queries run
	// outside it; SessionRepo reads are safe on their own, same pattern as
	// HasMessages/GetTotalMessageCount).
	var memFloor, residentCount, evicted int
	resident := false
	sm.mu.RLock()
	if session, ok := sm.sessions[sessionKey]; ok {
		resident = true
		memFloor = session.firstInMemorySeq // exclusive upper bound of evicted region
		residentCount = len(session.Messages)
		evicted = session.evictedTotal
	}
	sm.mu.RUnlock()

	repo := sm.store.Sessions()
	if !resident {
		// Not loaded in memory. The persisted eviction boundary
		// (FirstInMemorySeq) defines the out-of-memory prefix: a cold load
		// via GetHistoryView materializes exactly seq >= boundary, so the
		// window must serve seq < boundary and nothing more (no overlap).
		// Pruning may have deleted the oldest rows, so count what remains.
		meta, err := repo.GetSessionMeta(sessionKey)
		if err != nil || meta == nil {
			return nil
		}
		memFloor = meta.FirstInMemorySeq
		if memFloor <= 0 {
			return nil // nothing was eviction-excluded; cold load serves all
		}
		n, cerr := repo.CountMessagesBefore(sessionKey, memFloor)
		if cerr != nil {
			return nil
		}
		evicted = n
		if evicted == 0 {
			return nil
		}
		total, merr := repo.MessageCount(sessionKey)
		if merr != nil {
			return nil
		}
		residentCount = total - evicted // the tail cold-load will bring back
	} else if evicted == 0 {
		// Resident with no eviction gap: nothing to serve.
		return nil
	}

	total := residentCount + evicted

	// Clamp the requested page to the out-of-memory region [0, memFloor).
	// `before` is a seq cursor: 0 is a valid value meaning "before seq 0"
	// (i.e. nothing older exists), so the "no cursor" sentinel is negative.
	bound := memFloor
	if before >= 0 && (bound < 0 || before < bound) {
		bound = before
	}

	var rows []store.MessageRowFull
	var err error
	if after > 0 {
		// Scroll-down / gap fill: rows after `after`, below the memory window.
		if memFloor >= 0 && after >= memFloor {
			return nil
		}
		upper := -1
		if memFloor >= 0 {
			upper = memFloor
		}
		rows, err = repo.LoadMessagesBetweenLimited(sessionKey, after, upper, limit)
	} else {
		if bound <= 0 {
			return nil // no evicted region below the memory window
		}
		rows, err = repo.LoadMessagesBeforeLimited(sessionKey, bound, limit)
	}
	if err != nil {
		logger.WarnCF("session", "LoadMessagesWindow store read failed", map[string]interface{}{
			"session_key": sessionKey,
			"error":       err.Error(),
		})
		return nil
	}
	if len(rows) == 0 {
		return nil
	}

	// `before` queries come back newest-first; normalize to chronological.
	if after <= 0 {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}

	msgs := make([]providers.Message, 0, len(rows))
	seqs := make([]int, 0, len(rows))
	for _, row := range rows {
		var msg providers.Message
		if uerr := json.Unmarshal([]byte(row.JSON), &msg); uerr != nil {
			continue // skip corrupted rows
		}
		msgs = append(msgs, msg)
		seqs = append(seqs, row.Seq)
	}
	if len(msgs) == 0 {
		return nil
	}

	firstSeq, lastSeq := seqs[0], seqs[len(seqs)-1]

	// Older rows: any persisted seq below the page (the evicted region is
	// everything under memFloor; pruning only removes the oldest rows, so a
	// simple count is enough).
	hasOlder := false
	olderThan := firstSeq
	if memFloor >= 0 && memFloor < olderThan {
		olderThan = memFloor
	}
	if olderThan > 0 {
		if n, cerr := repo.CountMessagesBefore(sessionKey, olderThan); cerr == nil && n > 0 {
			hasOlder = true
		}
	}

	// Newer rows within the out-of-memory region: the evicted region is
	// contiguous between the pruning floor and memFloor, so a simple bound
	// check suffices.
	hasNewer := lastSeq+1 < memFloor

	return &EvictedMessagesPage{
		Messages:     msgs,
		Seqs:         seqs,
		FirstSeq:     firstSeq,
		LastSeq:      lastSeq,
		HasOlder:     hasOlder,
		HasNewer:     hasNewer,
		EvictedCount: evicted,
		TotalCount:   total,
	}
}
