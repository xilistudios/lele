package session

import (
	"fmt"
	"strings"
	"time"
)

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

func (sm *SessionManager) GetName(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return session.Name
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Name
	}
	return ""
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

func (sm *SessionManager) SetSubagentStatus(key string, status string) {
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

	session.SubagentStatus = status
	session.metaDirty = true
	session.bumpEpoch()
	if meta, ok := sm.sessionMeta[key]; ok {
		meta.SubagentStatus = status
	}
	sm.touchSession(key)

	if sm.sessionRepo != nil {
		_ = sm.saveMetaOnlyUnlocked(key)
	}
}

func (sm *SessionManager) SetSubagentAgentID(key string, agentID string) {
	if key == "" || agentID == "" {
		return
	}
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session.AgentID == agentID {
		return
	}

	session.AgentID = agentID
	session.metaDirty = true
	session.bumpEpoch()
	sm.syncSessionMetaLocked(session)

	if sm.sessionRepo != nil {
		_ = sm.saveMetaOnlyUnlocked(key)
	}
}

const sessionStateAgentKeyPrefix = "sess:agent:"

func (sm *SessionManager) SubagentAgentPin(key string) string {
	if key == "" {
		return ""
	}
	sm.mu.RLock()
	kv := sm.sessionKV
	sm.mu.RUnlock()
	if kv == nil {
		return ""
	}
	value, ok, err := kv.Get(sessionStateAgentKeyPrefix + key)
	if err != nil || !ok {
		return ""
	}
	return value
}

func (sm *SessionManager) SetName(key string, name string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.Name = strings.TrimSpace(name)
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	return sm.saveMetaOnlyUnlocked(key)
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

func (sm *SessionManager) SetVerboseMode(key string, enabled bool) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.VerboseMode = enabled
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	return sm.saveMetaOnlyUnlocked(key)
}

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

	if session.VerboseLevel == "" && session.VerboseMode {
		return "full"
	}
	if session.VerboseLevel == "" {
		return "off"
	}
	return session.VerboseLevel
}

func (sm *SessionManager) SetVerboseLevel(key string, level string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.VerboseLevel = level
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	return sm.saveMetaOnlyUnlocked(key)
}

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

func (sm *SessionManager) SetModel(key string, model string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.Model = model
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	return sm.saveMetaOnlyUnlocked(key)
}

func (sm *SessionManager) GetFolder(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return session.Folder
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Folder
	}
	return ""
}

func (sm *SessionManager) SetFolder(key string, folder string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.Folder = folder
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	sm.syncSessionMetaLocked(session)

	return sm.saveMetaOnlyUnlocked(key)
}

func (sm *SessionManager) GetMode(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if ok {
		return session.Mode
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Mode
	}
	return ""
}

func (sm *SessionManager) SetMode(key string, mode string) error {
	validModes := map[string]bool{"": true, "chat": true, "agent": true, "group": true}
	if !validModes[mode] {
		return fmt.Errorf("invalid mode %q: must be one of \"\", \"chat\", \"agent\", \"group\"", mode)
	}

	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.Mode = mode
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	sm.syncSessionMetaLocked(session)

	return sm.saveMetaOnlyUnlocked(key)
}

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

func (sm *SessionManager) SetThinkingLevel(key string, level string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)

	session.ThinkingLevel = level
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	return sm.saveMetaOnlyUnlocked(key)
}
