package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/skills"
)

func writeSizedTestSkill(t *testing.T, workspace, name string, bodyBytes int) {
	t.Helper()
	dir := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("Failed to create skill dir %s: %v", name, err)
	}
	content := fmt.Sprintf("---\nname: %s\ndescription: the %s skill\n---\n\n", name, name) +
		strings.Repeat("x", bodyBytes)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("Failed to write SKILL.md for %s: %v", name, err)
	}
}

func newSevenSkillBuilder(t *testing.T) *ContextBuilder {
	t.Helper()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta"} {
		writeTestSkill(t, workspace, name, "the "+name+" skill")
	}
	return NewContextBuilder(workspace)
}

func TestLoadedSkills_AreSessionScoped(t *testing.T) {
	cb := newSevenSkillBuilder(t)

	pinned, already, rejected := cb.PinSkills("chat-a", []string{"zeta"})
	if len(pinned) != 1 || pinned[0] != "zeta" || len(already) != 0 || len(rejected) != 0 {
		t.Fatalf("PinSkills = (%v, %v, %v), want zeta pinned", pinned, already, rejected)
	}

	inA := cb.BuildSystemPromptForSession("chat-a", "")
	if !strings.Contains(inA, "# Loaded Skills") || !strings.Contains(inA, "### Skill: zeta") {
		t.Fatalf("session chat-a prompt missing the pinned skill: %s", inA)
	}

	inB := cb.BuildSystemPromptForSession("chat-b", "")
	if strings.Contains(inB, "# Loaded Skills") || strings.Contains(inB, "### Skill: zeta") {
		t.Fatalf("session chat-b prompt leaked the pin loaded by chat-a: %s", inB)
	}

	global := cb.GetInitialContext()
	if strings.Contains(global, "# Loaded Skills") || strings.Contains(global, "### Skill: zeta") {
		t.Fatalf("the agent-global prompt must never carry session pins: %s", global)
	}

	if names := cb.SkillsInContext("chat-a"); !containsName(names, "zeta") {
		t.Fatalf("SkillsInContext(chat-a) = %v, want zeta", names)
	}
	if names := cb.SkillsInContext("chat-b"); containsName(names, "zeta") {
		t.Fatalf("SkillsInContext(chat-b) = %v, must not include chat-a's pin", names)
	}
}

func TestLoadedSkills_EmptyKeyBucketRenders(t *testing.T) {
	cb := newSevenSkillBuilder(t)
	cb.PinSkills("", []string{"zeta"})

	if prompt := cb.BuildSystemPromptForSession("", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("the empty session key is a first-class bucket: %s", prompt)
	}
	if prompt := cb.BuildSystemPromptForSession("chat-a", ""); strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("a pin in the empty bucket leaked into chat-a: %s", prompt)
	}
}

func TestLoadedSkills_DisabledSkillNeitherRendersNorExcludes(t *testing.T) {
	cb := newSevenSkillBuilder(t)
	cb.PinSkills("", []string{"zeta"})

	if prompt := cb.BuildSystemPromptForSession("", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("precondition: pinned skill must render: %s", prompt)
	}
	if names := cb.SkillsInContext(""); !containsName(names, "zeta") {
		t.Fatalf("precondition: SkillsInContext = %v, want zeta", names)
	}

	if err := cb.SkillsLoader().GetConfigManager().SetDisabled("zeta"); err != nil {
		t.Fatalf("Failed to disable zeta: %v", err)
	}

	if prompt := cb.BuildSystemPromptForSession("", ""); strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("a disabled skill must not render: %s", prompt)
	}
	if names := cb.SkillsInContext(""); containsName(names, "zeta") {
		t.Fatalf("SkillsInContext = %v, must not report a disabled skill it cannot render", names)
	}
	listOf := cb.SkillsInContext("")
	for _, name := range listOf {
		if name == "zeta" {
			t.Fatalf("a disabled pin must stop being excluded from list_skills: %v", listOf)
		}
	}

	if err := cb.SkillsLoader().GetConfigManager().SetEnabled("zeta"); err != nil {
		t.Fatalf("Failed to re-enable zeta: %v", err)
	}
	if prompt := cb.BuildSystemPromptForSession("", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("re-enabling must restore the pin without reloading: %s", prompt)
	}
}

