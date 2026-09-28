package session

// Persistence: writing sessions back to SQLite.
//
// Implements the incremental save protocol. Each save path captures the
// session's saveEpoch before releasing sm.mu for disk I/O and discards its
// post-I/O bookkeeping if the epoch moved, so a concurrent mutation can never
// be lost. Callers must hold sm.mu (write lock) for the *Unlocked variants.

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// sessionRepo is the persistence backend used by the save paths in this file:
// exactly the subset of *store.SessionRepo methods they call.
//
// It exists as an interface plus the repoFor hook — instead of reading
// sm.sessionRepo directly — so tests can substitute a blocking fake and prove
// that no SQLite round trip (and no JSON marshalling) runs while sm.mu is
// held (see persist_lock_test.go, the T10 lock-scope regression test). The
// SessionManager field itself is a concrete *store.SessionRepo (manager.go),
// so the seam has to live where it is consumed.
type sessionRepo interface {
	UpsertSession(meta store.SessionMeta) error
	ReplaceMessages(sessionKey string, messages []store.MessageRow) error
	InsertMessages(sessionKey string, messages []store.MessageRow) error
	UpdateMessages(sessionKey string, messages []store.MessageRow) error
	DeleteMessagesFrom(sessionKey string, fromSeq int) error
	UpdateMessagesExcludedWithJSON(sessionKey string, messages []store.MessageRow) error
	PruneExcluded(sessionKey string, keepCount int) (int, error)
	LoadMessagesFullBeforeSeq(sessionKey string, beforeSeq int) ([]store.MessageRowFull, error)
	// UpdateFirstInMemorySeq is the targeted metadata write used by the
	// eviction path (eviction.go, EvictExcludedMessages) when the fold did not
	// change the summary. It is part of the seam so the T9 lock-scope test can
	// park that write and prove it runs with sm.mu released.
	UpdateFirstInMemorySeq(sessionKey string, seq int) error
}

// repoFor resolves the repository the save paths in this file write through.
// It is a variable (not a direct sm.sessionRepo read) so tests can swap in a
// fake; in production it always yields sm.sessionRepo, or nil when no
// repository is configured.
var repoFor = func(sm *SessionManager) sessionRepo {
	if sm.sessionRepo == nil {
		return nil
	}
	return sm.sessionRepo
}

// unlocked runs fn with sm.mu released and re-acquires it before returning —
// including when fn panics.
//
// The save and eviction paths all follow the same three-phase shape: collect
// with the write lock held, run the I/O plus the JSON encoding with the lock
// released, then relock and reconcile the bookkeeping. Writing that inline as
// `sm.mu.Unlock(); fn(); sm.mu.Lock()` is NOT panic-safe: if fn panics, the
// lock is left UNLOCKED and the caller's deferred Unlock trips sync's
// "unlock of unlocked mutex" (an unrecoverable throw), masking the original
// failure and leaving the manager unusable even when the panic is recovered
// upstream. The deferred Lock below re-acquires the lock while the panic
// unwinds, so recovery upstream sees the real panic and a consistent manager.
//
// Every unlock/relock site in the package goes through this helper, so the
// property holds by construction rather than site by site: the free functions
// that hold the whole payload (writeFullUnlocked, writeExcludedRangeUnlocked,
// persistEvictionBoundary) and the direct repository writes in
// saveMetaOnlyUnlocked, saveIncrementalUnlocked and saveDeleteLastUnlocked.
//
// The caller MUST hold the write lock. fn must not call back into the manager
// methods that take it (that would deadlock): the payloads above are exactly
// the ones that provably do not.
func (sm *SessionManager) unlocked(fn func()) {
	sm.mu.Unlock()
	defer sm.mu.Lock()
	fn()
}

// The concrete SQLite repository must keep satisfying the seam above.
var _ sessionRepo = (*store.SessionRepo)(nil)

func (sm *SessionManager) Save(key string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.saveUnlocked(key)
}

