package session

// Session inventory and read-only queries.
//
// Everything here answers "what sessions exist / how big are they" for the
// WebUI, cron and subagent machinery, without mutating session state.

import (
	"sort"
	"strings"
	"time"
)

// GetEvictedMessageCount returns the number of messages that have been evicted
// from memory (excluded + persisted in SQLite but not in the in-memory slice).
// Consumers use this to decide when to lazy-load evicted history.
//
// For a non-resident session (LRU/TTL-evicted, metadata only), the eviction
// boundary persisted in the sessions table defines the prefix: rows with
// seq < first_in_memory_seq exist in SQLite but not in memory. Without this
// fallback a fully evicted session would report 0 and frontends would never
// page into its history.
func (sm *SessionManager) GetEvictedMessageCount(key string) int {
	sm.ensureLoaded()
	sm.mu.RLock()
	if session, ok := sm.sessions[key]; ok {
		n := session.evictedTotal
		sm.mu.RUnlock()
		return n
	}
	store := sm.store
	sm.mu.RUnlock()

	if store == nil {
		return 0
	}
	meta, err := store.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil || meta.FirstInMemorySeq <= 0 {
		return 0
	}
	n, err := store.Sessions().CountMessagesBefore(key, meta.FirstInMemorySeq)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// HasMessages returns true if the session has any persisted messages (in-memory
// slice or evicted), WITHOUT loading/deserializing the full history. Lightweight
// check used by the session-listing hot path (WebUI sidebar) to avoid the N+1
// full history load that GetHistory/GetHistoryView would trigger.
func (sm *SessionManager) HasMessages(key string) bool {
	if key == "" {
		return false
	}
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if session, ok := sm.sessions[key]; ok {
		return len(session.Messages) > 0 || session.evictedTotal > 0
	}
	// Cold session (metadata only): query the store count without materializing.
	// Safe under RLock: SessionRepo.MessageCount only touches the SQLite
	// connection (database/sql is goroutine-safe) and never acquires sm.mu.
	if sm.store != nil {
		if n, err := sm.store.Sessions().MessageCount(key); err == nil {
			return n > 0
		}
	}
	return false
}

// GetTotalMessageCount returns the total number of messages for a session:
// the in-memory slice length plus any evicted (excluded) messages still
// persisted in SQLite. Used for compaction threshold guards and session
// counters so eviction doesn't make a session look smaller than it is.
func (sm *SessionManager) GetTotalMessageCount(key string) int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return len(session.Messages) + session.evictedTotal
	}
	// Not in memory: fall back to the store count if available.
	if sm.store != nil {
		if n, err := sm.store.Sessions().MessageCount(key); err == nil {
			return n
		}
	}
	return 0
}

func (sm *SessionManager) GetUpdated(key string) time.Time {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return session.Updated
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Updated
	}
	return time.Time{}
}

func (sm *SessionManager) GetCreated(key string) time.Time {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return session.Created
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Created
	}
	return time.Time{}
}

// ListSessionsByMode returns sessions whose effective mode matches the given mode.
// The parameter mode is normalized: "" is treated as "agent".
// For each session, its effective mode is session.Mode; if "" it is treated as "agent".
func (sm *SessionManager) ListSessionsByMode(mode string) []*Session {
	// Normalize requested mode: "" -> "agent"
	normalizedMode := mode
	if normalizedMode == "" {
		normalizedMode = "agent"
	}

	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	res := make([]*Session, 0)

	seen := make(map[string]bool)
	for key := range sm.sessions {
		seen[key] = true
		session := sm.sessions[key]
		effectiveMode := session.Mode
		if effectiveMode == "" {
			effectiveMode = "agent"
		}
		if effectiveMode == normalizedMode {
			res = append(res, session)
		}
	}
	for key, meta := range sm.sessionMeta {
		if !seen[key] {
			effectiveMode := meta.Mode
			if effectiveMode == "" {
				effectiveMode = "agent"
			}
			if effectiveMode == normalizedMode {
				res = append(res, &Session{
					Key:     meta.Key,
					Name:    meta.Name,
					Mode:    meta.Mode,
					Created: meta.Created,
					Updated: meta.Updated,
				})
			}
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Updated.After(res[j].Updated)
	})
	return res
}

