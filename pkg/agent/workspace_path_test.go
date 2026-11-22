package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func workspacePathCfg(defaultsWS string, agents ...config.AgentConfig) *config.Config {
	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = defaultsWS
	cfg.Agents.List = agents
	return cfg
}

func TestAgentWorkspacePath_ImplicitMain(t *testing.T) {
	defaultsWS := filepath.Join(t.TempDir(), "shared", "workspace")
	cfg := workspacePathCfg(defaultsWS)

	for _, id := range []string{"", "main", "MAIN", "  main  "} {
		got, ok := AgentWorkspacePath(cfg, id)
		if !ok || got != defaultsWS {
			t.Errorf("AgentWorkspacePath(%q) = (%q, %v), want (%q, true)", id, got, ok, defaultsWS)
		}
	}
	if got, ok := AgentWorkspacePath(cfg, "worker"); ok || got != "" {
		t.Errorf(`AgentWorkspacePath("worker") = (%q, %v), want ("", false)`, got, ok)
	}
	if ids := AgentIDs(cfg); len(ids) != 1 || ids[0] != "main" {
		t.Errorf("AgentIDs(implicit) = %v, want [main]", ids)
	}
}

func TestAgentWorkspacePath_NoSideEffect(t *testing.T) {
	base := t.TempDir()
	defaultsWS := filepath.Join(base, "shared", "workspace")
	leleDir := filepath.Join(base, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)

	implicit := workspacePathCfg(defaultsWS)
	if got, ok := AgentWorkspacePath(implicit, "main"); !ok || got != defaultsWS {
		t.Fatalf("AgentWorkspacePath(main) = (%q, %v), want (%q, true)", got, ok, defaultsWS)
	}
	explicit := workspacePathCfg(defaultsWS, config.AgentConfig{ID: "writer"})
	if got, ok := AgentWorkspacePath(explicit, "writer"); !ok || got != filepath.Join(leleDir, "workspace-writer") {
		t.Fatalf("AgentWorkspacePath(writer) = (%q, %v), want the derived path", got, ok)
	}
	for _, dir := range []string{defaultsWS, filepath.Join(leleDir, "workspace-writer"), leleDir} {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("%s was created by a pure resolution, want no filesystem side effect", dir)
		}
	}

	cwd := t.TempDir()
	t.Chdir(cwd)
	emptyCfg := workspacePathCfg("")
	got, ok := AgentWorkspacePath(emptyCfg, "main")
	if !ok || got != "" {
		t.Fatalf(`AgentWorkspacePath(main) = (%q, %v), want ("", true) for an unset workspace`, got, ok)
	}
	if entries, err := os.ReadDir(cwd); err != nil {
		t.Fatalf("read cwd: %v", err)
	} else if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("resolving an unset workspace wrote into the cwd: %v", names)
	}
}

func TestAgentWorkspacePath_ExplicitList(t *testing.T) {
	base := t.TempDir()
	leleDir := filepath.Join(base, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)
	defaultsWS := filepath.Join(base, "shared")
	own := filepath.Join(base, "own-ws")

	cfg := workspacePathCfg(defaultsWS,
		config.AgentConfig{ID: "main", Default: true},
		config.AgentConfig{ID: "Worker-1"},
		config.AgentConfig{ID: "Custom", Workspace: own},
	)

	tests := []struct {
		id   string
		want string
	}{
		{"main", defaultsWS},
		{"worker-1", filepath.Join(leleDir, "workspace-worker-1")},
		{"WORKER-1", filepath.Join(leleDir, "workspace-worker-1")},
		{"custom", own},
	}
	for _, tt := range tests {
		got, ok := AgentWorkspacePath(cfg, tt.id)
		if !ok || got != tt.want {
			t.Errorf("AgentWorkspacePath(%q) = (%q, %v), want (%q, true)", tt.id, got, ok, tt.want)
		}
	}
	if got, ok := AgentWorkspacePath(cfg, "ghost"); ok || got != "" {
		t.Errorf("AgentWorkspacePath(ghost) = (%q, %v), want (\"\", false)", got, ok)
	}

	wantIDs := []string{"custom", "main", "worker-1"}
	if got := AgentIDs(cfg); strings.Join(got, ",") != strings.Join(wantIDs, ",") {
		t.Errorf("AgentIDs = %v, want %v (sorted, normalized)", got, wantIDs)
	}
	if _, err := os.Stat(filepath.Join(leleDir, "workspace-worker-1")); err == nil {
		t.Error("the derived workspace fallback was created, want no side effect")
	}
}

func TestAgentWorkspacePath_DuplicateIDLastWins(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "first")
	second := filepath.Join(base, "second")
	cfg := workspacePathCfg(filepath.Join(base, "shared"),
		config.AgentConfig{ID: "dup", Workspace: first},
		config.AgentConfig{ID: "dup", Workspace: second},
	)
	got, ok := AgentWorkspacePath(cfg, "dup")
	if !ok || got != second {
		t.Errorf("AgentWorkspacePath(dup) = (%q, %v), want (%q, true) (last wins)", got, ok, second)
	}
	if ids := AgentIDs(cfg); len(ids) != 1 || ids[0] != "dup" {
		t.Errorf("AgentIDs = %v, want [dup] (deduplicated)", ids)
	}
}

func TestAgentWorkspacePath_MatchesRegistry(t *testing.T) {
	fixtures := []struct {
		name   string
		agents []config.AgentConfig
	}{
		{"implicit main", nil},
		{"explicit list", []config.AgentConfig{
			{ID: "main", Default: true},
			{ID: "Worker-1"},
			{ID: "Custom", Workspace: filepath.Join(t.TempDir(), "own")},
		}},
		{"duplicate id", []config.AgentConfig{
			{ID: "dup", Workspace: filepath.Join(t.TempDir(), "a")},
			{ID: "dup", Workspace: filepath.Join(t.TempDir(), "b")},
		}},
	}
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			cfg := testCfg(t, f.agents)
			registry := NewAgentRegistry(cfg)

			wantIDs := registry.ListAgentIDs()
			sort.Strings(wantIDs)
			if got := AgentIDs(cfg); strings.Join(got, ",") != strings.Join(wantIDs, ",") {
				t.Errorf("AgentIDs = %v, registry holds %v", got, wantIDs)
			}
			for _, id := range wantIDs {
				inst, ok := registry.GetAgent(id)
				if !ok {
					t.Fatalf("registry lost agent %q it just listed", id)
				}
				got, found := AgentWorkspacePath(cfg, id)
				if !found || got != inst.Workspace {
					t.Errorf("AgentWorkspacePath(%q) = (%q, %v), registry workspace = %q", id, got, found, inst.Workspace)
				}
			}
			if got, ok := AgentWorkspacePath(cfg, "ghost"); ok || got != "" {
				t.Errorf(`AgentWorkspacePath("ghost") = (%q, %v), want an unknown agent`, got, ok)
			}
		})
	}
}
