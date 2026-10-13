// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// The pure resolvers exist so a read-only front-end (`lele mcp`) can find an
// agent's workspace without the registry's mkdir side effect. These tests pin
// both halves of that claim: the paths must equal what the registry would
// resolve (same rules as NewAgentRegistry/resolveAgentWorkspace) AND resolving
// them must not touch the filesystem.

// workspacePathCfg builds a config with the given defaults workspace and
// explicit agent list.
func workspacePathCfg(defaultsWS string, agents ...config.AgentConfig) *config.Config {
	cfg := &config.Config{}
	cfg.Agents.Defaults.Workspace = defaultsWS
	cfg.Agents.List = agents
	return cfg
}

// TestAgentWorkspacePath_ImplicitMain: with an empty agents.list the registry
// builds a single implicit "main" agent whose workspace is the defaults one.
func TestAgentWorkspacePath_ImplicitMain(t *testing.T) {
	defaultsWS := filepath.Join(t.TempDir(), "shared", "workspace")
	cfg := workspacePathCfg(defaultsWS)

	for _, id := range []string{"", "main", "MAIN", "  main  "} {
		got, ok := AgentWorkspacePath(cfg, id)
		if !ok || got != defaultsWS {
			t.Errorf("AgentWorkspacePath(%q) = (%q, %v), want (%q, true)", id, got, ok, defaultsWS)
		}
	}
	// Any other ID is unknown: the implicit registry holds only "main".
	if got, ok := AgentWorkspacePath(cfg, "worker"); ok || got != "" {
		t.Errorf(`AgentWorkspacePath("worker") = (%q, %v), want ("", false)`, got, ok)
	}
	if ids := AgentIDs(cfg); len(ids) != 1 || ids[0] != "main" {
		t.Errorf("AgentIDs(implicit) = %v, want [main]", ids)
	}
}

// TestAgentWorkspacePath_NoSideEffect is the point of the export: unlike
// NewAgentRegistry (→ NewAgentInstance → InitializeWorkspace), resolving a
// path must create nothing — not the workspace, not its memory/ subdirectory,
// and nothing relative to the process cwd when the workspace is unset.
func TestAgentWorkspacePath_NoSideEffect(t *testing.T) {
	base := t.TempDir()
	defaultsWS := filepath.Join(base, "shared", "workspace")
	leleDir := filepath.Join(base, "lele")
	t.Setenv("LELE_CONFIG_DIR", leleDir)

	// Both fallbacks the runtime would materialize: the shared defaults
	// workspace (implicit main) and the derived <config-dir>/workspace-writer.
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

	// The reviewer's exact reproduction: an unset workspace resolves to ""
	// and the registry's InitializeWorkspace("") used to create ./memory
	// relative to the process cwd. Nothing may appear there now.
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

// TestAgentWorkspacePath_ExplicitList pins the explicit agents.list rules:
// an agent's own workspace wins, a non-default agent without one falls back to
// <config-dir>/workspace-<id>, the default one falls back to the defaults
// workspace, and unknown IDs are reported as such.
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
		{"worker-1", filepath.Join(leleDir, "workspace-worker-1")}, // normalized ID
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
	// Nothing above created a directory (the derived fallback is a path, not
	// a mkdir).
	if _, err := os.Stat(filepath.Join(leleDir, "workspace-worker-1")); err == nil {
		t.Error("the derived workspace fallback was created, want no side effect")
	}
}

// TestAgentWorkspacePath_DuplicateIDLastWins: the registry stores instances in
// a map, so a duplicated ID keeps the last entry — the lookup must agree.
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

// TestAgentWorkspacePath_MatchesRegistry pins the pure resolvers against the
// real thing: for any config, AgentWorkspacePath/AgentIDs must answer exactly
// what a freshly built registry holds — the implicit "main" agent, normalized
// IDs and a duplicated ID (the registry's map keeps the last occurrence). This
// is what makes mirroring the rule safe: if either side's rule moves, one of
// the two fails here instead of silently letting the CLI edit the wrong file.
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
			// An ID nobody configured is unknown to the lookup (the registry
			// would refuse GetAgent too).
			if got, ok := AgentWorkspacePath(cfg, "ghost"); ok || got != "" {
				t.Errorf(`AgentWorkspacePath("ghost") = (%q, %v), want an unknown agent`, got, ok)
			}
		})
	}
}
