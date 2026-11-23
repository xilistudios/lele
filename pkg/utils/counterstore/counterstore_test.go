package counterstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeAndSave_AccumulatesOntoDiskSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.json")

	first := map[string]map[string]uint64{"a": {"x": 2}}
	if _, err := MergeAndSave(path, "test", first); err != nil {
		t.Fatalf("first merge: %v", err)
	}

	second := map[string]map[string]uint64{"a": {"x": 3, "y": 1}, "b": {"z": 5}}
	merged, err := MergeAndSave(path, "test", second)
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if merged["a"]["x"] != 5 || merged["a"]["y"] != 1 || merged["b"]["z"] != 5 {
		t.Fatalf("merged = %v, want deltas added onto the previous snapshot", merged)
	}

	reloaded := Load(path, "test")
	if reloaded["a"]["x"] != 5 || reloaded["b"]["z"] != 5 {
		t.Fatalf("reloaded = %v, want the merged snapshot persisted", reloaded)
	}
}

func TestLoad_CorruptFileYieldsEmptyCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path, "test"); len(got) != 0 {
		t.Fatalf("corrupt file loaded as %v, want empty counts", got)
	}
}

func TestLoad_MissingFileYieldsEmptyCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	if got := Load(path, "test"); len(got) != 0 {
		t.Fatalf("missing file loaded as %v, want empty counts", got)
	}
}

func TestMergeAndSave_LockTimeoutSkipsTheWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "counts.json")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	merged, err := MergeAndSave(path, "test", map[string]map[string]uint64{"a": {"x": 1}})
	if err == nil {
		t.Fatal("MergeAndSave must fail when the lock is held by someone else")
	}
	if merged != nil {
		t.Fatalf("merged = %v, want nil on a lock failure", merged)
	}
	if !strings.Contains(err.Error(), "lock") {
		t.Fatalf("error = %q, want it to name the lock", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("the usage file was written without the lock: %v", statErr)
	}
}
