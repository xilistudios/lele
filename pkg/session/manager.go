package session

import (
	"encoding/json"
	"fmt"

	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// maxStoredMessages is the maximum number of messages kept in a session file.
// When exceeded, the oldest excluded messages are pruned on save.
const maxStoredMessages = 10000

type Session struct {
	Key                string              `json:"key"`
	Name               string              `json:"name,omitempty"`
	Mode               string              `json:"mode,omitempty"` // "chat", "agent", "group" (default empty = "agent")
	Messages           []providers.Message `json:"messages"`
	Summary            string              `json:"summary,omitempty"`
	VerboseMode        bool                `json:"verbose_mode,omitempty"`   // Deprecated: use VerboseLevel
	VerboseLevel       string              `json:"verbose_level,omitempty"`  // "off", "basic", or "full"
	Model              string              `json:"model,omitempty"`          // Session-specific model override
	ThinkingLevel      string              `json:"thinking_level,omitempty"` // "off", "low", "medium", "high"
	Created            time.Time           `json:"created"`
	Updated            time.Time           `json:"updated"`
	lastStreamFlush    time.Time           // throttle for stream persistence (not persisted)
	hadStreamedContent bool                // tracks if content was delivered via streaming this turn (not persisted)
	lastPersistedSeq   int                 // last message seq persisted to SQLite (-1 = none)
	metaDirty          bool                // metadata changed since last save (needs UpsertSession)
	msgsAppended       int                 // messages appended since lastPersistedSeq (needs InsertMessage)
	modifiedFrom       int                 // 1 + lowest in-memory index modified in-place since last save (0 = none; needs UpdateMessage)
	excludedRange      [2]int              // [start, end) range of messages whose excluded flag changed (needs UpdateMessagesExcluded)
	lastMsgDeleted     bool                // last message was removed (needs DeleteLastMessage)
	// deleteFromSeq is the absolute SQLite seq of the message removed by
	// RemoveLastMessage, captured at deletion time. saveDeleteLastUnlocked
	// uses it as a watermark (DELETE WHERE seq >= deleteFromSeq) instead of a
	// position-based "delete max seq", which would race with concurrent
	// appends that reuse the same seq slot. 0 = no pending delete.
	deleteFromSeq int
	// firstInMemorySeq is the SQLite seq of in-memory slice element 0.
	// 0 = no eviction gap (slice index == seq, legacy behavior).
	// > 0 = messages with seq < firstInMemorySeq were evicted from memory
	//       (they remain in SQLite with excluded = 1).
	firstInMemorySeq int
	// evictedTotal is the number of messages currently persisted in SQLite but
	// not present in the in-memory slice (evicted after compaction).
	evictedTotal int
	// saveEpoch is bumped on every logical mutation (content or metadata
	// change). Save paths capture it before releasing the lock for disk I/O
	// and compare after re-acquiring it; a mismatch means the session was
	// mutated while the I/O was in flight, so the save is stale and its
	// post-I/O bookkeeping must be discarded (dirty flags left set) to avoid
	// losing the concurrent mutation. Bookkeeping-only changes (clearing dirty
	// flags, advancing lastPersistedSeq) do NOT bump it.
	saveEpoch uint64
	// Token tracking
	InputTokens     int `json:"input_tokens,omitempty"`
	OutputTokens    int `json:"output_tokens,omitempty"`
	CompactionCount int `json:"compaction_count,omitempty"`
}

// sessionMetadata holds lightweight session info for sessions not yet
// fully loaded into memory. This allows listing sessions without
// deserializing their entire message history.
type sessionMetadata struct {
	Key     string    `json:"key"`
	Name    string    `json:"name"`
	Mode    string    `json:"mode,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	store    *store.Store // SQLite store
	loadOnce sync.Once    // ensures loadSessions runs exactly once, on first access

	// Lazy loading: lightweight metadata for sessions not yet loaded into memory.
	// Populated by loadSessionMetadata() instead of loading full message history.
	sessionMeta map[string]*sessionMetadata // keyed by session key

	// LRU eviction
	maxInMemory int                  // max sessions to keep in memory (0 = unlimited). Default: 50.
	evictionTTL time.Duration        // idle time before a session is eligible for eviction. Default: 30m.
	accessTimes map[string]time.Time // last access time per session key (for LRU)
}

func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions:    make(map[string]*Session),
		sessionMeta: make(map[string]*sessionMetadata),
		maxInMemory: 50,
		evictionTTL: 30 * time.Minute,
		accessTimes: make(map[string]time.Time),
	}

	return sm
}

// SetStore sets the SQLite store for persistence. When set, the manager
// will use SQLite instead of JSON files for session storage.
func (sm *SessionManager) SetStore(s *store.Store) {
	sm.store = s
}

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
				if _, ok := sm.sessions[key]; ok {
					if !sm.saveForEviction(key) {
						continue
					}
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
		if !sm.saveForEviction(key) {
			continue
		}
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
				if _, ok := sm.sessions[key]; ok {
					if !sm.saveForEviction(key) {
						continue
					}
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

func (sm *SessionManager) GetOrCreate(key string) *Session {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Try in-memory first
	if session, ok := sm.sessions[key]; ok {
		sm.touchSession(key)
		return session
	}

	// Try loading from disk
	if session, ok := sm.loadSessionFromDisk(key); ok {
		sm.touchSession(key)
		return session
	}

	// Create new session
	session := &Session{
		Key:              key,
		Messages:         []providers.Message{},
		Created:          time.Now(),
		Updated:          time.Now(),
		lastPersistedSeq: -1,
	}
	sm.evictIfNeeded()
	sm.sessions[key] = session
	sm.accessTimes[key] = time.Now()
	// Register in metadata
	sm.sessionMeta[key] = &sessionMetadata{
		Key:     key,
		Mode:    session.Mode,
		Created: session.Created,
		Updated: session.Updated,
	}
	return session
}

func generateSessionName(content string) string {
	maxLen := 50
	content = strings.TrimSpace(content)
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")
	content = strings.ReplaceAll(content, "\t", " ")

	for _, r := range []string{".", ",", "!", "?", ";", ":", "'", "\"", "`"} {
		content = strings.ReplaceAll(content, r, "")
	}

	words := strings.Fields(content)
	if len(words) == 0 {
		return "New Chat"
	}

	result := strings.Join(words, " ")
	if len(result) <= maxLen {
		return result
	}

	result = result[:maxLen]
	lastSpace := strings.LastIndex(result, " ")
	if lastSpace > 0 && lastSpace > maxLen-20 {
		result = result[:lastSpace]
	}

	return strings.TrimSpace(result)
}

