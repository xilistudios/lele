package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

func TestStopAgent_StopsActiveGroup(t *testing.T) {
	defs := []e2eAgentDef{
		{id: "agent-a", name: "Agent A", response: "response-A"},
	}
	al, _, _ := buildE2EAgentLoop(t, defs)

	blocking := &llmRunnerMockLLMProvider{
		onChatCalled: func(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	agent, _ := al.registry.GetAgent("agent-a")
	agent.Provider = blocking

	sessionKey := "test:chat123"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	participants := []group.Participant{
		{AgentID: "agent-a", Label: "Agent A"},
	}

	groupID, err := al.GroupManager().Start(ctx,
		"test-group-stop", "", "test objective", "round_robin",
		participants,
		group.GroupOptions{Rounds: 100},
		"test", sessionKey,
	)
	if err != nil {
		t.Fatalf("GroupManager.Start failed: %v", err)
	}

	state, ok := al.GroupManager().Status(groupID)
	if !ok || state.Status != group.StatusRunning {
		t.Fatalf("group should be running, got status=%q found=%v", state.Status, ok)
	}

	response := al.providable.StopAgent(sessionKey)

	if !strings.Contains(response, "Agente detenido") {
		t.Errorf("response should contain 'Agente detenido', got: %s", response)
	}
	if !strings.Contains(response, "grupo(s)") {
		t.Errorf("response should mention groups, got: %s", response)
	}

	time.Sleep(200 * time.Millisecond)

	state, ok = al.GroupManager().Status(groupID)
	if !ok {
		t.Fatal("group should still be tracked after stop")
	}
	if state.Status != group.StatusStopped {
		t.Errorf("group status after StopAgent = %q, want %q", state.Status, group.StatusStopped)
	}
}

func TestStopAgent_ResponseMessageFormatting(t *testing.T) {
	cases := []struct {
		name           string
		subagentCount  int
		groupCount     int
		procCount      int
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:          "all zero",
			subagentCount: 0,
			groupCount:    0,
			procCount:     0,
			wantContains:  []string{"Agente detenido"},
		},
		{
			name:           "subagents only",
			subagentCount:  3,
			groupCount:     0,
			wantContains:   []string{"Agente detenido", "subagente(s)"},
			wantNotContain: []string{"grupo(s)", "proceso(s)"},
		},
		{
			name:           "groups only",
			subagentCount:  0,
			groupCount:     2,
			wantContains:   []string{"Agente detenido", "grupo(s)"},
			wantNotContain: []string{"subagente(s)"},
		},
		{
			name:           "processes only",
			subagentCount:  0,
			groupCount:     0,
			procCount:      1,
			wantContains:   []string{"Agente detenido", "proceso(s) en segundo plano"},
			wantNotContain: []string{"subagente(s)", "grupo(s)"},
		},
		{
			name:          "all three",
			subagentCount: 4,
			groupCount:    1,
			procCount:     2,
			wantContains:  []string{"Agente detenido", "subagente(s)", "grupo(s)", "proceso(s) en segundo plano"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatStopAgentResponse(tc.subagentCount, tc.groupCount, tc.procCount)
			if !strings.Contains(result, "Agente detenido") {
				t.Errorf("response should contain 'Agente detenido', got: %s", result)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(result, want) {
					t.Errorf("response should contain %q, got: %s", want, result)
				}
			}
			for _, avoid := range tc.wantNotContain {
				if strings.Contains(result, avoid) {
					t.Errorf("response should NOT contain %q, got: %s", avoid, result)
				}
			}
			if tc.subagentCount == 0 && tc.groupCount == 0 && tc.procCount == 0 {
				if result != "⏹️ Agente detenido." {
					t.Errorf("exact response = %q, want %q", result, "⏹️ Agente detenido.")
				}
			}
		})
	}
}
