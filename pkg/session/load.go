package session

// Session loading: materialising a Session from SQLite.
//
// One-time metadata bootstrap plus the lazy full-session load used when a
// known session is accessed but is not resident in memory.

import (
	"encoding/json"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

// ensureLoaded triggers loadSessionMetadata exactly once, on the first call.
// Must be called BEFORE acquiring sm.mu to avoid deadlock.
func (sm *SessionManager) ensureLoaded() {
	sm.loadOnce.Do(func() {
		if sm.store != nil {
			sm.loadSessionMetadataFromSQLite()
		} else {
			logger.WarnCF("session", "SessionManager has no store — sessions will not persist to disk", nil)
		}
	})
}

// loadSessionFromDisk loads a full session (including messages) from SQLite
// into the sessions map. Called when a session is accessed that exists in
// metadata but not in the in-memory map.
// Caller must hold sm.mu (write lock).
func (sm *SessionManager) loadSessionFromDisk(key string) (*Session, bool) {
	// Check if already loaded
	if s, ok := sm.sessions[key]; ok {
		return s, true
	}

	// Check if we have metadata for this session
	_, ok := sm.sessionMeta[key]
	if !ok {
		return nil, false
	}

	// Load from SQLite if available
	if sm.store != nil {
		return sm.loadFromSQLite(key)
	}

	return nil, false
}

// loadFromSQLite loads a session from the SQLite store.
// Caller must hold sm.mu (write lock).
func (sm *SessionManager) loadFromSQLite(key string) (*Session, bool) {
	repo := sm.store.Sessions()

	// Load metadata
	meta, err := repo.GetSessionMeta(key)
	if err != nil || meta == nil {
		return nil, false
	}

	// Restore the persisted eviction boundary. SQLite seqs are 0-based and
	// contiguous within the non-evicted region, so the number of evicted rows
	// equals firstInMemorySeq. EvictExcludedMessages maintains the invariant
	// evictedTotal == firstInMemorySeq; they can only diverge transiently after
	// a pruned full rewrite, where the pruned rows no longer exist in SQLite
	// anyway, so treating them as equal on cold load is the correct durable
	// boundary.
	boundary := meta.FirstInMemorySeq
	if boundary < 0 {
		boundary = 0
	}

	// Load only non-evicted messages (seq >= boundary) so evicted rows are not
	// inflated into RAM.
	msgJSONs, err := repo.LoadMessagesFromSeq(key, boundary)
	if err != nil {
		return nil, false
	}

	// Validate contiguity of rows AT/ABOVE the boundary using MaxSeq, not
	// MessageCount. MessageCount - boundary wrongly assumes rows are contiguous
	// from seq 0, but saveFullUnlocked calls PruneExcluded for oversized
	// sessions, physically deleting the OLDEST excluded rows and leaving a gap
	// BELOW the boundary. Those gaps are expected and must NOT trigger the
	// fallback. Rows at/above the boundary are valid iff the number of loaded
	// rows equals maxSeq - boundary + 1. If MaxSeq errors, trust the boundary.
	if boundary > 0 {
		if maxSeq, seqErr := repo.MaxSeq(key); seqErr == nil {
			if len(msgJSONs) != maxSeq-boundary+1 {
				logger.WarnCF("session", "Stale eviction boundary detected; recovering from persisted rows", map[string]interface{}{
					"session_key":     key,
					"first_in_memory": boundary,
					"max_seq":         maxSeq,
					"expected_rows":   maxSeq - boundary + 1,
					"loaded_rows":     len(msgJSONs),
				})
				// Genuine corruption above the boundary: rebuild from persisted rows
				// and anchor the boundary to the first non-excluded in-context message.
				fullRows, fErr := repo.LoadMessagesWithSeq(key)
				if fErr != nil {
					return nil, false
				}
				firstInContextIdx := 0
				for firstInContextIdx < len(fullRows) && fullRows[firstInContextIdx].Excluded {
					firstInContextIdx++
				}
				if firstInContextIdx < len(fullRows) {
					boundary = fullRows[firstInContextIdx].Seq
					msgJSONs = make([]string, 0, len(fullRows)-firstInContextIdx)
					for _, fr := range fullRows[firstInContextIdx:] {
						msgJSONs = append(msgJSONs, fr.JSON)
					}
				} else if len(fullRows) > 0 {
					lastIdx := len(fullRows) - 1
					boundary = fullRows[lastIdx].Seq
					msgJSONs = []string{fullRows[lastIdx].JSON}
				} else {
					boundary = 0
					msgJSONs = nil
				}
				_ = repo.UpdateFirstInMemorySeq(key, boundary)
			}
		}
	}

	messages := make([]providers.Message, 0, len(msgJSONs))
	for _, msgJSON := range msgJSONs {
		var msg providers.Message
		if err := json.Unmarshal([]byte(msgJSON), &msg); err != nil {
			continue // skip corrupted messages
		}
		messages = append(messages, msg)
	}

	// If boundary was 0 (e.g. unmigrated session or fallback), check if the
	// loaded messages have an excluded prefix. If so, prune the excluded prefix
	// so only in-context messages are kept resident in memory.
	if boundary == 0 && len(messages) > 0 {
		hasExcluded := false
		for _, m := range messages {
			if m.ExcludeFromContext {
				hasExcluded = true
				break
			}
		}
		if hasExcluded {
			firstInContext := 0
			for firstInContext < len(messages) && messages[firstInContext].ExcludeFromContext {
				firstInContext++
			}
			if firstInContext > 0 && firstInContext < len(messages) {
				boundary = firstInContext
				messages = messages[firstInContext:]
				_ = repo.UpdateFirstInMemorySeq(key, boundary)
			}
		}
	}

	// Count rows still persisted but not resident in memory (evicted +
	// lazy-loadable). Computed from the FINAL msgJSONs (after any fallback
	// above); rows pruned below the boundary are no longer in SQLite and thus
	// not counted as evicted. This matches saveFullUnlocked's post-prune
	// semantics (evictedTotal = len(evictedJSONs) - pruned).
	evictedPersisted := 0
	if total, tErr := repo.MessageCount(key); tErr == nil {
		evictedPersisted = total - len(messages)
		if evictedPersisted < 0 {
			evictedPersisted = 0
		}
	}

	session := &Session{
		Key:              meta.Key,
		Name:             meta.Name,
		Mode:             meta.Mode,
		Summary:          meta.Summary,
		VerboseLevel:     meta.VerboseLevel,
		Model:            meta.Model,
		ThinkingLevel:    meta.ThinkingLevel,
		Folder:           meta.Folder,
		SubagentStatus:   meta.SubagentStatus,
		InputTokens:      meta.InputTokens,
		OutputTokens:     meta.OutputTokens,
		CompactionCount:  meta.CompactionCount,
		Created:          meta.CreatedAt,
		Updated:          meta.UpdatedAt,
		Messages:         messages,
		firstInMemorySeq: boundary,
		evictedTotal:     evictedPersisted,
		lastPersistedSeq: len(messages) - 1, // all messages are persisted
	}

	// Enforce memory limit before adding a new session
	sm.evictIfNeeded()

	sm.sessions[key] = session
	sm.accessTimes[key] = time.Now()
	return session, true
}

// loadSessionMetadataFromSQLite loads session metadata from the SQLite store.
func (sm *SessionManager) loadSessionMetadataFromSQLite() error {
	repo := sm.store.Sessions()
	metas, err := repo.ListSessionMeta()
	if err != nil {
		return err
	}

	sm.sessionMeta = make(map[string]*sessionMetadata, len(metas))
	for _, meta := range metas {
		sm.sessionMeta[meta.Key] = &sessionMetadata{
			Key:            meta.Key,
			Name:           meta.Name,
			Mode:           meta.Mode,
			Folder:         meta.Folder,
			SubagentStatus: meta.SubagentStatus,
			Created:        meta.CreatedAt,
			Updated:        meta.UpdatedAt,
		}
	}
	return nil
}