func (sm *SessionManager) AddMessage(sessionKey, role, content string) {
	sm.AddFullMessage(sessionKey, providers.Message{
		Role:    role,
		Content: content,
	})
}

// AddFullMessage adds a complete message with tool calls and tool call ID to the session.
// This is used to save the full conversation flow including tool calls and tool results.
// If the last message is a streaming assistant message (added by AppendAssistantChunk),
// it updates that message in-place instead of appending a duplicate.
func (sm *SessionManager) AddFullMessage(sessionKey string, msg providers.Message) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionKey]
	if !ok {
		session, ok = sm.loadSessionFromDisk(sessionKey)
		if !ok {
			session = &Session{
				Key:              sessionKey,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[sessionKey] = session
		}
	}
	sm.touchSession(sessionKey)

	if msg.Role == "user" && len(session.Messages) == 0 && session.Name == "" {
		session.Name = generateSessionName(msg.Content)
		session.bumpEpoch()
	}

	// New user message starts a new turn — clear the streamed content flag
	// so the deduplication logic is fresh for the next assistant response.
	if msg.Role == "user" {
		session.hadStreamedContent = false
	}

	// If the last message is a streaming assistant and this is an assistant
	// message, update it in-place to avoid duplicates.
	if msg.Role == "assistant" && len(session.Messages) > 0 {
		lastMsg := &session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming {
			// Replace the streaming message with the final version.
			// Keep hadStreamedContent=true so HasStreamedContent still returns
			// true until the next user message arrives.
			msg.Streaming = false
			*lastMsg = msg
			session.Updated = time.Now()
			session.markModified(len(session.Messages) - 1) // in-place update, not a new append
			return
		}
	}

	session.Messages = append(session.Messages, msg)
	session.Updated = time.Now()
	session.msgsAppended++
	session.bumpEpoch()
}

func (sm *SessionManager) GetHistory(key string) []providers.Message {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Try in-memory first
	session, ok := sm.sessions[key]
	if !ok {
		// Try loading from disk (needs write lock, which we already hold)
		session, ok = sm.loadSessionFromDisk(key)
	}
	if !ok {
		logger.DebugCF("session", "GetHistory: session not found", map[string]interface{}{
			"session_key": key,
		})
		return []providers.Message{}
	}

	sm.touchSession(key)
	history := make([]providers.Message, len(session.Messages))
	copy(history, session.Messages)
	logger.DebugCF("session", "GetHistory: returning history", map[string]interface{}{
		"session_key":    key,
		"messages_count": len(history),
	})
	return history
}