// SessionExists reports whether a session exists for the given key in any
// layer: in-memory, metadata index, or on disk. The disk check matters
// because a session created before the metadata mirror was fixed (or by a
// concurrent process) can exist in SQLite without an in-memory entry.
//
// This is used to detect subagent session-key collisions (e.g. after a
// restart, when in-memory ID counters reset). It only performs a cheap
// query — it never loads the session.
func (sm *SessionManager) SessionExists(key string) bool {
	if key == "" {
		return false
	}
	sm.ensureLoaded()
	sm.mu.RLock()
	_, inMemory := sm.sessions[key]
	_, inMeta := sm.sessionMeta[key]
	sm.mu.RUnlock()
	if inMemory || inMeta {
		return true
	}

	// Check SQLite if available
	if sm.store != nil {
		exists, err := sm.store.Sessions().SessionExists(key)
		if err == nil && exists {
			return true
		}
		return false
	}

	return false
}

// ActiveCount returns the number of sessions that exist (have metadata on disk).
// This is useful for detecting agents with active conversations.
func (sm *SessionManager) ActiveCount() int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	// Count sessions that have metadata (exist on disk)
	return len(sm.sessionMeta)
}

// ListSessions returns a slice of all sessions (including metadata-only sessions
// not yet fully loaded into memory), sorted by updated time descending.
// Sessions only in metadata have nil Messages — they are loaded on-demand
// when accessed via GetOrCreate or GetHistory.
func (sm *SessionManager) ListSessions() []*Session {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// For sessions in memory, return the full session
	// For sessions only in metadata, create a lightweight Session with no messages
	res := make([]*Session, 0, len(sm.sessionMeta))

	// Collect all keys from both maps
	seen := make(map[string]bool)
	for key := range sm.sessions {
		seen[key] = true
		res = append(res, sm.sessions[key])
	}
	for key, meta := range sm.sessionMeta {
		if !seen[key] {
			res = append(res, &Session{
				Key:     meta.Key,
				Name:    meta.Name,
				Mode:    meta.Mode,
				Created: meta.Created,
				Updated: meta.Updated,
				// Messages is nil — not loaded
			})
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Updated.After(res[j].Updated)
	})
	return res
}

// AllMessageCounts returns a map of session_key → message count for every
// persisted session. For sessions in memory, it counts user+assistant messages
// directly. For sessions only in metadata (evicted or not yet loaded), it
// queries SQLite in a single batch query. This avoids loading full session
// history just to count messages, which is critical for the WebUI sidebar
// that lists all sessions.
func (sm *SessionManager) AllMessageCounts() map[string]int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	counts := make(map[string]int, len(sm.sessionMeta))

	// Count messages for in-memory sessions directly (accurate, no I/O)
	for key, session := range sm.sessions {
		count := 0
		for _, msg := range session.Messages {
			if msg.Role == "user" || msg.Role == "assistant" {
				// Skip injected context messages (e.g. from read_image tool)
				if msg.Role == "user" && msg.Content == "" && len(msg.ContentParts) > 0 {
					continue
				}
				count++
			}
		}
		counts[key] = count
	}

	// For sessions only in metadata (not in memory), query SQLite in batch
	if sm.store != nil {
		var needFromStore []string
		for key := range sm.sessionMeta {
			if _, ok := sm.sessions[key]; !ok {
				needFromStore = append(needFromStore, key)
			}
		}
		if len(needFromStore) > 0 {
			// Release lock for I/O
			sm.mu.RUnlock()
			storeCounts, err := sm.store.Sessions().AllMessageCounts()
			sm.mu.RLock()
			if err == nil {
				for _, key := range needFromStore {
					if c, ok := storeCounts[key]; ok {
						counts[key] = c
					}
				}
			}
		}
	}

	return counts
}

