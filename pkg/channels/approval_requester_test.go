package channels

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApproval_RequesterMatchAccepted(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous", 555, "111|alice")

	handled, err := am.HandleApprovalForRequester(approval.ID, true, "telegram:555", "111|alice")
	if err != nil {
		t.Fatalf("requester resolution failed: %v", err)
	}
	if handled.ID != approval.ID {
		t.Fatalf("handled approval ID = %q, want %q", handled.ID, approval.ID)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be removed after the requester resolves it")
	}
	select {
	case got := <-approval.responseChan:
		if !got {
			t.Fatal("expected approved=true on the response channel")
		}
	case <-time.After(time.Second):
		t.Fatal("response channel was not signaled")
	}
}

func TestApproval_OtherUserRejected(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous", 555, "111|alice")

	_, err := am.HandleApprovalForRequester(approval.ID, true, "telegram:555", "222|bob")
	if err == nil {
		t.Fatal("a different member of the same chat must not approve the command")
	}
	if !errors.Is(err, ErrApprovalRequesterMismatch) {
		t.Fatalf("error = %v, want ErrApprovalRequesterMismatch", err)
	}
	if !strings.Contains(err.Error(), "approval requester mismatch") {
		t.Fatalf("error text = %q, want it to mention 'approval requester mismatch'", err.Error())
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must stay pending after a rejected foreign resolution")
	}

	if _, err := am.HandleApprovalForRequester(approval.ID, false, "telegram:555", "222|bob"); err == nil {
		t.Fatal("a different member must not reject the command either")
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must stay pending after a rejected foreign rejection")
	}

	if _, err := am.HandleApprovalForRequester(approval.ID, true, "telegram:555", "111|alice"); err != nil {
		t.Fatalf("the requester must still be able to approve: %v", err)
	}
}

func TestApproval_UsernameChangeStillMatches(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous", 555, "111|alice")

	if _, err := am.HandleApprovalForRequester(approval.ID, true, "telegram:555", "111|alice_renamed"); err != nil {
		t.Fatalf("renaming the telegram username must not lock the requester out: %v", err)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be removed after resolution")
	}
}

func TestApproval_BareIDRequesterMatchesUsernameCaller(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "make test", "build", 555, "111")

	if _, err := am.HandleApprovalForRequester(approval.ID, true, "telegram:555", "111|alice"); err != nil {
		t.Fatalf("bare numeric requester must match the same user id: %v", err)
	}
}

func TestApproval_EmptyRequesterFallsBackToSession(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("native:session-1", "make test", "build", 0, "")

	if _, err := am.HandleApprovalForRequester(approval.ID, true, "native:session-1", ""); err != nil {
		t.Fatalf("surfaces without a requester id must fall back to the session check: %v", err)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be removed after resolution")
	}

	other := am.CreateApproval("native:session-1", "make test", "build", 0, "")
	_, mismatchErr := am.HandleApprovalForRequester(other.ID, true, "native:session-2", "web")
	if mismatchErr == nil {
		t.Fatal("a foreign session must still be rejected when the requester id is empty")
	}
	if !errors.Is(mismatchErr, ErrApprovalSessionMismatch) {
		t.Fatalf("error = %v, want ErrApprovalSessionMismatch", mismatchErr)
	}
}

func TestApproval_RequesterRecordedWithoutUsernameIsNotAMatchForAnotherUser(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous", 555, "111|alice")

	_, err := am.HandleApprovalForRequester(approval.ID, true, "telegram:555", "")
	if err == nil {
		t.Fatal("an unidentified caller must not approve a requester-scoped command")
	}
	if !errors.Is(err, ErrApprovalRequesterMismatch) {
		t.Fatalf("error = %v, want ErrApprovalRequesterMismatch", err)
	}
}

func TestApprovalSessionMatches_EmptyKeysDeny(t *testing.T) {
	cases := []struct {
		name        string
		approvalKey string
		callerKey   string
	}{
		{name: "empty approval key", approvalKey: "", callerKey: "telegram:555"},
		{name: "empty caller key", approvalKey: "telegram:555", callerKey: ""},
		{name: "both empty", approvalKey: "", callerKey: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if approvalSessionMatches(tc.approvalKey, tc.callerKey) {
				t.Fatalf("approvalSessionMatches(%q, %q) = true, want false (empty keys must deny)", tc.approvalKey, tc.callerKey)
			}
		})
	}

	if !approvalSessionMatches("telegram:555", "telegram:555") {
		t.Fatal("identical session keys must match")
	}
	if !approvalSessionMatches("telegram:555:chat:9", "telegram:555") {
		t.Fatal("conversation alias suffixes must still be tolerated")
	}
	if approvalSessionMatches("telegram:555", "telegram:556") {
		t.Fatal("different chat ids must not match")
	}
}

func TestApproval_EmptySessionKeyDenied(t *testing.T) {
	am := NewApprovalManager()
	unscoped := am.CreateApproval("", "make test", "build", 0, "")

	_, err := am.HandleApprovalForSession(unscoped.ID, true, "telegram:555")
	if err == nil {
		t.Fatal("an approval without a session key must not be resolvable from a chat")
	}
	if !errors.Is(err, ErrApprovalSessionMismatch) {
		t.Fatalf("error = %v, want ErrApprovalSessionMismatch", err)
	}
	if am.GetApproval(unscoped.ID) == nil {
		t.Fatal("approval must stay pending after a denied resolution")
	}

	_, err = am.HandleApprovalForSession(unscoped.ID, true, "")
	if err == nil {
		t.Fatal("an empty caller session key must not resolve an approval")
	}
	if am.GetApproval(unscoped.ID) == nil {
		t.Fatal("approval must stay pending after an empty-key resolution attempt")
	}
}

func TestCreateApproval_RecordsRequester(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "rm -rf /tmp/x", "dangerous", 555, "111|alice")

	if approval.RequesterID != "111|alice" {
		t.Fatalf("approval.RequesterID = %q, want %q", approval.RequesterID, "111|alice")
	}
	stored := am.GetApproval(approval.ID)
	if stored == nil {
		t.Fatal("approval was not stored")
	}
	if stored.RequesterID != "111|alice" {
		t.Fatalf("stored RequesterID = %q, want %q", stored.RequesterID, "111|alice")
	}
}

func TestApproval_UnscopedResolutionStillWorks(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("telegram:555", "echo hi", "trivial", 555, "111|alice")

	handled, err := am.HandleApproval(approval.ID, false)
	if err != nil {
		t.Fatalf("in-process resolution failed: %v", err)
	}
	if handled.ID != approval.ID {
		t.Fatalf("unexpected handled approval: %+v", handled)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be removed after in-process resolution")
	}
}

func TestApproval_NotFoundIsSentinel(t *testing.T) {
	am := NewApprovalManager()
	_, err := am.HandleApprovalForRequester("missing", true, "telegram:555", "111|alice")
	if !errors.Is(err, ErrApprovalNotFound) {
		t.Fatalf("error = %v, want ErrApprovalNotFound", err)
	}
}

func TestRequesterIDPart(t *testing.T) {
	cases := map[string]string{
		"111|alice": "111",
		"111":       "111",
		"":          "",
		"|alice":    "",
	}
	for input, want := range cases {
		if got := requesterIDPart(input); got != want {
			t.Errorf("requesterIDPart(%q) = %q, want %q", input, got, want)
		}
	}
}