// GetHistoryView returns a defensive copy of the session's message slice.
// The returned slice is safe to read without holding the session lock and
// will not be affected by concurrent AppendAssistantChunk/AddFullMessage
// calls. The caller MUST NOT modify the returned slice or any messages in it.
// For external use where the caller may modify, use GetHistory instead.
func (sm *SessionManager) GetHistoryView(key string) []providers.Message {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Try in-memory first
	if session, ok := sm.sessions[key]; ok {
		sm.touchSession(key)
		view := make([]providers.Message, len(session.Messages))
		copy(view, session.Messages)
		return view
	}
	// Try loading from disk
	if session, ok := sm.loadSessionFromDisk(key); ok {
		sm.touchSession(key)
		view := make([]providers.Message, len(session.Messages))
		copy(view, session.Messages)
		return view
	}
	return []providers.Message{}
}

func (sm *SessionManager) GetSummary(key string) string {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return ""
		}
	}
	return session.Summary
}

// GetEvictedMessageCount returns the number of messages that have been evicted
// from memory (excluded + persisted in SQLite but not in the in-memory slice).
// Consumers use this to decide when to lazy-load evicted history.
func (sm *SessionManager) GetEvictedMessageCount(key string) int {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if session, ok := sm.sessions[key]; ok {
		return session.evictedTotal
	}
	return 0
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

// LoadEvictedMessages re-inserts evicted (excluded) messages from SQLite back
// into the in-memory slice, before the current first in-memory message,
// restoring full display history. Idempotent: no-op when evictedTotal == 0.
// Returns the number of messages loaded.
func (sm *SessionManager) LoadEvictedMessages(key string) int {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		// Nothing in memory to prepend to; loading here would inflate a cold
		// session. Callers invoke this only for sessions known to be active.
		return 0
	}
	if session.evictedTotal == 0 || session.firstInMemorySeq <= 0 {
		return 0
	}

	evictedJSONs, err := sm.store.Sessions().LoadMessagesBeforeSeq(key, session.firstInMemorySeq)
	if err != nil {
		logger.WarnCF("session", "LoadEvictedMessages store read failed", map[string]interface{}{
			"session_key": key,
			"error":       err.Error(),
		})
		return 0
	}

	loaded := make([]providers.Message, 0, len(evictedJSONs)+len(session.Messages))
	for _, msgJSON := range evictedJSONs {
		var msg providers.Message
		if uerr := json.Unmarshal([]byte(msgJSON), &msg); uerr != nil {
			continue // skip corrupted rows
		}
		loaded = append(loaded, msg)
	}
	loaded = append(loaded, session.Messages...)

	session.Messages = loaded
	session.firstInMemorySeq = 0
	session.evictedTotal = 0
	session.lastPersistedSeq = len(loaded) - 1
	sm.touchSession(key)

	// CRITICAL: restore the invariant `seq = firstInMemorySeq + sliceIndex`.
	// The evicted rows loaded back above may have gaps (PruneExcluded
	// physically deletes the oldest excluded rows once a session exceeds
	// maxStoredMessages), so slice index i no longer maps to seq i. Without a
	// rebase, every subsequent save (saveExcludedRangeUnlocked, streaming
	// updates, incremental appends) computes seqs via seqForIndex and writes
	// content onto the WRONG rows — shifting JSON blobs relative to the role
	// column and corrupting the persisted history. A full rewrite re-numbers
	// all rows contiguously from 0, healing any gaps. saveFullUnlocked sees
	// firstInMemorySeq == 0 and rewrites purely from the (now complete)
	// in-memory slice.
	if err := sm.saveFullUnlocked(key); err != nil {
		logger.WarnCF("session", "LoadEvictedMessages rebase save failed; seqs may be misaligned until next full rewrite", map[string]interface{}{
			"session_key": key,
			"error":       err.Error(),
		})
	}

	logger.InfoCF("session", "Loaded evicted messages back into memory", map[string]interface{}{
		"session_key": key,
		"loaded":      len(evictedJSONs),
		"total":       len(loaded),
	})
	return len(evictedJSONs)
}

func (sm *SessionManager) GetName(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Try in-memory first
	if session, ok := sm.sessions[key]; ok {
		return session.Name
	}
	// Try metadata
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Name
	}
	return ""
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

func (sm *SessionManager) SetSummary(key string, summary string) {
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

	session.Summary = summary
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)
}

func (sm *SessionManager) SetName(key string, name string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.Name = strings.TrimSpace(name)
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	return sm.saveMetaOnlyUnlocked(key)
}

