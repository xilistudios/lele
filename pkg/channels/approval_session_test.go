package channels

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TUI-H2 regression tests: approvals are a capability that must be resolvable
// only by the session that originated them. Network entry points (WebSocket,
// REST, Telegram) MUST call HandleApprovalForSession with a non-empty caller
// session key.

// TestHandleApprovalForSessionRejectsCrossSession verifies that a caller from a
// different session cannot resolve (approve or reject) an approval it did not
// create, and that the approval remains pending for its legitimate owner.
func TestHandleApprovalForSessionRejectsCrossSession(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:111", "rm -rf /", "dangerous command", 111)

	if approval == nil {
		t.Fatal("expected approval to be created")
	}

	// Caller from another session (telegram:222) must be rejected.
	_, err := am.HandleApprovalForSession(approval.ID, true, "telegram:222")
	if err == nil {
		t.Fatal("expected error for cross-session approval resolution, got nil")
	}
	if !strings.Contains(err.Error(), "approval session mismatch") {
		t.Fatalf("expected 'approval session mismatch' error, got: %v", err)
	}

	// The approval must STILL be pending — not consumed by the rejected call.
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must remain pending after a rejected cross-session resolution")
	}

	// Same must hold for rejection attempts.
	if _, err := am.HandleApprovalForSession(approval.ID, false, "telegram:222"); err == nil {
		t.Fatal("expected error for cross-session rejection, got nil")
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must remain pending after a rejected cross-session rejection")
	}

	// The legitimate owner can still resolve it.
	handled, err := am.HandleApprovalForSession(approval.ID, true, "telegram:111")
	if err != nil {
		t.Fatalf("owner resolution failed: %v", err)
	}
	if handled.ID != approval.ID {
		t.Fatalf("handled approval ID mismatch: got %q, want %q", handled.ID, approval.ID)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be removed after the owner resolves it")
	}
}

// TestHandleApprovalForSessionAllowsSameSession verifies that a caller with the
// matching session key can resolve the approval normally.
func TestHandleApprovalForSessionAllowsSameSession(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:777", "make test", "build verification", 777)

	handled, err := am.HandleApprovalForSession(approval.ID, true, "telegram:777")
	if err != nil {
		t.Fatalf("same-session resolution failed: %v", err)
	}
	if handled.ID != approval.ID || handled.Command != "make test" {
		t.Fatalf("unexpected handled approval: %+v", handled)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be removed after same-session resolution")
	}

	// The response must reach the waiter (approved=true).
	select {
	case got := <-approval.responseChan:
		if !got {
			t.Fatal("expected approved=true on response channel")
		}
	case <-time.After(time.Second):
		t.Fatal("response channel was not signaled")
	}
}

// TestHandleApprovalUnscopedStillWorks verifies the legacy unscoped entry
// point: trusted local callers (TUI) and tests pass no session key and must
// keep working with unchanged semantics.
func TestHandleApprovalUnscopedStillWorks(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "echo hi", "trivial", 555)

	// Empty caller key (legacy path) resolves regardless of approval session.
	handled, err := am.HandleApproval(approval.ID, false)
	if err != nil {
		t.Fatalf("unscoped resolution failed: %v", err)
	}
	if handled.ID != approval.ID {
		t.Fatalf("unexpected handled approval: %+v", handled)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be removed after unscoped resolution")
	}
	select {
	case got := <-approval.responseChan:
		if got {
			t.Fatal("expected approved=false on response channel")
		}
	case <-time.After(time.Second):
		t.Fatal("response channel was not signaled")
	}

	// HandleApprovalForSession with an empty caller key is the same legacy path.
	a2 := am.CreateApproval("", "ls -la", "listing", 0)
	if _, err := am.HandleApprovalForSession(a2.ID, true, ""); err != nil {
		t.Fatalf("empty caller key must be allowed (legacy), got: %v", err)
	}
	if am.GetApproval(a2.ID) != nil {
		t.Fatal("approval must be removed after legacy resolution")
	}
}

// TestHandleApprovalForSessionExpiredNotFound verifies the not-found path is
// preserved (same semantics as HandleApproval for unknown IDs).
func TestHandleApprovalForSessionExpiredNotFound(t *testing.T) {
	am := NewApprovalManager()
	if _, err := am.HandleApprovalForSession("nonexistent-id", true, "telegram:111"); err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}

// TestRESTApprove_CrossSessionForbidden is a REST-level regression test: an
// authenticated client posting an approval ID that belongs to a DIFFERENT
// session must get 403 approval_forbidden, and the approval must stay pending.
func TestRESTApprove_CrossSessionForbidden(t *testing.T) {
	ts := newNativeTestServer(t)

	am := NewApprovalManager()
	ts.channel.approvalManager = am
	ownerSession := "test-session-" + ts.clientID
	approval := am.CreateApproval(ownerSession, "rm -rf /tmp/x", "dangerous", 0)

	// Authenticated caller targets a DIFFERENT session path with an ID it
	// learned second-hand.
	otherSession := "test-session-other"
	body := mustMarshal(ApproveRequest{RequestID: approval.ID, Approved: true})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(otherSession)+"/approve", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (approval_forbidden)", resp.StatusCode, http.StatusForbidden)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must remain pending after cross-session REST attempt")
	}
}

// TestRESTApprove_SameSessionAllowed verifies the happy path end-to-end: the
// owner session resolving its own approval gets 200 and the approval is
// consumed.
func TestRESTApprove_SameSessionAllowed(t *testing.T) {
	ts := newNativeTestServer(t)

	am := NewApprovalManager()
	ts.channel.approvalManager = am
	sessionKey := "test-session-" + ts.clientID
	approval := am.CreateApproval(sessionKey, "echo hi", "trivial", 0)

	body := mustMarshal(ApproveRequest{RequestID: approval.ID, Approved: true})
	req, _ := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/approve", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be consumed after same-session REST approval")
	}
}
