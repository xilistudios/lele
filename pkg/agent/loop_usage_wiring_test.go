package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/skills"
	"github.com/xilistudios/lele/pkg/store"
	"github.com/xilistudios/lele/pkg/utils/counterstore"
)

// The registry eagerly builds agent instances inside NewAgentLoopWithStore,
// and each instance constructs its skills usage store. If the usage repo is
// wired after the registry, those stores silently start in legacy file mode
// and the JSON counters never migrate. This test pins the wiring order by
// asserting the observable outcome: a legacy skills_usage.json present at
// startup is imported into SQLite and renamed.
func TestNewAgentLoopWithStore_ImportsLegacySkillUsage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	legacyPath := filepath.Join(tmpDir, "skills_usage.json")
	if _, err := counterstore.MergeAndSave(legacyPath, "skills", map[string]map[string]uint64{
		"main": {"github": 3},
	}); err != nil {
		t.Fatalf("seed legacy usage file: %v", err)
	}

	db, err := store.Open(filepath.Join(tmpDir, "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()

	t.Cleanup(func() {
		skills.SetUsageRepo(nil)
		mcp.SetUsageRepo(nil)
	})

	al := NewAgentLoopWithStore(twoAgentConfig(t, tmpDir), bus.NewMessageBus(), db)
	if al == nil {
		t.Fatal("NewAgentLoopWithStore returned nil")
	}

	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy skills_usage.json still live after startup (stat err = %v); usage repo was wired too late", err)
	}
	if _, err := os.Stat(legacyPath + ".migrated"); err != nil {
		t.Fatalf("legacy file not renamed to .migrated: %v", err)
	}

	stored, err := db.Usage().LoadUsage("skills")
	if err != nil {
		t.Fatalf("LoadUsage: %v", err)
	}
	if stored["main"]["github"] != 3 {
		t.Fatalf("stored skills usage = %v, want main.github=3", stored)
	}
}

// A loop without a store must reset the usage repos: otherwise a repo wired
// by an earlier loop (possibly already closed) keeps hijacking persistence
// and the "falling back to JSON storage" promise is false.
func TestNewAgentLoopWithStore_NilStoreResetsUsageRepo(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	db, err := store.Open(filepath.Join(tmpDir, "lele.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() {
		skills.SetUsageRepo(nil)
		mcp.SetUsageRepo(nil)
	})

	NewAgentLoopWithStore(twoAgentConfig(t, tmpDir), bus.NewMessageBus(), db)
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	NewAgentLoopWithStore(twoAgentConfig(t, tmpDir), bus.NewMessageBus(), nil)

	path := filepath.Join(tmpDir, "skills_usage.json")
	s := skills.NewUsageStore(path)
	s.Inc("main", "github")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("usage not written to the JSON fallback after nil-store reset: %v", err)
	}
}
