package group

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

// groupClosedDB returns a repo over a closed database so any later
// operation fails (sql.ErrConnDone). Exercises the error branches of the
// group persistence backends.

func TestSaveGroup_SQLiteSetError(t *testing.T) {
	// A repo over a closed DB → SetGroupState fails.
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	UseStore(s.Groups())
	// Close the underlying store to make operations fail.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	t.Cleanup(func() { UseStore(nil) })

	err = SaveGroup(dir, &GroupState{ID: "g"})
	if err == nil {
		t.Fatal("expected error when SetGroupState fails")
	}
}

func TestLoadGroup_SQLiteGetError(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	UseStore(s.Groups())
	s.Close()
	t.Cleanup(func() { UseStore(nil) })

	_, err = LoadGroup("", "g")
	if err == nil {
		t.Fatal("expected error when GetGroupState fails")
	}
}

func TestLoadGroup_SQLiteNotFoundNoDirReturnsNotFound(t *testing.T) {
	s := openSQLiteStore(t)
	useTestGroupRepo(t, s.Groups())

	_, err := LoadGroup("", "not-in-repo")
	if err == nil {
		t.Fatal("expected error when not in repo and no dir")
	}
}

func TestListGroups_SQLiteListError(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	UseStore(s.Groups())
	s.Close()
	t.Cleanup(func() { UseStore(nil) })

	_, err = ListGroups("ignored")
	if err == nil {
		t.Fatal("expected error when ListGroupStates fails")
	}
}

func TestLoadGroup_SQLiteCorruptEntry(t *testing.T) {
	s := openSQLiteStore(t)
	useTestGroupRepo(t, s.Groups())

	// Insert a corrupt JSON entry for the ID directly.
	if err := s.Groups().SetGroupState("group:corrupt", "{not json"); err != nil {
		t.Fatalf("SetGroupState corrupt: %v", err)
	}

	_, err := LoadGroup("", "group:corrupt")
	if err == nil {
		t.Fatal("expected error when stored JSON is corrupt")
	}
}

func TestLoadGroup_SQLiteLazyMigrationMissingFile(t *testing.T) {
	// dir is provided but the legacy file is absent → loadGroupLegacy
	// errors → wrapped as ErrNotExist rather than propagating.
	s := openSQLiteStore(t)
	useTestGroupRepo(t, s.Groups())

	dir := t.TempDir()
	_, err := LoadGroup(dir, "group:nope")
	if err == nil {
		t.Fatal("expected error for missing group during lazy migration")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestSaveGroup_LegacyCreatesNestedDir(t *testing.T) {
	UseStore(nil)
	t.Cleanup(func() { UseStore(nil) })

	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := SaveGroup(dir, &GroupState{ID: "g", Status: StatusDone}); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, sanitizeGroupID("g")+".json")); err != nil {
		t.Errorf("expected file to exist: %v", err)
	}
}

func TestListGroups_LegacySortByUpdatedAtDesc(t *testing.T) {
	UseStore(nil)
	t.Cleanup(func() { UseStore(nil) })

	dir := t.TempDir()
	now := time.Now()
	for i, id := range []string{"old", "new"} {
		u := now.Add(time.Duration(i) * time.Hour)
		if err := saveGroupLegacy(dir, &GroupState{ID: id, Status: StatusDone, CreatedAt: u, UpdatedAt: u}); err != nil {
			t.Fatalf("save: %v", err)
		}
	}
	list, err := ListGroups(dir)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListGroups returned %d, want 2", len(list))
	}
	if list[0].ID != "new" || list[1].ID != "old" {
		t.Errorf("sort order wrong: got %q then %q, want new then old", list[0].ID, list[1].ID)
	}
}