func TestPinSkills_CountBudget(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	names := make([]string, 0, 9)
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("skill-%02d", i)
		names = append(names, name)
		writeTestSkill(t, workspace, name, "the "+name+" skill")
	}

	cb := NewContextBuilder(workspace)
	pinned, already, rejected := cb.PinSkills("chat-a", names)

	if len(pinned) != skills.MaxPinnedSkillsPerSession {
		t.Fatalf("pinned %d skills (%v), want the cap of %d", len(pinned), pinned, skills.MaxPinnedSkillsPerSession)
	}
	if len(already) != 0 {
		t.Fatalf("already = %v, want none", already)
	}
	if len(rejected) != 1 || rejected[0] != names[8] {
		t.Fatalf("rejected = %v, want only the ninth skill", rejected)
	}
	if got := cb.PinnedSkills("chat-a"); len(got) != skills.MaxPinnedSkillsPerSession {
		t.Fatalf("PinnedSkills = %v, want %d entries", got, skills.MaxPinnedSkillsPerSession)
	}
}

func TestPinSkills_ByteBudget(t *testing.T) {
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	workspace := t.TempDir()
	for _, name := range []string{"big-one", "big-two"} {
		writeSizedTestSkill(t, workspace, name, 16<<10)
	}
	writeSizedTestSkill(t, workspace, "small", 2<<10)

	cb := NewContextBuilder(workspace)

	pinned, _, rejected := cb.PinSkills("chat-a", []string{"big-one", "big-two"})
	if len(pinned) != 2 || len(rejected) != 0 {
		t.Fatalf("PinSkills = (%v, rejected %v), want both 16 KiB skills pinned at the exact budget", pinned, rejected)
	}

	pinned, _, rejected = cb.PinSkills("chat-a", []string{"small"})
	if len(pinned) != 0 || len(rejected) != 1 {
		t.Fatalf("PinSkills = (%v, rejected %v), want the small skill rejected over the byte budget", pinned, rejected)
	}

	removed, absent := cb.UnpinSkills("chat-a", []string{"big-one"})
	if len(removed) != 1 || len(absent) != 0 {
		t.Fatalf("UnpinSkills = (%v, %v), want big-one removed", removed, absent)
	}

	pinned, _, rejected = cb.PinSkills("chat-a", []string{"small"})
	if len(pinned) != 1 || len(rejected) != 0 {
		t.Fatalf("PinSkills = (%v, rejected %v), want the small skill pinned after unloading", pinned, rejected)
	}
}

func TestPinSkills_AlreadyPinnedIsIdempotent(t *testing.T) {
	cb := newSevenSkillBuilder(t)

	cb.PinSkills("chat-a", []string{"zeta"})
	pinned, already, rejected := cb.PinSkills("chat-a", []string{"zeta"})

	if len(pinned) != 0 || len(rejected) != 0 {
		t.Fatalf("second PinSkills = (pinned %v, rejected %v), want neither", pinned, rejected)
	}
	if len(already) != 1 || already[0] != "zeta" {
		t.Fatalf("already = %v, want [zeta]", already)
	}
	if got := cb.PinnedSkills("chat-a"); len(got) != 1 {
		t.Fatalf("PinnedSkills = %v, want the skill pinned once", got)
	}
}

func TestUnpinSkills_RemovesAndReportsAbsent(t *testing.T) {
	cb := newSevenSkillBuilder(t)
	cb.PinSkills("chat-a", []string{"zeta", "gamma"})
	cb.PinSkills("chat-b", []string{"zeta"})

	removed, absent := cb.UnpinSkills("chat-a", []string{"zeta", "ghost"})
	if len(removed) != 1 || removed[0] != "zeta" {
		t.Fatalf("removed = %v, want [zeta]", removed)
	}
	if len(absent) != 1 || absent[0] != "ghost" {
		t.Fatalf("absent = %v, want [ghost]", absent)
	}

	inA := cb.BuildSystemPromptForSession("chat-a", "")
	if strings.Contains(inA, "### Skill: zeta") {
		t.Fatalf("unloaded skill still renders in chat-a: %s", inA)
	}
	if !strings.Contains(inA, "### Skill: gamma") {
		t.Fatalf("unload removed the wrong pin: %s", inA)
	}
	if inB := cb.BuildSystemPromptForSession("chat-b", ""); !strings.Contains(inB, "### Skill: zeta") {
		t.Fatalf("unloading in chat-a must not touch chat-b: %s", inB)
	}
}