// SaveAll persists every session currently resident in memory and returns the
// number of sessions saved. Sessions already evicted by the LRU/TTL path are
// not resident and are therefore already durable, so they are skipped.
//
// It is intended for graceful shutdown: it guarantees no in-memory mutation
// survives only in RAM when the process exits. A save failure for one session
// is logged and does not abort the others — shutdown must always be able to
// proceed — so the error return only reports a count of failures.
func (sm *SessionManager) SaveAll() (saved int, failed int) {
	sm.ensureLoaded()

	if sm.sessionRepo == nil {
		// Nothing to flush to: ensureLoaded already warned once about the
		// missing repository. Report (0, 0) so shutdown can proceed unblocked.
		logger.DebugCF("session", "SaveAll skipped: SessionManager has no session repository", nil)
		return 0, 0
	}

	// Snapshot the resident keys and release the lock before any disk I/O.
	// saveUnlocked releases sm.mu while it writes and re-acquires it
	// afterwards (comparing saveEpoch to detect concurrent mutation), so
	// holding the lock across this loop would deadlock against that inner
	// re-acquire — same reason evictIfNeeded/saveForEviction drop the lock.
	keys := sm.residentKeys()
	sort.Strings(keys)

	for _, key := range keys {
		persisted, err := sm.saveIfResident(key)
		if err != nil {
			failed++
			logger.WarnCF("session", "SaveAll failed to persist session", map[string]interface{}{
				"session_key": key,
				"error":       err.Error(),
			})
			continue
		}
		if persisted {
			saved++
		}
	}

	logger.InfoCF("session", "SaveAll flushed resident sessions", map[string]interface{}{
		"resident": len(keys),
		"saved":    saved,
		"failed":   failed,
	})
	return saved, failed
}

// saveIfResident persists key through the standard save path, but only while
// the session is still resident. A concurrent eviction may have dropped it
// between the SaveAll snapshot and this call; an evicted session was saved by
// the eviction path, so it is skipped (persisted=false) rather than reloaded
// into memory. Returns persisted=true when the session was written (or was
// already clean, which is equally durable).
//
// The write lock is taken here and handed to saveUnlocked, which owns
// releasing it for the duration of its disk I/O — exactly like Save does.
func (sm *SessionManager) saveIfResident(key string) (persisted bool, err error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.sessions[key]; !ok {
		return false, nil
	}
	if err := sm.saveUnlocked(key); err != nil {
		return false, err
	}
	return true, nil
}

// sessionMetaFromSession builds a SessionMeta for persistence.
// FirstInMemorySeq is included so every save path persists the eviction
// boundary durably; cold-load restores it from SQLite on restart.
func sessionMetaFromSession(s *Session) store.SessionMeta {
	return store.SessionMeta{
		Key:              s.Key,
		Name:             s.Name,
		Mode:             s.Mode,
		Summary:          s.Summary,
		VerboseLevel:     s.VerboseLevel,
		Model:            s.Model,
		ThinkingLevel:    s.ThinkingLevel,
		Folder:           s.Folder,
		SubagentStatus:   s.SubagentStatus,
		InputTokens:      s.InputTokens,
		OutputTokens:     s.OutputTokens,
		CompactionCount:  s.CompactionCount,
		FirstInMemorySeq: s.firstInMemorySeq,
		CreatedAt:        s.Created,
		UpdatedAt:        s.Updated,
	}
}

// saveMetaOnlyUnlocked persists only session metadata (no message rewrite).
// Caller must hold sm.mu.
func (sm *SessionManager) saveMetaOnlyUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	epoch := session.saveEpoch
	// The metadata write is the only I/O on this path: it runs with the lock
	// released, through unlocked() so that a panic inside it cannot leave the
	// lock free for the caller's deferred Unlock (R3.3).
	var err error
	sm.unlocked(func() { err = repo.UpsertSession(meta) })

	if err != nil {
		return fmt.Errorf("save session meta %q: %w", key, err)
	}
	// Epoch guard: UpsertSession is idempotent, so a stale write is harmless.
	// But if the session was mutated while the I/O was in flight, skip the
	// bookkeeping (leave metaDirty set) so the concurrent mutation is
	// re-persisted by the next Save.
	if session.saveEpoch != epoch {
		return nil
	}
	session.metaDirty = false
	return nil
}

