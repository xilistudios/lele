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

// TestGroupHydration_NewAgentLoopLoadsPersistedGroups (B7) pins the startup
// wiring: NewAgentLoop must rehydrate the groups found under
// <LeleDir>/groups so "/group list" and the WebSocket welcome payload still
// show them after a restart, the way chat sessions already do.
//
// The state is written as a legacy JSON file with a non-terminal status — the
// shape left behind when a process dies mid-run — so the test also pins the
// re-marking rule through the real loop: the group comes back as error, never
// as a permanently "running" entry nobody can wait on.
//
// Writing the JSON file (instead of opening the SQLite store directly) keeps
// the test valid on both backends: when the loop manages to open its store,
// ListGroups migrates the legacy file; where SQLite is unavailable the file is
// simply the store.
func TestGroupHydration_NewAgentLoopLoadsPersistedGroups(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "group-hydrate-loop-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	// The loop wires the SQLite group repository package-globally (see
	// group.UseStore); reset it so this test leaves no store pointing at the
	// temporary directory for its successors.
	t.Cleanup(func() { group.UseStore(nil) })

	const groupID = "hydrate-agent-group" // no ':' or '/' → file name is the ID
	state := &group.GroupState{
		ID:           groupID,
		ProfileID:    "p1",
		Task:         "interrupted by restart",
		Participants: []group.Participant{{AgentID: "a", Role: group.RoleProposer}},
		Strategy:     "round_robin",
		Status:       group.StatusRunning,
		Rounds:       1,
		MaxTurns:     4,
		CreatedAt:    time.Now().Add(-time.Minute),
		UpdatedAt:    time.Now().Add(-time.Minute),
		Transcript:   []group.Turn{{Index: 0, Speaker: "a", Content: "half a thought"}},
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

	// Hydration is best-effort: a store on disk must never break the loop.
	al := NewAgentLoop(cfg, bus.NewMessageBus())
	if al == nil {
		t.Fatal("NewAgentLoop returned nil")
	}
	gm := al.GroupManager()
	if gm == nil {
		t.Fatal("GroupManager should be initialized after NewAgentLoop")
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("persisted group was not hydrated by NewAgentLoop")
	}
	if st.Status != group.StatusError {
		t.Errorf("hydrated status = %q, want %q (non-terminal on disk → error)", st.Status, group.StatusError)
	}

	// The welcome snapshot path must see it, and Wait must not hang.
	found := false
	for _, snap := range al.AllGroupSnapshots() {
		if snap.GroupID == groupID {
			found = true
			break
		}
	}
	if !found {
		t.Error("hydrated group missing from AllGroupSnapshots (WS welcome payload)")
	}
	if _, err := gm.Wait(groupID); err == nil {
		t.Error("Wait on restart-orphaned group returned nil error")
	}
}
