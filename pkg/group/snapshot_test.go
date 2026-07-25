package group

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

// --- BuildSnapshot tests ---

func TestBuildSnapshot_MapsTurnsAndRoleMapping(t *testing.T) {
	createdAt := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
	state := &GroupState{
		ID:       "g1",
		Status:   StatusDone,
		Strategy: "round_robin",
		Participants: []Participant{
			{AgentID: "a", Role: RoleProposer, Label: "A"},
			{AgentID: "b", Role: RoleAggregator, Label: "B"},
			{AgentID: "c", Role: "", Label: "C"},
		},
		TotalTokens: 30,
		CreatedAt:   createdAt,
		Transcript: []Turn{
			{Index: 0, Layer: 0, Speaker: "a", Label: "A", Content: "hello", Tokens: 10},
			{Index: 1, Layer: 0, Speaker: "b", Label: "B", Content: "agg", Tokens: 10},
			{Index: 2, Layer: 1, Speaker: "c", Label: "C", Content: "world", Tokens: 10},
		},
		OriginChannel: "native",
		OriginChatID:  "chat-1",
	}

	snap := BuildSnapshot(state, "final synthesis")

	if snap.GroupID != "g1" {
		t.Errorf("GroupID = %s, want g1", snap.GroupID)
	}
	if snap.Status != StatusDone {
		t.Errorf("Status = %s, want done", snap.Status)
	}
	if snap.Strategy != "round_robin" {
		t.Errorf("Strategy = %s, want round_robin", snap.Strategy)
	}
	if snap.Participants != "a,b,c" {
		t.Errorf("Participants = %s, want a,b,c", snap.Participants)
	}
	if snap.TotalTokens != 30 {
		t.Errorf("TotalTokens = %d, want 30", snap.TotalTokens)
	}
	if snap.Synthesis != "final synthesis" {
		t.Errorf("Synthesis = %s, want final synthesis", snap.Synthesis)
	}
	if snap.CreatedAt != createdAt.Format(time.RFC3339) {
		t.Errorf("CreatedAt = %s, want %s", snap.CreatedAt, createdAt.Format(time.RFC3339))
	}
	if snap.OriginChannel != "native" {
		t.Errorf("OriginChannel = %s, want native", snap.OriginChannel)
	}
	if snap.OriginChatID != "chat-1" {
		t.Errorf("OriginChatID = %s, want chat-1", snap.OriginChatID)
	}

	// Layers = max layer (1) + 1 = 2.
	if snap.Layers != 2 {
		t.Errorf("Layers = %d, want 2", snap.Layers)
	}

	if len(snap.Turns) != 3 {
		t.Fatalf("turns len = %d, want 3", len(snap.Turns))
	}

	// Role mapping: proposer -> "proposer", aggregator -> "aggregator", "" -> "proposer"
	expectedRoles := []string{"proposer", "aggregator", "proposer"}
	for i, want := range expectedRoles {
		if snap.Turns[i].Role != want {
			t.Errorf("turn[%d].Role = %s, want %s", i, snap.Turns[i].Role, want)
		}
	}

	// Turn index mapping.
	for i, turn := range snap.Turns {
		if turn.TurnIndex != i {
			t.Errorf("turn[%d].TurnIndex = %d, want %d", i, turn.TurnIndex, i)
		}
	}

	// Layer mapping.
	if snap.Turns[0].Layer != 0 || snap.Turns[2].Layer != 1 {
		t.Errorf("layers not mapped correctly")
	}
}

func TestBuildSnapshot_ParticipantsJoin(t *testing.T) {
	state := &GroupState{
		ID:       "g2",
		Status:   StatusRunning,
		Strategy: "moa",
		Participants: []Participant{
			{AgentID: "x"},
		},
	}
	snap := BuildSnapshot(state, "")
	if snap.Participants != "x" {
		t.Errorf("Participants = %s, want x", snap.Participants)
	}
}

func TestBuildSnapshot_EmptyTranscript(t *testing.T) {
	state := &GroupState{
		ID:       "g3",
		Status:   StatusRunning,
		Strategy: "round_robin",
	}
	snap := BuildSnapshot(state, "synth")
	if snap.Layers != 1 {
		t.Errorf("Layers = %d, want 1 (empty transcript)", snap.Layers)
	}
	if len(snap.Turns) != 0 {
		t.Errorf("turns len = %d, want 0", len(snap.Turns))
	}
}

