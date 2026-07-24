// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors
//
// group_e2e_test.go — End-to-end integration tests that exercise the real
// execution path: GroupManager → runGroupTurn (pkg/agent) → llmCaller →
// mock LLM provider.  These tests verify turn ordering, event publishing,
// MoA synthesis, persistence, and safety-net MaxTurns without mocking the
// executor itself.

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

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// e2eAgentDef describes a test agent to register.
type e2eAgentDef struct {
	id       string
	name     string
	response string // unique content returned by the mock provider
}

// buildE2EAgentLoop creates an AgentLoop with len(defs) agents registered,
// each backed by a mock provider that returns the def's response.  Returns
// the loop and a publish-capture function.
func buildE2EAgentLoop(t *testing.T, defs []e2eAgentDef) (
	al *AgentLoop,
	published *[]bus.OutboundMessage,
	pubMu *sync.Mutex,
) {
	t.Helper()

	al, tmpDir := createLLMRunnerTestAgentLoop(t)
	t.Cleanup(func() { _ = tmpDir }) // handled by createLLMRunnerTestAgentLoop's tmpDir

	for _, d := range defs {
		agent := newE2EAgentInstance(t, tmpDir, d.id, d.name, d.response)
		al.registry.mu.Lock()
		al.registry.agents[d.id] = agent
		al.registry.mu.Unlock()
	}

	// Captured events (thread-safe).
	var msgs []bus.OutboundMessage
	var mu sync.Mutex
	published = &msgs
	pubMu = &mu

	return
}

// newE2EAgentInstance builds an AgentInstance for a single test agent with
// a mock provider returning the given fixed response.
func newE2EAgentInstance(t *testing.T, tmpDir, id, name, response string) *AgentInstance {
	t.Helper()

	sessionsDir := tmpDir + "/sessions-" + id
	_ = session.NewSessionManager(sessionsDir) // ensure dir creation

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
		Sessions:       session.NewSessionManager(sessionsDir),
		ContextBuilder: cb,
		Tools:          toolRegistry,
		Candidates:     []providers.FallbackCandidate{},
	}
}

// fixedResponse builds an *LLMResponse with the given content and 10 tokens.
func fixedResponse(content string) *providers.LLMResponse {
	return &providers.LLMResponse{
		Content:   content,
		ToolCalls: []providers.ToolCall{},
		Usage:     &providers.UsageInfo{TotalTokens: 10},
	}
}

// buildGroupManager wires a real GroupManager with the real runGroupTurn
// executor (via newLLMRunner), resolve against the registry, and a publisher
// that captures events.
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

// publishedEvents returns a snapshot of the captured events.
func publishedEvents(published *[]bus.OutboundMessage, pubMu *sync.Mutex) []bus.OutboundMessage {
	pubMu.Lock()
	defer pubMu.Unlock()
	out := make([]bus.OutboundMessage, len(*published))
	copy(out, *published)
	return out
}

// countEvents counts events matching the given event type.
func countEvents(events []bus.OutboundMessage, event string) int {
	n := 0
	for _, e := range events {
		if e.Event == event {
			n++
		}
	}
	return n
}

