package group

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

// Regression tests for B5: Turn.Index collided under Parallel execution.
//
// Before the fix the index was computed as len(state.Transcript) when the turn
// was appended. With state.Parallel the speakers of a batch run concurrently
// and append in completion order, so two turns could end up with the same
// index, and the turn_index a client saw on group.tool (computed at turn
// start) could differ from the one on group.turn (computed at append time).
// The frontend dedupes group.turn by turnIndex, so a collision silently drops
// turns.
//
// The fix reserves the index from GroupState.NextTurnIndex inside prepareTurn
// (under gm.mu) before the speaker runs; recordTurn stores that reserved index
// verbatim. These tests pin the contract: unique indices, full coverage of the
// index domain, and group.tool/group.turn agreement per speaker.

// --- helpers ---

// indexedTurnPub is a thread-safe event recorder that keeps group.turn and
// group.tool events in publication order.
type indexedTurnPub struct {
	mu       sync.Mutex
	turns    []bus.OutboundMessage
	tools    []bus.OutboundMessage
	received chan struct{} // signalled (non-blocking) on every event
}

func newIndexedTurnPub() *indexedTurnPub {
	return &indexedTurnPub{received: make(chan struct{}, 1024)}
}

func (p *indexedTurnPub) publish(msg bus.OutboundMessage) {
	p.mu.Lock()
	switch msg.Event {
	case "group.turn":
		p.turns = append(p.turns, msg)
	case "group.tool":
		p.tools = append(p.tools, msg)
	}
	p.mu.Unlock()
	select {
	case p.received <- struct{}{}:
	default:
	}
}

func (p *indexedTurnPub) snapshotTurns() []bus.OutboundMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]bus.OutboundMessage, len(p.turns))
	copy(out, p.turns)
	return out
}

func (p *indexedTurnPub) snapshotTools() []bus.OutboundMessage {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]bus.OutboundMessage, len(p.tools))
	copy(out, p.tools)
	return out
}

// turnIndexOf reads the turn_index metadata of an event.
func turnIndexOf(t *testing.T, msg bus.OutboundMessage) int {
	t.Helper()
	raw, ok := msg.Metadata["turn_index"]
	if !ok {
		t.Fatalf("event %q has no turn_index metadata", msg.Event)
	}
	idx, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("turn_index %q is not an int: %v", raw, err)
	}
	return idx
}

// delayToolExecutor sleeps a per-speaker duration (to force an inverted
// completion order under Parallel) and emits one tool call pair tagged with
// the speaker, so group.tool events can be matched against group.turn events.
type delayToolExecutor struct {
	delay map[string]time.Duration
}

func (e *delayToolExecutor) execute(ctx context.Context, req TurnRequest) (string, int, error) {
	if d := e.delay[req.Speaker]; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return "", 0, ctx.Err()
		}
	}
	if req.OnToolCall != nil {
		id := "call-" + req.Speaker
		req.OnToolCall(id, "some_tool", `{"q":"1"}`, "executing", "")
		req.OnToolCall(id, "some_tool", `{"q":"1"}`, "completed", "ok")
	}
	return fmt.Sprintf("content-%s", req.Speaker), 10, nil
}

