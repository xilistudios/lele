package session

// Per-session token accounting.
//
// Tracks input/output token totals and the compaction counter used to bound
// repeated compaction attempts.

import "time"

// GetTokenCounts returns the input and output token counts for a session.
func (sm *SessionManager) GetTokenCounts(key string) (inputTokens, outputTokens int) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return 0, 0
		}
		sm.touchSession(key)
	}
	return session.InputTokens, session.OutputTokens
}

// AddTokenCounts adds token counts to a session.
func (sm *SessionManager) AddTokenCounts(key string, inputTokens, outputTokens int) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.InputTokens += inputTokens
	session.OutputTokens += outputTokens
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)
}

// ResetTokenCounts resets the input and output token counts for a session to zero.
func (sm *SessionManager) ResetTokenCounts(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return
		}
	}

	session.InputTokens = 0
	session.OutputTokens = 0
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)
}

// IncrementCompactionCount atomically increments the compaction counter for a session.
func (sm *SessionManager) IncrementCompactionCount(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.CompactionCount++
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)
}
