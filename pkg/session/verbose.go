package session

import "sync"

// VerboseManager manages verbose mode state per session.
// When verbose is enabled, the agent sends real-time notifications
// for each tool execution to the user.
type VerboseManager struct {
	states sync.Map // map[string]bool: sessionKey -> verbose enabled
}

// NewVerboseManager creates a new VerboseManager.
func NewVerboseManager() *VerboseManager {
	return &VerboseManager{}
}

// IsVerbose returns true if verbose mode is enabled for the given session.
func (vm *VerboseManager) IsVerbose(sessionKey string) bool {
	if val, ok := vm.states.Load(sessionKey); ok {
		if enabled, ok := val.(bool); ok {
			return enabled
		}
	}
	return false
}

// SetVerbose sets the verbose mode for a session.
func (vm *VerboseManager) SetVerbose(sessionKey string, enabled bool) {
	vm.states.Store(sessionKey, enabled)
}

// Toggle toggles verbose mode for a session and returns the new state.
func (vm *VerboseManager) Toggle(sessionKey string) bool {
	current := vm.IsVerbose(sessionKey)
	newState := !current
	vm.SetVerbose(sessionKey, newState)
	return newState
}

// Clear removes the verbose state for a session.
func (vm *VerboseManager) Clear(sessionKey string) {
	vm.states.Delete(sessionKey)
}