// saveFullUnlocked persists metadata + all messages (DELETE + INSERT).
// Used for initial saves, after truncation, or when messages were reordered.
// Caller must hold sm.mu.
//
// Lock scope: the caller's write lock covers ONLY the collection of a
// consistent snapshot of what has to be written. Every SQLite round trip —
// including the LoadMessagesFullBeforeSeq read of the evicted prefix — and
// every json.Marshal/json.Unmarshal runs with the lock RELEASED, and the lock
// is re-acquired afterwards to reconcile the bookkeeping. This matters because
// this is the path that runs right after every compaction
// (firstInMemorySeq > 0) and inside the LRU-eviction cascades: holding sm.mu
// across its I/O meant any concurrent GetHistoryView (a TUI frame) blocked for
// the whole rewrite.
func (sm *SessionManager) saveFullUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	// ---- Phase 1 (sm.mu held): collect, no I/O and no JSON work ----
	//
	// The resident messages come from the copy-on-write view snapshot
	// (view.go): a private, immutable copy stamped with the saveEpoch it was
	// taken at, so it can be marshalled after the lock is dropped without
	// ever aliasing session.Messages. viewLocked returns the published
	// snapshot when it is fresh — the common case, since every structural
	// writer publishes — and pays one O(n) copy otherwise (the same copy
	// GetHistoryView would pay for a stale snapshot). Nothing else about the
	// message slice is touched here.
	meta := sessionMetaFromSession(session)
	epoch := session.saveEpoch
	firstInMemorySeq := session.firstInMemorySeq
	messages := sm.viewLocked(session)

	// ---- Phase 2 (lock released): all I/O and all JSON encoding ----
	//
	// writeFullUnlocked owns everything that follows: the evicted-prefix read,
	// the unmarshal of each evicted row, the marshal of each resident message,
	// and the SQLite writes. Keeping it in a single helper makes the contract
	// unmissable — sm.mu is released around it and re-acquired right after by
	// unlocked(), so no error path can return with the lock held, leak it, or
	// leave it unlocked when the payload panics.
	var (
		evicted int
		pruned  int
		ioErr   error
	)
	sm.unlocked(func() {
		evicted, pruned, ioErr = writeFullUnlocked(repo, key, meta, firstInMemorySeq, messages)
	})

	if ioErr != nil {
		return fmt.Errorf("save session %q to sqlite: %w", key, ioErr)
	}

	// Epoch guard: ReplaceMessages is destructive (DELETE all + re-insert).
	// If the session was mutated while the I/O was in flight, the rows we
	// wrote may be interleaved with the concurrent save's writes in any
	// order, and the bookkeeping below (firstInMemorySeq, evictedTotal,
	// clearDirtyFlags) would clobber the concurrent mutation's dirty state.
	// Force a clean full rewrite from the in-memory source of truth on the
	// next Save — that heals any I/O-ordering damage.
	if session.saveEpoch != epoch {
		session.lastPersistedSeq = -1
		return nil
	}

	// Residency guard: the epoch check above covers concurrent mutations, but
	// a concurrent eviction can also have dropped this session from
	// sm.sessions while the I/O was in flight (and the next access may have
	// reloaded a different Session object for the same key, rebuilding its
	// bookkeeping from disk). The write itself is durable and stays; only the
	// in-memory bookkeeping below is skipped, because it belongs to the object
	// we collected from, not to the resident one.
	if cur, ok := sm.sessions[key]; !ok || cur != session {
		return nil
	}

	// Bookkeeping must reflect that re-materialized evicted rows are persisted
	// but still NOT resident in the in-memory slice. The in-memory messages
	// start at absolute seq `evicted`; only the rows are renumbered, not the
	// memory residency. Set AFTER the I/O + epoch guard: if the session is
	// mutated mid-flight, the guard above forces a rewrite via
	// lastPersistedSeq == -1, and pre-setting these fields would defeat it.
	session.lastPersistedSeq = len(session.Messages) - 1
	session.firstInMemorySeq = evicted
	session.evictedTotal = evicted - pruned
	if session.evictedTotal < 0 {
		session.evictedTotal = 0
	}
	session.clearDirtyFlags()
	return nil
}

