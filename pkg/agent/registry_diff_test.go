package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

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
			if got := sameStringSet(tc.b, tc.a); got != tc.want {
				t.Errorf("sameStringSet(%v, %v) [swapped] = %v, want %v", tc.b, tc.a, got, tc.want)
			}
		})
	}
}

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

func TestAgentConfigChanged_ToolsBaselineIsConfigNotLiveRegistry(t *testing.T) {
	cfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec", "read_file"}},
	})
	registry := NewAgentRegistry(cfg)
	first, _ := registry.GetAgent("alpha")

	names := first.Tools.List()
	if len(names) < 2 {
		t.Fatalf("precondition: expected the filtered registry to keep the allowed tools, got %v", names)
	}
	for _, n := range names {
		if n != "exec" && n != "read_file" {
			t.Fatalf("unexpected surviving tool %q in base registry (shared tools are added by the coordinator, not here)", n)
		}
	}

	sameCfg := testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Tools: []string{"exec", "read_file"}},
	})
	registry.ReloadAgents(sameCfg)
	if again, _ := registry.GetAgent("alpha"); again != first {
		t.Error("expected instance preserved on identical tools config, got new instance (diff must use the recorded allowlist)")
	}
}

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

func TestReloadAgents_SkillsChangeReachesPreservedBuilder(t *testing.T) {
	registry := NewAgentRegistry(testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"a", "b"}},
	}))
	first, _ := registry.GetAgent("alpha")
	if got := first.ContextBuilder.currentSkillsFilter(); !sameStringSet(got, []string{"a", "b"}) {
		t.Fatalf("precondition: builder filter = %v, want [a b]", got)
	}
	builder := first.ContextBuilder

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

func TestReloadAgents_SkillsClearedOnPreservedBuilder(t *testing.T) {
	registry := NewAgentRegistry(testCfg(t, []config.AgentConfig{
		{ID: "alpha", Default: true, Skills: []string{"a", "b"}},
	}))
	first, _ := registry.GetAgent("alpha")

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

func TestAgentConfigChanged_IdentityFieldsRecreate(t *testing.T) {
	cases := []struct {
		name   string
		before config.AgentConfig
		after  config.AgentConfig
		check  func(t *testing.T, got *AgentInstance)
	}{
		{
			name:   "name",
			before: config.AgentConfig{ID: "alpha", Default: true, Name: "Alpha"},
			after:  config.AgentConfig{ID: "alpha", Default: true, Name: "Renamed"},
			check:  func(t *testing.T, got *AgentInstance) { assertStr(t, "Name", got.Name, "Renamed") },
		},
		{
			name:   "description",
			before: config.AgentConfig{ID: "alpha", Default: true, Description: "old"},
			after:  config.AgentConfig{ID: "alpha", Default: true, Description: "new"},
			check:  func(t *testing.T, got *AgentInstance) { assertStr(t, "Description", got.Description, "new") },
		},
		{
			name:   "default flag moves to another agent",
			before: config.AgentConfig{ID: "alpha", Default: true},
			after:  config.AgentConfig{ID: "alpha"},
			check:  func(t *testing.T, got *AgentInstance) { assertBool(t, "IsDefault", got.IsDefault, false) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testCfg(t, []config.AgentConfig{tc.before})
			registry := NewAgentRegistry(cfg)
			original, _ := registry.GetAgent("alpha")

			registry.ReloadAgents(testCfg(t, []config.AgentConfig{tc.after}))

			reloaded, ok := registry.GetAgent("alpha")
			if !ok {
				t.Fatal("agent alpha missing after reload")
			}
			if reloaded == original {
				t.Fatalf("expected instance recreate when %s changed, got same pointer", tc.name)
			}
			tc.check(t, reloaded)
		})
	}
}

func TestAgentConfigChanged_IdentityUnchangedDoesNotRecreate(t *testing.T) {
	agents := []config.AgentConfig{
		{ID: "alpha", Default: true, Name: "Alpha", Description: "same"},
	}
	cfg := testCfg(t, agents)
	registry := NewAgentRegistry(cfg)
	original, _ := registry.GetAgent("alpha")

	registry.ReloadAgents(testCfg(t, agents))

	reloaded, _ := registry.GetAgent("alpha")
	if reloaded != original {
		t.Fatal("unchanged identity fields must preserve the instance, got a recreate")
	}
}

func assertStr(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func assertBool(t *testing.T, field string, got, want bool) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", field, got, want)
	}
}
