package group

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

// --- mocks ---

// mockResolve returns known agents "a", "b", "c" (and "agg" for moa tests).
func mockResolve(agentID string) (AgentContext, bool) {
	known := map[string]string{
		"a":   "Agent A",
		"b":   "Agent B",
		"c":   "Agent C",
		"agg": "Aggregator",
	}
	name, ok := known[agentID]
	if !ok {
		return AgentContext{}, false
	}
	return AgentContext{
		AgentID:      agentID,
		Name:         name,
		SystemPrompt: "persona of " + agentID,
	}, true
}

// mockExecutor returns deterministic content and 10 tokens per turn.
// It is thread-safe.
type mockExecutor struct {
	mu        sync.Mutex
	callCount int
}

func (e *mockExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	e.mu.Lock()
	e.callCount++
	n := e.callCount
	e.mu.Unlock()
	return fmt.Sprintf("turn-%d-%s", n, req.Speaker), 10, nil
}

// mockPublisher captures published messages. Thread-safe.
type mockPublisher struct {
	mu       sync.Mutex
	messages []bus.OutboundMessage
}

func (p *mockPublisher) publish(msg bus.OutboundMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, msg)
}

func (p *mockPublisher) byEvent(event string) []bus.OutboundMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []bus.OutboundMessage
	for _, m := range p.messages {
		if m.Event == event {
			out = append(out, m)
		}
	}
	return out
}

func (p *mockPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.messages)
}

// --- helpers ---

func proposer(id string) Participant {
	return Participant{AgentID: id, Role: RoleProposer, Label: id}
}

func aggregator(id string) Participant {
	return Participant{AgentID: id, Role: RoleAggregator, Label: id}
}

func plainParticipant(id string) Participant {
	return Participant{AgentID: id, Label: id}
}

// --- tests ---

func TestRoundRobin_BasicFlow(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
		plainParticipant("c"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "rr-1", "p1", "solve X", "round_robin",
		participants, GroupOptions{Rounds: 1}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	synthesis, err := gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if synthesis == "" {
		t.Fatal("expected non-empty synthesis")
	}

	// Verify 3 turns in order a, b, c.
	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found after Wait")
	}
	if st.Status != StatusDone {
		t.Errorf("status = %s, want done", st.Status)
	}
	if len(st.Transcript) != 3 {
		t.Fatalf("transcript length = %d, want 3", len(st.Transcript))
	}
	for i, id := range []string{"a", "b", "c"} {
		if st.Transcript[i].Speaker != id {
			t.Errorf("turn[%d].Speaker = %s, want %s", i, st.Transcript[i].Speaker, id)
		}
	}

	// Verify events: 1 started + 3 turns + 1 complete = 5.
	started := pub.byEvent("group.status")
	turns := pub.byEvent("group.turn")
	complete := pub.byEvent("group.complete")

	if len(started) < 1 || started[0].Metadata["status"] != "started" {
		t.Errorf("expected group.status started, got %v", started)
	}
	if len(turns) != 3 {
		t.Errorf("expected 3 group.turn events, got %d", len(turns))
	}
	if len(complete) != 1 {
		t.Errorf("expected 1 group.complete event, got %d", len(complete))
	}
	if complete[0].IsIntermediate {
		t.Error("group.complete should not be intermediate")
	}
}