// writeFullUnlocked performs the whole full-rewrite payload: the read of the
// evicted prefix, the JSON encode/decode of every row and the SQLite writes.
//
// It MUST run with sm.mu released (saveFullUnlocked owns the release/acquire
// around it): that is the whole point of the T10 split — this is the expensive
// part of a full rewrite and it used to hold the write lock, freezing every
// concurrent GetHistoryView. It is a free function (no sm.mu access at all) so
// that the "no I/O and no JSON under the lock" property is structural rather
// than something a later edit can quietly break.
//
// Returns the number of re-materialized evicted rows and the number pruned by
// PruneExcluded, both needed by the caller's post-I/O bookkeeping.
func writeFullUnlocked(repo sessionRepo, key string, meta store.SessionMeta, firstInMemorySeq int, messages []providers.Message) (evicted int, pruned int, err error) {
	// If messages were evicted from memory (firstInMemorySeq > 0), the
	// evicted rows still live in SQLite. A full rewrite runs ReplaceMessages
	// (DELETE all + re-insert), which would permanently destroy those rows.
	// Re-materialize them from SQLite and prepend them so the rewrite keeps
	// the full persisted set (evicted rows keep their persisted excluded
	// flag, so the model never sees them and the eviction gap is closed:
	// seqs re-base from 0).
	var evictedRows []store.MessageRowFull
	if firstInMemorySeq > 0 {
		evictedRows, err = repo.LoadMessagesFullBeforeSeq(key, firstInMemorySeq)
		if err != nil {
			return 0, 0, fmt.Errorf("re-materialize evicted messages %q: %w", key, err)
		}
	}
	evicted = len(evictedRows)

	rows := make([]store.MessageRow, 0, evicted+len(messages))
	for i, evictedRow := range evictedRows {
		var evt providers.Message
		if uerr := json.Unmarshal([]byte(evictedRow.JSON), &evt); uerr != nil {
			// Corrupted row: skip it for real. The slice used to be
			// pre-allocated to evicted+len(messages) and indexed by position, so
			// this continue left a zero MessageRow{} in its slot — Seq 0, empty
			// role and empty JSON — which ReplaceMessages then wrote as a real
			// row: at seq 0 it clobbered the row that belongs there, and for any
			// other index it collided with the genuine seq-0 row on the
			// UNIQUE(session_key, seq) constraint and failed the whole rewrite.
			// The append below is what makes the skip real; Seq keeps the
			// row's absolute index so the evicted-prefix seqs (holes included)
			// and the resident block at `evicted+i` stay exactly as before.
			continue
		}
		// Carry the persisted excluded flag through the rewrite: the evicted
		// prefix can contain non-excluded rows (the original request folded
		// into a summary stays excluded=false), and hardcoding true here
		// would corrupt their state on every full rewrite.
		rows = append(rows, store.MessageRow{
			Seq:      i,
			Role:     evt.Role,
			JSON:     evictedRow.JSON,
			Excluded: evictedRow.Excluded,
		})
	}
	for i, msg := range messages {
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			return 0, 0, fmt.Errorf("marshal message %d: %w", i, mErr)
		}
		rows = append(rows, store.MessageRow{
			Seq:      evicted + i,
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}

	if err = repo.UpsertSession(meta); err != nil {
		return 0, 0, err
	}
	if err = repo.ReplaceMessages(key, rows); err != nil {
		return 0, 0, err
	}
	// Pruning the oldest excluded overflow is best-effort: a failure only
	// leaves extra rows in SQLite, so the error is not propagated (unchanged
	// pre-T10 behaviour).
	pruned, _ = repo.PruneExcluded(key, maxStoredMessages)
	return evicted, pruned, nil
}

// saveIncrementalUnlocked persists metadata + only new/modified messages.
// Uses InsertMessages (batch) for appended messages and UpdateMessage for
// in-place changes (e.g., streaming). Caller must hold sm.mu.
func (sm *SessionManager) saveIncrementalUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	startSeq := session.lastPersistedSeq + 1

	// Build batch rows for all new messages
	var newRows []store.MessageRow
	for i := startSeq; i < len(session.Messages); i++ {
		msg := session.Messages[i]
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			return fmt.Errorf("marshal message %d: %w", i, mErr)
		}
		newRows = append(newRows, store.MessageRow{
			Seq:      session.seqForIndex(i),
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}

	// Build update rows for every in-place-modified message in the already
	// persisted region [modifiedFrom-1 .. startSeq-1]. The old code only
	// updated the LAST message, which silently dropped the final assistant
	// replacement (with tool_calls) whenever tool results had been appended
	// after it — leaving a stale streaming row in SQLite.
	var updateRows []store.MessageRow
	if session.modifiedFrom > 0 {
		fromIdx := session.modifiedFrom - 1
		if fromIdx > len(session.Messages)-1 {
			fromIdx = len(session.Messages) // index vanished (e.g. message deleted); nothing to update
		}
		if fromIdx < startSeq {
			endIdx := startSeq - 1
			if endIdx > len(session.Messages)-1 {
				endIdx = len(session.Messages) - 1
			}
			for i := fromIdx; i <= endIdx; i++ {
				msg := session.Messages[i]
				msgJSON, mErr := json.Marshal(msg)
				if mErr != nil {
					return fmt.Errorf("marshal message %d: %w", i, mErr)
				}
				updateRows = append(updateRows, store.MessageRow{
					Seq:      session.seqForIndex(i),
					Role:     msg.Role,
					JSON:     string(msgJSON),
					Excluded: msg.ExcludeFromContext,
				})
			}
		}
	}

	// Single lock release for all I/O (unlocked() so a panicking driver cannot
	// leave the lock free for the caller's deferred Unlock, R3.3).
	epoch := session.saveEpoch
	var err error
	sm.unlocked(func() {
		err = repo.UpsertSession(meta)
		if err == nil && len(newRows) > 0 {
			err = repo.InsertMessages(key, newRows)
		}
		if err == nil && len(updateRows) > 0 {
			err = repo.UpdateMessages(key, updateRows)
		}
	})

	if err != nil {
		return fmt.Errorf("incremental save %q: %w", key, err)
	}

	// Epoch guard: INSERT OR REPLACE / UPDATE are idempotent, so a stale
	// write is harmless. But if the session was mutated while the I/O was in
	// flight, skip the bookkeeping (leave dirty flags set) so the concurrent
	// mutation is re-persisted by the next Save.
	if session.saveEpoch != epoch {
		return nil
	}

	session.lastPersistedSeq = len(session.Messages) - 1
	// Only clear what this path persisted. excludedRange/lastMsgDeleted are
	// owned by their own save paths — wiping them here (the old
	// clearDirtyFlags()) could drop pending work when maybeFlushStream runs
	// saveIncrementalUnlocked directly.
	session.msgsAppended = 0
	session.modifiedFrom = 0
	session.metaDirty = false
	return nil
}

// saveDeleteLastUnlocked persists metadata + deletes the last message from SQLite.
// Used when RemoveLastMessage removes the final message.
// Caller must hold sm.mu.
func (sm *SessionManager) saveDeleteLastUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	fromSeq := session.deleteFromSeq
	epoch := session.saveEpoch

	// Both writes run with the lock released, through unlocked() so that a
	// panic inside them cannot leave the lock free for the caller's deferred
	// Unlock (R3.3).
	var err error
	sm.unlocked(func() {
		err = repo.UpsertSession(meta)
		if err == nil {
			// Watermark delete (seq >= fromSeq) instead of position-based
			// DeleteLastMessage. fromSeq was captured at deletion time, so a
			// concurrent append that reuses the same seq slot is not at risk of
			// being deleted by a stale "delete max seq" — and the operation is
			// idempotent (safe to retry).
			err = repo.DeleteMessagesFrom(key, fromSeq)
		}
	})

	if err != nil {
		return fmt.Errorf("delete-last save %q: %w", key, err)
	}

	// Epoch guard: the watermark delete is destructive. If the session was
	// mutated while the I/O was in flight (e.g. a concurrent append reused
	// the deleted seq slot), the delete may have wiped the new row. Force a
	// clean full rewrite from the in-memory source of truth on the next Save.
	if session.saveEpoch != epoch {
		session.lastPersistedSeq = -1
		return nil
	}

	session.lastPersistedSeq = len(session.Messages) - 1
	// Only clear what this path persisted. If the deleted message was the
	// modified one, its index now lies past the end of the slice and the
	// incremental update loop safely skips it; otherwise a pending
	// modification must still be persisted by a chained incremental save.
	session.lastMsgDeleted = false
	session.deleteFromSeq = 0
	session.metaDirty = false
	return nil
}

