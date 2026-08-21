package group

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ===========================================================================
// sortGroupsByUpdatedAt
// ===========================================================================

func TestSortGroupsByUpdatedAt_TieBreaksByIDAscending(t *testing.T) {
	now := time.Now()
	s1 := &GroupState{ID: "b", CreatedAt: now, UpdatedAt: now}
	s2 := &GroupState{ID: "a", CreatedAt: now, UpdatedAt: now} // same UpdatedAt, ID "a" sorts first
	s3 := &GroupState{ID: "z", CreatedAt: now, UpdatedAt: now.Add(2 * time.Hour)}

	states := []*GroupState{s1, s3, s2}
	sortGroupsByUpdatedAt(states)
	// z (newest), then tie between a and b → a first.
	if states[0].ID != "z" {
		t.Errorf("first = %q, want z", states[0].ID)
	}
	if states[1].ID != "a" {
		t.Errorf("second = %q, want a", states[1].ID)
	}
	if states[2].ID != "b" {
		t.Errorf("third = %q, want b", states[2].ID)
	}
}

// ===========================================================================
// ListGroups / listGroupsLegacy edge cases
// ===========================================================================

func TestListGroups_SkipsCorruptAndDirectories(t *testing.T) {
	UseStore(nil)
	t.Cleanup(func() { UseStore(nil) })

	dir := t.TempDir()

	good := &GroupState{ID: "good", Task: "x", Status: StatusDone, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := saveGroupLegacy(dir, good); err != nil {
		t.Fatalf("saveGroupLegacy: %v", err)
	}

	// Corrupt JSON file.
	if err := os.WriteFile(filepath.Join(dir, "corrupt.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile corrupt: %v", err)
	}

	// A subdirectory (should be skipped).
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// A non-json file (should be skipped).
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("WriteFile txt: %v", err)
	}

	list, err := ListGroups(dir)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListGroups returned %d, want 1 (only the good file)", len(list))
	}
	if list[0].ID != "good" {
		t.Errorf("list[0].ID = %q, want good", list[0].ID)
	}
}

func TestListGroups_ReadDirError(t *testing.T) {
	UseStore(nil)
	t.Cleanup(func() { UseStore(nil) })

	// A path whose parent component is a file → ReadDir fails with non-NotExist.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "afile")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// dir is a file, so os.ReadDir(filePath) errors (ENOTDIR).
	_, err := ListGroups(filePath)
	if err == nil {
		t.Fatal("expected error when ReadDir fails")
	}
}

func TestSaveGroup_NilDir(t *testing.T) {
	UseStore(nil)
	t.Cleanup(func() { UseStore(nil) })

	state := &GroupState{ID: "g", Status: StatusDone}
	err := SaveGroup("", state)
	if err == nil {
		t.Fatal("expected error for empty dir in legacy backend")
	}
}

func TestLoadGroup_LegacyCorruptFile(t *testing.T) {
	UseStore(nil)
	t.Cleanup(func() { UseStore(nil) })

	dir := t.TempDir()
	// Write a corrupt file that will not unmarshal.
	filename := filepath.Join(dir, sanitizeGroupID("g:corrupt")+".json")
	if err := os.WriteFile(filename, []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := LoadGroup(dir, "g:corrupt")
	if err == nil {
		t.Fatal("expected error loading corrupt JSON")
	}
}

func TestSaveGroup_NilState_PriorityOverDir(t *testing.T) {
	// nil state returns error regardless of backend.
	UseStore(nil)
	t.Cleanup(func() { UseStore(nil) })
	if err := SaveGroup("", nil); err == nil {
		t.Fatal("expected error for nil state")
	}
}

// ===========================================================================
// saveGroupLegacy / loadGroupLegacy direct edge cases
// ===========================================================================

func TestSaveGroupLegacy_EmptyDir(t *testing.T) {
	if err := saveGroupLegacy("", &GroupState{ID: "x"}); err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestLoadGroupLegacy_NilDir(t *testing.T) {
	if _, err := loadGroupLegacy("", "x"); err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestLoadGroupLegacy_MissingFileTiesToErrNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := loadGroupLegacy(dir, "no-such-id")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// ===========================================================================
// SaveGroup / LoadGroup / ListGroups with SQLite backend error paths
// ===========================================================================

func TestListGroups_SQLiteSkipsCorruptEntries(t *testing.T) {
	s := openSQLiteStore(t)
	useTestGroupRepo(t, s.Groups())

	// Insert a valid and a corrupt entry directly into the repo.
	good := &GroupState{ID: "good", Task: "x", Status: StatusDone,
		CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := SaveGroup("ignored", good); err != nil {
		t.Fatalf("SaveGroup good: %v", err)
	}
	if err := s.Groups().SetGroupState("corrupt", "{not json"); err != nil {
		t.Fatalf("SetGroupState corrupt: %v", err)
	}

	list, err := ListGroups("")
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListGroups returned %d, want 1", len(list))
	}
	if list[0].ID != "good" {
		t.Errorf("list[0].ID = %q, want good", list[0].ID)
	}
}

func TestListGroups_EmptySQLiteWithEmptyDir(t *testing.T) {
	s := openSQLiteStore(t)
	useTestGroupRepo(t, s.Groups())

	list, err := ListGroups("")
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListGroups returned %d, want 0", len(list))
	}
}

// ===========================================================================
// MoAAggregator edge cases
// ===========================================================================

func TestMoAAggregator_EmptyParticipants(t *testing.T) {
	if agg := MoAAggregator(&GroupState{}); agg != "" {
		t.Errorf("MoAAggregator() = %q, want empty", agg)
	}
}

func TestMoAStrategy_NoAggregatorTurnFallbackDone(t *testing.T) {
	// When all proposers have spoken but aggregator never gets a turn in the
	// current layer (layer advanced unexpectedly), Next returns done.
	state := &GroupState{
		ID:       "moa-fb",
		Strategy: "moa",
		Rounds:   1,
		Participants: []Participant{
			{AgentID: "P1", Role: RoleProposer},
			{AgentID: "P2", Role: RoleProposer},
			{AgentID: "AGG", Role: RoleAggregator}, // distinct aggregator
		},
		// Layer 0 proposers already spoken; no aggregator turn.
		Transcript: []Turn{
			{Speaker: "P1", Layer: 0, Content: "x"},
			{Speaker: "P2", Layer: 0, Content: "y"},
		},
	}
	s := &MoAStrategy{}
	speakers, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if done {
		t.Fatal("should not be done; should ask aggregator to speak")
	}
	// MoAAggregator = AGG (has RoleAggregator).
	if len(speakers) != 1 || speakers[0] != "AGG" {
		t.Errorf("speakers = %v, want [AGG]", speakers)
	}
}