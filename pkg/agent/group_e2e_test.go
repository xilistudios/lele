package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/group"
	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/tools"
)

type e2eAgentDef struct {
	id       string
	name     string
	response string
}

func buildE2EAgentLoop(t *testing.T, defs []e2eAgentDef) (
	al *AgentLoop,
	published *[]bus.OutboundMessage,
	pubMu *sync.Mutex,
) {
	t.Helper()

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	t.Cleanup(func() { _ = tmpDir })

	for _, d := range defs {
		agent := newE2EAgentInstance(t, tmpDir, d.id, d.name, d.response)
		al.registry.mu.Lock()
		al.registry.agents[d.id] = agent
		al.registry.mu.Unlock()
	}

	var msgs []bus.OutboundMessage
	var mu sync.Mutex
	published = &msgs
	pubMu = &mu

	return
}

func newE2EAgentInstance(t *testing.T, tmpDir, id, name, response string) *AgentInstance {
	t.Helper()

	_ = session.NewSessionManager()

	toolRegistry := tools.NewToolRegistry()
	cb := NewContextBuilder(tmpDir)
	cb.SetToolsRegistry(toolRegistry)

	return &AgentInstance{
		ID:             id,
		Name:           name,
		Model:          "test-model",
		Workspace:      tmpDir,
		MaxIterations:  10,
		MaxTokens:      4096,
		Temperature:    0.7,
		ContextWindow:  128000,
		Provider:       &llmRunnerMockLLMProvider{response: fixedResponse(response)},
		Sessions:       session.NewSessionManager(),
		ContextBuilder: cb,
		Tools:          toolRegistry,
		Candidates:     []providers.FallbackCandidate{},
	}
}

func fixedResponse(content string) *providers.LLMResponse {
	return &providers.LLMResponse{
		Content:   content,
		ToolCalls: []providers.ToolCall{},
		Usage:     &providers.UsageInfo{TotalTokens: 10},
	}
}

func buildGroupManager(
	al *AgentLoop,
	published *[]bus.OutboundMessage,
	pubMu *sync.Mutex,
) *group.GroupManager {

	resolve := func(agentID string) (group.AgentContext, bool) {
		agent, ok := al.registry.GetAgent(agentID)
		if !ok || agent == nil {
			return group.AgentContext{}, false
		}
		persona := ""
		if agent.ContextBuilder != nil {
			persona = agent.ContextBuilder.GetInitialContext()
		}
		name := agent.Name
		if name == "" {
			name = agent.ID
		}
		return group.AgentContext{
			AgentID:       agent.ID,
			Name:          name,
			Workspace:     agent.Workspace,
			SystemPrompt:  persona,
			ContextWindow: agent.ContextWindow,
			MaxTokens:     agent.MaxTokens,
		}, true
	}

	executor := func(ctx context.Context, req group.TurnRequest) (string, int, error) {
		lr := newLLMRunner(al)
		return lr.runGroupTurn(ctx, req)
	}

	publisher := func(msg bus.OutboundMessage) {
		pubMu.Lock()
		*published = append(*published, msg)
		pubMu.Unlock()
	}

	return group.NewGroupManager(resolve, executor, publisher)
}

func publishedEvents(published *[]bus.OutboundMessage, pubMu *sync.Mutex) []bus.OutboundMessage {
	pubMu.Lock()
	defer pubMu.Unlock()
	out := make([]bus.OutboundMessage, len(*published))
	copy(out, *published)
	return out
}

func countEvents(events []bus.OutboundMessage, event string) int {
	n := 0
	for _, e := range events {
		if e.Event == event {
			n++
		}
	}
	return n
}

func filterEvents(events []bus.OutboundMessage, event string) []bus.OutboundMessage {
	var out []bus.OutboundMessage
	for _, e := range events {
		if e.Event == event {
			out = append(out, e)
		}
	}
	return out
}