// saveExcludedRangeUnlocked persists metadata + updates the excluded flag
// for a range of messages in SQLite. Used when ExcludeOldMessagesFromContext
// marks messages as excluded. Updates both the excluded column and the
// serialized JSON to keep them in sync.
// Caller must hold sm.mu.
//
// Lock scope (T11): the caller's write lock covers ONLY the collection of a
// private copy of the messages in the range. Every json.Marshal of them and
// both SQLite round trips (UpsertSession + UpdateMessagesExcludedWithJSON) run
// with the lock RELEASED, and the lock is re-acquired afterwards to reconcile
// the bookkeeping.
//
// This was the third and last save path still doing real work under the write
// lock, and the one that froze the TUI on compaction: the range a compaction
// excludes is typically thousands of messages, and re-marshalling all of them
// under sm.mu blocked every concurrent GetHistoryView (a TUI frame) for the
// whole encode. The two sibling paths already use this exact pattern —
// saveFullUnlocked → writeFullUnlocked (this file) and EvictExcludedMessages →
// persistEvictionBoundary (eviction.go). Nothing about the routing (saveUnlocked
// and its priority order), about *what* this path writes or about *when* it
// writes it changes here: only where the lock sits relative to the marshal/I/O.
func (sm *SessionManager) saveExcludedRangeUnlocked(key string) error {
	repo := repoFor(sm)
	if repo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	// ---- Phase 1 (sm.mu held): collect, no I/O and no JSON work ----
	//
	// What has to be re-marshalled is the RESIDENT slice: excludedRange is an
	// in-memory index window [from, to) into session.Messages, the old loop
	// bound it with `i < len(session.Messages)` and the rows are addressed by
	// the absolute seq firstInMemorySeq+i. A range that is no longer covered by
	// the resident slice (its prefix was evacuated to SQLite) therefore
	// contributes no row at all — metadata only — exactly as before. Reading
	// the missing prefix back from SQLite would CHANGE what this path writes
	// instead of preserving today's behaviour, so the window is clamped to the
	// resident slice here and the content is taken from memory.
	//
	// The content of the window comes from a private copy of just the flagged
	// messages (cloneMessages, view.go) rather than from the whole-session COW
	// snapshot saveFullUnlocked uses:
	//
	//   - only [from, to) is involved, so the copy costs O(range) message
	//     headers (192 B each) instead of O(history) — the expensive part of
	//     this path is the JSON encoding, and that is what moves out of the
	//     lock;
	//   - reading the live slice under the lock keeps the read semantics
	//     exactly equal to the pre-T11 inline loop, which is what the
	//     byte-parity test pins: a row flagged in place by a writer that did
	//     not bump the epoch (and therefore did not invalidate the published
	//     snapshot) is still persisted with the flag it has in memory right
	//     now.
	//
	// The copy never aliases session.Messages, so phase 2 may marshal it after
	// the lock is dropped (the same guarantee GetHistoryView relies on).
	meta := sessionMetaFromSession(session)
	epoch := session.saveEpoch
	from, to := session.excludedRange[0], session.excludedRange[1]
	if from < 0 {
		from = 0
	}
	if to > len(session.Messages) {
		to = len(session.Messages)
	}
	if from > to {
		from = to
	}
	firstSeq := session.firstInMemorySeq + from
	window := cloneMessages(session.Messages[from:to])

	// ---- Phase 2 (lock released): all JSON encoding + both SQLite writes ----
	//
	// writeExcludedRangeUnlocked owns the marshal of the window and the two
	// statements. It is a free function (no sm.mu access at all), so "no I/O
	// and no JSON under the lock" is structural rather than a convention a
	// later edit can quietly break — there is no way to reach it while holding
	// the manager lock. unlocked() re-acquires the lock immediately after it
	// (panic included), so no error path can return with the lock held, leak
	// it, or leave it unlocked.
	var writeErr error
	sm.unlocked(func() {
		writeErr = writeExcludedRangeUnlocked(repo, key, meta, firstSeq, window)
	})

	if writeErr != nil {
		return fmt.Errorf("excluded-range save %q: %w", key, writeErr)
	}

	// Epoch guard + healing, the same contract as saveFullUnlocked: if the
	// session was mutated while the marshalling/UPDATE was in flight, the rows
	// we wrote may be interleaved with the concurrent save's writes in any
	// order, and the bookkeeping below would clobber the concurrent mutation's
	// dirty state. Leave excludedRange set (so the range is simply re-written
	// by the next Save) and force that Save to rebuild the whole history from
	// the in-memory source of truth (lastPersistedSeq == -1 is what makes
	// saveUnlocked pick saveFullUnlocked), which heals any ordering damage.
	if session.saveEpoch != epoch {
		session.lastPersistedSeq = -1
		return nil
	}

	// Residency guard, the same as saveFullUnlocked: a concurrent eviction can
	// have dropped this session from sm.sessions while the I/O was in flight —
	// and the next access may have reloaded a DIFFERENT Session object for the
	// same key, with its bookkeeping rebuilt from disk. The write itself is
	// durable and stays; only the in-memory bookkeeping below is skipped,
	// because it belongs to the object we collected from, not to the resident
	// one.
	if cur, ok := sm.sessions[key]; !ok || cur != session {
		return nil
	}

	// Only clear what this path persisted. Appended/modified messages outside
	// the excluded range still need an incremental save — do NOT wipe them here
	// (the old clearDirtyFlags() silently dropped a pending streaming
	// finalization when compaction ran in the same Save call).
	session.excludedRange = [2]int{}
	session.metaDirty = false
	return nil
}

