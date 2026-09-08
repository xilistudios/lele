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

	if sm.store == nil {
		// Nothing to flush to: ensureLoaded already warned once about the
		// missing store. Report (0, 0) so shutdown can proceed unblocked.
		logger.DebugCF("session", "SaveAll skipped: SessionManager has no store", nil)
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
	if sm.store == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	epoch := session.saveEpoch
	sm.mu.Unlock()
	err := sm.store.Sessions().UpsertSession(meta)
	sm.mu.Lock()

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
func (sm *SessionManager) saveFullUnlocked(key string) error {
	if sm.store == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	// If messages were evicted from memory (firstInMemorySeq > 0), the
	// evicted rows still live in SQLite. A full rewrite runs ReplaceMessages
	// (DELETE all + re-insert), which would permanently destroy those rows.
	// Re-materialize them from SQLite and prepend them so the rewrite keeps
	// the full persisted set (evicted rows keep excluded=1, so the model
	// never sees them and the eviction gap is closed: seqs re-base from 0).
	var evictedRows []store.MessageRowFull
	var err error
	if session.firstInMemorySeq > 0 {
		evictedRows, err = sm.store.Sessions().LoadMessagesFullBeforeSeq(key, session.firstInMemorySeq)
		if err != nil {
			return fmt.Errorf("re-materialize evicted messages %q: %w", key, err)
		}
	}

	rows := make([]store.MessageRow, len(evictedRows)+len(session.Messages))
	for i, evicted := range evictedRows {
		var evt providers.Message
		if uerr := json.Unmarshal([]byte(evicted.JSON), &evt); uerr != nil {
			continue // skip corrupted rows
		}
		// Carry the persisted excluded flag through the rewrite: the evicted
		// prefix can contain non-excluded rows (the original request folded
		// into a summary stays excluded=false), and hardcoding true here
		// would corrupt their state on every full rewrite.
		rows[i] = store.MessageRow{
			Seq:      i,
			Role:     evt.Role,
			JSON:     evicted.JSON,
			Excluded: evicted.Excluded,
		}
	}
	offset := len(evictedRows)
	for i, msg := range session.Messages {
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			return fmt.Errorf("marshal message %d: %w", i, mErr)
		}
		rows[offset+i] = store.MessageRow{
			Seq:      offset + i,
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		}
	}

	meta := sessionMetaFromSession(session)

	// Release lock during I/O
	pruned := 0
	epoch := session.saveEpoch
	sm.mu.Unlock()
	err = sm.store.Sessions().UpsertSession(meta)
	if err == nil {
		err = sm.store.Sessions().ReplaceMessages(key, rows)
		if err == nil {
			pruned, _ = sm.store.Sessions().PruneExcluded(key, maxStoredMessages)
		}
	}
	sm.mu.Lock()

	if err != nil {
		return fmt.Errorf("save session %q to sqlite: %w", key, err)
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

	// Bookkeeping must reflect that re-materialized evicted rows are persisted
	// but still NOT resident in the in-memory slice. The in-memory messages
	// start at absolute seq len(evictedJSONs); only the rows are renumbered,
	// not the memory residency. Set AFTER the I/O + epoch guard: if the
	// session is mutated mid-flight, the guard above forces a rewrite via
	// lastPersistedSeq == -1, and pre-setting these fields would defeat it.
	session.lastPersistedSeq = len(session.Messages) - 1
	session.firstInMemorySeq = len(evictedRows)
	session.evictedTotal = len(evictedRows) - pruned
	if session.evictedTotal < 0 {
		session.evictedTotal = 0
	}
	session.clearDirtyFlags()
	return nil
}

// saveIncrementalUnlocked persists metadata + only new/modified messages.
// Uses InsertMessages (batch) for appended messages and UpdateMessage for
// in-place changes (e.g., streaming). Caller must hold sm.mu.
func (sm *SessionManager) saveIncrementalUnlocked(key string) error {
	if sm.store == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	repo := sm.store.Sessions()
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

	// Single lock release for all I/O
	epoch := session.saveEpoch
	sm.mu.Unlock()
	err := repo.UpsertSession(meta)
	if err == nil && len(newRows) > 0 {
		err = repo.InsertMessages(key, newRows)
	}
	if err == nil && len(updateRows) > 0 {
		err = repo.UpdateMessages(key, updateRows)
	}
	sm.mu.Lock()

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
	if sm.store == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	repo := sm.store.Sessions()
	fromSeq := session.deleteFromSeq
	epoch := session.saveEpoch

	sm.mu.Unlock()
	err := repo.UpsertSession(meta)
	if err == nil {
		// Watermark delete (seq >= fromSeq) instead of position-based
		// DeleteLastMessage. fromSeq was captured at deletion time, so a
		// concurrent append that reuses the same seq slot is not at risk of
		// being deleted by a stale "delete max seq" — and the operation is
		// idempotent (safe to retry).
		err = repo.DeleteMessagesFrom(key, fromSeq)
	}
	sm.mu.Lock()

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
func (sm *SessionManager) saveExcludedRangeUnlocked(key string) error {
	if sm.store == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	meta := sessionMetaFromSession(session)
	repo := sm.store.Sessions()
	from, to := session.excludedRange[0], session.excludedRange[1]

	// Build update rows for the excluded range (re-marshal with updated flag)
	rows := make([]store.MessageRow, 0, to-from)
	for i := from; i < to && i < len(session.Messages); i++ {
		msg := session.Messages[i]
		msgJSON, mErr := json.Marshal(msg)
		if mErr != nil {
			return fmt.Errorf("marshal excluded message %d: %w", i, mErr)
		}
		rows = append(rows, store.MessageRow{
			Seq:      session.seqForIndex(i),
			Role:     msg.Role,
			JSON:     string(msgJSON),
			Excluded: msg.ExcludeFromContext,
		})
	}

	epoch := session.saveEpoch
	sm.mu.Unlock()
	err := repo.UpsertSession(meta)
	if err == nil {
		err = repo.UpdateMessagesExcludedWithJSON(key, rows)
	}
	sm.mu.Lock()

	if err != nil {
		return fmt.Errorf("excluded-range save %q: %w", key, err)
	}

	// Epoch guard: the excluded-flag UPDATE is idempotent, so a stale write
	// is harmless. But if the session was mutated while the I/O was in
	// flight, skip the bookkeeping (leave excludedRange set) so the
	// concurrent mutation is re-persisted by the next Save.
	if session.saveEpoch != epoch {
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

// saveUnlocked auto-detects the optimal save strategy:
//   - If no store or session not in memory: no-op
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
	if sm.store == nil {
		return nil
	}
	session, ok := sm.sessions[key]
	if !ok {
		return nil
	}

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
