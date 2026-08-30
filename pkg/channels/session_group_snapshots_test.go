package channels

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
)

// ---------------------------------------------------------------------------
// sessionGroupSnapshots rewiring (#239, read path).
//
// The channel used to filter AllGroupSnapshots() — memory only — so anything
// the retention sweep had evicted, or any group from before a daemon restart,
// was invisible to the welcome/reconnected/history payloads even though its
// transcript was still persisted. It now delegates the whole lookup to the
// agent loop, which unions memory with the store and resolves session aliases.
//
// These tests pin the channel side of that contract: it must call the session
// API with the caller's session key and forward the result untouched.
// ---------------------------------------------------------------------------

func sessionSnap(id, originChat, status string) group.GroupSnapshot {
	return group.GroupSnapshot{
		GroupID:       id,
		Status:        status,
		Strategy:      "round_robin",
		OriginChannel: "web",
		OriginChatID:  originChat,
	}
}

// TestSessionGroupSnapshots_DelegatesToAgentLoop pins that the channel asks the
// loop for the session's snapshots instead of filtering memory itself, and
// forwards exactly what it got.
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
	// The channel must never reach for the memory-only API any more.
	if loop.allGroupSnapshotsCallCount() != 0 {
		t.Errorf("AllGroupSnapshots called %d times, want 0 (memory-only path must not be used)", loop.allGroupSnapshotsCallCount())
	}
}

// TestSessionGroupSnapshots_NilAgentLoopIsSafe pins the guard: a channel built
// without a loop answers with nothing instead of panicking on the WS handshake.
func TestSessionGroupSnapshots_NilAgentLoopIsSafe(t *testing.T) {
	native := &NativeChannel{}
	if got := native.sessionGroupSnapshots("chat-1"); got != nil {
		t.Errorf("sessionGroupSnapshots with no agent loop = %v, want nil", got)
	}
}

// TestSessionGroupSnapshots_BlankSessionGetsNothing documents the contract for a
// blank key: the loop decides, the channel does not widen it.
func TestSessionGroupSnapshots_BlankSessionGetsNothing(t *testing.T) {
	loop := newNativeTestAgentLoop(config.DefaultConfig())
	loop.setGroupSnapshotsForSession("chat-1", sessionSnap("group:alpha", "chat-1", group.StatusDone))

	native := &NativeChannel{agentLoop: loop}
	if got := native.sessionGroupSnapshots(""); len(got) != 0 {
		t.Errorf("empty sessionKey returned %v, want nothing", got)
	}
}

// TestChatHistoryCarriesSessionGroups drives the real HTTP route to prove
// store-backed groups reach the /history payload the WebUI reads on a session
// switch.
func TestChatHistoryCarriesSessionGroups(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "agent:main:web:chat-h"
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