// writeExcludedRangeUnlocked performs the excluded-range payload: the
// json.Marshal of every message in the window and the two SQLite writes.
//
// It MUST run with sm.mu released (saveExcludedRangeUnlocked owns the
// release/acquire around it): that is the whole point of the T11 split — this
// is the expensive part of the path and it used to hold the write lock,
// freezing every concurrent GetHistoryView. Being a free function (no sm.mu
// access at all) makes the property structural, exactly like its siblings
// writeFullUnlocked (this file) and persistEvictionBoundary (eviction.go).
//
// window is the private, immutable copy collected under the lock and firstSeq
// is the absolute SQLite seq of its first element, so the rows keep the
// `seq = firstInMemorySeq + sliceIndex` mapping the caller used to compute
// inline.
func writeExcludedRangeUnlocked(repo sessionRepo, key string, meta store.SessionMeta, firstSeq int, window []providers.Message) error {
	// Build update rows for the excluded range (re-marshal with updated flag)
	rows := make([]store.MessageRow, 0, len(window))
	for i, msg := range window {
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			return fmt.Errorf("marshal excluded message seq %d: %w", firstSeq+i, mErr)
		}
		rows = append(rows, store.MessageRow{
			Seq:      firstSeq + i,
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}

	if err := repo.UpsertSession(meta); err != nil {
		return err
	}
	return repo.UpdateMessagesExcludedWithJSON(key, rows)
}

// saveUnlocked auto-detects the optimal save strategy:
//   - If no session repository or session not in memory: no-op
//   - If session is new (lastPersistedSeq == -1): full rewrite
//   - If messages were truncated: targeted DELETE from SQLite
//   - If last message was deleted: targeted DELETE from SQLite
//   - If excluded range changed: targeted UPDATE from SQLite
//   - If messages were appended or modified: incremental save
//   - If only metadata changed: metadata-only save
//   - Otherwise: no-op (nothing changed)
//
// Caller must hold sm.mu.
func (sm *SessionManager) saveUnlocked(key string) error {
	if sm.sessionRepo == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}
	// Keep the listing index in step with whatever we are about to persist,
	// so an idle eviction that lands mid-save still leaves a usable shell.
	sm.syncSessionMetaLocked(session)

	// Full rewrite needed: new session (never persisted)
	if session.lastPersistedSeq == -1 {
		return sm.saveFullUnlocked(key)
	}

	// Targeted DELETE: last message was removed
	if session.lastMsgDeleted {
		if err := sm.saveDeleteLastUnlocked(key); err != nil {
			return err
		}
		// saveDeleteLastUnlocked forces a full rewrite when the session was
		// mutated while its DELETE I/O was in flight (epoch mismatch): the
		// DELETE may have raced ahead of the concurrent INSERT, so the DB
		// state is uncertain and only a full rewrite can re-establish it.
		// lastPersistedSeq == -1 signals that.
		if session.lastPersistedSeq == -1 {
			return sm.saveFullUnlocked(key)
		}
		// Fall through: a pending in-place modification on an earlier
		// message may still need persisting.
	}

	// Targeted UPDATE: excluded flag changed on a range
	if session.excludedRange[1] > session.excludedRange[0] {
		if err := sm.saveExcludedRangeUnlocked(key); err != nil {
			return err
		}
		// saveExcludedRangeUnlocked forces a full rewrite when the session was
		// mutated while its UPDATE I/O was in flight (epoch mismatch): the rows
		// it wrote may be interleaved with the concurrent save's writes in any
		// order, and the DELETE-all + re-insert of a full rewrite is the only
		// thing that re-establishes the DB from the in-memory source of truth
		// (lastPersistedSeq == -1 signals it). Re-verify it here, exactly like
		// the delete-last branch above: WITHOUT this check the fall-through
		// below reaches saveIncrementalUnlocked, which resets lastPersistedSeq
		// unconditionally and silently swallows the heal.
		if session.lastPersistedSeq == -1 {
			return sm.saveFullUnlocked(key)
		}
		// Fall through: appended/modified messages outside the excluded
		// range may still need persisting (e.g. a streaming finalization
		// that happened in the same Save call as compaction).
	}

	// Incremental: new messages appended or existing modified
	if session.msgsAppended > 0 || session.modifiedFrom > 0 {
		return sm.saveIncrementalUnlocked(key)
	}

	// Metadata only
	if session.metaDirty {
		return sm.saveMetaOnlyUnlocked(key)
	}

	// Nothing changed
	return nil
}