func TestBuildSnapshot_ToolCallsCopied(t *testing.T) {
	tc := []GroupToolCall{
		{ToolCallID: "tc1", Tool: "exec", Status: "completed", Result: "ok"},
	}
	state := &GroupState{
		ID:       "g4",
		Status:   StatusDone,
		Strategy: "round_robin",
		Participants: []Participant{
			{AgentID: "a", Role: RoleProposer},
		},
		Transcript: []Turn{
			{Index: 0, Layer: 0, Speaker: "a", Content: "used tool", ToolCalls: tc},
		},
	}

	snap := BuildSnapshot(state, "used tool")
	if len(snap.Turns) != 1 {
		t.Fatalf("turns len = %d, want 1", len(snap.Turns))
	}
	if len(snap.Turns[0].ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(snap.Turns[0].ToolCalls))
	}
	if snap.Turns[0].ToolCalls[0].ToolCallID != "tc1" {
		t.Errorf("tool_call_id = %s, want tc1", snap.Turns[0].ToolCalls[0].ToolCallID)
	}

	// Mutating the original should not affect the snapshot.
	tc[0].ToolCallID = "mutated"
	if snap.Turns[0].ToolCalls[0].ToolCallID != "tc1" {
		t.Error("snapshot shares backing array with original tool calls")
	}
}

func TestBuildSnapshot_SliceCopied(t *testing.T) {
	// Verify snapshot doesn't share backing arrays with state.
	state := &GroupState{
		ID:       "g5",
		Status:   StatusRunning,
		Strategy: "round_robin",
		Participants: []Participant{
			{AgentID: "a"},
			{AgentID: "b"},
		},
		Transcript: []Turn{
			{Index: 0, Layer: 0, Speaker: "a", Content: "first"},
		},
	}

	snap := BuildSnapshot(state, "")

	// Mutate original.
	state.Participants[0].AgentID = "mutated"
	state.Transcript[0].Content = "mutated"

	if snap.Participants == "mutated,b" {
		t.Error("snapshot shares participants backing array")
	}
	if snap.Turns[0].Content == "mutated" {
		t.Error("snapshot shares transcript backing array")
	}
}

