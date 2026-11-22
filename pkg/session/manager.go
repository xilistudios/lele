package session

import (
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

type SessionManager struct {
	sessions    map[string]*Session
	mu          sync.RWMutex
	sessionRepo *store.SessionRepo
	loadOnce    sync.Once

	sessionKV *store.KVRepo

	sessionMeta map[string]*sessionMetadata

	maxInMemory int
	evictionTTL time.Duration
	accessTimes map[string]time.Time

	countsCache storeCountsCache
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

func (sm *SessionManager) SetSessionRepo(r *store.SessionRepo) {
	sm.sessionRepo = r
}

func (sm *SessionManager) HasSessionRepo() bool {
	return sm.sessionRepo != nil
}

func (sm *SessionManager) SetSessionKV(kv *store.KVRepo) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessionKV = kv
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

func (sm *SessionManager) AddFullMessage(sessionKey string, msg providers.Message) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(sessionKey)

	if msg.Role == "user" && len(session.Messages) == 0 && session.Name == "" {
		name := msg.DisplayContent
		if name == "" {
			name = msg.Content
		}
		session.Name = generateSessionName(name)
		session.bumpEpoch()
		sm.syncSessionMetaLocked(session)
	}

	if msg.Role == "user" {
		session.hadStreamedContent = false
	}

	if msg.Role == "assistant" && len(session.Messages) > 0 {
		lastMsg := &session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming {
			msg.Streaming = false
			*lastMsg = msg
			session.Updated = time.Now()
			session.markModified(len(session.Messages) - 1)
			session.publishViewLocked()
			sm.syncSessionMetaLocked(session)
			return
		}
	}

	if msg.Role != "assistant" && sm.resolveStreamingTailLocked(session, msg) {
		return
	}

	session.Messages = append(session.Messages, msg)
	session.Updated = time.Now()
	session.msgsAppended++
	session.bumpEpoch()
	session.publishViewLocked()
	sm.syncSessionMetaLocked(session)
}

func (sm *SessionManager) GetHistory(key string) []providers.Message {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
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

func (sm *SessionManager) GetHistoryView(key string) []providers.Message {
	sm.ensureLoaded()

	sm.mu.RLock()
	if session, ok := sm.sessions[key]; ok {
		snap := session.viewSnapshot.Load()
		if snap != nil && snap.epoch == session.saveEpoch {
			sm.mu.RUnlock()
			return snap.view
		}
		view := cloneMessages(session.Messages)
		fresh := &messageSnapshot{epoch: session.saveEpoch, view: view}
		sm.mu.RUnlock()
		session.viewSnapshot.CompareAndSwap(snap, fresh)
		return view
	}
	sm.mu.RUnlock()

	sm.mu.Lock()
	defer sm.mu.Unlock()
	if session, ok := sm.sessions[key]; ok {
		sm.touchSession(key)
		return sm.viewLocked(session)
	}
	if session, ok := sm.loadSessionFromDisk(key); ok {
		sm.touchSession(key)
		return sm.viewLocked(session)
	}
	return []providers.Message{}
}

func (sm *SessionManager) viewLocked(session *Session) []providers.Message {
	if snap := session.viewSnapshot.Load(); snap != nil && snap.epoch == session.saveEpoch {
		return snap.view
	}
	return session.publishViewLocked()
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
		session.lastPersistedSeq = -1
		session.excludedRange = [2]int{}
		session.excludeBoundary = 0
		session.bumpEpoch()
		session.publishViewLocked()
		sm.touchSession(key)
		return
	}

	if len(session.Messages) <= keepLast {
		return
	}

	session.Messages = session.Messages[len(session.Messages)-keepLast:]
	session.Updated = time.Now()
	session.lastPersistedSeq = -1
	session.excludedRange = [2]int{}
	session.excludeBoundary = 0
	session.bumpEpoch()
	session.publishViewLocked()
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

	session.deleteFromSeq = session.seqForIndex(len(session.Messages) - 1)
	session.Messages = session.Messages[:len(session.Messages)-1]
	session.Updated = time.Now()
	session.lastMsgDeleted = true
	if session.excludeBoundary > len(session.Messages) {
		session.excludeBoundary = 0
	}
	session.bumpEpoch()
	session.publishViewLocked()
	sm.touchSession(key)
	return true
}

func (sm *SessionManager) residentKeys() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	keys := make([]string, 0, len(sm.sessions))
	for key := range sm.sessions {
		keys = append(keys, key)
	}
	return keys
}

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

	msgs := make([]providers.Message, len(history))
	copy(msgs, history)
	session.Messages = msgs
	session.Updated = time.Now()
	session.lastPersistedSeq = -1
	session.excludedRange = [2]int{}
	if session.excludeBoundary > len(msgs) {
		session.excludeBoundary = 0
	}
	session.bumpEpoch()
	session.publishViewLocked()
	sm.touchSession(key)
}

func (sm *SessionManager) syncSessionMetaLocked(session *Session) {
	if session == nil || session.Key == "" {
		return
	}
	if meta, ok := sm.sessionMeta[session.Key]; ok {
		meta.Name = session.Name
		meta.Mode = session.Mode
		meta.Folder = session.Folder
		meta.AgentID = session.AgentID
		meta.SubagentStatus = session.SubagentStatus
		meta.Created = session.Created
		meta.Updated = session.Updated
		return
	}
	sm.sessionMeta[session.Key] = &sessionMetadata{
		Key:            session.Key,
		Name:           session.Name,
		Mode:           session.Mode,
		Folder:         session.Folder,
		AgentID:        session.AgentID,
		SubagentStatus: session.SubagentStatus,
		Created:        session.Created,
		Updated:        session.Updated,
	}
}

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
			sm.syncSessionMetaLocked(session)
		}
	}
	sm.touchSession(key)
	return session
}