func TestGroupE2E_RoundRobin(t *testing.T) {
	defs := []e2eAgentDef{
		{id: "agent-a", name: "Agent A", response: "respuesta-de-A"},
		{id: "agent-b", name: "Agent B", response: "respuesta-de-B"},
		{id: "agent-c", name: "Agent C", response: "respuesta-de-C"},
	}
	al, published, pubMu := buildE2EAgentLoop(t, defs)
	gm := buildGroupManager(al, published, pubMu)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	participants := []group.Participant{
		{AgentID: "agent-a", Label: "Agent A"},
		{AgentID: "agent-b", Label: "Agent B"},
		{AgentID: "agent-c", Label: "Agent C"},
	}

	groupID, err := gm.Start(ctx,
		"g-e2e-rr", "", "test objective", "round_robin",
		participants,
		group.GroupOptions{Rounds: 1, MaxTurns: 0},
		"test", "0",
	)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	synthesis, err := gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	state, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("Status: group not found")
	}
	if state.Status != group.StatusDone {
		t.Errorf("status = %q, want %q", state.Status, group.StatusDone)
	}
	if len(state.Transcript) != 3 {
		t.Fatalf("transcript length = %d, want 3", len(state.Transcript))
	}

	expectedSpeakers := []string{"agent-a", "agent-b", "agent-c"}
	expectedContent := []string{"respuesta-de-A", "respuesta-de-B", "respuesta-de-C"}
	for i, turn := range state.Transcript {
		if turn.Speaker != expectedSpeakers[i] {
			t.Errorf("turn[%d].Speaker = %q, want %q", i, turn.Speaker, expectedSpeakers[i])
		}
		if turn.Content != expectedContent[i] {
			t.Errorf("turn[%d].Content = %q, want %q", i, turn.Content, expectedContent[i])
		}
	}

	if synthesis != "respuesta-de-C" {
		t.Errorf("synthesis = %q, want %q", synthesis, "respuesta-de-C")
	}

	if state.TotalTokens <= 0 {
		t.Errorf("TotalTokens = %d, want > 0", state.TotalTokens)
	}

	events := publishedEvents(published, pubMu)
	if c := countEvents(events, "group.status"); c < 1 {
		t.Errorf("group.status events = %d, want >= 1", c)
	}
	if c := countEvents(events, "group.turn"); c != 3 {
		t.Errorf("group.turn events = %d, want 3", c)
	}
	if c := countEvents(events, "group.complete"); c != 1 {
		t.Errorf("group.complete events = %d, want 1", c)
	}

	turnEvents := filterEvents(events, "group.turn")
	for i, te := range turnEvents {
		wantSpeaker := expectedSpeakers[i]
		if te.Metadata["speaker"] != wantSpeaker {
			t.Errorf("turn event[%d].speaker = %q, want %q", i, te.Metadata["speaker"], wantSpeaker)
		}
		if te.Metadata["label"] == "" {
			t.Errorf("turn event[%d].label is empty", i)
		}
	}
}

func TestGroupE2E_MoA(t *testing.T) {
	defs := []e2eAgentDef{
		{id: "agent-a", name: "Agent A", response: "proposal-A"},
		{id: "agent-b", name: "Agent B", response: "proposal-B"},
		{id: "agent-c", name: "Agent C", response: "moa-synthesis-C"},
	}
	al, published, pubMu := buildE2EAgentLoop(t, defs)
	gm := buildGroupManager(al, published, pubMu)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	participants := []group.Participant{
		{AgentID: "agent-a", Role: group.RoleProposer, Label: "Agent A"},
		{AgentID: "agent-b", Role: group.RoleProposer, Label: "Agent B"},
		{AgentID: "agent-c", Role: group.RoleAggregator, Label: "Agent C"},
	}

	groupID, err := gm.Start(ctx,
		"g-e2e-moa", "", "test objective", "moa",
		participants,
		group.GroupOptions{Rounds: 1, Parallel: true, Moderator: "agent-c"},
		"test", "0",
	)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	synthesis, err := gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	state, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("Status: group not found")
	}
	if state.Status != group.StatusDone {
		t.Errorf("status = %q, want %q", state.Status, group.StatusDone)
	}

	if len(state.Transcript) != 3 {
		t.Fatalf("transcript length = %d, want 3", len(state.Transcript))
	}

	last := state.Transcript[2]
	if last.Speaker != "agent-c" {
		t.Errorf("last turn speaker = %q, want %q (aggregator)", last.Speaker, "agent-c")
	}

	proposerSpeakers := map[string]bool{
		state.Transcript[0].Speaker: true,
		state.Transcript[1].Speaker: true,
	}
	if !proposerSpeakers["agent-a"] || !proposerSpeakers["agent-b"] {
		t.Errorf("first two turns should be proposers agent-a and agent-b, got %q and %q",
			state.Transcript[0].Speaker, state.Transcript[1].Speaker)
	}

	for i, turn := range state.Transcript {
		if turn.Layer != 0 {
			t.Errorf("turn[%d].Layer = %d, want 0", i, turn.Layer)
		}
	}

	if synthesis != "moa-synthesis-C" {
		t.Errorf("synthesis = %q, want %q", synthesis, "moa-synthesis-C")
	}

	if state.TotalTokens <= 0 {
		t.Errorf("TotalTokens = %d, want > 0", state.TotalTokens)
	}

	events := publishedEvents(published, pubMu)
	if c := countEvents(events, "group.complete"); c != 1 {
		t.Errorf("group.complete events = %d, want 1", c)
	}

	completeEvents := filterEvents(events, "group.complete")
	if len(completeEvents) > 0 {
		if completeEvents[0].Metadata["strategy"] != "moa" {
			t.Errorf("group.complete strategy = %q, want %q",
				completeEvents[0].Metadata["strategy"], "moa")
		}
		if completeEvents[0].Metadata["layers"] == "" {
			t.Error("group.complete layers metadata is empty")
		}
	}
}