func TestBuildSnapshot_RoleMappingAll(t *testing.T) {
	tests := []struct {
		role string
		want string
	}{
		{RoleProposer, "proposer"},
		{RoleAggregator, "aggregator"},
		{RoleModerator, "moderator"},
		{RoleCritic, "critic"},
		{"", "proposer"},
		{"unknown", "proposer"},
	}
	for _, tt := range tests {
		got := mapRole(tt.role)
		if got != tt.want {
			t.Errorf("mapRole(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}

// --- AllSnapshots tests ---

// snapshotMockResolve returns agents "a" and "b".
func snapshotMockResolve(agentID string) (AgentContext, bool) {
	known := map[string]string{"a": "A", "b": "B"}
	name, ok := known[agentID]
	if !ok {
		return AgentContext{}, false
	}
	return AgentContext{AgentID: agentID, Name: name, SystemPrompt: "p"}, true
}

// snapshotMockExecutor returns deterministic content and 10 tokens.
type snapshotMockExecutor struct {
	mu        sync.Mutex
	callCount int
}

func (e *snapshotMockExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	e.mu.Lock()
	e.callCount++
	e.mu.Unlock()
	return "turn-" + req.Speaker, 10, nil
}

// snapshotMockPublisher captures published messages.
type snapshotMockPublisher struct {
	mu       sync.Mutex
	messages []bus.OutboundMessage
}

func (p *snapshotMockPublisher) publish(msg bus.OutboundMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, msg)
}

func TestAllSnapshots_ReturnsManagedGroups(t *testing.T) {
	exec := &snapshotMockExecutor{}
	pub := &snapshotMockPublisher{}
	gm := NewGroupManager(snapshotMockResolve, exec.execute, pub.publish)

	participants := []Participant{
		{AgentID: "a", Role: RoleProposer, Label: "A"},
		{AgentID: "b", Role: RoleProposer, Label: "B"},
	}

	ctx := context.Background()

	// Start two groups.
	_, err := gm.Start(ctx, "snap-1", "p1", "task1", "round_robin",
		participants, GroupOptions{Rounds: 1}, "ch", "chat1")
	if err != nil {
		t.Fatalf("Start snap-1: %v", err)
	}
	_, err = gm.Start(ctx, "snap-2", "p1", "task2", "round_robin",
		participants, GroupOptions{Rounds: 1}, "ch", "chat2")
	if err != nil {
		t.Fatalf("Start snap-2: %v", err)
	}

	// Wait for both to finish.
	gm.Wait("snap-1")
	gm.Wait("snap-2")

	snaps := gm.AllSnapshots()
	if len(snaps) != 2 {
		t.Fatalf("AllSnapshots returned %d, want 2", len(snaps))
	}

	// Verify each snapshot has expected fields.
	ids := map[string]bool{}
	for _, s := range snaps {
		ids[s.GroupID] = true
		if s.Status != StatusDone {
			t.Errorf("group %s status = %s, want done", s.GroupID, s.Status)
		}
		if s.Synthesis == "" {
			t.Errorf("group %s synthesis is empty", s.GroupID)
		}
		if s.Participants != "a,b" {
			t.Errorf("group %s participants = %s, want a,b", s.GroupID, s.Participants)
		}
		if len(s.Turns) != 2 {
			t.Errorf("group %s turns = %d, want 2", s.GroupID, len(s.Turns))
		}
	}
	if !ids["snap-1"] || !ids["snap-2"] {
		t.Errorf("expected snap-1 and snap-2, got %v", ids)
	}
}

func TestAllSnapshots_EmptyWhenNoGroups(t *testing.T) {
	gm := NewGroupManager(snapshotMockResolve, nil, nil)
	snaps := gm.AllSnapshots()
	if len(snaps) != 0 {
		t.Errorf("AllSnapshots on empty manager returned %d, want 0", len(snaps))
	}
}

func TestAllSnapshots_OriginFields(t *testing.T) {
	exec := &snapshotMockExecutor{}
	pub := &snapshotMockPublisher{}
	gm := NewGroupManager(snapshotMockResolve, exec.execute, pub.publish)

	participants := []Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}}

	ctx := context.Background()
	_, err := gm.Start(ctx, "origin-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "my-channel", "my-chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	gm.Wait("origin-1")

	snaps := gm.AllSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("AllSnapshots returned %d, want 1", len(snaps))
	}
	if snaps[0].OriginChannel != "my-channel" {
		t.Errorf("OriginChannel = %s, want my-channel", snaps[0].OriginChannel)
	}
	if snaps[0].OriginChatID != "my-chat" {
		t.Errorf("OriginChatID = %s, want my-chat", snaps[0].OriginChatID)
	}
}

func TestAllSnapshots_ConcurrentSafety(t *testing.T) {
	exec := &snapshotMockExecutor{}
	pub := &snapshotMockPublisher{}
	gm := NewGroupManager(snapshotMockResolve, exec.execute, pub.publish)

	participants := []Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}}

	ctx := context.Background()
	_, err := gm.Start(ctx, "conc-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Concurrent AllSnapshots while group is running.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			snaps := gm.AllSnapshots()
			if len(snaps) == 0 {
				// Might be empty if called before group is registered.
			}
		}()
	}
	wg.Wait()

	gm.Wait("conc-1")
	snaps := gm.AllSnapshots()
	if len(snaps) != 1 {
		t.Errorf("AllSnapshots after wait = %d, want 1", len(snaps))
	}
}

func TestAllSnapshots_StoppedGroup(t *testing.T) {
	be := &blockingSnapshotExecutor{unblockCh: make(chan struct{})}
	pub := &snapshotMockPublisher{}
	gm := NewGroupManager(snapshotMockResolve, be.execute, pub.publish)

	participants := []Participant{
		{AgentID: "a", Role: RoleProposer, Label: "A"},
		{AgentID: "b", Role: RoleProposer, Label: "B"},
	}

	ctx := context.Background()
	_, err := gm.Start(ctx, "stopped-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	gm.Stop("stopped-1")

	// Wait for it to finish.
	done := make(chan struct{})
	go func() { gm.Wait("stopped-1"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(be.unblockCh)
		t.Fatal("Wait did not return")
	}

	snaps := gm.AllSnapshots()
	if len(snaps) != 1 {
		t.Fatalf("AllSnapshots = %d, want 1", len(snaps))
	}
	if snaps[0].Status != StatusStopped {
		t.Errorf("status = %s, want stopped", snaps[0].Status)
	}
}

// blockingSnapshotExecutor blocks until unblockCh is closed.
type blockingSnapshotExecutor struct {
	unblockCh chan struct{}
}

func (e *blockingSnapshotExecutor) execute(ctx context.Context, req TurnRequest) (string, int, error) {
	select {
	case <-e.unblockCh:
		return "blocked-" + req.Speaker, 10, nil
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
}