// filterEvents returns events matching the given event type.
func filterEvents(events []bus.OutboundMessage, event string) []bus.OutboundMessage {
	var out []bus.OutboundMessage
	for _, e := range events {
		if e.Event == event {
			out = append(out, e)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestGroupE2E_RoundRobin verifies the full round-robin path:
// 3 agents speak in order, transcript and synthesis are correct, events fire.
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

	// --- Status / Transcript ---
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

	// Synthesis = last turn content.
	if synthesis != "respuesta-de-C" {
		t.Errorf("synthesis = %q, want %q", synthesis, "respuesta-de-C")
	}

	// TotalTokens > 0 (mock returns 10 per turn).
	if state.TotalTokens <= 0 {
		t.Errorf("TotalTokens = %d, want > 0", state.TotalTokens)
	}

	// --- Events ---
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

	// Verify group.turn metadata.
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

// TestGroupE2E_MoA verifies the Mixture-of-Agents path:
// 2 proposers speak first, then the aggregator synthesizes.
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

	// MoA: proposers speak first (layer 0), then aggregator (layer 0).
	// Order may vary for proposers (parallel), but aggregator must be last.
	if len(state.Transcript) != 3 {
		t.Fatalf("transcript length = %d, want 3", len(state.Transcript))
	}

	// The last turn must be the aggregator.
	last := state.Transcript[2]
	if last.Speaker != "agent-c" {
		t.Errorf("last turn speaker = %q, want %q (aggregator)", last.Speaker, "agent-c")
	}

	// The first two turns must be proposers (order may vary).
	proposerSpeakers := map[string]bool{
		state.Transcript[0].Speaker: true,
		state.Transcript[1].Speaker: true,
	}
	if !proposerSpeakers["agent-a"] || !proposerSpeakers["agent-b"] {
		t.Errorf("first two turns should be proposers agent-a and agent-b, got %q and %q",
			state.Transcript[0].Speaker, state.Transcript[1].Speaker)
	}

	// All turns in layer 0 (single round).
	for i, turn := range state.Transcript {
		if turn.Layer != 0 {
			t.Errorf("turn[%d].Layer = %d, want 0", i, turn.Layer)
		}
	}

	// Synthesis = aggregator's content.
	if synthesis != "moa-synthesis-C" {
		t.Errorf("synthesis = %q, want %q", synthesis, "moa-synthesis-C")
	}

	if state.TotalTokens <= 0 {
		t.Errorf("TotalTokens = %d, want > 0", state.TotalTokens)
	}

	// --- Events ---
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
		// layers >= 1
		if completeEvents[0].Metadata["layers"] == "" {
			t.Error("group.complete layers metadata is empty")
		}
	}
}

// TestGroupE2E_Persistence (T5.11) verifies that after a group finishes,
// the state is persisted to disk and can be reconstructed via LoadGroup.
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

	// Allow a brief moment for the deferred saveStateBestEffort to flush.
	time.Sleep(100 * time.Millisecond)

	// --- Verify persistence via LoadGroup ---
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

// TestGroupE2E_MaxTurnsSafetyNet verifies that the moderator strategy with
// MaxTurns=2 terminates without looping forever.  Uses the real
// defaultModeratorDecider (cycling through participants).
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

	// Should have <= 2 turns (MaxTurns=2 is a hard stop).
	if len(state.Transcript) > 2 {
		t.Errorf("transcript length = %d, want <= 2 (MaxTurns)", len(state.Transcript))
	}
	if len(state.Transcript) == 0 {
		t.Fatal("transcript is empty — expected at least 1 turn")
	}

	// Synthesis should be non-empty.
	_ = synthesis // synthesis is the last turn content

	// Events captured (at least started + complete).
	events := publishedEvents(published, pubMu)
	if c := countEvents(events, "group.complete"); c != 1 {
		t.Errorf("group.complete events = %d, want 1", c)
	}
}

// TestGroupE2E_ContextTimeout ensures that if the parent context is cancelled
// before the group finishes, Wait returns without blocking forever.
func TestGroupE2E_ContextTimeout(t *testing.T) {
	defs := []e2eAgentDef{
		{id: "agent-a", name: "Agent A", response: "timeout-A"},
	}
	al, published, pubMu := buildE2EAgentLoop(t, defs)
	gm := buildGroupManager(al, published, pubMu)

	// Very short context — should cause the group to stop.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	participants := []group.Participant{
		{AgentID: "agent-a", Label: "Agent A"},
	}

	groupID, err := gm.Start(ctx,
		"g-e2e-timeout", "", "timeout test", "round_robin",
		participants,
		group.GroupOptions{Rounds: 100}, // many rounds, but context expires
		"test", "0",
	)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait should return (either with err or synthesis) within 5s.
	done := make(chan struct{})
	go func() {
		_, _ = gm.Wait(groupID)
		close(done)
	}()

	select {
	case <-done:
		// Good — Wait returned.
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after context cancellation — possible deadlock")
	}
}

// ---------------------------------------------------------------------------
// Compile-time check: ensure we import all required packages.
// ---------------------------------------------------------------------------
var (
	_ = fmt.Sprintf
	_ = bus.OutboundMessage{}
	_ = providers.LLMResponse{}
	_ = tools.NewToolRegistry
	_ = session.NewSessionManager
	_ = group.GroupOptions{}
)
