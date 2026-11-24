package store

import (
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

func TestUsageRepo_MergeAndLoad(t *testing.T) {
	s := openTestStore(t)
	repo := s.Usage()

	empty, err := repo.LoadUsage("skills")
	if err != nil {
		t.Fatalf("LoadUsage on empty scope: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("LoadUsage on empty scope = %v, want empty", empty)
	}

	merged, err := repo.MergeUsage("skills", map[string]map[string]uint64{
		"coder": {"go-practicas": 2, "chrome": 1},
	})
	if err != nil {
		t.Fatalf("MergeUsage: %v", err)
	}
	want := map[string]map[string]uint64{"coder": {"go-practicas": 2, "chrome": 1}}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("MergeUsage = %v, want %v", merged, want)
	}

	// A second merge accumulates instead of overwriting.
	merged, err = repo.MergeUsage("skills", map[string]map[string]uint64{
		"coder":  {"go-practicas": 3},
		"writer": {"memory": 1},
	})
	if err != nil {
		t.Fatalf("MergeUsage second: %v", err)
	}
	want = map[string]map[string]uint64{
		"coder":  {"go-practicas": 5, "chrome": 1},
		"writer": {"memory": 1},
	}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("MergeUsage second = %v, want %v", merged, want)
	}

	// Scopes are isolated.
	if _, err := repo.MergeUsage("mcp", map[string]map[string]uint64{"srv": {"tool": 7}}); err != nil {
		t.Fatalf("MergeUsage mcp: %v", err)
	}
	got, err := repo.LoadUsage("skills")
	if err != nil {
		t.Fatalf("LoadUsage skills: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("skills scope leaked: %v, want %v", got, want)
	}
}

func TestUsageRepo_MergeConcurrent(t *testing.T) {
	s := openTestStore(t)
	repo := s.Usage()

	const workers = 8
	const incs = 25
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incs; j++ {
				if _, err := repo.MergeUsage("command", map[string]map[string]uint64{
					"": {"/status": 1},
				}); err != nil {
					t.Errorf("MergeUsage concurrent: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got, err := repo.LoadUsage("command")
	if err != nil {
		t.Fatalf("LoadUsage: %v", err)
	}
	if got[""]["/status"] != workers*incs {
		t.Fatalf("count = %d, want %d (increments must not be lost)", got[""]["/status"], workers*incs)
	}
}

func TestUsageRepo_Replace(t *testing.T) {
	s := openTestStore(t)
	repo := s.Usage()

	if _, err := repo.MergeUsage("command", map[string]map[string]uint64{
		"": {"/a": 5, "/b": 3},
	}); err != nil {
		t.Fatalf("MergeUsage: %v", err)
	}
	if err := repo.ReplaceUsage("command", map[string]map[string]uint64{
		"": {"/a": 1},
	}); err != nil {
		t.Fatalf("ReplaceUsage: %v", err)
	}
	got, err := repo.LoadUsage("command")
	if err != nil {
		t.Fatalf("LoadUsage: %v", err)
	}
	want := map[string]map[string]uint64{"": {"/a": 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReplaceUsage result = %v, want %v", got, want)
	}
}

func TestUsageRepo_PersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := s.Usage().MergeUsage("mcp", map[string]map[string]uint64{
		"srv": {"tool": 4},
	}); err != nil {
		t.Fatalf("MergeUsage: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.Usage().LoadUsage("mcp")
	if err != nil {
		t.Fatalf("LoadUsage after reopen: %v", err)
	}
	want := map[string]map[string]uint64{"srv": {"tool": 4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after reopen = %v, want %v", got, want)
	}
}

func TestMigrations_UpgradeV9ToV10_PreservesData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.KV().Set("marker", "kept"); err != nil {
		t.Fatalf("KV.Set: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate a v9 database: drop the v10 table and rewind schema_version.
	db, err := openRawDB(path)
	if err != nil {
		t.Fatalf("openRawDB: %v", err)
	}
	if _, err := db.Exec(`DROP TABLE usage_counters`); err != nil {
		db.Close()
		t.Fatalf("DROP usage_counters: %v", err)
	}
	if _, err := db.Exec(`UPDATE schema_meta SET value = '9' WHERE key = 'schema_version'`); err != nil {
		db.Close()
		t.Fatalf("downgrade schema_version: %v", err)
	}
	db.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open after downgrade: %v", err)
	}
	defer s2.Close()

	var version string
	if err := s2.DB().QueryRow(
		`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if want := strconv.Itoa(SchemaVersion); version != want {
		t.Errorf("schema_version = %q, want %q", version, want)
	}

	if value, found, err := s2.KV().Get("marker"); err != nil || !found || value != "kept" {
		t.Fatalf("existing data lost in upgrade: (%q, %v, %v)", value, found, err)
	}
	if _, err := s2.Usage().MergeUsage("command", map[string]map[string]uint64{"": {"/x": 1}}); err != nil {
		t.Fatalf("usage repo unusable after upgrade: %v", err)
	}
}

func TestUsageRepo_ReplaceIsScopeIsolated(t *testing.T) {
	s := openTestStore(t)
	repo := s.Usage()

	if _, err := repo.MergeUsage("skills", map[string]map[string]uint64{"coder": {"github": 4}}); err != nil {
		t.Fatalf("seed skills: %v", err)
	}
	if _, err := repo.MergeUsage("mcp", map[string]map[string]uint64{"srv": {"tool": 6}}); err != nil {
		t.Fatalf("seed mcp: %v", err)
	}
	if _, err := repo.MergeUsage("command", map[string]map[string]uint64{"": {"/a": 1, "/b": 2}}); err != nil {
		t.Fatalf("seed command: %v", err)
	}

	// The TUI palette prune replaces the whole command scope; it must never
	// touch the other features' counters.
	if err := repo.ReplaceUsage("command", map[string]map[string]uint64{"": {"/a": 9}}); err != nil {
		t.Fatalf("ReplaceUsage: %v", err)
	}

	skills, err := repo.LoadUsage("skills")
	if err != nil {
		t.Fatalf("LoadUsage skills: %v", err)
	}
	if skills["coder"]["github"] != 4 {
		t.Fatalf("skills counters damaged by command replace: %v", skills)
	}
	mcp, err := repo.LoadUsage("mcp")
	if err != nil {
		t.Fatalf("LoadUsage mcp: %v", err)
	}
	if mcp["srv"]["tool"] != 6 {
		t.Fatalf("mcp counters damaged by command replace: %v", mcp)
	}
	command, err := repo.LoadUsage("command")
	if err != nil {
		t.Fatalf("LoadUsage command: %v", err)
	}
	want := map[string]map[string]uint64{"": {"/a": 9}}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command = %v, want %v", command, want)
	}
}
