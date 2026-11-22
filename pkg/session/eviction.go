package session

import (
	"sort"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

const evictionWaterMark = 16

func (sm *SessionManager) touchSession(key string) {
	sm.accessTimes[key] = time.Now()
}

func (sm *SessionManager) saveForEviction(key string) bool {
	prevAccess, hadAccess := sm.accessTimes[key]
	_ = sm.saveUnlocked(key)
	curAccess, hasAccess := sm.accessTimes[key]
	if hadAccess != hasAccess {
		return false
	}
	if hasAccess && !curAccess.Equal(prevAccess) {
		return false
	}
	return true
}

func (sm *SessionManager) evictIfNeeded() {
	if sm.maxInMemory <= 0 {
		return
	}

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
			}
		}
	}

	band := evictionWaterMark
	if band > sm.maxInMemory {
		band = sm.maxInMemory
	}
	if len(sm.sessions)+1 <= sm.maxInMemory+band {
		return
	}

	if len(sm.sessions) <= sm.maxInMemory {
		return
	}

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
					sm.syncSessionMetaLocked(session)
				}
				delete(sm.sessions, key)
				delete(sm.accessTimes, key)
				evicted++
			}
		}
	}

	if evicted > 0 {
		logger.InfoCF("session", "Idle sessions cleaned up", map[string]interface{}{
			"evicted": evicted,
		})
	}

	return evicted
}

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

func (sm *SessionManager) SetMaxInMemory(max int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxInMemory = max
}

func (sm *SessionManager) SetEvictionTTL(ttl time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.evictionTTL = ttl
}

func (sm *SessionManager) EvictExcludedMessages(key string) int {
	sm.ensureLoaded()

	if sm.sessionRepo == nil {
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

	lastExcluded := -1
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if session.Messages[i].ExcludeFromContext {
			lastExcluded = i
			break
		}
	}
	if lastExcluded < 0 {
		return 0
	}
	evictUpTo := lastExcluded + 1

	if session.excludeBoundary > 0 {
		if evictUpTo > session.excludeBoundary {
			evictUpTo = session.excludeBoundary
		}
	} else {
		runStart := 0
		for runStart < len(session.Messages) && !session.Messages[runStart].ExcludeFromContext {
			runStart++
		}
		evictUpTo = runStart
		for evictUpTo < len(session.Messages) && session.Messages[evictUpTo].ExcludeFromContext {
			evictUpTo++
		}
	}

	if evictUpTo >= len(session.Messages) {
		for i := len(session.Messages) - 1; i >= 0; i-- {
			if !session.Messages[i].ExcludeFromContext {
				if i < evictUpTo {
					evictUpTo = i
				}
				break
			}
		}
	}

	if evictUpTo <= 0 {
		session.clearDirtyFlags()
		session.lastPersistedSeq = len(session.Messages) - 1
		sm.touchSession(key)
		return 0
	}

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

	tail := make([]providers.Message, len(session.Messages)-evictUpTo)
	copy(tail, session.Messages[evictUpTo:])

	session.Messages = tail
	session.firstInMemorySeq += evictUpTo
	session.evictedTotal += evictUpTo
	session.bumpEpoch()
	session.publishViewLocked()
	sm.syncSessionMetaLocked(session)

	repo := repoFor(sm)
	epoch := session.saveEpoch
	meta := sessionMetaFromSession(session)
	firstInMemorySeq := session.firstInMemorySeq

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

	logger.InfoCF("session", "Evicted excluded messages from memory", map[string]interface{}{
		"session_key":     key,
		"evicted":         evictUpTo,
		"remaining":       len(tail),
		"evicted_total":   session.evictedTotal,
		"first_in_memory": session.firstInMemorySeq,
	})

	if session.saveEpoch != epoch {
		session.lastPersistedSeq = -1
		return evictUpTo
	}

	if cur, ok := sm.sessions[key]; !ok || cur != session {
		return evictUpTo
	}

	session.clearDirtyFlags()
	if foldedSummary && metaPersistErr != nil {
		session.metaDirty = true
	}
	session.lastPersistedSeq = len(session.Messages) - 1
	sm.touchSession(key)
	return evictUpTo
}

func persistEvictionBoundary(repo sessionRepo, key string, foldedSummary bool, meta store.SessionMeta, firstInMemorySeq int) error {
	if repo == nil {
		return nil
	}
	if foldedSummary {
		return repo.UpsertSession(meta)
	}
	return repo.UpdateFirstInMemorySeq(key, firstInMemorySeq)
}

func (sm *SessionManager) foldEvictedIntoSummary(session *Session, evicted []providers.Message) string {
	parts := make([]string, 0, len(evicted))
	for _, m := range evicted {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
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
	if strings.Contains(session.Summary, folded) {
		return session.Summary
	}
	return folded + "\n\n" + session.Summary
}

func (sm *SessionManager) EvictSession(key string) bool {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
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
