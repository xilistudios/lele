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

type indexedTurnPub struct {
	mu       sync.Mutex
	turns    []bus.OutboundMessage
	tools    []bus.OutboundMessage
	received chan struct{}
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

func TestRegression_ParallelTurnIndexesUnique(t *testing.T) {
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

	const wantTurns = 4
	if len(turnEvents) != wantTurns {
		t.Fatalf("group.turn events = %d, want %d", len(turnEvents), wantTurns)
	}

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

func TestRegression_TurnIndexStableAcrossStop(t *testing.T) {
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

	waitForTurns(t, pub, 1, 5*time.Second)
	before := pub.snapshotTurns()
	beforeIdx := make(map[int]string, len(before))
	for _, ev := range before {
		beforeIdx[turnIndexOf(t, ev)] = ev.Metadata["speaker"]
	}

	if !gm.Stop(groupID) {
		t.Fatal("Stop: group not found")
	}
	_, _ = gm.Wait(groupID)

	after := pub.snapshotTurns()
	if len(after) < len(before) {
		t.Fatalf("fewer group.turn events after completion (%d) than before stop (%d)",
			len(after), len(before))
	}

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

func TestRegression_ReservedIndexRebasedOnRehydratedState(t *testing.T) {
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, (&mockPublisher{}).publish)

	state := &GroupState{
		ID:            "b5-rehydrate",
		Strategy:      "round_robin",
		Status:        StatusRunning,
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

	inputs2 := gm.prepareTurn(mg, "b", 0, AgentContext{AgentID: "b", Name: "Agent B"})
	if inputs2.turnIndex != 3 {
		t.Errorf("second reserved turnIndex = %d, want 3", inputs2.turnIndex)
	}
}
