package session

// Memory-pressure management: LRU eviction and idle cleanup.
//
// Eviction only drops messages from memory; the SQLite rows remain and can be
// reloaded (window.go / LoadMessagesWindow). CleanupIdleSessions is the
// time-based counterpart driven by StartCleanupGoroutine.

import (
	"sort"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

// touchSession updates the last access time for a session.
// Caller MUST hold sm.mu (write lock). Writing to the accessTimes map
// requires exclusive access — a read lock is NOT sufficient.
func (sm *SessionManager) touchSession(key string) {
	sm.accessTimes[key] = time.Now()
}

// saveForEviction saves the session and reports whether it is still safe to
// evict it from memory. saveUnlocked releases the lock during disk I/O, so a
// concurrent goroutine may touch the session in that window; if that happens
// the caller must NOT delete the in-memory copy (it may contain data newer
// than the persisted snapshot). Caller must hold sm.mu.
func (sm *SessionManager) saveForEviction(key string) bool {
	prevAccess, hadAccess := sm.accessTimes[key]
	_ = sm.saveUnlocked(key)
	// If the session was accessed while our I/O was in flight, keep it.
	// (A missing accessTimes entry both before and after means the session
	// was never touched, so eviction is safe.)
	curAccess, hasAccess := sm.accessTimes[key]
	if hadAccess != hasAccess {
		return false
	}
	if hasAccess && !curAccess.Equal(prevAccess) {
		return false
	}
	return true
}

// evictIfNeeded evicts idle sessions when the in-memory session count
// exceeds maxInMemory. Caller must hold sm.mu (write lock).
func (sm *SessionManager) evictIfNeeded() {
	if sm.maxInMemory <= 0 {
		return
	}

	// First pass: evict sessions that have been idle longer than evictionTTL
	if sm.evictionTTL > 0 {
		cutoff := time.Now().Add(-sm.evictionTTL)
		for key, lastAccess := range sm.accessTimes {
			if lastAccess.Before(cutoff) {
				if session, ok := sm.sessions[key]; ok {
					if !sm.saveForEviction(key) {
						continue
					}
					sm.syncSessionMetaLocked(session)
				}
				delete(sm.sessions, key)
				delete(sm.accessTimes, key)
				// Also clean up sessionMeta for evicted sessions whose
				// underlying file no longer exists or is stale.
				// Keep metadata for sessions that still exist on disk
				// (needed for ListSessions), but only if the session
				// might be reloaded. We keep it — metadata is tiny.
			}
		}
	}

	// Second pass: if still over limit, evict least recently used
	if len(sm.sessions) <= sm.maxInMemory {
		return
	}

	// Find LRU sessions to evict
	type sessionAccess struct {
		key  string
		time time.Time
	}
	accesses := make([]sessionAccess, 0, len(sm.accessTimes))
	for key, t := range sm.accessTimes {
		if _, ok := sm.sessions[key]; ok {
			accesses = append(accesses, sessionAccess{key, t})
		}
	}
	sort.Slice(accesses, func(i, j int) bool {
		return accesses[i].time.Before(accesses[j].time)
	})

	toEvict := len(sm.sessions) - sm.maxInMemory
	for i := 0; i < toEvict && i < len(accesses); i++ {
		key := accesses[i].key
		session, ok := sm.sessions[key]
		if !ok {
			continue
		}
		if !sm.saveForEviction(key) {
			continue
		}
		sm.syncSessionMetaLocked(session)
		delete(sm.sessions, key)
		delete(sm.accessTimes, key)
		logger.InfoCF("session", "LRU evicted session", map[string]interface{}{
			"session_key": key,
		})
	}
}

// CleanupIdleSessions evicts sessions that have been idle longer than evictionTTL.
// Unlike evictIfNeeded, this runs unconditionally (not just when adding new sessions).
// Should be called periodically (e.g., via a background goroutine) to ensure
// idle sessions are evicted even when no new sessions are being created.
func (sm *SessionManager) CleanupIdleSessions() int {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	evicted := 0
	if sm.evictionTTL > 0 {
		cutoff := time.Now().Add(-sm.evictionTTL)
		for key, lastAccess := range sm.accessTimes {
			if lastAccess.Before(cutoff) {
				if session, ok := sm.sessions[key]; ok {
					if !sm.saveForEviction(key) {
						continue
					}
					// Listing index must outlive the resident copy: TUI/WebUI
					// read Name/Updated from sessionMeta for non-resident keys.
					sm.syncSessionMetaLocked(session)
				}
				delete(sm.sessions, key)
				delete(sm.accessTimes, key)
				evicted++
			}
		}
	}

	// Also clean up orphaned sessionMeta entries for subagents (handled below).
	// With SQLite as the primary backend, session files don't exist on disk as JSON,
	// so we no longer stat the filesystem here.

	if evicted > 0 {
		logger.InfoCF("session", "Idle sessions cleaned up", map[string]interface{}{
			"evicted": evicted,
		})
	}

	return evicted
}

// StartCleanupGoroutine launches a background goroutine that periodically
// calls CleanupIdleSessions. Returns a stop function that terminates the
// goroutine. The interval should be shorter than evictionTTL for timely cleanup.
func (sm *SessionManager) StartCleanupGoroutine(interval time.Duration) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				sm.CleanupIdleSessions()
			}
		}
	}()
	return func() { close(stop) }
}

