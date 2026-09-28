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
	"github.com/xilistudios/lele/pkg/store"
)

// evictionWaterMark is the hysteresis band of the LRU eviction that runs on
// session insertion (evictIfNeeded).
//
// Both places that materialize a session — getOrCreateUnlocked (a new chat or
// subagent) and loadSessionFromDisk (a cold load) — call evictIfNeeded right
// before they assign the session to sm.sessions. With a plain "one in, one out"
// trigger, every single one of those insertions paid a full eviction cascade
// under the write lock: a sort of every accessTimes entry plus saveForEviction
// → saveFullUnlocked → ReplaceMessages. A burst of subagent sessions growing
// the resident set from maxInMemory to 200 fired ~150 cascades, all of them on
// the TUI's insertion path (and therefore blocking frames with sm.mu held for
// the collection phase of each save).
//
// The water-mark turns that into hysteresis: the LRU pass does not start until
// the resident set reaches maxInMemory+evictionWaterMark, and then it evicts the
// whole backlog back down to maxInMemory in ONE batch. Effects:
//
//   - cascades: ~evictionWaterMark× fewer (one per band of insertions instead of
//     one per insertion) — in the burst above, ~9 instead of ~150;
//   - resident peak: bounded by maxInMemory+evictionWaterMark. The extra
//     sessions are LRU-eligible (they would be the next ones evicted anyway), so
//     this is bounded extra memory, never data that is lost;
//   - evictions themselves: unchanged. Every session dropped from memory is still
//     saved exactly once on the way out (that is durability, not waste), still
//     logged one line per session, and still evicted in LRU order.
//
// The band is capped at maxInMemory (see evictIfNeeded): a manager configured for
// a handful of sessions must not overshoot by 16 of them, and maxInMemory=1
// degenerates to the historical one-in-one-out trigger (band 1). The TTL sweep in
// evictIfNeeded is deliberately NOT gated by the water-mark: each idle session is
// evicted at most once, so it cannot repeat and cannot storm.
const evictionWaterMark = 16

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
//
// It runs on the insertion path (every new/cold session), so the common case —
// nothing to do — must stay a couple of comparisons; the LRU pass only starts
// once the resident set has crossed the high water-mark (see
// evictionWaterMark).
func (sm *SessionManager) evictIfNeeded() {
	if sm.maxInMemory <= 0 {
		return
	}

	// First pass: evict sessions that have been idle longer than evictionTTL.
	// NOT behind the water-mark below: an idle session is evicted once and is
	// then gone from accessTimes, so this sweep cannot repeat and cannot storm
	// — the cascade the water-mark amortizes is the LRU one, which ran once per
	// insertion. Leaving the sweep unconditional keeps today's semantics exact:
	// an idle session is dropped by the next insertion, no matter how small the
	// resident set is.
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

	// Second pass (LRU), gated by the hysteresis band. The band is capped at
	// maxInMemory so a small limit keeps a tight bound (peak <= 2*maxInMemory)
	// and maxInMemory=1 reproduces the historical trigger — the behaviour
	// TestSaveAllSkipsEvicted pins.
	band := evictionWaterMark
	if band > sm.maxInMemory {
		band = sm.maxInMemory
	}
	// The caller is about to make exactly one more session resident (both call
	// sites assign sm.sessions[key] right after this call), so the count this
	// call must budget for is len(sm.sessions)+1. Gating on that count keeps the
	// resident peak at exactly maxInMemory+band instead of one above it, and it
	// makes every insertion inside the band an O(1) no-op.
	if len(sm.sessions)+1 <= sm.maxInMemory+band {
		return
	}

	// Over the band: evict the whole backlog in one batch (down to
	// maxInMemory), not one session per insertion.
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
	// Eviction is map-only: it drops the resident pointer and never touches
	// Session.Messages or the Session itself, so the copy-on-write history view
	// (view.go) and its saveEpoch are untouched by batching the evictions —
	// a reader already holding the published messageSnapshot keeps reading the
	// immutable copy, and the next reader for this key takes the cold path.
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
// Lock scope: sm.mu covers ONLY the decision and the in-memory mutation. The
// boundary is persisted by persistEvictionBoundary — a free helper — with the
// lock RELEASED, and the bookkeeping is reconciled after re-acquiring it,
// exactly like saveFullUnlocked (persist.go). This is the path every
// compaction (evict_excluded_from_memory) goes through, and GetHistoryView
// takes the read lock: with the SQLite round trip under the write lock, every
// compaction froze the TUI frames for the whole duration of the write.
//
// Returns the number of messages evicted.
func (sm *SessionManager) EvictExcludedMessages(key string) int {
	sm.ensureLoaded()

	// Eviction is only safe when messages are persisted in SQLite: eviction
	// is memory-only and relies on the store as the source of truth for
	// lazy-load. Without a store, Save is a no-op and evicting would drop
	// messages permanently.
	if sm.sessionRepo == nil {
		return 0
	}

	// ---- Phase 1 (sm.mu held): decide, mutate memory, collect ----
	//
	// No I/O and no JSON work happens here, and every early return below
	// happens BEFORE any mutation, so the historical no-op semantics are
	// preserved: an eviction with nothing to do takes no I/O at all. The
	// deferred Unlock owns the lock taken here for the whole function — the
	// phase-2 release/re-acquire below is temporary and always balanced, so
	// the defer is what leaves the lock in the state the caller expects.
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

	// Structural invariant: never evict the entire slice. excludeBoundary can
	// legitimately be == len(Messages) (the anti-split guard in
	// ExcludeOldMessagesFromContext pushes excludeUpTo to the end when the tail
	// is a tool-result group), and the clamp above is then inert: evicting to
	// len would leave the resident context empty and fold the in-flight turn
	// and the pinned human turns into the lossy summary (the model would get
	// no conversation message at all). Cap at the last in-context message so
	// at least one message survives in RAM when any exists.
	if evictUpTo >= len(session.Messages) {
		for i := len(session.Messages) - 1; i >= 0; i-- {
			if !session.Messages[i].ExcludeFromContext {
				// The block condition pins evictUpTo >= len(Messages), so
				// i < evictUpTo holds by construction today; the min is kept
				// so relaxing that condition later cannot turn this cap into
				// an over-eviction.
				if i < evictUpTo {
					evictUpTo = i
				}
				break
			}
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
	// MUST stay under sm.mu, immediately after the slice swap: the published
	// snapshot is what GetHistoryView reads WITHOUT the lock, and "a snapshot
	// whose epoch equals saveEpoch reflects the current Messages" is the
	// invariant the whole copy-on-write scheme rests on (view.go). Publishing
	// from phase 2 (or after the I/O) would leave the view stale for the
	// duration of the round trip, even though the epoch was already bumped.
	session.publishViewLocked() // the slice was replaced by a shorter tail
	sm.syncSessionMetaLocked(session)

	// Everything phase 2 needs, captured while the lock still guarantees it
	// cannot change:
	//
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
	repo := repoFor(sm)
	epoch := session.saveEpoch
	meta := sessionMetaFromSession(session)
	firstInMemorySeq := session.firstInMemorySeq

	// ---- Phase 2 (lock released): the SQLite round trip ----
	//
	// persistEvictionBoundary is a free function: "no I/O under the lock" is
	// structural, not a convention a later edit can quietly break. unlocked()
	// re-acquires the lock immediately after it (panic included), so no error
	// path can return with the lock held, leak it, or leave it unlocked for
	// this function's deferred Unlock.
	var metaPersistErr error
	sm.unlocked(func() {
		metaPersistErr = persistEvictionBoundary(repo, key, foldedSummary, meta, firstInMemorySeq)
	})

	if metaPersistErr != nil {
		logger.WarnCF("session", "Failed to persist eviction boundary", map[string]interface{}{
			"session_key":     key,
			"first_in_memory": firstInMemorySeq,
			"error":           metaPersistErr.Error(),
		})
	}

	// The eviction itself already happened in phase 1 and is reported here for
	// every post-mutation path below — including the two guard exits, where
	// only the bookkeeping is skipped.
	logger.InfoCF("session", "Evicted excluded messages from memory", map[string]interface{}{
		"session_key":     key,
		"evicted":         evictUpTo,
		"remaining":       len(tail),
		"evicted_total":   session.evictedTotal,
		"first_in_memory": session.firstInMemorySeq,
	})

	// ---- Phase 3 (relock + guards) ----
	//
	// Epoch guard + healing, the same contract as saveFullUnlocked: if the
	// session was mutated while the boundary I/O was in flight, the
	// bookkeeping below belongs to a state that no longer exists — the
	// concurrent mutation is the one that must survive. Its dirty flags are
	// left untouched, and the next Save is forced to rewrite the whole history
	// from the in-memory source of truth (lastPersistedSeq == -1 is what makes
	// saveUnlocked pick saveFullUnlocked), healing any ordering damage between
	// our boundary write and the concurrent save's writes.
	if session.saveEpoch != epoch {
		session.lastPersistedSeq = -1
		return evictUpTo
	}

	// Residency guard: the epoch check covers concurrent mutations, but a
	// concurrent eviction (LRU/TTL) can also have dropped this session from
	// sm.sessions while the I/O was in flight — and the next access may have
	// reloaded a DIFFERENT Session object for the same key, with its own
	// bookkeeping rebuilt from disk. The boundary write itself is durable and
	// stays; only the in-memory bookkeeping is skipped, because it belongs to
	// the object we collected from, not to the resident one (nothing to keep
	// hot either, so touchSession is skipped too).
	if cur, ok := sm.sessions[key]; !ok || cur != session {
		return evictUpTo
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
	return evictUpTo
}

// persistEvictionBoundary makes phase 1's eviction boundary durable in SQLite.
//
// It MUST run with sm.mu released (EvictExcludedMessages owns the
// release/acquire around it): that is the whole point of the T9 split — this
// write used to run with the write lock held, and because GetHistoryView takes
// the read lock, every compaction froze the TUI for the duration of the round
// trip. Being a free function (no access to sm.mu at all) makes the property
// structural: there is no way to reach it while holding the manager lock.
//
// Which statement to use is decided by the caller: UpsertSession rewrites the
// whole metadata row — Summary included — and is required when the fold
// rewrote the summary (its text is about to leave memory, see
// foldEvictedIntoSummary); otherwise the targeted UpdateFirstInMemorySeq
// avoids rewriting metadata that did not change.
func persistEvictionBoundary(repo sessionRepo, key string, foldedSummary bool, meta store.SessionMeta, firstInMemorySeq int) error {
	if repo == nil {
		return nil
	}
	if foldedSummary {
		return repo.UpsertSession(meta)
	}
	return repo.UpdateFirstInMemorySeq(key, firstInMemorySeq)
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
