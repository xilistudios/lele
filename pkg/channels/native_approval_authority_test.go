package channels

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func restApprove(t *testing.T, ts *nativeTestServer, sessionKey, approvalID string, approved bool) (int, string) {
	t.Helper()

	body := mustMarshal(ApproveRequest{RequestID: approvalID, Approved: approved})
	req, err := http.NewRequest(http.MethodPost, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey)+"/approve", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(raw)
}

func newNativeApprovalServer(t *testing.T) (*nativeTestServer, *ApprovalManager, string) {
	t.Helper()

	ts := newNativeTestServer(t)
	am := NewApprovalManager()
	ts.channel.approvalManager = am
	sessionKey := "test-session-" + ts.clientID
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)
	return ts, am, sessionKey
}

func TestRESTApprove_RecordedRequesterStillResolves(t *testing.T) {
	ts, am, sessionKey := newNativeApprovalServer(t)
	approval := am.CreateApproval(sessionKey, "rm -rf /tmp/x", "dangerous", 0, ts.clientID)

	status, body := restApprove(t, ts, sessionKey, approval.ID, true)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s, want %d: a native approval must stay resolvable from its own session", status, body, http.StatusOK)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be consumed after the native approval")
	}
}

func TestRESTReject_RecordedRequesterStillResolves(t *testing.T) {
	ts, am, sessionKey := newNativeApprovalServer(t)
	approval := am.CreateApproval(sessionKey, "rm -rf /tmp/x", "dangerous", 0, ts.clientID)

	status, body := restApprove(t, ts, sessionKey, approval.ID, false)
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s, want %d: a native rejection must stay resolvable from its own session", status, body, http.StatusOK)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be consumed after the native rejection")
	}
}

func TestRESTApprove_RecordedRequesterForeignSessionStillForbidden(t *testing.T) {
	ts, am, sessionKey := newNativeApprovalServer(t)
	approval := am.CreateApproval(sessionKey, "rm -rf /tmp/x", "dangerous", 0, ts.clientID)

	status, body := restApprove(t, ts, "test-session-someone-else", approval.ID, true)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want %d", status, body, http.StatusForbidden)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("a foreign-session attempt must not consume the approval")
	}
}

func TestHandleApprovalForSessionIgnoresRecordedRequester(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("agent:main:telegram:group:-100123", "rm -rf /srv", "dangerous", -100123, "111|alice")

	if _, err := am.HandleApprovalForSession(approval.ID, true, "agent:main:telegram:group:-100123"); err != nil {
		t.Fatalf("session-scoped resolution must not check the requester: %v", err)
	}
	if am.GetApproval(approval.ID) != nil {
		t.Fatal("approval must be consumed by the session-scoped resolution")
	}
}

func TestHandleApprovalForRequesterStillChecksRequester(t *testing.T) {
	am := NewApprovalManager()
	approval := am.CreateApproval("agent:main:telegram:group:-100123", "rm -rf /srv", "dangerous", -100123, "111|alice")

	_, err := am.HandleApprovalForRequester(approval.ID, true, "agent:main:telegram:group:-100123", "222|bob")
	if !errors.Is(err, ErrApprovalRequesterMismatch) {
		t.Fatalf("error = %v, want ErrApprovalRequesterMismatch", err)
	}
	if am.GetApproval(approval.ID) == nil {
		t.Fatal("approval must stay pending after a foreign requester attempt")
	}
}

func TestApprovalRequesterMatch_FailsClosedOnEmptyCaller(t *testing.T) {
	cases := []struct {
		name        string
		approval    string
		caller      string
		wantMatched bool
	}{
		{name: "same id", approval: "111|alice", caller: "111|bob", wantMatched: true},
		{name: "empty approval requester", approval: "", caller: "111|alice", wantMatched: false},
		{name: "empty caller", approval: "111|alice", caller: "", wantMatched: false},
		{name: "both empty", approval: "", caller: "", wantMatched: false},
		{name: "different id", approval: "111|alice", caller: "222|alice", wantMatched: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := approvalRequesterMatches(tc.approval, tc.caller); got != tc.wantMatched {
				t.Fatalf("approvalRequesterMatches(%q, %q) = %v, want %v", tc.approval, tc.caller, got, tc.wantMatched)
			}
		})
	}
}

func TestApprovalAuthority_RequesterCheckIsOptIn(t *testing.T) {
	sessionKey := "agent:main:telegram:group:-100123"

	cases := []struct {
		name              string
		authority         approvalAuthority
		approvalRequester string
		wantChecked       bool
	}{
		{
			name:              "session authority never checks the requester",
			authority:         approvalAuthority{sessionKey: sessionKey},
			approvalRequester: "111|alice",
			wantChecked:       false,
		},
		{
			name:              "requester authority checks a recorded requester",
			authority:         approvalAuthority{sessionKey: sessionKey, requesterID: "111|alice", requireRequester: true},
			approvalRequester: "111|alice",
			wantChecked:       true,
		},
		{
			name:              "requester authority falls back to the session when nothing was recorded",
			authority:         approvalAuthority{sessionKey: sessionKey, requesterID: "111|alice", requireRequester: true},
			approvalRequester: "",
			wantChecked:       false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			approval := &PendingApproval{SessionKey: sessionKey, RequesterID: tc.approvalRequester}
			if got := tc.authority.requiresRequesterMatch(approval); got != tc.wantChecked {
				t.Fatalf("requiresRequesterMatch = %v, want %v", got, tc.wantChecked)
			}
		})
	}
}
