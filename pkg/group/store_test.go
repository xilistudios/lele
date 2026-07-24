package group

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveLoad_Roundtrip(t *testing.T) {
	dir := t.TempDir()

	state := &GroupState{
		ID:           "group:test/abc",
		Task:         "solve it",
		Participants: []Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}},
		Strategy:     "round_robin",
		Status:       StatusDone,
		CreatedAt:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2025, 1, 1, 0, 0, 5, 0, time.UTC),
		TotalTokens:  150,
		Transcript: []Turn{
			{Index: 0, Layer: 0, Speaker: "a", Label: "A", Content: "hello", CreatedAt: time.Now(), Tokens: 50},
			{Index: 1, Layer: 0, Speaker: "b", Label: "B", Content: "world", CreatedAt: time.Now(), Tokens: 100},
		},
		Rounds:   1,
		MaxTurns: 2,
	}

	if err := SaveGroup(dir, state); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}

	loaded, err := LoadGroup(dir, state.ID)
	if err != nil {
		t.Fatalf("LoadGroup: %v", err)
	}

	if loaded.ID != state.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, state.ID)
	}
	if loaded.Task != state.Task {
		t.Errorf("Task = %q, want %q", loaded.Task, state.Task)
	}
	if len(loaded.Participants) != len(state.Participants) {
		t.Errorf("Participants len = %d, want %d", len(loaded.Participants), len(state.Participants))
	}
	if len(loaded.Transcript) != len(state.Transcript) {
		t.Errorf("Transcript len = %d, want %d", len(loaded.Transcript), len(state.Transcript))
	}
	if loaded.Status != state.Status {
		t.Errorf("Status = %q, want %q", loaded.Status, state.Status)
	}
	if loaded.TotalTokens != state.TotalTokens {
		t.Errorf("TotalTokens = %d, want %d", loaded.TotalTokens, state.TotalTokens)
	}
	if loaded.Strategy != state.Strategy {
		t.Errorf("Strategy = %q, want %q", loaded.Strategy, state.Strategy)
	}
	if loaded.Rounds != state.Rounds {
		t.Errorf("Rounds = %d, want %d", loaded.Rounds, state.Rounds)
	}
}

func TestLoadGroup_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadGroup(dir, "nonexistent-group")
	if err == nil {
		t.Fatal("expected error for nonexistent group")
	}
	if !strings.Contains(err.Error(), "nonexistent-group") {
		t.Errorf("error should mention group ID: %v", err)
	}
}

func TestLoadGroup_NilDir(t *testing.T) {
	_, err := LoadGroup("", "some-id")
	if err == nil {
		t.Fatal("expected error for empty dir")
	}
}

func TestSaveGroup_NilState(t *testing.T) {
	err := SaveGroup(t.TempDir(), nil)
	if err == nil {
		t.Fatal("expected error for nil state")
	}
}

func TestListGroups_Multiple(t *testing.T) {
	dir := t.TempDir()

	now := time.Now()

	s1 := &GroupState{
		ID:        "group-alpha",
		Task:      "task A",
		Status:    StatusDone,
		CreatedAt: now.Add(-3 * time.Hour),
		UpdatedAt: now.Add(-3 * time.Hour),
	}
	s2 := &GroupState{
		ID:        "group-beta",
		Task:      "task B",
		Status:    StatusRunning,
		CreatedAt: now.Add(-1 * time.Hour),
		UpdatedAt: now.Add(-1 * time.Hour),
	}
	s3 := &GroupState{
		ID:        "group-gamma",
		Task:      "task C",
		Status:    StatusStopped,
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: now.Add(-2 * time.Hour),
	}

	for _, s := range []*GroupState{s1, s2, s3} {
		if err := SaveGroup(dir, s); err != nil {
			t.Fatalf("SaveGroup(%s): %v", s.ID, err)
		}
	}

	list, err := ListGroups(dir)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}

	if len(list) != 3 {
		t.Fatalf("ListGroups returned %d, want 3", len(list))
	}

	// Should be sorted by UpdatedAt descending → beta, gamma, alpha.
	expected := []string{"group-beta", "group-gamma", "group-alpha"}
	for i, id := range expected {
		if list[i].ID != id {
			t.Errorf("list[%d].ID = %q, want %q", i, list[i].ID, id)
		}
	}
}

func TestListGroups_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	list, err := ListGroups(dir)
	if err != nil {
		t.Fatalf("ListGroups on empty dir: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 groups, got %d", len(list))
	}
}

func TestListGroups_NonexistentDir(t *testing.T) {
	list, err := ListGroups("/tmp/definitely-does-not-exist-lele-" + time.Now().Format("20060102150405"))
	if err != nil {
		t.Fatalf("ListGroups on nonexistent dir: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 groups, got %d", len(list))
	}
}

func TestSanitizeGroupID(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"group:abc/123", "group_abc_123"},
		{"simple", "simple"},
		{"a:b\\c/d", "a_b_c_d"},
		{"", ""},
	}
	for _, tc := range cases {
		got := sanitizeGroupID(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeGroupID(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSanitizeGroupID_SafeForFilepath(t *testing.T) {
	// IDs with ":" and "/" must still round-trip through SaveGroup/LoadGroup.
	dir := t.TempDir()

	dangerousIDs := []string{
		"group:abc/123",
		"a:b:c:d",
		"has/slash/and:colon",
		"back\\slash:too",
	}

	for _, id := range dangerousIDs {
		state := &GroupState{
			ID:        id,
			Task:      "test task for " + id,
			Status:    StatusDone,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := SaveGroup(dir, state); err != nil {
			t.Fatalf("SaveGroup(%q): %v", id, err)
		}

		loaded, err := LoadGroup(dir, id)
		if err != nil {
			t.Fatalf("LoadGroup(%q): %v", id, err)
		}
		if loaded.ID != id {
			t.Errorf("round-trip ID = %q, want %q", loaded.ID, id)
		}
	}

	// Verify no path separators snuck into filenames.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != len(dangerousIDs) {
		t.Fatalf("file count = %d, want %d", len(entries), len(dangerousIDs))
	}
	for _, e := range entries {
		if strings.ContainsAny(e.Name(), ":/\\") {
			t.Errorf("filename %q contains unsafe characters", e.Name())
		}
	}
}

func TestSaveGroup_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "path")
	state := &GroupState{
		ID:        "group-new",
		Task:      "auto-create test",
		Status:    StatusRunning,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := SaveGroup(dir, state); err != nil {
		t.Fatalf("SaveGroup should create dir: %v", err)
	}

	loaded, err := LoadGroup(dir, state.ID)
	if err != nil {
		t.Fatalf("LoadGroup after auto-create: %v", err)
	}
	if loaded.ID != state.ID {
		t.Errorf("ID mismatch after auto-create: %q", loaded.ID)
	}
}