func TestMoA_ParallelProposers(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		proposer("a"),
		proposer("b"),
		aggregator("agg"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "moa-1", "p1", "solve Y", "moa",
		participants, GroupOptions{Rounds: 1, Parallel: true}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	synthesis, err := gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if synthesis == "" {
		t.Fatal("expected non-empty synthesis")
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status != StatusDone {
		t.Errorf("status = %s, want done", st.Status)
	}

	// MoA with 2 proposers + 1 aggregator, 1 round = 3 turns total.
	if len(st.Transcript) != 3 {
		t.Fatalf("transcript length = %d, want 3", len(st.Transcript))
	}

	// Proposers (a, b) should be in layer 0, aggregator also in layer 0.
	for _, turn := range st.Transcript {
		if turn.Layer != 0 {
			t.Errorf("turn %s has layer %d, want 0", turn.Speaker, turn.Layer)
		}
	}

	// Aggregator should be last.
	last := st.Transcript[len(st.Transcript)-1]
	if last.Speaker != "agg" {
		t.Errorf("last speaker = %s, want agg", last.Speaker)
	}

	// Verify group.complete published with strategy=moa.
	complete := pub.byEvent("group.complete")
	if len(complete) != 1 {
		t.Fatalf("expected 1 group.complete, got %d", len(complete))
	}
	if complete[0].Metadata["strategy"] != "moa" {
		t.Errorf("complete strategy = %s, want moa", complete[0].Metadata["strategy"])
	}
}

// blockingExecutor blocks until unblockCh is closed or context is done.
// Thread-safe.
type blockingExecutor struct {
	unblockCh chan struct{}
}

func (e *blockingExecutor) execute(ctx context.Context, req TurnRequest) (string, int, error) {
	select {
	case <-e.unblockCh:
		return "blocked-" + req.Speaker, 10, nil
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
}

func TestStop(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
		plainParticipant("c"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "stop-1", "p1", "long task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Let the goroutine get scheduled and block on the first executor call.
	time.Sleep(50 * time.Millisecond)

	stopped := gm.Stop(groupID)
	if !stopped {
		t.Fatal("Stop returned false")
	}

	// Wait should return quickly after stop.
	done := make(chan struct{})
	go func() {
		gm.Wait(groupID)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		// Unblock if somehow stuck.
		close(be.unblockCh)
		t.Fatal("Wait did not return after Stop (deadlock?)")
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status != StatusStopped {
		t.Errorf("status = %s, want stopped", st.Status)
	}

	// Should have a stopped event.
	statuses := pub.byEvent("group.status")
	foundStopped := false
	for _, s := range statuses {
		if s.Metadata["status"] == "stopped" {
			foundStopped = true
		}
	}
	if !foundStopped {
		t.Error("expected group.status stopped event")
	}
}

func TestStart_NonExistentParticipant(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("ghost"), // not in mockResolve
	}

	_, err := gm.Start(context.Background(), "bad-1", "p1", "task", "round_robin",
		participants, GroupOptions{}, "test-ch", "test-chat")
	if err == nil {
		t.Fatal("expected error for non-existent participant")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention 'ghost': %v", err)
	}
}

func TestStopAll(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
	}

	ctx := context.Background()
	_, err := gm.Start(ctx, "g1", "p1", "task1", "round_robin",
		participants, GroupOptions{Rounds: 100}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start g1: %v", err)
	}
	_, err = gm.Start(ctx, "g2", "p1", "task2", "round_robin",
		participants, GroupOptions{Rounds: 100}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start g2: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	count := gm.StopAll()
	if count != 2 {
		t.Errorf("StopAll = %d, want 2", count)
	}

	// Wait for both to finish.
	for _, id := range []string{"g1", "g2"} {
		done := make(chan struct{})
		go func(gid string) {
			gm.Wait(gid)
			close(done)
		}(id)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			close(be.unblockCh)
			t.Fatalf("Wait for %s did not return after StopAll", id)
		}
	}

	// Both should be stopped.
	for _, id := range []string{"g1", "g2"} {
		st, ok := gm.Status(id)
		if !ok {
			t.Fatalf("group %s not found", id)
		}
		if st.Status != StatusStopped {
			t.Errorf("group %s status = %s, want stopped", id, st.Status)
		}
	}
}

func TestMaxTurns_HardStop(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "mt-1", "p1", "task", "moderator",
		participants, GroupOptions{MaxTurns: 2}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	synthesis, err := gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	if synthesis == "" {
		t.Fatal("expected non-empty synthesis")
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status != StatusDone {
		t.Errorf("status = %s, want done", st.Status)
	}

	// With MaxTurns=2, should have exactly 2 turns.
	if len(st.Transcript) != 2 {
		t.Errorf("transcript length = %d, want 2", len(st.Transcript))
	}
}

func TestWait_NotFound(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	_, err := gm.Wait("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent group")
	}
}

func TestList(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{plainParticipant("a")}

	ctx := context.Background()
	gm.Start(ctx, "l1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "ch", "chat")
	gm.Start(ctx, "l2", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "ch", "chat")

	list := gm.List()
	if len(list) != 2 {
		t.Errorf("List returned %d groups, want 2", len(list))
	}
}

func TestDuplicateGroupID(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{plainParticipant("a")}
	ctx := context.Background()

	_, err := gm.Start(ctx, "dup", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "ch", "chat")
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}

	_, err = gm.Start(ctx, "dup", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "ch", "chat")
	if err == nil {
		t.Fatal("expected error for duplicate group ID")
	}
}

func TestInvalidStrategy(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{plainParticipant("a")}
	_, err := gm.Start(context.Background(), "bad", "p1", "task", "no_such_strategy",
		participants, GroupOptions{}, "ch", "chat")
	if err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}

// slowExecutor sleeps briefly before returning, giving concurrent readers
// time to race with writer goroutines when run with -race.
type slowExecutor struct {
	delay time.Duration
}

func (e *slowExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	time.Sleep(e.delay)
	return "slow-" + req.Speaker, 10, nil
}

func TestStatusList_ConcurrentAccessNoRace(t *testing.T) {
	se := &slowExecutor{delay: 10 * time.Millisecond}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, se.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
		plainParticipant("c"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "race-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 2}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// While the group is running, hammer Status() and List() from a
	// separate goroutine. With the old code (returning the live pointer)
	// the race detector would fire.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			gs, ok := gm.Status(groupID)
			if ok {
				// Read fields that the runner mutates under lock.
				_ = gs.Status
				_ = len(gs.Transcript)
				_ = gs.TotalTokens
				_ = len(gs.Participants)
			}
			list := gm.List()
			for _, s := range list {
				_ = s.Status
				_ = len(s.Transcript)
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Wait for the group to finish.
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	// Ensure the reader goroutine completed.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("reader goroutine did not finish in time")
	}

	// Final sanity: status should be done.
	gs, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found after Wait")
	}
	if gs.Status != StatusDone {
		t.Errorf("final status = %s, want done", gs.Status)
	}
}

func TestStart_AppliesDefaultRoundsWhenUnset(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
		plainParticipant("c"),
		plainParticipant("a"), // 4th participant (duplicate agentID is fine for this test)
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "default-1", "p1", "task", "round_robin",
		participants, GroupOptions{}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer gm.Stop(groupID)

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}

	if st.Rounds != DefaultGroupRounds {
		t.Errorf("Rounds = %d, want %d (DefaultGroupRounds)", st.Rounds, DefaultGroupRounds)
	}
	// Rounds=2, 4 participants → MaxTurns = 8
	if st.MaxTurns != 8 {
		t.Errorf("MaxTurns = %d, want 8", st.MaxTurns)
	}
}

func TestStart_DerivesMaxTurnsFromRounds(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "derive-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 3}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer gm.Stop(groupID)

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}

	if st.Rounds != 3 {
		t.Errorf("Rounds = %d, want 3", st.Rounds)
	}
	// Rounds=3, 2 participants → MaxTurns = 6
	if st.MaxTurns != 6 {
		t.Errorf("MaxTurns = %d, want 6", st.MaxTurns)
	}
}

func TestStart_RespectsExplicitMaxTurns(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "explicit-1", "p1", "task", "round_robin",
		participants, GroupOptions{MaxTurns: 5}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer gm.Stop(groupID)

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}

	// MaxTurns was explicitly set to 5, Rounds was 0 → no default should apply.
	if st.MaxTurns != 5 {
		t.Errorf("MaxTurns = %d, want 5", st.MaxTurns)
	}
	if st.Rounds != 0 {
		t.Errorf("Rounds = %d, want 0 (no default applied because MaxTurns was explicit)", st.Rounds)
	}
}