func (sm *SessionManager) TruncateHistory(key string, keepLast int) {
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

	if keepLast <= 0 {
		session.Messages = []providers.Message{}
		session.Updated = time.Now()
		session.lastPersistedSeq = -1 // full rewrite: all messages removed
		session.bumpEpoch()
		sm.touchSession(key)
		return
	}

	if len(session.Messages) <= keepLast {
		return
	}

	session.Messages = session.Messages[len(session.Messages)-keepLast:]
	session.Updated = time.Now()
	session.lastPersistedSeq = -1 // full rewrite: kept messages re-indexed
	session.bumpEpoch()
	sm.touchSession(key)
}

// isToolResultMessage returns true if the message is a tool result
// (role "tool" with a non-empty ToolCallID, or role "user" with ToolCallID).
func isToolResultMessage(msg providers.Message) bool {
	return (msg.Role == "tool" || msg.Role == "user") && msg.ToolCallID != ""
}

// ExcludeOldMessagesFromContext marks the first len(messages)-keepCount messages
// as excluded from the LLM context, preserving them in storage for the web UI.
// If keepCount <= 0, all messages are excluded.
//
// The first message (index 0) is always preserved — it usually contains the
// original user request/goal and must survive compaction. If it was previously
// excluded (by an older version), it is un-excluded.
func (sm *SessionManager) ExcludeOldMessagesFromContext(key string, keepCount int) {
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

	if len(session.Messages) <= keepCount {
		return
	}

	excludeUpTo := len(session.Messages) - keepCount

	// Adjust boundary to avoid splitting tool_use/tool_result groups.
	// If the first kept message (at excludeUpTo) is a tool result whose
	// corresponding assistant tool_use is in the excluded range, move the
	// boundary forward to exclude the tool_result too. Repeat for any
	// consecutive tool results in the same group.
	for excludeUpTo < len(session.Messages) && isToolResultMessage(session.Messages[excludeUpTo]) {
		excludeUpTo++
	}

	// Also check: if the last excluded message is an assistant with tool_use
	// but the first kept message is NOT a tool_result, the tool_use blocks
	// are orphaned (no results). Move the boundary back to also exclude
	// this assistant message.
	if excludeUpTo > 0 {
		lastExcluded := session.Messages[excludeUpTo-1]
		if lastExcluded.Role == "assistant" && len(lastExcluded.ToolCalls) > 0 {
			// The assistant has tool_use blocks. Check if the next message
			// (first kept) is a tool_result for those.
			if excludeUpTo >= len(session.Messages) || !isToolResultMessage(session.Messages[excludeUpTo]) {
				// No tool_results follow — the tool_use is orphaned.
				// Move boundary back to also exclude this assistant message.
				excludeUpTo--
			}
		}
	}

	// Never exclude the first message (index 0) — it usually contains the
	// original user request/goal and must survive compaction.
	// If it was previously excluded (e.g., by an older version), un-exclude it.
	rangeStart := 1
	if len(session.Messages) > 0 && session.Messages[0].ExcludeFromContext {
		session.Messages[0].ExcludeFromContext = false
		rangeStart = 0
	}

	if excludeUpTo <= 1 {
		if rangeStart == 0 {
			// Only change is un-excluding msg 0 — persist that.
			session.Updated = time.Now()
			session.excludedRange = [2]int{0, 1}
			session.bumpEpoch()
		}
		return
	}

	for i := 1; i < excludeUpTo; i++ {
		session.Messages[i].ExcludeFromContext = true
	}
	session.Updated = time.Now()
	session.excludedRange = [2]int{rangeStart, excludeUpTo}
	session.bumpEpoch()
}

// CompactSession applies a loop-compaction result to the persisted session:
// it stores the summary produced by the tool-loop compactor, marks all but
// the last keepCount messages as excluded from context, persists the change,
// and optionally evicts excluded messages from memory when evict is true.
// It returns an error only if persistence fails; state mutations are applied
// before the save attempt so callers can decide whether to continue.
func (sm *SessionManager) CompactSession(key string, summary string, keepCount int, evict bool) error {
	sm.SetSummary(key, summary)
	sm.ExcludeOldMessagesFromContext(key, keepCount)
	err := sm.Save(key)
	if err != nil {
		return err
	}
	if evict {
		sm.EvictExcludedMessages(key)
	}
	sm.IncrementCompactionCount(key)
	return nil
}
func (sm *SessionManager) RemoveLastMessage(key string) bool {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return false
		}
	}

	if len(session.Messages) == 0 {
		return false
	}

	// Capture the absolute seq of the message being removed BEFORE slicing.
	// The save path uses this as a delete watermark (DELETE WHERE seq >= N)
	// instead of "delete max seq", which would race with concurrent appends
	// that reuse the same seq slot.
	session.deleteFromSeq = session.seqForIndex(len(session.Messages) - 1)
	session.Messages = session.Messages[:len(session.Messages)-1]
	session.Updated = time.Now()
	session.lastMsgDeleted = true
	session.bumpEpoch()
	sm.touchSession(key)
	return true
}

