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

func TestGroupHydration_NewAgentLoopLoadsPersistedGroups(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "group-hydrate-loop-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	t.Setenv("LELE_CONFIG_DIR", tmpDir)
	t.Cleanup(func() { group.UseStore(nil) })

	const groupID = "hydrate-agent-group"
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