func TestStart_ClampsToCeiling(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		plainParticipant("a"),
		plainParticipant("b"),
		plainParticipant("c"),
		plainParticipant("a"),
		plainParticipant("b"),
		plainParticipant("c"),
		plainParticipant("a"),
		plainParticipant("b"),
		plainParticipant("c"),
		plainParticipant("a"), // 10 participants
	}

	ctx := context.Background()
	groupID, err := gm.Start(ctx, "clamp-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer gm.Stop(groupID)

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}

	// Rounds=100, 10 participants → derived MaxTurns=1000 → clamped to ceiling.
	if st.MaxTurns != MaxGroupTurnsCeiling {
		t.Errorf("MaxTurns = %d, want %d (MaxGroupTurnsCeiling)", st.MaxTurns, MaxGroupTurnsCeiling)
	}
	// Rounds should remain 100 (we only clamp MaxTurns, not Rounds).
	if st.Rounds != 100 {
		t.Errorf("Rounds = %d, want 100", st.Rounds)
	}
}

// --- StopByOrigin tests ---

func TestStopByOrigin_StopsMatchingGroup(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)

	participants := []Participant{plainParticipant("a"), plainParticipant("b")}
	ctx := context.Background()

	groupID, err := gm.Start(ctx, "sbo-match", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "native", "tui:chat:abc")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	count := gm.StopByOrigin("native", "tui:chat:abc")
	if count != 1 {
		t.Errorf("StopByOrigin returned %d, want 1", count)
	}

	// Wait for goroutine to drain.
	done := make(chan struct{})
	go func() { gm.Wait(groupID); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(be.unblockCh)
		t.Fatal("Wait did not return after StopByOrigin")
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status != StatusStopped {
		t.Errorf("status = %s, want stopped", st.Status)
	}
}

