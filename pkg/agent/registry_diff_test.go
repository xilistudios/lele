// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// B3: the reload diff in agentConfigChanged must compare Skills and Tools by
// CONTENT (order-insensitive), not by length. These tests go through
// ReloadAgents (the only production caller) and assert on instance identity:
// changed=true => new instance, changed=false => same pointer.

// --- helpers unit ------------------------------------------------------------

func TestSameStringSet(t *testing.T) {
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"nil vs nil", nil, nil, true},
		{"nil vs empty", nil, []string{}, true},
		{"empty vs empty", []string{}, []string{}, true},
		{"nil vs one", nil, []string{"x"}, false},
		{"same single", []string{"x"}, []string{"x"}, true},
		{"diff single", []string{"x"}, []string{"y"}, false},
		{"same order", []string{"a", "b"}, []string{"a", "b"}, true},
		{"reversed order", []string{"a", "b"}, []string{"b", "a"}, true},
		{"same length diff content", []string{"a", "b"}, []string{"c", "d"}, false},
		{"subset", []string{"exec"}, []string{"exec", "read_file"}, false},
		{"superset", []string{"exec", "read_file"}, []string{"exec"}, false},
		{"duplicates collapse", []string{"a", "a", "b"}, []string{"a", "b"}, true},
		{"duplicates only side", []string{"a", "a"}, []string{"a"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameStringSet(tc.a, tc.b); got != tc.want {
				t.Errorf("sameStringSet(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
			// Symmetry: the diff must not depend on argument order.
			if got := sameStringSet(tc.b, tc.a); got != tc.want {
				t.Errorf("sameStringSet(%v, %v) [swapped] = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

// --- skills content diff -----------------------------------------------------

// TestAgentConfigChanged_SkillsSameLenDiffContent is the regression this task
// exists for: ["a","b"] -> ["c","d"] kept the same length, so the old
// length-only check reported "unchanged" and the agent kept serving stale
// skills until a full restart.
func TestAgentConfigChanged_SkillsSameLenDiffContent(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"a", "b"}},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"c", "d"}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == original {
		t.Fatal("expected instance recreate when skills content changed at same length, got same pointer")
	}
	if !sameStringSet(reloaded.SkillsFilter, []string{"c", "d"}) {
		t.Errorf("reloaded SkillsFilter = %v, want {c,d}", reloaded.SkillsFilter)
	}
}

func TestAgentConfigChanged_SkillsSameContentDiffOrder(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"a", "b"}},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"b", "a"}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded != original {
		t.Error("expected instance preserved when skills set is unchanged (only reordered), got new instance")
	}
}

func TestAgentConfigChanged_SkillsNilToAdd(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"x"}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == original {
		t.Fatal("expected instance recreate when skills went from nil to [x], got same pointer")
	}
}

func TestAgentConfigChanged_SkillsNilToEmpty(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded != original {
		t.Error("expected instance preserved when skills went from nil to [] (both = all skills), got new instance")
	}
}

// --- tools content diff ------------------------------------------------------

func TestAgentConfigChanged_ToolsNilToEmpty(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded != original {
		t.Error("expected instance preserved when tools went from nil to [] (both = all tools), got new instance")
	}
}

func TestAgentConfigChanged_ToolsGrow(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec"}},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec", "read_file"}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == original {
		t.Fatal("expected instance recreate when tools allowlist grew, got same pointer")
	}
	if _, ok := reloaded.Tools.Get("read_file"); !ok {
		t.Error("reloaded instance must have read_file after allowlist grew")
	}
}

func TestAgentConfigChanged_ToolsSameContentDiffOrder(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec", "read_file"}},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"read_file", "exec"}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded != original {
		t.Error("expected instance preserved when tools set is unchanged (only reordered), got new instance")
	}
}

func TestAgentConfigChanged_ToolsSameLenDiffContent(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec", "read_file"}},
	})
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec", "list_dir"}},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == original {
		t.Fatal("expected instance recreate when tools content changed at same length, got same pointer")
	}
	if _, ok := reloaded.Tools.Get("list_dir"); !ok {
		t.Error("reloaded instance must have list_dir after allowlist swap")
	}
	if _, ok := reloaded.Tools.Get("read_file"); ok {
		t.Error("reloaded instance must NOT have read_file: it left the allowlist")
	}
}

func TestAgentConfigChanged_ToolsShrinkToNil(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec"}},
	})
	registry := NewAgentRegistry(cfg)
	limited, _ := registry.GetAgent("alpha")

	newCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	})
	registry.ReloadAgents(newCfg)

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded == limited {
		t.Fatal("expected instance recreate when tools allowlist was removed, got same pointer")
	}
	if _, ok := reloaded.Tools.Get("write_file"); !ok {
		t.Error("removing the allowlist must restore all tools (write_file missing)")
	}
}

