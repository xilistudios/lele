package session

// Per-session settings: name, summary, model, folder, mode, thinking and
// verbosity levels.
//
// Every setter validates its input, applies the change, marks the session's
// metadata dirty, and persists. Setters that change persisted metadata return
// an error when persistence fails so callers can surface it.

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

// SetSubagentStatus records the terminal status of a subagent session
// ("completed", "failed", "not_done", "cancelled", "needs_context") and
// persists it immediately. It creates no session when the key is unknown:
// the runner only calls it for sessions it already recorded via the
// SessionRecorder, so there is nothing legitimate to materialize here.
// Failures are best-effort: the in-memory task keeps the authoritative
// status until eviction/restart regardless of this call's result.
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

	// Best-effort persistence: ignore errors, the in-memory task holds the
	// authoritative status until eviction/restart regardless.
	if sm.store != nil {
		_ = sm.saveMetaOnlyUnlocked(key)
	}
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

	session := sm.getOrCreateUnlocked(key)

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

	session := sm.getOrCreateUnlocked(key)

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

	session := sm.getOrCreateUnlocked(key)

	session.Model = model
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	// Persist immediately
	return sm.saveMetaOnlyUnlocked(key)
}

// GetFolder returns the user-selected folder for a session.
// Returns "" when no folder is set.
//
// Unlike GetModel, this reads the lightweight metadata when the session is not
// resident in memory: the WebUI session-list endpoints call it once per
// session, and a full loadSessionFromDisk fallback would re-materialize every
// session's entire message history just to read one string (the N+1 the meta
// fast path exists to avoid).
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

// SetFolder sets the user-selected folder for a session and persists it.
// An empty folder clears the selection.
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

	// Keep the lightweight metadata in sync so read-only listing paths
	// (GetFolder on non-resident sessions) observe the new value without a
	// full load.
	if meta, ok := sm.sessionMeta[key]; ok {
		meta.Folder = folder
		meta.Name = session.Name
		meta.Mode = session.Mode
		meta.Updated = session.Updated
	} else {
		sm.sessionMeta[key] = &sessionMetadata{
			Key:     session.Key,
			Name:    session.Name,
			Mode:    session.Mode,
			Folder:  session.Folder,
			Created: session.Created,
			Updated: session.Updated,
		}
	}

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

	session := sm.getOrCreateUnlocked(key)

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
		Folder:  session.Folder,
		Created: session.Created,
		Updated: session.Updated,
	}

	return sm.saveMetaOnlyUnlocked(key)
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

	session := sm.getOrCreateUnlocked(key)

	session.ThinkingLevel = level
	session.Updated = time.Now()
	session.metaDirty = true
	session.bumpEpoch()
	sm.touchSession(key)

	return sm.saveMetaOnlyUnlocked(key)
}