func (sm *SessionManager) ShouldStartFreshSession(key string, threshold time.Duration) (bool, time.Duration) {
	if threshold <= 0 {
		return false, 0
	}

	sm.ensureLoaded()
	sm.mu.Lock() // write lock for loadSessionFromDisk
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
	}
	if !ok || session == nil {
		return false, 0
	}

	if len(session.Messages) == 0 && strings.TrimSpace(session.Summary) == "" {
		return false, 0
	}

	lastActivity := session.Updated
	if lastActivity.IsZero() {
		lastActivity = session.Created
	}
	if lastActivity.IsZero() {
		return false, 0
	}

	idle := time.Since(lastActivity)
	if idle <= threshold {
		return false, idle
	}

	return true, idle
}

func (sm *SessionManager) Save(key string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.saveUnlocked(key)
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
			Key:     meta.Key,
			Name:    meta.Name,
			Mode:    meta.Mode,
			Created: meta.CreatedAt,
			Updated: meta.UpdatedAt,
		}
	}
	return nil
}

// SetHistory updates the messages of a session.
func (sm *SessionManager) SetHistory(key string, history []providers.Message) {
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

	// Create a deep copy to strictly isolate internal state
	// from the caller's slice.
	msgs := make([]providers.Message, len(history))
	copy(msgs, history)
	session.Messages = msgs
	session.Updated = time.Now()
	session.lastPersistedSeq = -1 // force full rewrite on next save
	session.bumpEpoch()
	sm.touchSession(key)
}

func (sm *SessionManager) HasVerbosePreference(key string) bool {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}

	return session.VerboseLevel != "" || session.VerboseMode
}

// GetVerboseMode returns the verbose mode setting for a session (legacy compatibility).
func (sm *SessionManager) GetVerboseMode(key string) bool {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}
	return session.VerboseMode
}

// SetVerboseMode sets the verbose mode for a session and persists it (legacy compatibility).
func (sm *SessionManager) SetVerboseMode(key string, enabled bool) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			// Create session if it doesn't exist
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.VerboseMode = enabled
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	// Persist immediately
	return sm.saveMetaOnlyUnlocked(key)
}

// GetVerboseLevel returns the verbose level for a session ("off", "basic", or "full").
// Migration: if VerboseMode is true but VerboseLevel is empty, returns "full".
func (sm *SessionManager) GetVerboseLevel(key string) string {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return "off"
		}
	}

	// Migration: handle legacy VerboseMode field
	if session.VerboseLevel == "" && session.VerboseMode {
		return "full"
	}
	if session.VerboseLevel == "" {
		return "off"
	}
	return session.VerboseLevel
}

// SetVerboseLevel sets the verbose level for a session and persists it.
func (sm *SessionManager) SetVerboseLevel(key string, level string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			// Create session if it doesn't exist
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.VerboseLevel = level
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	// Persist immediately
	return sm.saveMetaOnlyUnlocked(key)
}

// GetModel returns the model override for a session.
func (sm *SessionManager) GetModel(key string) string {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return ""
		}
	}
	return session.Model
}

// SetModel sets the model override for a session and persists it.
func (sm *SessionManager) SetModel(key string, model string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			// Create session if it doesn't exist
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.Model = model
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	// Persist immediately
	return sm.saveMetaOnlyUnlocked(key)
}

// GetMode returns the mode override for a session.
// Returns "" if not set. Callers should normalize "" to "agent" (backward compat).
func (sm *SessionManager) GetMode(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if ok {
		return session.Mode
	}
	// Try metadata
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Mode
	}
	return ""
}

// SetMode sets the mode for a session and persists it.
// Valid values: "", "chat", "agent", "group".
func (sm *SessionManager) SetMode(key string, mode string) error {
	// Validate mode
	validModes := map[string]bool{"": true, "chat": true, "agent": true, "group": true}
	if !validModes[mode] {
		return fmt.Errorf("invalid mode %q: must be one of \"\", \"chat\", \"agent\", \"group\"", mode)
	}

	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			// Create session if it doesn't exist
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.Mode = mode
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	// Update metadata
	sm.sessionMeta[key] = &sessionMetadata{
		Key:     session.Key,
		Name:    session.Name,
		Mode:    session.Mode,
		Created: session.Created,
		Updated: session.Updated,
	}

	return sm.saveMetaOnlyUnlocked(key)
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

// GetThinkingLevel returns the thinking level for a session.
func (sm *SessionManager) GetThinkingLevel(key string) string {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return ""
		}
	}
	return session.ThinkingLevel
}