// AllTotalMessageCounts returns a map of session_key → total message count
// for every known session, using the same semantics as GetTotalMessageCount:
// in-memory sessions report len(Messages) + evictedTotal (no I/O); sessions
// only present in metadata are counted via a single batched SQLite query
// (all rows, including evicted ones). This is the batch alternative to
// calling GetTotalMessageCount in a loop, avoiding N+1 queries when UIs
// list all sessions.
func (sm *SessionManager) AllTotalMessageCounts() map[string]int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	counts := make(map[string]int, len(sm.sessionMeta))

	// In-memory sessions: accurate count without I/O.
	for key, session := range sm.sessions {
		counts[key] = len(session.Messages) + session.evictedTotal
	}

	// Cold sessions (metadata only): one batched store query.
	if sm.store != nil {
		needFromStore := false
		for key := range sm.sessionMeta {
			if _, ok := sm.sessions[key]; !ok {
				needFromStore = true
				break
			}
		}
		if needFromStore {
			// Release lock for I/O (same pattern as AllMessageCounts).
			sm.mu.RUnlock()
			storeCounts, err := sm.store.Sessions().AllMessageCounts()
			sm.mu.RLock()
			if err == nil {
				for key := range sm.sessionMeta {
					if _, ok := sm.sessions[key]; ok {
						continue
					}
					if c, ok := storeCounts[key]; ok {
						counts[key] = c
					}
				}
			}
		}
	}

	return counts
}

// SubagentSessionInfo contains metadata about a persisted subagent session.
type SubagentSessionInfo struct {
	Key        string
	TaskID     string
	Created    time.Time
	Updated    time.Time
	Iterations int    // number of assistant messages
	Summary    string // session summary if available
	Name       string // session name if available
}

// FindSubagentSessions returns persisted subagent sessions whose keys start
// with the given parent prefix followed by ":subagent-". This allows the API
// to surface past subagents even after a server restart when the in-memory
// SubagentManager no longer tracks them.
func (sm *SessionManager) FindSubagentSessions(parentPrefix string) []SubagentSessionInfo {
	sm.ensureLoaded()
	sm.mu.Lock() // need write lock for potential loadSessionFromDisk
	defer sm.mu.Unlock()

	prefix := parentPrefix + ":subagent-"
	var results []SubagentSessionInfo

	// Collect subagent keys from both metadata and in-memory sessions
	subagentKeys := make(map[string]bool)
	for key := range sm.sessionMeta {
		if strings.HasPrefix(key, prefix) {
			subagentKeys[key] = true
		}
	}
	for key := range sm.sessions {
		if strings.HasPrefix(key, prefix) {
			subagentKeys[key] = true
		}
	}

	// Load each matching session from disk on-demand (if not already in memory)
	for key := range subagentKeys {
		session, ok := sm.sessions[key]
		if !ok {
			session, ok = sm.loadSessionFromDisk(key)
			if !ok {
				continue
			}
		}

		taskID := key[len(parentPrefix)+1:] // everything after "{parent}:"

		// Count assistant messages as iteration proxy
		iterations := 0
		for _, msg := range session.Messages {
			if msg.Role == "assistant" {
				iterations++
			}
		}

		summary := session.Summary
		if summary == "" && len(session.Messages) > 0 {
			// Use the last assistant message content as a summary fallback
			for i := len(session.Messages) - 1; i >= 0; i-- {
				if session.Messages[i].Role == "assistant" {
					content := strings.TrimSpace(session.Messages[i].Content)
					if len(content) > 200 {
						content = content[:200] + "…"
					}
					summary = content
					break
				}
			}
		}

		results = append(results, SubagentSessionInfo{
			Key:        key,
			TaskID:     taskID,
			Created:    session.Created,
			Updated:    session.Updated,
			Iterations: iterations,
			Summary:    summary,
			Name:       session.Name,
		})
	}

	return results
}
