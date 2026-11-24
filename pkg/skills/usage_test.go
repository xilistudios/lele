package skills

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/utils/counterstore"
)

func usagePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "skills_usage.json")
}

func TestUsageStore_PersistsAcrossReload(t *testing.T) {
	path := usagePath(t)
	store := NewUsageStore(path)
	store.Inc("coder", "github")
	store.Inc("coder", "github")
	store.Inc("coder", "weather")

	reloaded := NewUsageStore(path)
	counts := reloaded.CountsFor("coder")
	if counts["github"] != 2 || counts["weather"] != 1 {
		t.Fatalf("reloaded counts = %v, want github:2 weather:1", counts)
	}
}

func TestUsageStore_IsolatesAgents(t *testing.T) {
	store := NewUsageStore(usagePath(t))
	store.Inc("coder", "github")
	store.Inc("writer", "summarize")
	store.Inc("writer", "summarize")

	if got := store.CountsFor("coder"); got["summarize"] != 0 || got["github"] != 1 {
		t.Fatalf("coder counts = %v, want only its own loads", got)
	}
	if got := store.CountsFor("nobody"); len(got) != 0 {
		t.Fatalf("unknown agent counts = %v, want empty", got)
	}
}

func TestUsageStore_ConcurrentStoresDoNotLoseWrites(t *testing.T) {
	path := usagePath(t)
	const writers = 4
	const perWriter = 10

	stores := make([]*UsageStore, writers)
	for i := range stores {
		stores[i] = NewUsageStore(path)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range stores {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < perWriter; j++ {
				stores[i].Inc("coder", "github")
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if got := NewUsageStore(path).CountsFor("coder")["github"]; got != writers*perWriter {
		t.Fatalf("persisted github = %d, want %d (a concurrent write was lost)", got, writers*perWriter)
	}
}

func TestUsageStore_ReturnedCountsAreACopy(t *testing.T) {
	store := NewUsageStore(usagePath(t))
	store.Inc("coder", "github")
	counts := store.CountsFor("coder")
	counts["github"] = 999
	if got := store.CountsFor("coder")["github"]; got != 1 {
		t.Fatalf("store mutated through the returned map: github = %d, want 1", got)
	}
}

func TestUsageStore_NilAndMemoryOnlyAreSafe(t *testing.T) {
	var nilStore *UsageStore
	nilStore.Inc("coder", "github")
	if got := nilStore.CountsFor("coder"); len(got) != 0 {
		t.Fatalf("nil store counts = %v, want empty", got)
	}

	mem := NewUsageStore("")
	mem.Inc("coder", "github")
	if got := mem.CountsFor("coder")["github"]; got != 1 {
		t.Fatalf("memory-only count = %d, want 1", got)
	}
}
func newTestUsageRepo(t *testing.T) *store.UsageRepo {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		if errors.Is(err, store.ErrUnsupportedPlatform) {
			t.Skip("SQLite not supported on this platform")
		}
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s.Usage()
}

func TestUsageStore_SQLite_PersistsAcrossReload(t *testing.T) {
	repo := newTestUsageRepo(t)
	SetUsageRepo(repo)
	t.Cleanup(func() { SetUsageRepo(nil) })

	path := usagePath(t)
	s := NewUsageStore(path)
	s.Inc("coder", "github")
	s.Inc("coder", "github")
	s.Inc("coder", "weather")

	reloaded := NewUsageStore(path)
	counts := reloaded.CountsFor("coder")
	if counts["github"] != 2 || counts["weather"] != 1 {
		t.Fatalf("reloaded counts = %v, want github:2 weather:1", counts)
	}

	stored, err := repo.LoadUsage("skills")
	if err != nil {
		t.Fatalf("LoadUsage: %v", err)
	}
	if stored["coder"]["github"] != 2 || stored["coder"]["weather"] != 1 {
		t.Fatalf("stored counts = %v, want github:2 weather:1", stored)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("JSON usage file was created despite SQLite mode (stat err = %v)", err)
	}
}

func TestUsageStore_SQLite_ImportsLegacyFile(t *testing.T) {
	repo := newTestUsageRepo(t)
	path := usagePath(t)
	if _, err := counterstore.MergeAndSave(path, "skills", map[string]map[string]uint64{
		"coder": {"github": 3},
	}); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}

	SetUsageRepo(repo)
	t.Cleanup(func() { SetUsageRepo(nil) })

	s := NewUsageStore(path)
	if got := s.CountsFor("coder")["github"]; got != 3 {
		t.Fatalf("imported count = %d, want 3", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("legacy file still present after import (stat err = %v)", err)
	}
	if _, err := os.Stat(path + ".migrated"); err != nil {
		t.Fatalf("legacy file not renamed to .migrated: %v", err)
	}

	s.Inc("coder", "github")
	if got := s.CountsFor("coder")["github"]; got != 4 {
		t.Fatalf("count after Inc = %d, want 4 (accumulates onto imported counts)", got)
	}
}

func TestUsageStore_SQLite_ConcurrentStores(t *testing.T) {
	repo := newTestUsageRepo(t)
	SetUsageRepo(repo)
	t.Cleanup(func() { SetUsageRepo(nil) })

	path := usagePath(t)
	const workers = 4
	const incs = 20
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := NewUsageStore(path)
			for j := 0; j < incs; j++ {
				s.Inc("coder", "github")
			}
		}()
	}
	wg.Wait()

	stored, err := repo.LoadUsage("skills")
	if err != nil {
		t.Fatalf("LoadUsage: %v", err)
	}
	if stored["coder"]["github"] != workers*incs {
		t.Fatalf("count = %d, want %d (increments must not be lost)", stored["coder"]["github"], workers*incs)
	}
}