// SetMaxInMemory sets the maximum number of sessions to keep in memory.
// 0 means unlimited (no LRU eviction).
func (sm *SessionManager) SetMaxInMemory(max int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxInMemory = max
}

// SetEvictionTTL sets how long a session must be idle before it's eligible
// for eviction. 0 means no TTL-based eviction.
func (sm *SessionManager) SetEvictionTTL(ttl time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.evictionTTL = ttl
}

// EvictExcludedMessages removes excluded messages from the in-memory slice
// and records the eviction gap in `firstInMemorySeq`/`evictedTotal`.
// The evicted messages remain persisted in SQLite (excluded = 1) and can be
// reloaded on demand via LoadEvictedMessages.
//
// The eviction region spans from the first message through the last excluded
// message. With the preserved-user-messages feature, non-excluded preserved
// messages (index 0, recent human turns) can sit inside this region as holes.
// These holes are folded into the summary (never silently dropped) and are
// evicted together with the excluded messages, so the in-memory slice stays a
// clean suffix and the invariant `seq = firstInMemorySeq + sliceIndex` holds
// for every kept slot.
//
// Because preserved messages inside the eviction region are also removed from
// memory, their content is folded into the session summary first so no
// information is lost (the summary stays in context and in SQLite metadata).
// With eviction enabled the preserved messages are removed from memory together
// with the excluded region and their content is appended to session.Summary
// verbatim (so it still reaches the model inside the summary block). That fold
// is persisted immediately via UpsertSession for that reason — the folded text
// is about to leave memory entirely with the evicted region, so a crash before
// the next Save would otherwise lose it.
//
// PRECONDITION: the caller must have already persisted the excluded flags
// (Save returned nil). Eviction itself is memory-only and idempotent.
//
// Returns the number of messages evicted.
func (sm *SessionManager) EvictExcludedMessages(key string) int {
	sm.ensureLoaded()

	// Eviction is only safe when messages are persisted in SQLite: eviction
	// is memory-only and relies on the store as the source of truth for
	// lazy-load. Without a store, Save is a no-op and evicting would drop
	// messages permanently.
	if sm.store == nil {
		return 0
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return 0
		}
	}

	// Find the last excluded message. If none → no-op.
	lastExcluded := -1
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if session.Messages[i].ExcludeFromContext {
			lastExcluded = i
			break
		}
	}
	if lastExcluded < 0 {
		// Nothing excluded; no-op.
		return 0
	}
	evictUpTo := lastExcluded + 1

	if session.excludeBoundary > 0 {
		// A compaction recorded the boundary it meant. Rows excluded by OTHER
		// writers (the WebUI approval/rejection message) sit in the kept tail and
		// must stay resident: never evict past the compaction boundary.
		if evictUpTo > session.excludeBoundary {
			evictUpTo = session.excludeBoundary
		}
	} else {
		// No boundary recorded (cold load, direct call): fall back to the
		// contiguous excluded run, exactly like the pre-branch behaviour, so a
		// trailing excluded row cannot drag the whole slice away.
		runStart := 0
		for runStart < len(session.Messages) && !session.Messages[runStart].ExcludeFromContext {
			runStart++
		}
		evictUpTo = runStart
		for evictUpTo < len(session.Messages) && session.Messages[evictUpTo].ExcludeFromContext {
			evictUpTo++
		}
	}

	// Collect non-excluded messages in [0, evictUpTo) — these are preserved
	// holes (index 0, recent human turns) that sit inside the eviction region.
	// Their content is folded into the summary so nothing is lost.
	var kept []providers.Message
	for i := 0; i < evictUpTo; i++ {
		if !session.Messages[i].ExcludeFromContext {
			kept = append(kept, session.Messages[i])
		}
	}
	foldedSummary := false
	if len(kept) > 0 {
		if folded := sm.foldEvictedIntoSummary(session, kept); folded != "" {
			session.Summary = folded
			session.Updated = time.Now()
			foldedSummary = true
		}
	}

	// Rebuild the kept slice: the in-context suffix starting at evictUpTo.
	tail := make([]providers.Message, len(session.Messages)-evictUpTo)
	copy(tail, session.Messages[evictUpTo:])

	session.Messages = tail
	// Absolute seq of the first kept message: the number of rows evicted
	// before it.
	session.firstInMemorySeq += evictUpTo
	session.evictedTotal += evictUpTo
	session.bumpEpoch()
	sm.syncSessionMetaLocked(session)
	// Persist the new eviction boundary to SQLite so a cold restart restores
	// firstInMemorySeq and does not re-inflate evicted rows into RAM. The
	// boundary is stored in session metadata (FirstInMemorySeq); subsequent
	// Save calls also carry it via sessionMetaFromSession. Failure to persist
	// here only affects the durability of the boundary metadata (the in-memory
	// eviction still succeeds), so it is logged, not fatal.
	//
	// When the fold changed the summary, we must persist the FULL metadata
	// (via UpsertSession which carries both Summary and FirstInMemorySeq)
	// instead of the targeted UpdateFirstInMemorySeq. The folded text is
	// about to leave memory entirely with the evicted region, so a crash
	// before the next Save would lose it — it must be durable immediately.
	var metaPersistErr error
	if sm.store != nil {
		if foldedSummary {
			metaPersistErr = sm.store.Sessions().UpsertSession(sessionMetaFromSession(session))
		} else {
			metaPersistErr = sm.store.Sessions().UpdateFirstInMemorySeq(key, session.firstInMemorySeq)
		}
		if metaPersistErr != nil {
			logger.WarnCF("session", "Failed to persist eviction boundary", map[string]interface{}{
				"session_key":     key,
				"first_in_memory": session.firstInMemorySeq,
				"error":           metaPersistErr.Error(),
			})
		}
	}
	// Dirty flags are reset: everything in memory is already persisted; the
	// next Save must be a no-op (NOT a full rewrite).
	session.clearDirtyFlags()
	// If the fold happened but the metadata write failed, mark metaDirty
	// AFTER clearDirtyFlags so the next Save retries via saveMetaOnlyUnlocked.
	if foldedSummary && metaPersistErr != nil {
		session.metaDirty = true
	}
	session.lastPersistedSeq = len(session.Messages) - 1
	sm.touchSession(key)

	logger.InfoCF("session", "Evicted excluded messages from memory", map[string]interface{}{
		"session_key":     key,
		"evicted":         evictUpTo,
		"remaining":       len(tail),
		"evicted_total":   session.evictedTotal,
		"first_in_memory": session.firstInMemorySeq,
	})
	return evictUpTo
}