// SetThinkingLevel sets the thinking level for a session and persists it.
func (sm *SessionManager) SetThinkingLevel(key string, level string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:     key,
				Created: time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.ThinkingLevel = level
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	return sm.saveMetaOnlyUnlocked(key)
}

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

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

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

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.CompactionCount++
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)
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
		InputTokens:      s.InputTokens,
		OutputTokens:     s.OutputTokens,
		CompactionCount:  s.CompactionCount,
		FirstInMemorySeq: s.firstInMemorySeq,
		CreatedAt:        s.Created,
		UpdatedAt:        s.Updated,
	}
}

// clearDirtyFlags resets all dirty tracking flags on a session.
func (s *Session) clearDirtyFlags() {
	s.metaDirty = false
	s.msgsAppended = 0
	s.modifiedFrom = 0
	s.excludedRange = [2]int{}
	s.lastMsgDeleted = false
	s.deleteFromSeq = 0
}

// markModified records that the in-memory message at idx was changed in-place
// (e.g. streaming chunks or the final replacement carrying tool_calls). It
// keeps the LOWEST modified index (stored 1-based so 0 means "none") so an
// incremental save rewrites every stale row — not just the last message,
// which may already have been superseded by appended tool results.
func (s *Session) markModified(idx int) {
	if idx < 0 {
		return
	}
	if s.modifiedFrom == 0 || idx+1 < s.modifiedFrom {
		s.modifiedFrom = idx + 1
	}
	s.bumpEpoch()
}

// bumpEpoch advances the save epoch, invalidating any in-flight save whose
// snapshot predates this mutation. Every logical mutation (message append,
// in-place edit, deletion, metadata change, eviction-boundary change) must
// call this — directly or via markModified — while holding sm.mu.
func (s *Session) bumpEpoch() {
	s.saveEpoch++
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
	var evictedJSONs []string
	var err error
	if session.firstInMemorySeq > 0 {
		evictedJSONs, err = sm.store.Sessions().LoadMessagesBeforeSeq(key, session.firstInMemorySeq)
		if err != nil {
			return fmt.Errorf("re-materialize evicted messages %q: %w", key, err)
		}
	}

	rows := make([]store.MessageRow, len(evictedJSONs)+len(session.Messages))
	for i, msgJSON := range evictedJSONs {
		var evt providers.Message
		if uerr := json.Unmarshal([]byte(msgJSON), &evt); uerr != nil {
			continue // skip corrupted rows
		}
		rows[i] = store.MessageRow{
			Seq:      i,
			Role:     evt.Role,
			JSON:     msgJSON,
			Excluded: true,
		}
	}
	offset := len(evictedJSONs)
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
	session.firstInMemorySeq = len(evictedJSONs)
	session.evictedTotal = len(evictedJSONs) - pruned
	if session.evictedTotal < 0 {
		session.evictedTotal = 0
	}
	session.clearDirtyFlags()
	return nil
}

