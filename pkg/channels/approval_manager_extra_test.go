package channels

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestApprovalManager_CreateGetHandle covers CreateApproval, GetApproval,
// HandleApproval (approve & reject paths), GetTimeout, SetTimeout and the
// callbacks that fire on handling.
func TestApprovalManager_CreateGetHandle(t *testing.T) {
	am := NewApprovalManager()
	if am.GetTimeout() != 5*time.Minute {
		t.Errorf("default timeout = %v, want 5m", am.GetTimeout())
	}

	am.SetTimeout(10 * time.Minute)
	if am.GetTimeout() != 10*time.Minute {
		t.Errorf("timeout after SetTimeout = %v, want 10m", am.GetTimeout())
	}

	var approvedCalled, rejectedCalled sync.Once
	var approvedCalls, rejectedCalls int

	approval := am.CreateApproval("session-1", "/some command", "because yes", 12345)
	if approval == nil {
		t.Fatal("expected non-nil approval")
	}
	if approval.ID == "" {
		t.Error("expected generated approval ID")
	}
	if approval.SessionKey != "session-1" || approval.Command != "/some command" {
		t.Errorf("unexpected approval fields: %+v", approval)
	}
	if approval.ChatID != 12345 {
		t.Errorf("ChatID = %d", approval.ChatID)
	}

	// Override callbacks to count invocations.
	approval.OnApproved = func() { approvedCalls++ ; approvedCalled.Do(func() {}) }
	approval.OnRejected = func() { rejectedCalls++ ; rejectedCalled.Do(func() {}) }

	// GetApproval should return the same instance.
	got := am.GetApproval(approval.ID)
	if got != approval {
		t.Error("GetApproval did not return the stored approval")
	}

	// GetApproval for unknown ID returns nil.
	if am.GetApproval("nope") != nil {
		t.Error("GetApproval for unknown ID should be nil")
	}

	// Handle approve.
	handled, err := am.HandleApproval(approval.ID, true)
	if err != nil {
		t.Fatalf("HandleApproval(approve) error: %v", err)
	}
	if handled != approval {
		t.Error("HandleApproval should return the handled approval")
	}
	if approvedCalls != 1 {
		t.Errorf("approved callback calls = %d, want 1", approvedCalls)
	}
	if rejectedCalls != 0 {
		t.Errorf("rejected callback calls = %d, want 0", rejectedCalls)
	}

	// Approvals are removed after handling.
	if am.GetApproval(approval.ID) != nil {
		t.Error("handled approval should be removed from pending map")
	}

	// Handling again should fail.
	if _, err := am.HandleApproval(approval.ID, false); err == nil {
		t.Error("expected error handling already-handled approval")
	}
}

func TestApprovalManager_HandleReject(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("s", "cmd", "reason", 1)

	var called bool
	approval.OnRejected = func() { called = true }

	handled, err := am.HandleApproval(approval.ID, false)
	if err != nil {
		t.Fatalf("HandleApproval(reject) error: %v", err)
	}
	if handled != approval {
		t.Error("expected handled approval returned")
	}
	if !called {
		t.Error("OnRejected callback should have been called")
	}
}

// TestApprovalManager_WaitForResponse covers WaitForResponse success and
// default-context (nil ctx) behaviour.
func TestApprovalManager_WaitForResponse(t *testing.T) {
	am := NewApprovalManager()
	am.SetTimeout(time.Hour)
	approval := am.CreateApproval("s", "cmd", "", 1)

	// Receiving the response through HandleApproval.
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = am.HandleApproval(approval.ID, true)
	}()

	approved, err := approval.WaitForResponse(context.Background(), time.Second)
	if err != nil {
		t.Fatalf("WaitForResponse error: %v", err)
	}
	if !approved {
		t.Error("expected approved=true from HandleApproval(true)")
	}
}

func TestApprovalManager_WaitForResponse_NilCtx(t *testing.T) {
	approval := &PendingApproval{responseChan: make(chan bool, 1)}
	approval.responseChan <- true
	// context.Background() is used when nil is passed.
	approved, err := approval.WaitForResponse(nil, time.Second)
	if err != nil {
		t.Fatalf("WaitForResponse(nil) error: %v", err)
	}
	if !approved {
		t.Error("expected approved=true")
	}
}

func TestApprovalManager_WaitForResponse_Timeout(t *testing.T) {
	approval := &PendingApproval{responseChan: make(chan bool, 1)}
	started := time.Now()
	_, err := approval.WaitForResponse(context.Background(), 30*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if time.Since(started) < 20*time.Millisecond {
		t.Error("returned too quickly, did not respect timeout")
	}
}

// TestApprovalManager_cleanupExpired ensures expired approvals are purged
// and notifications fired.
func TestApprovalManager_cleanupExpired(t *testing.T) {
	am := NewApprovalManager()
	am.timeout = 0 // everything is immediately expired

	var rejected bool
	approval := am.CreateApproval("s", "cmd", "r", 1)
	approval.OnRejected = func() { rejected = true }

	// Force a cleanup by creating another approval (CreateApproval calls cleanupExpired).
	_ = am.CreateApproval("s2", "c2", "", 2)

	if am.GetApproval(approval.ID) != nil {
		t.Error("expired approval should have been cleaned up")
	}
	if !rejected {
		t.Error("OnRejected should have been called for expired approval")
	}

	// Direct cleanup call too.
	am.cleanupExpired()
}

// TestApprovalManager_BuildApprovalKeyboard verifies the inline keyboard
// contains approve/reject/view callback data referencing the approval ID.
func TestApprovalManager_BuildApprovalKeyboard(t *testing.T) {
	am := NewApprovalManager()
	kb := am.BuildApprovalKeyboard("id-123")

	if kb == nil || kb.InlineKeyboard == nil {
		t.Fatal("expected non-nil keyboard")
	}
	if len(kb.InlineKeyboard) != 2 {
		t.Fatalf("expected 2 keyboard rows, got %d", len(kb.InlineKeyboard))
	}

	// Row 0: approve + reject.
	row0 := kb.InlineKeyboard[0]
	if len(row0) != 2 {
		t.Fatalf("expected 2 buttons in row 0, got %d", len(row0))
	}
	if row0[0].CallbackData != "approval:approve:id-123" {
		t.Errorf("approve callback = %q", row0[0].CallbackData)
	}
	if row0[1].CallbackData != "approval:reject:id-123" {
		t.Errorf("reject callback = %q", row0[1].CallbackData)
	}

	// Row 1: view command.
	row1 := kb.InlineKeyboard[1]
	if len(row1) != 1 {
		t.Fatalf("expected 1 button in row 1, got %d", len(row1))
	}
	if row1[0].CallbackData != "approval:view:id-123" {
		t.Errorf("view callback = %q", row1[0].CallbackData)
	}
}

// TestApprovalManager_TimeoutAutoReject verifies the background timeout goroutine
// removes the approval and fires OnRejected.
func TestApprovalManager_TimeoutAutoReject(t *testing.T) {
	am := NewApprovalManager()
	am.SetTimeout(50 * time.Millisecond)

	var rejected bool
	approval := am.CreateApproval("s", "cmd", "", 1)
	approval.OnRejected = func() { rejected = true }

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if am.GetApproval(approval.ID) == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if am.GetApproval(approval.ID) != nil {
		t.Error("approval should have been removed by timeout")
	}
	if !rejected {
		t.Error("OnRejected should have been called on timeout")
	}
}