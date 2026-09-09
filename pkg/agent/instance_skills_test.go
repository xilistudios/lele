package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// TestNewAgentInstance_WiresSkillsFilterToPrompt is the end-to-end guard for
// AgentConfig.Skills: the allowlist must reach the ContextBuilder (not just
// AgentInstance.SkillsFilter) so the system prompt the LLM actually receives
// lists only the configured skills.
func TestNewAgentInstance_WiresSkillsFilterToPrompt(t *testing.T) {
	workspace, err := os.MkdirTemp("", "instance-skills-test-*")
	if err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}
	defer os.RemoveAll(workspace)

	// Isolate the global skills dir (~/.lele/skills) so the assertion only sees
	// the two skills this test installs.
	configDir, err := os.MkdirTemp("", "instance-skills-config-*")
	if err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)
	t.Setenv("LELE_CONFIG_DIR", configDir)

	writeTestSkill(t, workspace, "alpha-skill", "Alpha description")
	writeTestSkill(t, workspace, "beta-skill", "Beta description")

	cfg := createTestConfig(workspace)
	agentCfg := &config.AgentConfig{
		ID:        "skills-agent",
		Workspace: workspace,
		Skills:    []string{"alpha-skill"},
	}

	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg)
	if agent == nil {
		t.Fatal("NewAgentInstance returned nil")
	}

	// The filter is kept on the instance...
	if len(agent.SkillsFilter) != 1 || agent.SkillsFilter[0] != "alpha-skill" {
		t.Fatalf("SkillsFilter = %v, want [alpha-skill]", agent.SkillsFilter)
	}

	// ...and it must be applied to the prompt.
	prompt := agent.ContextBuilder.GetInitialContext()
	if !strings.Contains(prompt, "<name>alpha-skill</name>") {
		t.Errorf("Expected alpha-skill in prompt, got names: %v", skillNamesInPrompt(t, prompt))
	}
	if strings.Contains(prompt, "<name>beta-skill</name>") {
		t.Errorf("beta-skill must be filtered out of the prompt, got names: %v", skillNamesInPrompt(t, prompt))
	}
}

// TestNewAgentInstance_NoSkillsFilterKeepsAllSkills pins the back-compat
// default: an agent without `skills:` still sees every installed skill.
func TestNewAgentInstance_NoSkillsFilterKeepsAllSkills(t *testing.T) {
	workspace, err := os.MkdirTemp("", "instance-noskills-test-*")
	if err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}
	defer os.RemoveAll(workspace)

	configDir, err := os.MkdirTemp("", "instance-noskills-config-*")
	if err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	defer os.RemoveAll(configDir)
	t.Setenv("LELE_CONFIG_DIR", configDir)

	writeTestSkill(t, workspace, "alpha-skill", "Alpha description")
	writeTestSkill(t, workspace, "beta-skill", "Beta description")

	cfg := createTestConfig(workspace)
	agent := NewAgentInstance(&config.AgentConfig{ID: "plain-agent", Workspace: workspace}, &cfg.Agents.Defaults, cfg)

	prompt := agent.ContextBuilder.GetInitialContext()
	for _, want := range []string{"<name>alpha-skill</name>", "<name>beta-skill</name>"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("Expected %s with no skills filter, got names: %v", want, skillNamesInPrompt(t, prompt))
		}
	}
}