// TestAgentConfigChanged_ToolsBaselineIsConfigNotLiveRegistry locks WHY the
// diff compares against the recorded allowlist instead of the instance's live
// ToolRegistry: NewAgentInstance registers base tools (read_file, exec, ...)
// and the tool coordinator layers shared tools (spawn, web_search, sleep, ...)
// on top, so the live registry is never equal to the config allowlist. A diff
// against it would recreate the instance on every single reload.
func TestAgentConfigChanged_ToolsBaselineIsConfigNotLiveRegistry(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec", "read_file"}},
	})
	registry := NewAgentRegistry(cfg)
	first, _ := registry.GetAgent("alpha")

	// The live registry has more names than the allowlist (base tools survive
	// the filter only when allowed; here exactly the two do — but shared tools
	// registered later by the coordinator are not part of ac.Tools either way).
	names := first.Tools.List()
	if len(names) < 2 {
		t.Fatalf("precondition: expected the filtered registry to keep the allowed tools, got %v", names)
	}
	for _, n := range names {
		if n != "exec" && n != "read_file" {
			t.Fatalf("unexpected surviving tool %q in base registry (shared tools are added by the coordinator, not here)", n)
		}
	}

	// Byte-identical config reloaded twice: must preserve the instance. If the
	// diff compared ac.Tools against the live registry this would flap forever.
	sameCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec", "read_file"}},
	})
	registry.ReloadAgents(sameCfg)
	if again, _ := registry.GetAgent("alpha"); again != first {
		t.Error("expected instance preserved on identical tools config, got new instance (diff must use the recorded allowlist)")
	}
}

// --- cloneStrings ------------------------------------------------------------

func TestCloneStrings(t *testing.T) {
	if cloneStrings(nil) != nil {
		t.Error("cloneStrings(nil) must return nil")
	}
	src := []string{"a", "b"}
	dst := cloneStrings(src)
	dst[0] = "mutated"
	if src[0] != "a" {
		t.Error("cloneStrings must not alias the source slice")
	}
}

// --- preserved builder carries the NEW skills filter -------------------------

// TestReloadAgents_SkillsChangeReachesPreservedBuilder closes the gap the
// content diff would otherwise leave open: when agentConfigChanged reports a
// skills change, ReloadAgents recreates the instance but deliberately keeps the
// OLD ContextBuilder (it owns live prompt/session state). That builder still
// holds the previous allowlist, so without an explicit refresh the agent keeps
// rendering the old <skills> block forever — the diff fires, the log says
// "recreated", and nothing changes for the model.
func TestReloadAgents_SkillsChangeReachesPreservedBuilder(t *testing.T) {
	registry := NewAgentRegistry(testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"a", "b"}},
	}))
	first, _ := registry.GetAgent("alpha")
	if got := first.ContextBuilder.currentSkillsFilter(); !sameStringSet(got, []string{"a", "b"}) {
		t.Fatalf("precondition: builder filter = %v, want [a b]", got)
	}
	builder := first.ContextBuilder

	// Same length, different content — the case the old length-only check missed.
	registry.ReloadAgents(testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"c", "d"}},
	}))

	second, _ := registry.GetAgent("alpha")
	if second == first {
		t.Fatal("expected a new instance for a skills content change")
	}
	if second.ContextBuilder != builder {
		t.Fatal("precondition: builder is expected to be preserved, not rebuilt")
	}
	got := second.ContextBuilder.currentSkillsFilter()
	if !sameStringSet(got, []string{"c", "d"}) {
		t.Errorf("preserved builder still carries %v; want [c d]", got)
	}
}

// TestReloadAgents_SkillsClearedOnPreservedBuilder covers the reverse direction:
// dropping the allowlist must restore the historical "all skills" behaviour
// (nil/empty) on the builder that survives the reload.
func TestReloadAgents_SkillsClearedOnPreservedBuilder(t *testing.T) {
	registry := NewAgentRegistry(testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"a", "b"}},
	}))
	first, _ := registry.GetAgent("alpha")

	// Model change forces recreation while Skills goes back to unset.
	registry.ReloadAgents(testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true},
	}))

	second, _ := registry.GetAgent("alpha")
	if second == first {
		t.Fatal("precondition: model change should recreate the instance")
	}
	if len(second.ContextBuilder.currentSkillsFilter()) != 0 {
		t.Errorf("builder filter = %v, want empty (all skills)", second.ContextBuilder.currentSkillsFilter())
	}
}

// TestReloadAgents_UnchangedSkillsKeepsInstance is the guard against the
// opposite mistake: refreshing the builder must not become a reason to
// recreate. A no-op reload with skills set has to preserve the instance.
func TestReloadAgents_UnchangedSkillsKeepsInstance(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"a", "b"}},
	})
	registry := NewAgentRegistry(cfg)
	first, _ := registry.GetAgent("alpha")

	registry.ReloadAgents(testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"b", "a"}},
	}))

	if again, _ := registry.GetAgent("alpha"); again != first {
		t.Error("expected instance preserved when only the skills ORDER differs")
	}
}
