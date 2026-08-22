// Lele - coverage tests for session cancel registration and cancellation.
package agent

import (
	"context"
	"testing"
)

// TestRegisterSessionCancel_Flow covers the happy path: registering a cancel
// func, invoking the returned cleanup (calls cancel + removes group), and then
// cancelling the session (no-op).
func TestRegisterSessionCancel_Flow(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)

	cancelled := false
	cleanup := sm.RegisterSessionCancel("sess:1", func() { cancelled = true })
	cleanup()

	if !cancelled {
		t.Error("expected cancel func to be invoked by cleanup")
	}
	if sm.IsSessionProcessing("sess:1") {
		t.Error("expected session to be removed from processing after cleanup")
	}
}

// TestRegisterSessionCancel_EmptyKey covers the early-return when sessionKey is
// empty or cancel is nil.
func TestRegisterSessionCancel_EmptyKey(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)
	if fn := sm.RegisterSessionCancel("", func() {}); fn == nil {
		t.Fatal("expected non-nil no-op cleanup")
	} else {
		fn()
	}
	if fn := sm.RegisterSessionCancel("sess:x", nil); fn == nil {
		t.Fatal("expected non-nil no-op cleanup for nil cancel")
	} else {
		fn()
	}
}

// TestCancelSession_Group covers CancelSession with a registered cancel group.
func TestCancelSession_Group(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)

	count := 0
	sm.RegisterSessionCancel("sess:grp", func() { count++ })
	sm.RegisterSessionCancel("sess:grp", func() { count++ })

	stopped := sm.CancelSession("sess:grp")
	if stopped != 2 {
		t.Errorf("stopped = %d, want 2", stopped)
	}
	if count != 2 {
		t.Errorf("cancel funcs invoked = %d, want 2", count)
	}
	if sm.IsSessionProcessing("sess:grp") {
		t.Error("expected session removed after cancel")
	}
}

// TestCancelSession_EmptyAndMissing covers CancelSession with empty key and
// with no registered cancel group.
func TestCancelSession_EmptyAndMissing(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)
	if got := sm.CancelSession(""); got != 0 {
		t.Errorf("empty key cancel = %d, want 0", got)
	}
	if got := sm.CancelSession("sess:missing"); got != 0 {
		t.Errorf("missing key cancel = %d, want 0", got)
	}
}

// TestCancelSession_SingleFunc covers CancelSession when the stored entry is a
// single context.CancelFunc (the non-group fallback path).
func TestCancelSession_SingleFunc(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)

	called := false
	var cf context.CancelFunc = func() { called = true }
	sm.sessionCancels.Store("sess:single", cf)

	if got := sm.CancelSession("sess:single"); got != 1 {
		t.Errorf("stopped = %d, want 1", got)
	}
	if !called {
		t.Error("expected stored cancel func to be called")
	}
	if sm.IsSessionProcessing("sess:single") {
		t.Error("expected session removed after single-func cancel")
	}
}

// TestCancelSession_UnknownType covers the default branch of CancelSession when
// the stored entry is neither a group nor a CancelFunc.
func TestCancelSession_UnknownType(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)
	sm.sessionCancels.Store("sess:unknown", "not-a-cancel")
	if got := sm.CancelSession("sess:unknown"); got != 0 {
		t.Errorf("unknown-type cancel = %d, want 0", got)
	}
	if sm.IsSessionProcessing("sess:unknown") {
		t.Error("expected unknown entry removed")
	}
}
