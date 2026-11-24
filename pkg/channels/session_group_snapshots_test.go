package channels

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
)

func sessionSnap(id, originChat, status string) group.GroupSnapshot {
	return group.GroupSnapshot{
		GroupID:       id,
		Status:        status,
		Strategy:      "round_robin",
		OriginChannel: "web",
		OriginChatID:  originChat,
	}
}

func TestSessionGroupSnapshots_DelegatesToAgentLoop(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	want := []group.GroupSnapshot{
		sessionSnap("group:alpha", "chat-1", group.StatusDone),
		sessionSnap("group:beta", "chat-old", group.StatusDone),
	}
	loop.setGroupSnapshotsForSession("chat-1", want...)

	native := &NativeChannel{agentLoop: loop}

	got := native.sessionGroupSnapshots("chat-1")
	if len(got) != 2 {
		t.Fatalf("sessionGroupSnapshots returned %d snapshots, want the 2 the loop provided", len(got))
	}
	if got[0].GroupID != "group:alpha" || got[1].GroupID != "group:beta" {
		t.Errorf("snapshots were altered in transit: %+v", got)
	}
	if loop.groupSnapshotCallCount() != 1 {
		t.Errorf("loop calls = %d, want 1 — the channel is not delegating", loop.groupSnapshotCallCount())
	}
	if loop.allGroupSnapshotsCallCount() != 0 {
		t.Errorf("AllGroupSnapshots called %d times, want 0 (memory-only path must not be used)", loop.allGroupSnapshotsCallCount())
	}
}

func TestSessionGroupSnapshots_NilAgentLoopIsSafe(t *testing.T) {
	native := &NativeChannel{}
	if got := native.sessionGroupSnapshots("chat-1"); got != nil {
		t.Errorf("sessionGroupSnapshots with no agent loop = %v, want nil", got)
	}
}

func TestSessionGroupSnapshots_BlankSessionGetsNothing(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	loop.setGroupSnapshotsForSession("chat-1", sessionSnap("group:alpha", "chat-1", group.StatusDone))

	native := &NativeChannel{agentLoop: loop}
	if got := native.sessionGroupSnapshots(""); len(got) != 0 {
		t.Errorf("empty sessionKey returned %v, want nothing", got)
	}
}

func TestChatHistoryCarriesSessionGroups(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "agent:main:web:chat-h"
	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)
	ts.loop.setGroupSnapshotsForSession(sessionKey,
		sessionSnap("group:persisted", sessionKey, group.StatusDone))

	req, err := http.NewRequest(http.MethodGet,
		ts.server.URL+"/api/v1/chat/sessions/"+sessionKey+"/history", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var payload ChatHistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(payload.Groups) != 1 || payload.Groups[0].GroupID != "group:persisted" {
		t.Errorf("history groups = %+v, want [group:persisted]", payload.Groups)
	}
	if ts.loop.groupSnapshotCallCount() != 1 {
		t.Errorf("loop calls = %d, want 1", ts.loop.groupSnapshotCallCount())
	}
}
