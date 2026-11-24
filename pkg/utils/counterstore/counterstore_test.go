package counterstore

import (
	"fmt"
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

// fakeUsageRepo is an in-memory UsageRepo used to exercise the legacy-file
// import logic without depending on pkg/store.
type fakeUsageRepo struct {
	counts map[string]map[string]uint64
}

func newFakeUsageRepo() *fakeUsageRepo {
	return &fakeUsageRepo{counts: map[string]map[string]uint64{}}
}

func (f *fakeUsageRepo) LoadUsage(scope string) (map[string]map[string]uint64, error) {
	out := map[string]map[string]uint64{}
	for grp, keys := range f.counts {
		out[grp] = map[string]uint64{}
		for k, v := range keys {
			out[grp][k] = v
		}
	}
	return out, nil
}

func (f *fakeUsageRepo) MergeUsage(scope string, pending map[string]map[string]uint64) (map[string]map[string]uint64, error) {
	for grp, keys := range pending {
		if f.counts[grp] == nil {
			f.counts[grp] = map[string]uint64{}
		}
		for k, v := range keys {
			f.counts[grp][k] += v
		}
	}
	return f.LoadUsage(scope)
}

func (f *fakeUsageRepo) ReplaceUsage(scope string, counts map[string]map[string]uint64) error {
	f.counts = map[string]map[string]uint64{}
	for grp, keys := range counts {
		f.counts[grp] = map[string]uint64{}
		for k, v := range keys {
			f.counts[grp][k] = v
		}
	}
	return nil
}

func TestLoadWithRepo_ImportsLegacyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.json")
	if _, err := MergeAndSave(path, "test", map[string]map[string]uint64{"a": {"x": 2}}); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}

	repo := newFakeUsageRepo()
	got := LoadWithRepo(path, "test", "test", repo)
	if got["a"]["x"] != 2 {
		t.Fatalf("imported counts = %v, want a.x=2", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy file still present after import (stat err = %v)", err)
	}
	if _, err := os.Stat(path + ".migrated"); err != nil {
		t.Fatalf("legacy file not renamed to .migrated: %v", err)
	}

	// A second load (fresh process) sees the repo counts and no file:
	// nothing is imported again.
	got = LoadWithRepo(path, "test", "test", repo)
	if got["a"]["x"] != 2 {
		t.Fatalf("counts after second load = %v, want a.x=2 (no double import)", got)
	}
}

func TestLoadWithRepo_DoesNotImportWhenScopePopulated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.json")
	if _, err := MergeAndSave(path, "test", map[string]map[string]uint64{"a": {"x": 2}}); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}

	repo := newFakeUsageRepo()
	if _, err := repo.MergeUsage("test", map[string]map[string]uint64{"a": {"x": 5}}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	got := LoadWithRepo(path, "test", "test", repo)
	if got["a"]["x"] != 5 {
		t.Fatalf("counts = %v, want a.x=5 (repo is authoritative, stale file must not be merged)", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stale legacy file still live (stat err = %v); it must be retired so no second source of truth remains", err)
	}
	if _, err := os.Stat(path + ".migrated"); err != nil {
		t.Fatalf("stale legacy file not renamed to .migrated: %v", err)
	}
}

func TestLoadWithRepo_NoLegacyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	repo := newFakeUsageRepo()
	if _, err := repo.MergeUsage("test", map[string]map[string]uint64{"a": {"x": 3}}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}
	got := LoadWithRepo(path, "test", "test", repo)
	if got["a"]["x"] != 3 {
		t.Fatalf("counts = %v, want a.x=3", got)
	}
}
func TestLoadWithRepo_RetireDoesNotClobberBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.json")
	if _, err := MergeAndSave(path, "test", map[string]map[string]uint64{"a": {"x": 2}}); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}
	backup := path + ".migrated"
	if err := os.WriteFile(backup, []byte(`{"a":{"x":9}}`), 0o600); err != nil {
		t.Fatalf("seed existing backup: %v", err)
	}

	repo := newFakeUsageRepo()
	if _, err := repo.MergeUsage("test", map[string]map[string]uint64{"a": {"x": 5}}); err != nil {
		t.Fatalf("seed repo: %v", err)
	}

	got := LoadWithRepo(path, "test", "test", repo)
	if got["a"]["x"] != 5 {
		t.Fatalf("counts = %v, want a.x=5", got)
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("existing .migrated backup destroyed: %v", err)
	}
	if string(data) != `{"a":{"x":9}}` {
		t.Fatalf("existing .migrated backup clobbered: %s", data)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy file still live (stat err = %v)", err)
	}
	matches, _ := filepath.Glob(path + ".migrated.*")
	if len(matches) != 1 {
		t.Fatalf("retired file not preserved under a unique name: %v", matches)
	}
}

type failingMergeRepo struct {
	fakeUsageRepo
}

func (f *failingMergeRepo) MergeUsage(scope string, pending map[string]map[string]uint64) (map[string]map[string]uint64, error) {
	return nil, fmt.Errorf("store unavailable")
}

func TestLoadWithRepo_MergeFailureKeepsLegacyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "counts.json")
	if _, err := MergeAndSave(path, "test", map[string]map[string]uint64{"a": {"x": 2}}); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}

	repo := &failingMergeRepo{fakeUsageRepo: *newFakeUsageRepo()}
	got := LoadWithRepo(path, "test", "test", repo)
	if len(got) != 0 {
		t.Fatalf("counts = %v, want empty on failed import", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("legacy file must survive a failed import for the next retry: %v", err)
	}
	if _, err := os.Stat(path + ".migrated"); !os.IsNotExist(err) {
		t.Fatalf("legacy file renamed despite failed import (stat err = %v)", err)
	}
}