func TestClearLoadedSkills_OnlyThatSession(t *testing.T) {
	cb := newSevenSkillBuilder(t)
	cb.PinSkills("chat-a", []string{"zeta"})
	cb.PinSkills("chat-b", []string{"zeta"})

	cb.ClearLoadedSkills("chat-a")

	if got := cb.PinnedSkills("chat-a"); len(got) != 0 {
		t.Fatalf("chat-a pins = %v, want none", got)
	}
	if got := cb.PinnedSkills("chat-b"); len(got) != 1 {
		t.Fatalf("chat-b pins = %v, want the other session untouched", got)
	}
}

func TestLoadedSkills_SurvivePromptCacheInvalidation(t *testing.T) {
	cb := newSevenSkillBuilder(t)
	cb.PinSkills("", []string{"zeta"})

	cb.InvalidatePromptCache()
	if prompt := cb.BuildSystemPromptForSession("", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("pins must survive a prompt cache invalidation: %s", prompt)
	}

	cb.SetSkillsFilter(nil)
	if prompt := cb.BuildSystemPromptForSession("", ""); !strings.Contains(prompt, "### Skill: zeta") {
		t.Fatalf("pins must survive a skills filter change: %s", prompt)
	}
}

func TestHandleNewCommand_ClearsOnlyThatSessionsPins(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	writeTestSkill(t, workspace, "alpha", "the alpha skill")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil || agent.ContextBuilder == nil {
		t.Fatal("expected a default agent with a context builder")
	}
	if pinned, _, rejected := agent.ContextBuilder.PinSkills("test:new-session", []string{"alpha"}); len(pinned) != 1 || len(rejected) != 0 {
		t.Fatalf("precondition: PinSkills = (%v, rejected %v)", pinned, rejected)
	}
	if pinned, _, rejected := agent.ContextBuilder.PinSkills("other-chat", []string{"alpha"}); len(pinned) != 1 || len(rejected) != 0 {
		t.Fatalf("precondition: PinSkills = (%v, rejected %v)", pinned, rejected)
	}

	ch := newCommandHandler(al)
	ch.handleNewCommand(agent, "test:new-session")

	if got := agent.ContextBuilder.PinnedSkills("test:new-session"); len(got) != 0 {
		t.Fatalf("pins of the retired session = %v, want none", got)
	}
	if got := agent.ContextBuilder.PinnedSkills("other-chat"); len(got) != 1 {
		t.Fatalf("pins of another session = %v, want the other chat untouched", got)
	}
}

func TestResetAgentSession_ClearsThatSessionsPins(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", t.TempDir())
	writeTestSkill(t, workspace, "alpha", "the alpha skill")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}

	al := NewAgentLoop(cfg, bus.NewMessageBus())
	agent := al.registry.GetDefaultAgent()
	if agent == nil || agent.ContextBuilder == nil {
		t.Fatal("expected a default agent with a context builder")
	}

	sessionKey := "telegram:123456"
	otherKey := "telegram:654321"
	if pinned, _, rejected := agent.ContextBuilder.PinSkills(sessionKey, []string{"alpha"}); len(pinned) != 1 || len(rejected) != 0 {
		t.Fatalf("precondition: PinSkills = (%v, rejected %v)", pinned, rejected)
	}
	if pinned, _, rejected := agent.ContextBuilder.PinSkills(otherKey, []string{"alpha"}); len(pinned) != 1 || len(rejected) != 0 {
		t.Fatalf("precondition: PinSkills = (%v, rejected %v)", pinned, rejected)
	}

	if err := al.resetAgentSession(agent, sessionKey); err != nil {
		t.Fatalf("resetAgentSession: %v", err)
	}

	if got := agent.ContextBuilder.PinnedSkills(sessionKey); len(got) != 0 {
		t.Fatalf("pins after reset = %v, want none", got)
	}
	if got := agent.ContextBuilder.PinnedSkills(otherKey); len(got) != 1 {
		t.Fatalf("pins of another session = %v, want the other chat untouched", got)
	}
}

func containsName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
