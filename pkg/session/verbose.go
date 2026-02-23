package session

import (
	"log"
	"sync"
)

// VerboseManager manages verbose mode state per session.
// When verbose is enabled, the agent sends real-time notifications
// for each tool execution to the user.
// The state is persisted through the SessionManager to survive restarts.
type VerboseManager struct {
	mu       sync.RWMutex
	cache    map[string]bool // In-memory cache for performance
	sessions *SessionManager // Optional: for persistence
}

// NewVerboseManager creates a new VerboseManager.
// The sessions parameter is optional - if provided, verbose mode
// will be persisted across restarts.
func NewVerboseManager(sessions ...*SessionManager) *VerboseManager {
	var sm *SessionManager
	if len(sessions) > 0 {
		sm = sessions[0]
	}
	return &VerboseManager{
		cache:    make(map[string]bool),
		sessions: sm,
	}
}

// SetSessionManager allows setting the session manager after creation.
// This is useful when the SessionManager is created after VerboseManager.
func (vm *VerboseManager) SetSessionManager(sm *SessionManager) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.sessions = sm
}

// IsVerbose returns true if verbose mode is enabled for the given session.
// It first checks the in-memory cache, then falls back to the persistent store.
func (vm *VerboseManager) IsVerbose(sessionKey string) bool {
	vm.mu.RLock()
	if state, ok := vm.cache[sessionKey]; ok {
		vm.mu.RUnlock()
		return state
	}
	sessions := vm.sessions
	vm.mu.RUnlock()

	// If we have a session manager, load from persistent storage
	if sessions != nil {
		state := sessions.GetVerboseMode(sessionKey)
		vm.mu.Lock()
		vm.cache[sessionKey] = state
		vm.mu.Unlock()
		return state
	}

	return false
}

// SetVerbose sets the verbose mode for a session.
// If a SessionManager is configured, the state is persisted immediately.
func (vm *VerboseManager) SetVerbose(sessionKey string, enabled bool) {
	vm.mu.Lock()
	vm.cache[sessionKey] = enabled
	sessions := vm.sessions
	vm.mu.Unlock()

	// Persist if we have a session manager
	if sessions != nil {
		if err := sessions.SetVerboseMode(sessionKey, enabled); err != nil {
			log.Printf("[WARN] Failed to persist verbose mode for session %s: %v", sessionKey, err)
		}
	}
}

// Toggle toggles verbose mode for a session and returns the new state.
// The change is persisted if a SessionManager is configured.
func (vm *VerboseManager) Toggle(sessionKey string) bool {
	current := vm.IsVerbose(sessionKey)
	newState := !current
	vm.SetVerbose(sessionKey, newState)
	return newState
}

// Clear removes the verbose state from cache for a session.
// Note: This does not affect the persisted state.
func (vm *VerboseManager) Clear(sessionKey string) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	delete(vm.cache, sessionKey)
}

// InitializeFromSession loads the verbose mode from a session into the cache.
// This should be called when loading an existing session.
func (vm *VerboseManager) InitializeFromSession(sessionKey string) {
	vm.mu.RLock()
	sessions := vm.sessions
	vm.mu.RUnlock()

	if sessions != nil {
		state := sessions.GetVerboseMode(sessionKey)
		vm.mu.Lock()
		vm.cache[sessionKey] = state
		vm.mu.Unlock()
	}
}
