package session

// SessionManager: construction and message mutation.
//
// Owns the manager struct itself plus the API that creates sessions and
// mutates their message history (append, replace, truncate, remove, read).
// Loading lives in load.go, durability in persist.go, memory pressure in
// eviction.go.

import (
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

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

func (sm *SessionManager) GetOrCreate(key string) *Session {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.getOrCreateUnlocked(key)
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

	session := sm.getOrCreateUnlocked(sessionKey)

	// The session name is what the user sees they asked, not what the model
	// received: a harness command stores the expanded prompt in Content but the
	// original "/name args" in DisplayContent.
	if msg.Role == "user" && len(session.Messages) == 0 && session.Name == "" {
		name := msg.DisplayContent
		if name == "" {
			name = msg.Content
		}
		session.Name = generateSessionName(name)
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

	// HOT PATH: session already in memory. Copy under the READ lock so the
	// TUI render loop and concurrent streaming appends do not serialize on
	// the exclusive write lock. Previously this took sm.mu.Lock() and the
	// full O(n) copy ran under it, so every render frame queued behind every
	// in-flight AppendAssistantChunk / stream flush (measured: p95 frame
	// latency 29ms -> 23ms and max 51ms -> 33ms at 6k messages with a
	// continuous stream writer). An RWMutex lets readers proceed
	// concurrently; a writer (append/flush) only briefly excludes them.
	//
	// Reads intentionally do NOT touch the LRU access time: an active session
	// is kept fresh by its own writes (AppendAssistantChunk/AddFullMessage
	// call touchSession), and a purely-passive read of an idle session that
	// later gets evicted simply reloads from disk on the next call — cheap and
	// correct, never corrupt. This mirrors GetInProgressAssistant/HasMessages,
	// which already read under RLock without touching.
	sm.mu.RLock()
	if session, ok := sm.sessions[key]; ok {
		view := make([]providers.Message, len(session.Messages))
		copy(view, session.Messages)
		sm.mu.RUnlock()
		return view
	}
	sm.mu.RUnlock()

	// COLD PATH: not resident — load from disk. loadSessionFromDisk mutates the
	// sessions/accessTimes maps and may evict, so it needs the write lock.
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[key]; ok { // re-check under the write lock
		sm.touchSession(key)
		view := make([]providers.Message, len(session.Messages))
		copy(view, session.Messages)
		return view
	}
	if session, ok := sm.loadSessionFromDisk(key); ok {
		sm.touchSession(key)
		view := make([]providers.Message, len(session.Messages))
		copy(view, session.Messages)
		return view
	}
	return []providers.Message{}
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

// residentKeys returns the keys of the sessions currently held in memory.
// The result is not sorted; callers that need a stable order must sort it.
func (sm *SessionManager) residentKeys() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	keys := make([]string, 0, len(sm.sessions))
	for key := range sm.sessions {
		keys = append(keys, key)
	}
	return keys
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

// getOrCreateUnlocked returns or creates a session (caller must hold mu).
// Uses lazy loading to load sessions from disk on demand.
//
// It is the single place where a session becomes resident in memory, and it
// guarantees the invariant "resident ⇒ present in sessionMeta": the metadata
// index is what ListSessions (and therefore the WebUI session-history
// endpoints) use for sessions that are not resident, and loadSessionFromDisk
// refuses to reload a session that has no metadata entry. Any path that
// materializes a session must go through here — hand-rolling the
// sessions[key] assignment silently drops the session from every listing as
// soon as it is evicted (this was the cron-spawn/subagent invisibility bug).
func (sm *SessionManager) getOrCreateUnlocked(key string) *Session {
	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			now := time.Now()
			session = &Session{
				Key:              key,
				Messages:         []providers.Message{},
				Created:          now,
				Updated:          now,
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
