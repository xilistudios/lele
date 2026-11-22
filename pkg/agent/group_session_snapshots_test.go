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

func TestGroupSnapshotsForSession_ResolvesSessionAliases(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "group-session-snap-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	t.Cleanup(func() { group.UseStore(nil) })

	const groupID = "session-snap-group"
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

	if ids := groupSnapshotIDs(al.GroupSnapshotsForSession(fresh)); !containsID(ids, groupID) {
		t.Errorf("group %s not reported for the rotated session key %q (got %v): the loop did not wire the session-alias resolver into GroupManager", groupID, fresh, ids)
	}
	if ids := groupSnapshotIDs(al.GroupSnapshotsForSession(base)); !containsID(ids, groupID) {
		t.Errorf("group %s not reported for its own origin key %q (got %v)", groupID, base, ids)
	}
	if ids := groupSnapshotIDs(al.GroupSnapshotsForSession("agent:main:web:someone-else")); len(ids) != 0 {
		t.Errorf("unrelated session got %v, want none", ids)
	}
}

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