// foldEvictedIntoSummary prepends the evicted messages' content to the session
// summary (or creates one), preserving the original request text that was
// evicted from memory. Returns the new summary, or "" if nothing was folded.
// The evicted prefix is front-most content, so it is folded before the current
// summary to preserve chronological order. Caller must hold sm.mu.
func (sm *SessionManager) foldEvictedIntoSummary(session *Session, evicted []providers.Message) string {
	parts := make([]string, 0, len(evicted))
	for _, m := range evicted {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		// Skip the previous summary message: its text is already in
		// session.Summary (it was injected by the agent layer). Folding
		// it would duplicate the body and nest the
		// "## Summary of Previous Conversation" header inside the new
		// summary, causing ≈1.9× growth per round.
		if strings.HasPrefix(content, summaryMessageMarker) {
			continue
		}
		parts = append(parts, content)
	}
	if len(parts) == 0 {
		return ""
	}

	folded := strings.Join(parts, "\n")
	if strings.TrimSpace(session.Summary) == "" {
		return folded
	}
	// Prepend only if not already present (avoid duplication across evictions).
	if strings.Contains(session.Summary, folded) {
		return session.Summary
	}
	return folded + "\n\n" + session.Summary
}

// EvictSession removes a session from the in-memory map.
// The session data remains on disk and can be reloaded on demand.
// Returns true if the session was found and evicted.
//
// sessionMeta is deliberately NOT removed — not even for subagent sessions.
// It is the in-memory mirror of the persisted sessions row (which eviction
// keeps, and which nothing ever deletes), and two listing paths depend on
// that mirror: ListSessions reports metadata-only sessions (the WebUI
// session-history / cron-spawn views), and loadSessionFromDisk refuses to
// reload a session without a metadata entry. Dropping it here made finished
// cron-spawn/subagent sessions invisible until the next restart — the exact
// asymmetry "Run now" did not suffer, because the native client tracked the
// key independently. If subagent retention is ever wanted, delete the row in
// SQLite first (store.DeleteSession) so mirror and source stay consistent.
func (sm *SessionManager) EvictSession(key string) bool {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		// Save before evicting to ensure latest state is persisted.
		// saveForEviction re-checks that the session wasn't touched while
		// the save's disk I/O was in flight (the lock is released during it);
		// if it was, we keep the in-memory copy to avoid losing data.
		if sm.saveForEviction(key) {
			sm.syncSessionMetaLocked(session)
			delete(sm.sessions, key)
			delete(sm.accessTimes, key)
			logger.InfoCF("session", "Session evicted from memory", map[string]interface{}{
				"session_key": key,
			})
		} else {
			ok = false
		}
	}

	return ok
}