// seqForIndex returns the absolute SQLite seq for an in-memory slice index.
// It enforces the core invariant `seq = firstInMemorySeq + sliceIndex` in one
// place so all save paths agree on absolute seqs even after eviction created a
// gap at the front of the in-memory slice.
func (s *Session) seqForIndex(i int) int {
	return s.firstInMemorySeq + i
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

// ============================================================================
// Streaming support — persists assistant message chunks directly in the session
// file instead of using a separate streams/ directory.
// ============================================================================

// streamFlushInterval is the minimum time between session saves during active streaming.
const streamFlushInterval = 200 * time.Millisecond

// AppendAssistantChunk appends a content chunk to the in-progress assistant message.
// If no in-progress message exists, it creates one with Streaming=true.
// The session is saved to disk periodically (throttled) so the partial content
// survives restarts and allows reconnecting clients to recover the stream.
func (sm *SessionManager) AppendAssistantChunk(key, chunk string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session == nil {
		return
	}

	// Find or create the in-progress assistant message
	msg := sm.getOrCreateStreamingMsg(session)
	msg.Content += chunk
	session.Updated = time.Now()
	session.hadStreamedContent = true
	session.markModified(len(session.Messages) - 1)

	sm.maybeFlushStream(key)
}

// AppendReasoningChunk appends a reasoning/thinking chunk to the in-progress
// assistant message. Creates the streaming message if it doesn't exist yet.
func (sm *SessionManager) AppendReasoningChunk(key, chunk string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session == nil {
		return
	}

	msg := sm.getOrCreateStreamingMsg(session)
	msg.ReasoningContent += chunk
	session.Updated = time.Now()
	session.hadStreamedContent = true
	session.markModified(len(session.Messages) - 1)

	sm.maybeFlushStream(key)
}

// FinalizeAssistantMessage marks the in-progress assistant message as complete
// by persisting the session to disk immediately. The Streaming flag is NOT
// cleared here — it stays until AddFullMessage replaces the streaming message
// with the final version. This allows HasStreamedContent to detect that content
// was already delivered via streaming chunks for deduplication.
func (sm *SessionManager) FinalizeAssistantMessage(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok || len(session.Messages) == 0 {
			return
		}
	}
	if len(session.Messages) == 0 {
		return
	}

	lastMsg := &session.Messages[len(session.Messages)-1]
	if lastMsg.Role == "assistant" && lastMsg.Streaming {
		session.Updated = time.Now()
		sm.touchSession(key)
		sm.flushStreamNow(key)
	}
}

// HasStreamedContent returns true if the session already had content delivered
// via streaming chunks this turn. Used to prevent duplicate message.stream
// delivery. It checks the in-memory flag (set by AppendAssistantChunk and
// cleared when a new user message arrives).
// Note: If the session is not in memory (evicted), returns false. The session
// will be loaded on-demand by the streaming methods.
func (sm *SessionManager) HasStreamedContent(key string) bool {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}

	// Check the in-memory flag first (survives Streaming flag being cleared by AddFullMessage)
	if session.hadStreamedContent {
		return true
	}

	// Fallback: check if the last message is still streaming
	if len(session.Messages) > 0 {
		lastMsg := session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming &&
			(lastMsg.Content != "" || lastMsg.ReasoningContent != "") {
			return true
		}
	}

	return false
}

// GetInProgressAssistant returns the in-progress assistant message, if any.
// Note: If the session is not in memory (evicted), returns nil. The session
// will be loaded on-demand by the streaming methods.
func (sm *SessionManager) GetInProgressAssistant(key string) *providers.Message {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok || len(session.Messages) == 0 {
		return nil
	}

	lastMsg := session.Messages[len(session.Messages)-1]
	if lastMsg.Role == "assistant" && lastMsg.Streaming {
		msg := lastMsg
		return &msg
	}
	return nil
}

// getOrCreateUnlocked returns or creates a session (caller must hold mu).
// Uses lazy loading to load sessions from disk on demand.
func (sm *SessionManager) getOrCreateUnlocked(key string) *Session {
	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          time.Now(),
				lastPersistedSeq: -1,
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
			sm.sessionMeta[key] = &sessionMetadata{
				Key:     key,
				Mode:    session.Mode,
				Created: session.Created,
				Updated: session.Updated,
			}
		}
	}
	sm.touchSession(key)
	return session
}

// getOrCreateStreamingMsg finds or creates the in-progress assistant message.
// Caller must hold sm.mu.
func (sm *SessionManager) getOrCreateStreamingMsg(session *Session) *providers.Message {
	if len(session.Messages) > 0 {
		lastMsg := &session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming {
			return lastMsg
		}
	}

	// Create a new streaming assistant message
	session.Messages = append(session.Messages, providers.Message{
		Role:      "assistant",
		Streaming: true,
	})
	session.msgsAppended++
	return &session.Messages[len(session.Messages)-1]
}

// maybeFlushStream saves the session to disk if enough time has passed since
// the last stream flush. Uses incremental save to avoid rewriting all messages.
// Caller must hold sm.mu.
func (sm *SessionManager) maybeFlushStream(key string) {
	if sm.store == nil {
		return
	}

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	now := time.Now()
	if now.Sub(session.lastStreamFlush) >= streamFlushInterval {
		session.lastStreamFlush = now
		sm.saveIncrementalUnlocked(key)
	}
}

// flushStreamNow saves the session to disk immediately using incremental save.
// Caller must hold sm.mu.
func (sm *SessionManager) flushStreamNow(key string) {
	if sm.store == nil {
		return
	}
	session, ok := sm.sessions[key]
	if ok {
		session.lastStreamFlush = time.Now()
	}
	sm.saveIncrementalUnlocked(key)
}

