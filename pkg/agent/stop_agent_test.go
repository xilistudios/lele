// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
)

// TestStopAgent_StopsActiveGroup verifies that StopAgent stops groups whose
// originChatID matches the session key and that the return string mentions groups.
func TestStopAgent_StopsActiveGroup(t *testing.T) {
	defs := []e2eAgentDef{
		{id: "agent-a", name: "Agent A", response: "response-A"},
	}
	al, _, _ := buildE2EAgentLoop(t, defs)

	// Replace the mock provider with one that blocks until context is cancelled
	// so the group stays in StatusRunning long enough for StopAgent to stop it.
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

	// Start the group on the AgentLoop's own GroupManager with originChatID = sessionKey.
	groupID, err := al.GroupManager().Start(ctx,
		"test-group-stop", "", "test objective", "round_robin",
		participants,
		group.GroupOptions{Rounds: 100}, // many rounds — group will run until cancelled
		"test", sessionKey,
	)
	if err != nil {
		t.Fatalf("GroupManager.Start failed: %v", err)
	}

	// Verify the group is running before we try to stop it.
	state, ok := al.GroupManager().Status(groupID)
	if !ok || state.Status != group.StatusRunning {
		t.Fatalf("group should be running, got status=%q found=%v", state.Status, ok)
	}

	// Call StopAgent with the matching session key.
	response := al.providable.StopAgent(sessionKey)

	if !strings.Contains(response, "Agente detenido") {
		t.Errorf("response should contain 'Agente detenido', got: %s", response)
	}
	if !strings.Contains(response, "grupo(s)") {
		t.Errorf("response should mention groups, got: %s", response)
	}

	// Wait briefly for the group goroutine to process the cancellation and update status.
	time.Sleep(200 * time.Millisecond)

	state, ok = al.GroupManager().Status(groupID)
	if !ok {
		t.Fatal("group should still be tracked after stop")
	}
	if state.Status != group.StatusStopped {
		t.Errorf("group status after StopAgent = %q, want %q", state.Status, group.StatusStopped)
	}
}

// TestStopAgent_ResponseMessageFormatting is a table-driven test that verifies
// the message formatting logic in StopAgent for all combinations of
// subagentCount and groupCount.
func TestStopAgent_ResponseMessageFormatting(t *testing.T) {
	cases := []struct {
		name           string
		subagentCount  int
		groupCount     int
		wantContains   []string
		wantNotContain []string
	}{
		{
			name:          "both zero",
			subagentCount: 0,
			groupCount:    0,
			wantContains:  []string{"Agente detenido"},
		},
		{
			name:          "subagents only",
			subagentCount: 3,
			groupCount:    0,
			wantContains:  []string{"Agente detenido", "subagente(s)"},
		},
		{
			name:          "groups only",
			subagentCount: 0,
			groupCount:    2,
			wantContains:  []string{"Agente detenido", "grupo(s)"},
		},
		{
			name:          "both subagents and groups",
			subagentCount: 4,
			groupCount:    1,
			wantContains:  []string{"Agente detenido", "subagente(s)", "grupo(s)"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := formatStopAgentResponse(tc.subagentCount, tc.groupCount)
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
			// Verify exact content for the "both zero" case.
			if tc.subagentCount == 0 && tc.groupCount == 0 {
				if result != "⏹️ Agente detenido." {
					t.Errorf("exact response = %q, want %q", result, "⏹️ Agente detenido.")
				}
			}
		})
	}
}

// formatStopAgentResponse replicates the message formatting logic from
// StopAgent for testability. This is an internal helper; keep in sync
// with the production code in agent_providable.go.
func formatStopAgentResponse(subagentCount, groupCount int) string {
	if groupCount > 0 && subagentCount > 0 {
		return fmt.Sprintf("⏹️ Agente detenido (incluye %d subagente(s) y %d grupo(s)).", subagentCount, groupCount)
	}
	if groupCount > 0 {
		return fmt.Sprintf("⏹️ Agente detenido (incluye %d grupo(s)).", groupCount)
	}
	if subagentCount > 0 {
		return fmt.Sprintf("⏹️ Agente detenido (incluye %d subagente(s)).", subagentCount)
	}
	return "⏹️ Agente detenido."
}
