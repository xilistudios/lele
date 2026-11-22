package channels

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHandleApprovalForSessionRejectsCrossSession(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:111", "rm -rf /", "dangerous command", 111)

	if approval == nil {
		t.Fatal("expected approval to be created")
	}

	_, err := am.HandleApprovalForSession(approval.ID, true, "telegram:222")
	if err == nil {
		t.Fatal("expected error for cross-session approval resolution, got nil")
	}
	if !strings.Contains(err.Error(), "approval session mismatch") {
		t.Fatalf("expected 'approval session mismatch' error, got: %v", err)
	}

	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must remain pending after a rejected cross-session resolution")
	}

	if _, err := am.HandleApprovalForSession(approval.ID, false, "telegram:222"); err == nil {
		t.Fatal("expected error for cross-session rejection, got nil")
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must remain pending after a rejected cross-session rejection")
	}

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

	select {
	case got := <-approval.responseChan:
		if !got {
			t.Fatal("expected approved=true on response channel")
		}
	case <-time.After(time.Second):
		t.Fatal("response channel was not signaled")
	}
}

func TestHandleApprovalUnscopedStillWorks(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "echo hi", "trivial", 555)

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

	a2 := am.CreateApproval("", "ls -la", "listing", 0)
	if _, err := am.HandleApprovalForSession(a2.ID, true, ""); err != nil {
		t.Fatalf("empty caller key must be allowed (legacy), got: %v", err)
	}
	if am.GetApproval(a2.ID) != nil {
		t.Fatal("approval must be removed after legacy resolution")
	}
}

func TestHandleApprovalForSessionExpiredNotFound(t *testing.T) {
	am := NewApprovalManager()
	if _, err := am.HandleApprovalForSession("nonexistent-id", true, "telegram:111"); err == nil {
		t.Fatal("expected not-found error, got nil")
	}
}

func TestRESTApprove_CrossSessionForbidden(t *testing.T) {
	ts := newNativeTestServer(t)

	am := NewApprovalManager()
	ts.channel.approvalManager = am
	ownerSession := "test-session-" + ts.clientID
	approval := am.CreateApproval(ownerSession, "rm -rf /tmp/x", "dangerous", 0)

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
