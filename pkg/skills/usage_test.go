package skills

import (
	"path/filepath"
	"sync"
	"testing"
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
