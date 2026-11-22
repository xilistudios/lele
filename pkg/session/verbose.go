package session

import (
	"log"
	"sync"
)

type VerboseLevel string

type DefaultLevelResolver func(sessionKey string) (VerboseLevel, bool)

const (
	VerboseOff   VerboseLevel = "off"
	VerboseBasic VerboseLevel = "basic"
	VerboseFull  VerboseLevel = "full"
)

func IsValidVerboseLevel(level string) bool {
	switch VerboseLevel(level) {
	case VerboseOff, VerboseBasic, VerboseFull:
		return true
	}
	return false
}

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

type VerboseManager struct {
	mu                   sync.RWMutex
	cache                map[string]VerboseLevel
	sessions             *SessionManager
	defaultLevelResolver DefaultLevelResolver
}

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

func (vm *VerboseManager) IsVerbose(sessionKey string) bool {
	return vm.GetLevel(sessionKey) != VerboseOff
}

func (vm *VerboseManager) GetLevel(sessionKey string) VerboseLevel {
	vm.mu.RLock()
	if level, ok := vm.cache[sessionKey]; ok {
		vm.mu.RUnlock()
		return level
	}
	sessions := vm.sessions
	resolver := vm.defaultLevelResolver
	vm.mu.RUnlock()

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

func (vm *VerboseManager) SetLevel(sessionKey string, level VerboseLevel) {
	vm.mu.Lock()
	vm.cache[sessionKey] = level
	sessions := vm.sessions
	vm.mu.Unlock()

	if sessions != nil {
		if err := sessions.SetVerboseLevel(sessionKey, string(level)); err != nil {
			log.Printf("[WARN] Failed to persist verbose level for session %s: %v", sessionKey, err)
		}
	}
}

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

func (vm *VerboseManager) IsBasic(sessionKey string) bool {
	return vm.GetLevel(sessionKey) == VerboseBasic
}

func (vm *VerboseManager) IsFull(sessionKey string) bool {
	return vm.GetLevel(sessionKey) == VerboseFull
}

func (vm *VerboseManager) IsOff(sessionKey string) bool {
	return vm.GetLevel(sessionKey) == VerboseOff
}

func (vm *VerboseManager) Toggle(sessionKey string) bool {
	return vm.CycleLevel(sessionKey) != VerboseOff
}

func (vm *VerboseManager) SetVerbose(sessionKey string, enabled bool) {
	if enabled {
		vm.SetLevel(sessionKey, VerboseFull)
	} else {
		vm.SetLevel(sessionKey, VerboseOff)
	}
}

func (vm *VerboseManager) Clear(sessionKey string) {
	vm.mu.Lock()
	defer vm.mu.Unlock()
	delete(vm.cache, sessionKey)
}

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
