package session

// Session inventory and read-only queries.
//
// Everything here answers "what sessions exist / how big are they" for the
// WebUI, cron and subagent machinery, without mutating session state.

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
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
	repo := sm.sessionRepo
	sm.mu.RUnlock()

	if repo == nil {
		return 0
	}
	meta, err := repo.GetSessionMeta(key)
	if err != nil || meta == nil || meta.FirstInMemorySeq <= 0 {
		return 0
	}
	n, err := repo.CountMessagesBefore(key, meta.FirstInMemorySeq)
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
	if sm.sessionRepo != nil {
		if n, err := sm.sessionRepo.MessageCount(key); err == nil {
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
	if sm.sessionRepo != nil {
		if n, err := sm.sessionRepo.MessageCount(key); err == nil {
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
	if sm.sessionRepo != nil {
		exists, err := sm.sessionRepo.SessionExists(key)
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
				Folder:  meta.Folder,
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
	if sm.sessionRepo != nil {
		var needFromStore []string
		for key := range sm.sessionMeta {
			if _, ok := sm.sessions[key]; !ok {
				needFromStore = append(needFromStore, key)
			}
		}
		if len(needFromStore) > 0 {
			// Release lock for I/O
			sm.mu.RUnlock()
			storeCounts, err := sm.sessionRepo.AllMessageCounts()
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
	if sm.sessionRepo != nil {
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
			storeCounts, err := sm.sessionRepo.AllMessageCounts()
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
	// Status is the persisted terminal status of the subagent task
	// ("completed", "failed", ...). Empty means unknown (the session predates
	// subagent_status persistence); readers fall back to "completed", the
	// historical hard-coded value.
	Status string
}

// subagentSummary returns the summary a subagent listing reports: the
// session's stored summary when set, otherwise the text of its last assistant
// message, trimmed and truncated to 200 bytes. Kept byte-identical to the
// historical inline fallback so resident, evicted and restarted sessions all
// report the same string.
func subagentSummary(summary, lastAssistantContent string) string {
	if summary != "" {
		return summary
	}
	content := strings.TrimSpace(lastAssistantContent)
	if len(content) > 200 {
		content = content[:200] + "…"
	}
	return content
}

// decodeLastAssistantContent extracts the text of an assistant message from
// its raw SQLite JSON. Rows that did not come back are empty and unreadable
// rows decode to "", matching the "skip corrupted messages" policy of the
// cold-load path (loadFromSQLite). The store-side query already filters
// non-JSON payloads with json_valid (SessionRepo.SessionListingStats), so this
// is the defensive half of that contract: what the loader would drop must not
// become a summary either.
func decodeLastAssistantContent(messageJSON string) string {
	if messageJSON == "" {
		return ""
	}
	var msg providers.Message
	if err := json.Unmarshal([]byte(messageJSON), &msg); err != nil {
		return ""
	}
	return msg.Content
}

// subagentInfoFromSession builds the listing entry for a session that is
// RESIDENT in memory, whose message slice is exactly the in-context suffix
// (seq >= firstInMemorySeq) that the store-side count queries too.
func subagentInfoFromSession(key, parentPrefix string, session *Session) SubagentSessionInfo {
	iterations := 0
	lastAssistantContent := ""
	haveLastAssistant := false
	for i := len(session.Messages) - 1; i >= 0; i-- {
		if session.Messages[i].Role != "assistant" {
			continue
		}
		iterations++
		if !haveLastAssistant {
			lastAssistantContent = session.Messages[i].Content
			haveLastAssistant = true
		}
	}
	return SubagentSessionInfo{
		Key:        key,
		TaskID:     key[len(parentPrefix)+1:], // everything after "{parent}:"
		Created:    session.Created,
		Updated:    session.Updated,
		Iterations: iterations,
		Summary:    subagentSummary(session.Summary, lastAssistantContent),
		Name:       session.Name,
		Status:     session.SubagentStatus,
	}
}

// FindSubagentSessions returns persisted subagent sessions whose keys start
// with the given parent prefix followed by ":subagent-". This allows the API
// to surface past subagents even after a server restart when the in-memory
// SubagentManager no longer tracks them.
//
// Lock discipline: every in-memory read (the key scan, the metadata mirror and
// the resident sessions' message slices) happens under sm.mu.RLock(); the
// exclusive lock is never taken, and the store query runs with no lock held at
// all. This path deliberately does NOT call loadSessionFromDisk: that needs the
// write lock, deserializes whole message histories and inserts the loaded
// sessions into the LRU, evicting live chats on every poll. Measured on this
// repository's store with 94 subagents / 17.3k messages (~24 MB of JSON):
// 654 ms with 51 sessions pulled into the LRU before, 24 ms with no residency
// change after. The two facts that the metadata mirror does not carry — the
// assistant-message count and the summary fallback — come from one batched
// read-only store query.
//
// The LRU (sessions/accessTimes) is left untouched on purpose: a read-only
// listing must neither make a session resident nor change eviction order.
func (sm *SessionManager) FindSubagentSessions(parentPrefix string) []SubagentSessionInfo {
	sm.ensureLoaded()

	prefix := parentPrefix + ":subagent-"

	var (
		results []SubagentSessionInfo
		// Metadata-only entries (parallel to coldKeys): their name, timestamps
		// and status are already known, iterations/summary are not.
		cold     []SubagentSessionInfo
		coldKeys []string
	)

	sm.mu.RLock()
	// Resident sessions: everything is in memory, including the message
	// derived fields.
	for key, session := range sm.sessions {
		if session == nil || !strings.HasPrefix(key, prefix) {
			continue
		}
		results = append(results, subagentInfoFromSession(key, parentPrefix, session))
	}
	// Metadata-only sessions: sessionMeta is the lightweight listing index
	// built precisely to avoid loading sessions. Resident keys are skipped
	// here because they were already reported above.
	for key, meta := range sm.sessionMeta {
		if meta == nil || !strings.HasPrefix(key, prefix) {
			continue
		}
		if _, resident := sm.sessions[key]; resident {
			continue
		}
		cold = append(cold, SubagentSessionInfo{
			Key:     key,
			TaskID:  key[len(parentPrefix)+1:],
			Created: meta.Created,
			Updated: meta.Updated,
			Name:    meta.Name,
			Status:  meta.SubagentStatus,
		})
		coldKeys = append(coldKeys, key)
	}
	repo := sm.sessionRepo
	sm.mu.RUnlock() // store I/O below must not run under the session lock

	if len(coldKeys) == 0 || repo == nil {
		// Without a store these sessions could not have been loaded by the
		// pre-change code either (loadSessionFromDisk returns false), so they
		// are still omitted rather than reported as empty shells.
		return results
	}

	stats, err := repo.SessionListingStats(coldKeys)
	if err != nil {
		// Same policy as the load this replaces: a session whose rows cannot be
		// read is omitted (not reported with empty fields) and the next poll
		// retries.
		logger.WarnCF("session", "Failed to read subagent listing stats; omitting metadata-only subagents", map[string]interface{}{
			"error": err.Error(),
			"count": len(coldKeys),
		})
		return results
	}

	for i, key := range coldKeys {
		stat, ok := stats[key]
		if !ok {
			continue // sessions row is gone (deleted); the old load failed too
		}
		info := cold[i]
		info.Iterations = stat.AssistantCount
		info.Summary = subagentSummary(stat.Summary, decodeLastAssistantContent(stat.LastAssistantJSON))
		results = append(results, info)
	}

	return results
}

// SessionIndexEntry is one row of the session index used by listing endpoints.
// It carries every field the WebUI sidebar needs (name, mode, folder, kind
// inputs, timestamps) plus whether the session has any messages, so a handler
// can build a full response from a single pass over the manager instead of
// calling one getter per session. Each getter previously took sm.mu, ran
// ensureLoaded and walked the agent registry, which made the sidebar endpoint
// O(N_total) per page.
type SessionIndexEntry struct {
	Key      string
	Name     string
	Mode     string
	Folder   string
	Created  time.Time
	Updated  time.Time
	Resident bool
	// HasMessages mirrors SessionManager.HasMessages: user/assistant messages
	// in memory, or evicted/persisted ones. For non-resident sessions it is
	// only known once the caller consults the store, so it is left false here
	// and Resident is set to false — callers resolve the cold ones with one
	// batched query (see SessionKeysWithMessages).
	HasMessages bool
}

// ListSessionIndex returns one lightweight entry per known session (resident
// and metadata-only) in a single pass under one lock. It never loads message
// bodies: resident sessions are counted from the in-memory slice, and
// non-resident sessions come back with Resident=false so the caller can
// resolve emptiness with one batched store query instead of N.
func (sm *SessionManager) ListSessionIndex() []SessionIndexEntry {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	res := make([]SessionIndexEntry, 0, len(sm.sessions)+len(sm.sessionMeta))
	seen := make(map[string]bool, len(sm.sessions)+len(sm.sessionMeta))

	for key, session := range sm.sessions {
		if session == nil {
			continue
		}
		seen[key] = true
		res = append(res, SessionIndexEntry{
			Key:      key,
			Name:     session.Name,
			Mode:     session.Mode,
			Folder:   session.Folder,
			Created:  session.Created,
			Updated:  session.Updated,
			Resident: true,
			// Same expression as HasMessages for resident sessions.
			HasMessages: len(session.Messages) > 0 || session.evictedTotal > 0,
		})
	}

	for key, meta := range sm.sessionMeta {
		if seen[key] || meta == nil {
			continue
		}
		res = append(res, SessionIndexEntry{
			Key:      key,
			Name:     meta.Name,
			Mode:     meta.Mode,
			Folder:   meta.Folder,
			Created:  meta.Created,
			Updated:  meta.Updated,
			Resident: false,
			// Unknown without the store; caller resolves it.
		})
	}

	return res
}

// SessionKeysWithMessages returns the set of session keys that have at least
// one persisted message row, in a single store query. It is the batched
// alternative to calling HasMessages per non-resident session. Returns nil when
// no store is configured (callers should then treat unknown as empty, matching
// HasMessages).
func (sm *SessionManager) SessionKeysWithMessages() map[string]bool {
	sm.ensureLoaded()
	sm.mu.RLock()
	repo := sm.sessionRepo
	sm.mu.RUnlock()

	if repo == nil {
		return nil
	}
	keys, err := repo.SessionKeysWithMessages()
	if err != nil {
		return nil
	}
	return keys
}