// waitForTurns blocks until at least n group.turn events have been published.
func waitForTurns(t *testing.T, p *indexedTurnPub, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(p.snapshotTurns()) >= n {
			return
		}
		select {
		case <-p.received:
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("timed out after %v waiting for %d group.turn events (got %d)",
		timeout, n, len(p.snapshotTurns()))
}

// --- tests ---

// TestRegression_ParallelTurnIndexesUnique runs a MoA group with three
// proposers whose turns sleep in reverse order (a slowest, c fastest) so the
// completion order is inverted with respect to the reservation order, and
// asserts the WS contract holds:
//
//	(a) every group.turn carries a distinct turn_index;
//	(b) the set of indices is exactly the whole domain {0..N-1};
//	(c) each group.tool of speaker X carries the same turn_index as X's group.turn.
func TestRegression_ParallelTurnIndexesUnique(t *testing.T) {
	// a is slowest -> completes last; c is fastest -> completes first.
	// Reservation order (a,b,c) therefore differs from append order (c,b,a).
	exec := &delayToolExecutor{delay: map[string]time.Duration{
		"a":   180 * time.Millisecond,
		"b":   120 * time.Millisecond,
		"c":   60 * time.Millisecond,
		"agg": 10 * time.Millisecond,
	}}
	pub := newIndexedTurnPub()
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		proposer("a"),
		proposer("b"),
		proposer("c"),
		aggregator("agg"),
	}

	groupID, err := gm.Start(context.Background(), "b5-parallel", "p1", "objective", "moa",
		participants, GroupOptions{Rounds: 1, Parallel: true}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	turnEvents := pub.snapshotTurns()
	toolEvents := pub.snapshotTools()

	// 3 proposers + 1 aggregator = 4 turns expected.
	const wantTurns = 4
	if len(turnEvents) != wantTurns {
		t.Fatalf("group.turn events = %d, want %d", len(turnEvents), wantTurns)
	}

	// (a) All indices distinct; remember index->speaker and speaker->index.
	speakerByIndex := make(map[int]string, wantTurns)
	indexBySpeaker := make(map[string]int, wantTurns)
	for _, ev := range turnEvents {
		idx := turnIndexOf(t, ev)
		speaker := ev.Metadata["speaker"]
		if prev, dup := speakerByIndex[idx]; dup {
			t.Errorf("turn_index %d published twice (speakers %q and %q) - parallel collision",
				idx, prev, speaker)
			continue
		}
		speakerByIndex[idx] = speaker
		indexBySpeaker[speaker] = idx
	}

	// (b) The index set covers exactly {0..N-1} (0-based domain preserved).
	idxs := make([]int, 0, len(speakerByIndex))
	for i := range speakerByIndex {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	if len(idxs) != wantTurns {
		t.Fatalf("distinct indices = %d, want %d", len(idxs), wantTurns)
	}
	for i, got := range idxs {
		if got != i {
			t.Fatalf("index set = %v, want exactly {0..%d} (0-based, contiguous)", idxs, wantTurns-1)
		}
	}

	// (c) group.tool turn_index agrees with the speaker's group.turn turn_index.
	if len(toolEvents) == 0 {
		t.Fatal("no group.tool events published; assertion (c) was vacuous")
	}
	for _, ev := range toolEvents {
		speaker := ev.Metadata["speaker"]
		toolIdx := turnIndexOf(t, ev)
		turnIdx, ok := indexBySpeaker[speaker]
		if !ok {
			t.Errorf("group.tool for speaker %q has no matching group.turn event", speaker)
			continue
		}
		if toolIdx != turnIdx {
			t.Errorf("speaker %q: group.tool turn_index = %d, group.turn turn_index = %d (want equal)",
				speaker, toolIdx, turnIdx)
		}
	}

	// The persisted transcript must carry the same unique indices.
	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found after Wait")
	}
	if len(st.Transcript) != wantTurns {
		t.Fatalf("transcript length = %d, want %d", len(st.Transcript), wantTurns)
	}
	for _, tr := range st.Transcript {
		if sp, ok := speakerByIndex[tr.Index]; !ok {
			t.Errorf("transcript turn %d (speaker %q) has no matching group.turn event", tr.Index, tr.Speaker)
		} else if sp != tr.Speaker {
			t.Errorf("transcript index %d: speaker %q, event says %q (indices must survive append)",
				tr.Index, tr.Speaker, sp)
		}
	}
	if st.NextTurnIndex != wantTurns {
		t.Errorf("NextTurnIndex = %d, want %d (one reservation per turn, no reuse)", st.NextTurnIndex, wantTurns)
	}
}

// TestRegression_TurnIndexStableAcrossStop starts a parallel group, stops it
// mid-flight (after the first turns have been published) and asserts the
// indices already advertised to clients never change retroactively: every
// index published before the Stop must still be published afterwards, mapped
// to the same speaker, and no index may appear twice.
func TestRegression_TurnIndexStableAcrossStop(t *testing.T) {
	// Long turns so the Stop lands while the batch is still running.
	exec := &delayToolExecutor{delay: map[string]time.Duration{
		"a":   500 * time.Millisecond,
		"b":   600 * time.Millisecond,
		"c":   700 * time.Millisecond,
		"agg": 800 * time.Millisecond,
	}}
	pub := newIndexedTurnPub()
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{
		proposer("a"),
		proposer("b"),
		proposer("c"),
		aggregator("agg"),
	}

	groupID, err := gm.Start(context.Background(), "b5-stop", "p1", "objective", "moa",
		participants, GroupOptions{Rounds: 1, Parallel: true}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for the first turn to be published, snapshot its index, then stop.
	waitForTurns(t, pub, 1, 5*time.Second)
	before := pub.snapshotTurns()
	beforeIdx := make(map[int]string, len(before))
	for _, ev := range before {
		beforeIdx[turnIndexOf(t, ev)] = ev.Metadata["speaker"]
	}

	if !gm.Stop(groupID) {
		t.Fatal("Stop: group not found")
	}
	_, _ = gm.Wait(groupID) // stopped, or finished just in time: either is fine

	after := pub.snapshotTurns()
	if len(after) < len(before) {
		t.Fatalf("fewer group.turn events after completion (%d) than before stop (%d)",
			len(after), len(before))
	}

	// Every index published before the stop must still be present afterwards
	// with the same speaker - nothing renumbered retroactively.
	afterIdx := make(map[int]string, len(after))
	for _, ev := range after {
		idx := turnIndexOf(t, ev)
		sp := ev.Metadata["speaker"]
		if prev, dup := afterIdx[idx]; dup {
			t.Errorf("turn_index %d published twice (speakers %q, %q)", idx, prev, sp)
		}
		afterIdx[idx] = sp
	}
	for idx, sp := range beforeIdx {
		got, ok := afterIdx[idx]
		if !ok {
			t.Errorf("turn_index %d (speaker %q) published before Stop vanished after Stop", idx, sp)
			continue
		}
		if got != sp {
			t.Errorf("turn_index %d retroactively renumbered: speaker was %q, now %q", idx, sp, got)
		}
	}

	// The transcript agrees with the events: each recorded turn keeps its
	// reserved index, and no two recorded turns share one.
	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found after Wait")
	}
	seenTr := make(map[int]bool, len(st.Transcript))
	for _, tr := range st.Transcript {
		if seenTr[tr.Index] {
			t.Errorf("transcript contains duplicate Turn.Index %d (speaker %q)", tr.Index, tr.Speaker)
		}
		seenTr[tr.Index] = true
		if sp, ok := afterIdx[tr.Index]; !ok {
			t.Errorf("transcript turn %d (speaker %q) has no matching group.turn event", tr.Index, tr.Speaker)
		} else if sp != tr.Speaker {
			t.Errorf("transcript turn %d: speaker %q, event says %q", tr.Index, tr.Speaker, sp)
		}
	}
}

// TestRegression_SequentialIndicesUnchanged pins the non-parallel behaviour:
// sequential groups keep emitting contiguous 0-based indices in transcript
// order, exactly as before the fix (the WS contract is unchanged).
func TestRegression_SequentialIndicesUnchanged(t *testing.T) {
	exec := &mockExecutor{}
	pub := newIndexedTurnPub()
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{plainParticipant("a"), plainParticipant("b"), plainParticipant("c")}

	groupID, err := gm.Start(context.Background(), "b5-seq", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 2}, "test-ch", "test-chat")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	turnEvents := pub.snapshotTurns()
	if len(turnEvents) != 6 {
		t.Fatalf("group.turn events = %d, want 6 (2 rounds x 3 participants)", len(turnEvents))
	}
	for i, ev := range turnEvents {
		if got := turnIndexOf(t, ev); got != i {
			t.Errorf("turn event %d: turn_index = %d, want %d (sequential order must be preserved)", i, got, i)
		}
	}

	st, _ := gm.Status(groupID)
	for i, tr := range st.Transcript {
		if tr.Index != i {
			t.Errorf("transcript[%d].Index = %d, want %d", i, tr.Index, i)
		}
	}
	if st.NextTurnIndex != len(st.Transcript) {
		t.Errorf("NextTurnIndex = %d, want %d", st.NextTurnIndex, len(st.Transcript))
	}
}