// EvictExcludedMessages removes all excluded messages from the in-memory
// slice and records the eviction gap in `firstInMemorySeq`/`evictedTotal`.
// The evicted messages remain persisted in SQLite (excluded = 1) and can be
// reloaded on demand via LoadEvictedMessages. All excluded messages are a
// contiguous prefix of the in-memory slice starting at index 0, so a single
// firstInMemorySeq (= number of evicted rows) captures the gap and the
// invariant `seq = firstInMemorySeq + sliceIndex` holds for every kept slot.
//
// Because index 0 (normally the original user request) is also excluded and
// evicted, its content is folded into the session summary first so no
// information is lost (the summary stays in context and in SQLite metadata).
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

	// Locate the first excluded message.
	runStart := 0
	for runStart < len(session.Messages) && !session.Messages[runStart].ExcludeFromContext {
		runStart++
	}
	if runStart >= len(session.Messages) {
		// Nothing excluded; no-op.
		return 0
	}

	// Extend the boundary over the full contiguous excluded run.
	evictUpTo := runStart
	for evictUpTo < len(session.Messages) && session.Messages[evictUpTo].ExcludeFromContext {
		evictUpTo++
	}

	// Fold the leading in-context messages (just index 0, the original user
	// request) into the summary so no context is lost when we evict the whole
	// [0..evictUpTo) prefix to keep the in-memory slice contiguous.
	if runStart > 0 {
		if folded := sm.foldEvictedIntoSummary(session, session.Messages[:runStart]); folded != "" {
			session.Summary = folded
			session.Updated = time.Now()
		}
	}

	// Rebuild the kept slice: the in-context suffix starting at evictUpTo.
	kept := make([]providers.Message, len(session.Messages)-evictUpTo)
	copy(kept, session.Messages[evictUpTo:])

	session.Messages = kept
	// Absolute seq of the first kept message: the number of rows evicted
	// before it. With the contiguous-prefix model this is exactly `evictUpTo`.
	session.firstInMemorySeq += evictUpTo
	session.evictedTotal += evictUpTo
	session.bumpEpoch()
	// Persist the new eviction boundary to SQLite so a cold restart restores
	// firstInMemorySeq and does not re-inflate evicted rows into RAM. The
	// boundary is stored in session metadata (FirstInMemorySeq); subsequent
	// Save calls also carry it via sessionMetaFromSession. Failure to persist
	// here only affects the durability of the boundary metadata (the in-memory
	// eviction still succeeds), so it is logged, not fatal.
	if sm.store != nil {
		if perr := sm.store.Sessions().UpdateFirstInMemorySeq(key, session.firstInMemorySeq); perr != nil {
			logger.WarnCF("session", "Failed to persist eviction boundary", map[string]interface{}{
				"session_key":     key,
				"first_in_memory": session.firstInMemorySeq,
				"error":           perr.Error(),
			})
		}
	}
	// Dirty flags are reset: everything in memory is already persisted; the
	// next Save must be a no-op (NOT a full rewrite).
	session.clearDirtyFlags()
	session.lastPersistedSeq = len(session.Messages) - 1
	sm.touchSession(key)

	logger.InfoCF("session", "Evicted excluded messages from memory", map[string]interface{}{
		"session_key":     key,
		"evicted":         evictUpTo,
		"remaining":       len(kept),
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
func (sm *SessionManager) EvictSession(key string) bool {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	_, ok := sm.sessions[key]
	if ok {
		// Save before evicting to ensure latest state is persisted.
		// saveForEviction re-checks that the session wasn't touched while
		// the save's disk I/O was in flight (the lock is released during it);
		// if it was, we keep the in-memory copy to avoid losing data.
		if sm.saveForEviction(key) {
			delete(sm.sessions, key)
			delete(sm.accessTimes, key)
			logger.InfoCF("session", "Session evicted from memory", map[string]interface{}{
				"session_key": key,
			})
		} else {
			ok = false
		}
	}

	// For subagent sessions (key contains ":subagent-"), also remove
	// sessionMeta to prevent unbounded metadata growth. Subagent sessions
	// are transient — once evicted, they should not be reloaded.
	if strings.Contains(key, ":subagent-") {
		delete(sm.sessionMeta, key)
	}

	return ok
}

// SessionExists reports whether a session exists for the given key in any
// layer: in-memory, metadata index, or on disk. The disk check matters
// because EvictSession removes subagent sessions from both memory and the
// metadata index but deliberately leaves the persisted file behind.
//
// This is used to detect subagent session-key collisions (e.g. after a
// restart, when in-memory ID counters reset). It only performs a cheap
// os.Stat — it never loads the session.
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
