package session

import (
	"log"
	"sync"
)

// VerboseLevel represents the verbosity level for tool execution notifications
type VerboseLevel string

type DefaultLevelResolver func(sessionKey string) (VerboseLevel, bool)

const (
	// VerboseOff disables all tool execution notifications
	VerboseOff VerboseLevel = "off"
	// VerboseBasic shows simplified action descriptions only
	VerboseBasic VerboseLevel = "basic"
	// VerboseFull shows detailed tool calls and results (legacy behavior)
	VerboseFull VerboseLevel = "full"
)

// IsValidVerboseLevel checks if a string is a valid verbose level
func IsValidVerboseLevel(level string) bool {
	switch VerboseLevel(level) {
	case VerboseOff, VerboseBasic, VerboseFull:
		return true
	}
	return false
}

// VerboseLevelFromString converts a string to VerboseLevel (defaults to off)
func VerboseLevelFromString(s string) VerboseLevel {
	switch s {
	case "basic":
		return VerboseBasic
	case "full":
		return VerboseFull
	default:
		return VerboseOff
	}
}

// VerboseManager manages verbose mode state per session.
// When verbose is enabled, the agent sends real-time notifications
// for each tool execution to the user.
// The state is persisted through the SessionManager to survive restarts.
type VerboseManager struct {
	mu                   sync.RWMutex
	cache                map[string]VerboseLevel // In-memory cache for performance
	sessions             *SessionManager         // Optional: for persistence
	defaultLevelResolver DefaultLevelResolver
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
		cache:    make(map[string]VerboseLevel),
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

func (vm *VerboseManager) SetDefaultLevelResolver(resolver DefaultLevelResolver) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	vm.defaultLevelResolver = resolver
}

// IsVerbose returns true if verbose mode is enabled (not off) for the given session.
// It first checks the in-memory cache, then falls back to the persistent store.
func (vm *VerboseManager) IsVerbose(sessionKey string) bool {
	return vm.GetLevel(sessionKey) != VerboseOff
}

// GetLevel returns the verbose level for a session.
// It first checks the in-memory cache, then falls back to the persistent store.
func (vm *VerboseManager) GetLevel(sessionKey string) VerboseLevel {
	vm.mu.RLock()
	if level, ok := vm.cache[sessionKey]; ok {
		vm.mu.RUnlock()
		return level
	}
	sessions := vm.sessions
	resolver := vm.defaultLevelResolver
	vm.mu.RUnlock()

	// If we have a session manager, load from persistent storage
	if sessions != nil && sessions.HasVerbosePreference(sessionKey) {
		levelStr := sessions.GetVerboseLevel(sessionKey)
		level := VerboseLevelFromString(levelStr)
		vm.mu.Lock()
		vm.cache[sessionKey] = level
		vm.mu.Unlock()
		return level
	}

	if resolver != nil {
		if level, ok := resolver(sessionKey); ok {
			return level
		}
	}

	return VerboseOff
}

// SetLevel sets the verbose level for a session.
// If a SessionManager is configured, the state is persisted immediately.
func (vm *VerboseManager) SetLevel(sessionKey string, level VerboseLevel) {
	vm.mu.Lock()
	vm.cache[sessionKey] = level
	sessions := vm.sessions
	vm.mu.Unlock()

	// Persist if we have a session manager
	if sessions != nil {
		if err := sessions.SetVerboseLevel(sessionKey, string(level)); err != nil {
			log.Printf("[WARN] Failed to persist verbose level for session %s: %v", sessionKey, err)
		}
	}
}

// CycleLevel cycles through verbosity levels: off -> basic -> full -> off
// Returns the new level.
func (vm *VerboseManager) CycleLevel(sessionKey string) VerboseLevel {
	current := vm.GetLevel(sessionKey)
	var next VerboseLevel
	switch current {
	case VerboseOff:
		next = VerboseBasic
	case VerboseBasic:
		next = VerboseFull
	case VerboseFull:
		next = VerboseOff
	default:
		next = VerboseOff
	}
	vm.SetLevel(sessionKey, next)
	return next
}

// IsBasic returns true if verbose level is basic
func (vm *VerboseManager) IsBasic(sessionKey string) bool {
	return vm.GetLevel(sessionKey) == VerboseBasic
}

// IsFull returns true if verbose level is full
func (vm *VerboseManager) IsFull(sessionKey string) bool {
	return vm.GetLevel(sessionKey) == VerboseFull
}

// IsOff returns true if verbose level is off
func (vm *VerboseManager) IsOff(sessionKey string) bool {
	return vm.GetLevel(sessionKey) == VerboseOff
}

// Toggle toggles verbose mode on/off (legacy compatibility, cycles off->basic->full->off)
// The change is persisted if a SessionManager is configured.
// Returns true if the new state is not off (backwards compatibility).
func (vm *VerboseManager) Toggle(sessionKey string) bool {
	return vm.CycleLevel(sessionKey) != VerboseOff
}

// SetVerbose sets the verbose mode for a session (legacy compatibility).
// If enabled, sets to full; if disabled, sets to off.
func (vm *VerboseManager) SetVerbose(sessionKey string, enabled bool) {
	if enabled {
		vm.SetLevel(sessionKey, VerboseFull)
	} else {
		vm.SetLevel(sessionKey, VerboseOff)
	}
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

	if sessions != nil && sessions.HasVerbosePreference(sessionKey) {
		levelStr := sessions.GetVerboseLevel(sessionKey)
		level := VerboseLevelFromString(levelStr)
		vm.mu.Lock()
		vm.cache[sessionKey] = level
		vm.mu.Unlock()
		return
	}

	vm.Clear(sessionKey)
}
