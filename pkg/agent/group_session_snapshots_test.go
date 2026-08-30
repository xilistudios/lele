package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/group"
)

// ---------------------------------------------------------------------------
// AgentLoop.GroupSnapshotsForSession — the thin exposure of
// GroupManager.SnapshotsForSession for the channels layer (#239, read path).
//
// Two things must hold at this boundary:
//   - the loop injects a session-alias resolver into the group manager, so a
//     group whose origin is a base session key is still reported once
//     startFreshConversation has rotated that base to a new conversation key.
//     Without the wiring the WebUI loses the card on a session switch even
//     though the history payload asks for the right session;
//   - GroupSnapshotsForSession never panics on a loop without a group manager
//     (the same nil-guard AllGroupSnapshots uses), because channels call it on
//     every welcome/reconnected/history request.
// ---------------------------------------------------------------------------

// TestGroupSnapshotsForSession_ResolvesSessionAliases pins the resolver wiring
// through a real AgentLoop: the group is persisted under the base key, the loop
// rotates the conversation, and the group must still be reported for the new
// key — which only happens if the manager was given AgentLoop.ResolveSessionKey.
func TestGroupSnapshotsForSession_ResolvesSessionAliases(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "group-session-snap-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	// The loop wires the SQLite group repo package-globally (group.UseStore);
	// reset it so this test leaves no store pointing at the temp dir.
	t.Cleanup(func() { group.UseStore(nil) })

	const groupID = "session-snap-group" // no ':' or '/' → the legacy file name is the ID
	state := &group.GroupState{
		ID:            groupID,
		ProfileID:     "p1",
		Task:          "aliased session",
		Participants:  []group.Participant{{AgentID: "a", Role: group.RoleProposer}},
		Strategy:      "round_robin",
		Status:        group.StatusDone,
		CreatedAt:     time.Now().Add(-time.Minute),
		UpdatedAt:     time.Now().Add(-time.Minute),
		Transcript:    []group.Turn{{Index: 0, Speaker: "a", Content: "the answer"}},
		TotalTokens:   3,
		Rounds:        1,
		MaxTurns:      1,
		OriginChannel: "web",
		OriginChatID:  "agent:main:web:chat-alias",
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	groupsDir := filepath.Join(tmpDir, "groups")
	if err := os.MkdirAll(groupsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(groupsDir, groupID+".json"), data, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	if al == nil {
		t.Fatal("NewAgentLoop returned nil")
	}
	gm := al.GroupManager()
	if gm == nil {
		t.Fatal("GroupManager should be initialized after NewAgentLoop")
	}

	const base = "agent:main:web:chat-alias"
	fresh := al.startFreshConversation(base, "", "")
	if fresh == base || fresh == "" {
		t.Fatalf("precondition: startFreshConversation did not rotate %q (got %q)", base, fresh)
	}
	if got := al.ResolveSessionKey(base); got != fresh {
		t.Fatalf("ResolveSessionKey(%q) = %q, want %q", base, got, fresh)
	}

	// The rotated key is what the client now subscribes with: the group must be
	// reachable there through the alias resolver.
	if ids := groupSnapshotIDs(al.GroupSnapshotsForSession(fresh)); !containsID(ids, groupID) {
		t.Errorf("group %s not reported for the rotated session key %q (got %v): the loop did not wire the session-alias resolver into GroupManager", groupID, fresh, ids)
	}
	// The original key keeps working by exact match.
	if ids := groupSnapshotIDs(al.GroupSnapshotsForSession(base)); !containsID(ids, groupID) {
		t.Errorf("group %s not reported for its own origin key %q (got %v)", groupID, base, ids)
	}
	// An unrelated session must see nothing.
	if ids := groupSnapshotIDs(al.GroupSnapshotsForSession("agent:main:web:someone-else")); len(ids) != 0 {
		t.Errorf("unrelated session got %v, want none", ids)
	}
}

// TestGroupSnapshotsForSession_NilManagerIsSafe pins the defensive guard that
// keeps the channels from panicking on a partially built loop.
func TestGroupSnapshotsForSession_NilManagerIsSafe(t *testing.T) {
	al := &AgentLoop{}
	if got := al.GroupSnapshotsForSession("anything"); got != nil {
		t.Errorf("GroupSnapshotsForSession on a manager-less loop = %v, want nil", got)
	}
}

func groupSnapshotIDs(snaps []group.GroupSnapshot) []string {
	ids := make([]string, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.GroupID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