func TestStopByOrigin_IgnoresOtherChat(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)

	participants := []Participant{plainParticipant("a")}
	ctx := context.Background()

	groupID, err := gm.Start(ctx, "sbo-ignore", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "native", "chat-A")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	count := gm.StopByOrigin("native", "chat-B")
	if count != 0 {
		t.Errorf("StopByOrigin returned %d, want 0", count)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status == StatusStopped {
		t.Error("group should NOT have been stopped for a different chatID")
	}

	// Cleanup.
	gm.StopAll()
	close(be.unblockCh)
	gm.Wait(groupID)
}

func TestStopByOrigin_EmptyChannelMatchesAny(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)

	participants := []Participant{plainParticipant("a")}
	ctx := context.Background()

	groupID, err := gm.Start(ctx, "sbo-any-ch", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "telegram", "chat-X")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Empty channel should match any origin channel.
	count := gm.StopByOrigin("", "chat-X")
	if count != 1 {
		t.Errorf("StopByOrigin returned %d, want 1", count)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status != StatusStopped {
		t.Errorf("status = %s, want stopped", st.Status)
	}

	// Drain.
	done := make(chan struct{})
	go func() { gm.Wait(groupID); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		close(be.unblockCh)
		t.Fatal("Wait did not return")
	}
}

func TestStopByOrigin_EmptyChatIDReturnsZero(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)

	participants := []Participant{plainParticipant("a")}
	ctx := context.Background()

	groupID, err := gm.Start(ctx, "sbo-empty", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 100}, "native", "chat-Z")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	count := gm.StopByOrigin("native", "")
	if count != 0 {
		t.Errorf("StopByOrigin returned %d, want 0", count)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status == StatusStopped {
		t.Error("group should NOT have been stopped by empty chatID")
	}

	// Cleanup.
	gm.StopAll()
	close(be.unblockCh)
	gm.Wait(groupID)
}

func TestStopByOrigin_StopsMultipleGroups(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)

	participants := []Participant{plainParticipant("a")}
	ctx := context.Background()

	_, err := gm.Start(ctx, "sbo-multi-1", "p1", "task1", "round_robin",
		participants, GroupOptions{Rounds: 100}, "native", "chat-multi")
	if err != nil {
		t.Fatalf("Start g1: %v", err)
	}
	_, err = gm.Start(ctx, "sbo-multi-2", "p1", "task2", "round_robin",
		participants, GroupOptions{Rounds: 100}, "native", "chat-multi")
	if err != nil {
		t.Fatalf("Start g2: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	count := gm.StopByOrigin("native", "chat-multi")
	if count != 2 {
		t.Errorf("StopByOrigin returned %d, want 2", count)
	}

	// Drain both.
	for _, id := range []string{"sbo-multi-1", "sbo-multi-2"} {
		done := make(chan struct{})
		go func(gid string) { gm.Wait(gid); close(done) }(id)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			close(be.unblockCh)
			t.Fatalf("Wait for %s did not return", id)
		}
	}

	for _, id := range []string{"sbo-multi-1", "sbo-multi-2"} {
		st, ok := gm.Status(id)
		if !ok {
			t.Fatalf("group %s not found", id)
		}
		if st.Status != StatusStopped {
			t.Errorf("group %s status = %s, want stopped", id, st.Status)
		}
	}
}
