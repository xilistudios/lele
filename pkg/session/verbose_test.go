package session

import (
	"testing"
)

func TestIsValidVerboseLevel(t *testing.T) {
	tests := []struct {
		level string
		want  bool
	}{
		{"off", true},
		{"basic", true},
		{"full", true},
		{"", false},
		{"verbose", false},
		{"OFF", false},
		{"O ff", false},
	}
	for _, tt := range tests {
		if got := IsValidVerboseLevel(tt.level); got != tt.want {
			t.Errorf("IsValidVerboseLevel(%q) = %v, want %v", tt.level, got, tt.want)
		}
	}
}

func TestVerboseLevelFromString(t *testing.T) {
	tests := []struct {
		s    string
		want VerboseLevel
	}{
		{"basic", VerboseBasic},
		{"full", VerboseFull},
		{"off", VerboseOff},
		{"", VerboseOff},
		{"unknown", VerboseOff},
		{"BASIC", VerboseOff},
	}
	for _, tt := range tests {
		if got := VerboseLevelFromString(tt.s); got != tt.want {
			t.Errorf("VerboseLevelFromString(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

func TestNewVerboseManager_NoSessionManager(t *testing.T) {
	vm := NewVerboseManager()
	if vm == nil {
		t.Fatal("NewVerboseManager returned nil")
	}
	if vm.cache == nil {
		t.Fatal("cache should not be nil")
	}
	if vm.sessions != nil {
		t.Fatalf("sessions should be nil when not provided, got %v", vm.sessions)
	}

	// Not verbose by default
	if vm.IsVerbose("any") {
		t.Error("new manager should not report verbose for unknown session")
	}
}

func TestNewVerboseManager_WithSessionManager(t *testing.T) {
	sm := NewSessionManager()
	vm := NewVerboseManager(sm)
	if vm.sessions != sm {
		t.Errorf("sessions = %v, want %v", vm.sessions, sm)
	}
	// Multiple args should only use the first
	sm2 := NewSessionManager()
	vm2 := NewVerboseManager(sm, sm2)
	if vm2.sessions != sm {
		t.Errorf("sessions with multiple args = %v, want first %v", vm2.sessions, sm)
	}
}

func TestVerboseManager_SetSessionManager(t *testing.T) {
	vm := NewVerboseManager()
	if vm.sessions != nil {
		t.Fatalf("expected nil before SetSessionManager")
	}
	sm := NewSessionManager()
	vm.SetSessionManager(sm)
	if vm.sessions != sm {
		t.Errorf("sessions = %v, want %v after SetSessionManager", vm.sessions, sm)
	}
}

func TestVerboseManager_SetDefaultLevelResolver(t *testing.T) {
	vm := NewVerboseManager()
	called := false
	resolver := func(sessionKey string) (VerboseLevel, bool) {
		called = true
		if sessionKey == "sess:resolved" {
			return VerboseFull, true
		}
		return "", false
	}
	vm.SetDefaultLevelResolver(resolver)

	// Should call resolver for a session with no cache/persistence
	if got := vm.GetLevel("sess:resolved"); got != VerboseFull {
		t.Errorf("GetLevel(resolved) = %v, want full (resolver called=%v)", got, called)
	}
	if !called {
		t.Error("resolver was not called")
	}

	// Resolver returning ok=false should fall through to off
	if got := vm.GetLevel("sess:unresolved"); got != VerboseOff {
		t.Errorf("GetLevel(unresolved) = %v, want off", got)
	}
}

func TestVerboseManager_SetAndGetLevel(t *testing.T) {
	vm := NewVerboseManager()

	vm.SetLevel("sess:a", VerboseBasic)
	if got := vm.GetLevel("sess:a"); got != VerboseBasic {
		t.Errorf("GetLevel = %v, want basic", got)
	}

	// Different session is unaffected
	if got := vm.GetLevel("sess:b"); got != VerboseOff {
		t.Errorf("GetLevel(b) = %v, want off", got)
	}

	// Overwrite level
	vm.SetLevel("sess:a", VerboseFull)
	if got := vm.GetLevel("sess:a"); got != VerboseFull {
		t.Errorf("GetLevel after overwrite = %v, want full", got)
	}
}

func TestVerboseManager_GetLevel_DefaultsOff(t *testing.T) {
	vm := NewVerboseManager()
	if got := vm.GetLevel("missing"); got != VerboseOff {
		t.Errorf("GetLevel(missing) = %v, want off", got)
	}
}

func TestVerboseManager_CycleLevel(t *testing.T) {
	vm := NewVerboseManager()
	key := "sess:cycle"

	// off -> basic
	if got := vm.CycleLevel(key); got != VerboseBasic {
		t.Errorf("cycle off -> = %v, want basic", got)
	}
	// basic -> full
	if got := vm.CycleLevel(key); got != VerboseFull {
		t.Errorf("cycle basic -> = %v, want full", got)
	}
	// full -> off
	if got := vm.CycleLevel(key); got != VerboseOff {
		t.Errorf("cycle full -> = %v, want off", got)
	}
	// off -> basic again
	if got := vm.CycleLevel(key); got != VerboseBasic {
		t.Errorf("cycle round2 -> = %v, want basic", got)
	}
}

func TestVerboseManager_CycleLevel_UnknownDefault(t *testing.T) {
	vm := NewVerboseManager()
	// Directly set a nonsensical value in the cache
	vm.cache["sess:weird"] = VerboseLevel("weird")
	if got := vm.CycleLevel("sess:weird"); got != VerboseOff {
		t.Errorf("cycle unknown -> = %v, want off", got)
	}
}

func TestVerboseManager_IsVerbose_IsBasic_IsFull_IsOff(t *testing.T) {
	vm := NewVerboseManager()
	vm.SetLevel("sess:basic", VerboseBasic)
	vm.SetLevel("sess:full", VerboseFull)
	vm.SetLevel("sess:off", VerboseOff)

	if !vm.IsVerbose("sess:basic") || !vm.IsVerbose("sess:full") {
		t.Error("basic/full should be verbose")
	}
	if vm.IsVerbose("sess:off") {
		t.Error("off should not be verbose")
	}
	if vm.IsVerbose("sess:none") {
		t.Error("unknown should not be verbose")
	}

	if !vm.IsBasic("sess:basic") {
		t.Error("expected basic session to be IsBasic")
	}
	if vm.IsBasic("sess:full") {
		t.Error("full session should not be IsBasic")
	}

	if !vm.IsFull("sess:full") {
		t.Error("expected full session to be IsFull")
	}
	if vm.IsFull("sess:basic") {
		t.Error("basic session should not be IsFull")
	}

	if !vm.IsOff("sess:off") || !vm.IsOff("sess:none") {
		t.Error("off/unknown sessions should be IsOff")
	}
	if vm.IsOff("sess:full") {
		t.Error("full session should not be IsOff")
	}
}

func TestVerboseManager_Toggle(t *testing.T) {
	vm := NewVerboseManager()
	key := "sess:toggle"

	// Legacy toggle behaves like cycle: returns true when not off
	if !vm.Toggle(key) { // off -> basic -> true
		t.Error("toggle from off should be true")
	}
	if !vm.Toggle(key) { // basic -> full -> true
		t.Error("toggle from basic should be true")
	}
	if vm.Toggle(key) { // full -> off -> false
		t.Error("toggle from full should be false")
	}
	if !vm.Toggle(key) { // off -> basic -> true
		t.Error("toggle from off should be true")
	}
}

func TestVerboseManager_SetVerbose(t *testing.T) {
	vm := NewVerboseManager()
	key := "sess:sv"

	vm.SetVerbose(key, true)
	if got := vm.GetLevel(key); got != VerboseFull {
		t.Errorf("SetVerbose(true) level = %v, want full", got)
	}
	if !vm.IsVerbose(key) {
		t.Error("SetVerbose(true) should be verbose")
	}

	vm.SetVerbose(key, false)
	if got := vm.GetLevel(key); got != VerboseOff {
		t.Errorf("SetVerbose(false) level = %v, want off", got)
	}
	if vm.IsVerbose(key) {
		t.Error("SetVerbose(false) should not be verbose")
	}
}

func TestVerboseManager_Clear(t *testing.T) {
	vm := NewVerboseManager()
	key := "sess:clear"

	vm.SetLevel(key, VerboseFull)
	if !vm.IsVerbose(key) {
		t.Fatal("expected verbose before clear")
	}

	vm.Clear(key)
	if vm.IsVerbose(key) {
		t.Error("should not be verbose after clear")
	}
	if _, ok := vm.cache[key]; ok {
		t.Error("cache entry should be deleted after clear")
	}
}

// ---- Tests with persistence (SessionManager + store) ----

// withPersistentVerboseManager builds a VerboseManager backed by a SQLite store.
func withPersistentVerboseManager(t *testing.T) (*VerboseManager, *SessionManager) {
	t.Helper()
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	vm := NewVerboseManager(sm)
	return vm, sm
}

func TestVerboseManager_GetLevel_LoadsFromPersistence(t *testing.T) {
	sm := NewSessionManager()
	s := newTestStore(t)
	sm.SetStore(s)

	key := "sess:persist"
	// Persist basic level via the session manager.
	sm.GetOrCreate(key)
	if err := sm.SetVerboseLevel(key, "basic"); err != nil {
		t.Fatalf("SetVerboseLevel: %v", err)
	}

	// New VerboseManager over the same store should load the persisted level.
	vm2 := NewVerboseManager(sm)
	if got := vm2.GetLevel(key); got != VerboseBasic {
		t.Errorf("GetLevel(persisted) = %v, want basic", got)
	}
}

func TestVerboseManager_SetLevel_Persists(t *testing.T) {
	vm, sm := withPersistentVerboseManager(t)

	key := "sess:persist-set"
	sm.GetOrCreate(key)
	vm.SetLevel(key, VerboseFull)

	// Reload level directly from session manager.
	if got := sm.GetVerboseLevel(key); got != "full" {
		t.Errorf("persisted level = %q, want full", got)
	}
}

func TestVerboseManager_InitializeFromSession_WithPreference(t *testing.T) {
	vm, sm := withPersistentVerboseManager(t)

	key := "sess:init-has"
	sm.GetOrCreate(key)
	if err := sm.SetVerboseLevel(key, "full"); err != nil {
		t.Fatalf("SetVerboseLevel: %v", err)
	}

	// Ensure cache empty, then initialize from session.
	vm.InitializeFromSession(key)
	if got := vm.GetLevel(key); got != VerboseFull {
		t.Errorf("InitializeFromSession level = %v, want full", got)
	}
}

func TestVerboseManager_InitializeFromSession_NoPreferenceClearsCache(t *testing.T) {
	vm := NewVerboseManager()

	key := "sess:init-none"
	// Populate cache with a value.
	vm.SetLevel(key, VerboseFull)

	// InitializeFromSession with no persisted preference should clear the cache.
	vm.InitializeFromSession(key)
	if got := vm.GetLevel(key); got != VerboseOff {
		t.Errorf("InitializeFromSession level after clear = %v, want off", got)
	}
}

func TestVerboseManager_GetLevel_PreferenceBeforeResolver(t *testing.T) {
	sm := NewSessionManager()
	s := newTestStore(t)
	sm.SetStore(s)
	vm := NewVerboseManager(sm)

	key := "sess:pref-resolver"
	sm.GetOrCreate(key)
	if err := sm.SetVerboseLevel(key, "full"); err != nil {
		t.Fatalf("SetVerboseLevel: %v", err)
	}

	// Resolver would say off, but persistence should win.
	resolver := func(sessionKey string) (VerboseLevel, bool) {
		return VerboseOff, true
	}
	vm.SetDefaultLevelResolver(resolver)

	if got := vm.GetLevel(key); got != VerboseFull {
		t.Errorf("GetLevel with persisted preference = %v, want full (persistence should win)", got)
	}
}

func TestVerboseManager_IsVerbose_WithPersistence(t *testing.T) {
	vm, sm := withPersistentVerboseManager(t)
	key := "sess:isv"
	sm.GetOrCreate(key)
	vm.SetLevel(key, VerboseBasic)
	if !vm.IsVerbose(key) {
		t.Error("expected IsVerbose true after setting basic")
	}
}