// TestRegression_ReservedIndexRebasedOnRehydratedState covers the defensive
// re-base in prepareTurn: a GroupState whose NextTurnIndex is behind the
// transcript (for example a state rehydrated from a snapshot written before
// this field existed) must not reuse an index already present in the
// transcript.
func TestRegression_ReservedIndexRebasedOnRehydratedState(t *testing.T) {
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)

	state := &GroupState{
		ID:       "b5-rehydrate",
		Strategy: "round_robin",
		Status:   StatusRunning,
		// Two turns already recorded with indices 0 and 1; counter absent (0).
		Transcript:    []Turn{{Index: 0, Speaker: "a"}, {Index: 1, Speaker: "b"}},
		Participants:  []Participant{plainParticipant("a"), plainParticipant("b")},
		NextTurnIndex: 0,
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	inputs := gm.prepareTurn(mg, "a", 0, AgentContext{AgentID: "a", Name: "Agent A"})
	if inputs.turnIndex != 2 {
		t.Fatalf("reserved turnIndex = %d, want 2 (must re-base to len(Transcript))", inputs.turnIndex)
	}
	if state.NextTurnIndex != 3 {
		t.Errorf("NextTurnIndex = %d, want 3 after reservation", state.NextTurnIndex)
	}

	// A second reservation keeps advancing, never colliding.
	inputs2 := gm.prepareTurn(mg, "b", 0, AgentContext{AgentID: "b", Name: "Agent B"})
	if inputs2.turnIndex != 3 {
		t.Errorf("second reserved turnIndex = %d, want 3", inputs2.turnIndex)
	}
}
