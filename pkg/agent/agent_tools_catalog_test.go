package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/channels"
)

func TestListAgentTools(t *testing.T) {
	al, agent := newThinkLevelTestLoop(t, "")

	tools, ok := al.GetProvidable().ListAgentTools(agent.ID)
	if !ok {
		t.Fatalf("ListAgentTools(%q) ok = false, want true", agent.ID)
	}

	if len(tools) == 0 {
		t.Fatal("expected non-empty tool list for default agent")
	}
	if len(tools) != agent.Tools.Count() {
		t.Fatalf("tools = %d, want registry count %d", len(tools), agent.Tools.Count())
	}

	byName := make(map[string]channels.AgentToolInfo, len(tools))
	for i, tl := range tools {
		if tl.Name == "" {
			t.Fatalf("tools[%d] has empty name", i)
		}
		tool, found := agent.Tools.Get(tl.Name)
		if !found {
			t.Fatalf("tool %q not present in the real registry", tl.Name)
		}
		if tl.Description != tool.Description() {
			t.Fatalf("tool %q description = %q, want %q", tl.Name, tl.Description, tool.Description())
		}
		byName[tl.Name] = tl
	}
	for _, want := range []string{"read_file", "write_file", "exec"} {
		if _, found := byName[want]; !found {
			t.Fatalf("expected core tool %q in catalog, got %v", want, tools)
		}
	}

	for i := 1; i < len(tools); i++ {
		if tools[i-1].Name > tools[i].Name {
			t.Fatalf("tools not sorted: %q before %q", tools[i-1].Name, tools[i].Name)
		}
	}
}

func TestListAgentTools_UnknownAgent(t *testing.T) {
	al, _ := newThinkLevelTestLoop(t, "")

	tools, ok := al.GetProvidable().ListAgentTools("does-not-exist")
	if ok {
		t.Fatalf("ListAgentTools(unknown) ok = true, want false")
	}
	if tools != nil {
		t.Fatalf("ListAgentTools(unknown) tools = %v, want nil", tools)
	}
}