func TestGroupE2E_Persistence(t *testing.T) {
	defs := []e2eAgentDef{
		{id: "agent-a", name: "Agent A", response: "p-A"},
		{id: "agent-b", name: "Agent B", response: "p-B"},
		{id: "agent-c", name: "Agent C", response: "p-C"},
	}
	al, published, pubMu := buildE2EAgentLoop(t, defs)
	gm := buildGroupManager(al, published, pubMu)

	storeDir := t.TempDir()
	gm.SetStoreDir(storeDir)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	participants := []group.Participant{
		{AgentID: "agent-a", Label: "Agent A"},
		{AgentID: "agent-b", Label: "Agent B"},
		{AgentID: "agent-c", Label: "Agent C"},
	}

	groupID := "g-e2e-persist"
	_, err := gm.Start(ctx,
		groupID, "", "persistence test", "round_robin",
		participants,
		group.GroupOptions{Rounds: 1},
		"test", "0",
	)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	_, err = gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	loaded, err := group.LoadGroup(storeDir, groupID)
	if err != nil {
		t.Fatalf("LoadGroup failed: %v", err)
	}

	if loaded.Status != group.StatusDone {
		t.Errorf("loaded status = %q, want %q", loaded.Status, group.StatusDone)
	}
	if len(loaded.Transcript) != 3 {
		t.Fatalf("loaded transcript length = %d, want 3", len(loaded.Transcript))
	}

	expectedContent := []string{"p-A", "p-B", "p-C"}
	for i, turn := range loaded.Transcript {
		if turn.Content != expectedContent[i] {
			t.Errorf("loaded turn[%d].Content = %q, want %q", i, turn.Content, expectedContent[i])
		}
	}

	if loaded.TotalTokens <= 0 {
		t.Errorf("loaded TotalTokens = %d, want > 0", loaded.TotalTokens)
	}
}

func TestGroupE2E_MaxTurnsSafetyNet(t *testing.T) {
	defs := []e2eAgentDef{
		{id: "agent-a", name: "Agent A", response: "mod-A"},
		{id: "agent-b", name: "Agent B", response: "mod-B"},
	}
	al, published, pubMu := buildE2EAgentLoop(t, defs)
	gm := buildGroupManager(al, published, pubMu)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	participants := []group.Participant{
		{AgentID: "agent-a", Label: "Agent A"},
		{AgentID: "agent-b", Label: "Agent B"},
	}

	groupID, err := gm.Start(ctx,
		"g-e2e-maxturns", "", "safety net test", "moderator",
		participants,
		group.GroupOptions{MaxTurns: 2},
		"test", "0",
	)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	synthesis, err := gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	state, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("Status: group not found")
	}
	if state.Status != group.StatusDone {
		t.Errorf("status = %q, want %q", state.Status, group.StatusDone)
	}

	if len(state.Transcript) > 2 {
		t.Errorf("transcript length = %d, want <= 2 (MaxTurns)", len(state.Transcript))
	}
	if len(state.Transcript) == 0 {
		t.Fatal("transcript is empty — expected at least 1 turn")
	}

	_ = synthesis

	events := publishedEvents(published, pubMu)
	if c := countEvents(events, "group.complete"); c != 1 {
		t.Errorf("group.complete events = %d, want 1", c)
	}
}

func TestGroupE2E_ContextTimeout(t *testing.T) {
	defs := []e2eAgentDef{
		{id: "agent-a", name: "Agent A", response: "timeout-A"},
	}
	al, published, pubMu := buildE2EAgentLoop(t, defs)
	gm := buildGroupManager(al, published, pubMu)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	participants := []group.Participant{
		{AgentID: "agent-a", Label: "Agent A"},
	}

	groupID, err := gm.Start(ctx,
		"g-e2e-timeout", "", "timeout test", "round_robin",
		participants,
		group.GroupOptions{Rounds: 100},
		"test", "0",
	)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_, _ = gm.Wait(groupID)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after context cancellation — possible deadlock")
	}
}

var (
	_ = fmt.Sprintf
	_ = bus.OutboundMessage{}
	_ = providers.LLMResponse{}
	_ = tools.NewToolRegistry
	_ = session.NewSessionManager
	_ = group.GroupOptions{}
)
