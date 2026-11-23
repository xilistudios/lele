package agent

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func skillsSectionOf(prompt string) string {
	start := strings.Index(prompt, "\n# Skills\n")
	if start < 0 {
		if strings.HasPrefix(prompt, "# Skills\n") {
			start = 0
		} else {
			return ""
		}
	} else {
		start++
	}
	rest := prompt[start:]
	if end := strings.Index(rest, "\n\n---\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestSkillsSection_DiscoveryHintsMatchPermissions(t *testing.T) {
	testcases := []struct {
		name   string
		list   bool
		search bool
		load   bool
	}{
		{name: "all allowed", list: true, search: true, load: true},
		{name: "none allowed", list: false, search: false, load: false},
		{name: "list only", list: true},
		{name: "load only", load: true},
		{name: "search only", search: true},
		{name: "list and search", list: true, search: true},
		{name: "list and load", list: true, load: true},
		{name: "search and load", search: true, load: true},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			writeTestSkill(t, workspace, "alpha-skill", "Alpha description")

			cb := NewContextBuilder(workspace)
			cb.SetSkillsDiscovery(tc.list, tc.search, tc.load)

			sec := skillsSectionOf(cb.BuildSystemPrompt())
			if sec == "" {
				t.Fatal("the skills section is missing")
			}
			if !strings.Contains(sec, "<name>alpha-skill</name>") {
				t.Fatalf("the skill list must stay in every case:\n%s", sec)
			}
			if got := strings.Contains(sec, "`list_skills`"); got != tc.list {
				t.Errorf("section mentions list_skills = %v, want %v:\n%s", got, tc.list, sec)
			}
			if got := strings.Contains(sec, "`search_skill`"); got != tc.search {
				t.Errorf("section mentions search_skill = %v, want %v:\n%s", got, tc.search, sec)
			}
			if got := strings.Contains(sec, "`load_skill`"); got != tc.load {
				t.Errorf("section mentions load_skill = %v, want %v:\n%s", got, tc.load, sec)
			}
			if !tc.list && !tc.search {
				if strings.Contains(sec, "catalog has more") {
					t.Errorf("section claims the catalog has more without a browsing tool:\n%s", sec)
				}
			}
			if !tc.load && strings.Contains(sec, "instructions") {
				t.Errorf("section promises instructions without load_skill:\n%s", sec)
			}
		})
	}
}

func TestSkillsSection_ZeroValueBuilderKeepsTheFullCopy(t *testing.T) {
	perms := (&ContextBuilder{}).resolvedSkillsDiscovery()
	if !perms.List || !perms.Search || !perms.Load {
		t.Fatalf("a ContextBuilder built directly has %+v, want every discovery tool allowed", perms)
	}

	sec := skillsSection("<skills>\n  <skill>\n    <name>alpha-skill</name>\n  </skill>\n</skills>", perms)
	for _, want := range []string{"`list_skills`", "`search_skill`", "`load_skill`"} {
		if !strings.Contains(sec, want) {
			t.Errorf("default permissions dropped %s:\n%s", want, sec)
		}
	}
}

func TestPromptCacheInvalidation_SkillsDiscovery(t *testing.T) {
	workspace := t.TempDir()
	writeTestSkill(t, workspace, "alpha-skill", "Alpha description")

	cb := NewContextBuilder(workspace)
	full := cb.BuildSystemPrompt()

	cb.SetSkillsDiscovery(true, true, true)
	if again := cb.BuildSystemPrompt(); again != full {
		t.Error("repeating the same permissions must not change the prompt")
	}

	cb.SetSkillsDiscovery(false, false, false)
	tightened := cb.BuildSystemPrompt()
	if strings.Contains(tightened, "`load_skill`") {
		t.Error("the cached prompt was not rebuilt after the tightening")
	}
}

func newSkillAllowlistAgent(t *testing.T, workspace string, allowedTools []string) *AgentInstance {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	cfg.Agents.Defaults.Provider = "openai"
	cfg.Agents.Defaults.Model = "openai:gpt-4o-mini"
	cfg.Providers.Named = map[string]config.NamedProviderConfig{
		"openai": {Type: "openai"},
	}

	agentCfg := &config.AgentConfig{ID: "main", Tools: allowedTools}
	return NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg)
}

func TestNewAgentInstance_SkillsPromptFollowsToolAllowlist(t *testing.T) {
	workspace := t.TempDir()
	writeTestSkill(t, workspace, "alpha-skill", "Alpha description")

	restricted := newSkillAllowlistAgent(t, workspace, []string{"read_file"})
	sec := skillsSectionOf(restricted.ContextBuilder.BuildSystemPrompt())
	if sec == "" {
		t.Fatal("the skills section must stay even when the discovery tools are filtered out")
	}
	for _, unwanted := range []string{"`list_skills`", "`search_skill`", "`load_skill`"} {
		if strings.Contains(sec, unwanted) {
			t.Errorf("a restricted agent's prompt advertises %s:\n%s", unwanted, sec)
		}
	}

	full := newSkillAllowlistAgent(t, workspace, nil)
	fullSec := skillsSectionOf(full.ContextBuilder.BuildSystemPrompt())
	for _, want := range []string{"`list_skills`", "`search_skill`", "`load_skill`"} {
		if !strings.Contains(fullSec, want) {
			t.Errorf("an unrestricted agent's prompt dropped %s:\n%s", want, fullSec)
		}
	}
}
