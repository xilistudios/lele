package agent

import (
	"os"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

func TestNewAgentInstance_WiresSkillsFilterToPrompt(t *testing.T) {
	workspace, err := os.MkdirTemp("", "instance-skills-test-*")
	if err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}
	defer os.RemoveAll(workspace)

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

	if len(agent.SkillsFilter) != 1 || agent.SkillsFilter[0] != "alpha-skill" {
		t.Fatalf("SkillsFilter = %v, want [alpha-skill]", agent.SkillsFilter)
	}

	prompt := agent.ContextBuilder.GetInitialContext()
	if !strings.Contains(prompt, "<name>alpha-skill</name>") {
		t.Errorf("Expected alpha-skill in prompt, got names: %v", skillNamesInPrompt(t, prompt))
	}
	if strings.Contains(prompt, "<name>beta-skill</name>") {
		t.Errorf("beta-skill must be filtered out of the prompt, got names: %v", skillNamesInPrompt(t, prompt))
	}
}

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
