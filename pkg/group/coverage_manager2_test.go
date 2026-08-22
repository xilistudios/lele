package group

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ===========================================================================
// controllerErrorExecutor returns an error for a specific speaker and
// succeeds otherwise. Used to drive runGroup error paths.
// ===========================================================================

type controllerErrorExecutor struct {
	failOn string // speaker whose execution should error
	calls  []string
}

func (e *controllerErrorExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	e.calls = append(e.calls, req.Speaker)
	if req.Speaker == e.failOn {
		return "", 0, fmt.Errorf("executor failed for %s", req.Speaker)
	}
	return "ok-" + req.Speaker, 10, nil
}

func TestRunGroup_SequentialExecutorErrorSetsStatusError(t *testing.T) {
	exec := &controllerErrorExecutor{failOn: "b"}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	state := &GroupState{
		ID:       "rg-seq-err",
		Strategy: "round_robin",
		Status:   StatusRunning,
		Rounds:   1,
		Participants: []Participant{
			{AgentID: "a", Label: "A"},
			{AgentID: "b", Label: "B"},
		},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	gm.runGroup(context.Background(), mg)

	if state.Status != StatusError {
		t.Errorf("status = %s, want error", state.Status)
	}
	if mg.err == nil || !strings.Contains(mg.err.Error(), "b") {
		t.Errorf("err = %v, want mention of speaker b", mg.err)
	}
	// The failing speaker "b" should have published a group.status error event.
	ev := pub.byEvent("group.status")
	found := false
	for _, e := range ev {
		if e.Metadata["status"] == "error" {
			found = true
		}
	}
	if !found {
		t.Error("expected a group.status error event")
	}

	select {
	case <-mg.done:
	default:
		t.Error("done channel not closed")
	}
}

func TestRunGroup_ParallelExecutorErrorSetsStatusError(t *testing.T) {
	exec := &controllerErrorExecutor{failOn: "b"}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	state := &GroupState{
		ID:       "rg-par-err",
		Strategy: "moa",
		Status:   StatusRunning,
		Rounds:   1,
		Parallel: true,
		Participants: []Participant{
			{AgentID: "a", Role: RoleProposer, Label: "A"},
			{AgentID: "b", Role: RoleProposer, Label: "B"},
		},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	gm.runGroup(context.Background(), mg)

	if state.Status != StatusError {
		t.Errorf("status = %s, want error", state.Status)
	}
	select {
	case <-mg.done:
	default:
		t.Error("done channel not closed")
	}
}

func TestStart_NoParticipantsError(t *testing.T) {
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, nil, pub.publish)

	_, err := gm.Start(context.Background(), "no-parts", "p1", "task", "round_robin",
		nil, GroupOptions{}, "ch", "chat")
	if err == nil {
		t.Fatal("expected error when no participants")
	}
}

// participantReqExecutor fails the whole turn with an error regardless of
// speaker (drives the sequential non-context error branch deterministically).
type alwaysErrorExecutor struct{}

func (e *alwaysErrorExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	return "", 0, fmt.Errorf("always fail for %s", req.Speaker)
}

func TestRunGroup_ExecuteSequential_FirstSpeakerErrors(t *testing.T) {
	exec := &alwaysErrorExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	state := &GroupState{
		ID:       "rg-first-err",
		Strategy: "round_robin",
		Status:   StatusRunning,
		Rounds:   1,
		Participants: []Participant{
			{AgentID: "a", Label: "A"},
			{AgentID: "b", Label: "B"},
		},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	gm.runGroup(context.Background(), mg)

	if state.Status != StatusError {
		t.Errorf("status = %s, want error", state.Status)
	}
	select {
	case <-mg.done:
	default:
		t.Error("done channel not closed")
	}
}

// cancelAfterFirstExecutor succeeds for the first speaker then blocks on
// ctx.Done, simulating a Stop mid-parallel-batch where the batch returns a
// context-cancelled error.
type cancelAfterSleepExecutor struct {
	delay time.Duration
}

func (e *cancelAfterSleepExecutor) execute(ctx context.Context, req TurnRequest) (string, int, error) {
	select {
	case <-time.After(e.delay):
		return "slow-" + req.Speaker, 10, nil
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
}

func TestRunGroup_ParallelContextCancelledReturnsStopped(t *testing.T) {
	exec := &cancelAfterSleepExecutor{delay: 200 * time.Millisecond}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	state := &GroupState{
		ID:       "rg-par-cancel",
		Strategy: "moa",
		Status:   StatusRunning,
		Rounds:   100,
		Parallel: true,
		Participants: []Participant{
			{AgentID: "a", Role: RoleProposer, Label: "A"},
			{AgentID: "b", Role: RoleProposer, Label: "B"},
		},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		gm.runGroup(ctx, mg)
	}()

	// Let the parallel batch get started, then cancel mid-execution.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runGroup did not return after cancel")
	}

	if state.Status != StatusStopped {
		t.Errorf("status = %s, want stopped", state.Status)
	}
	select {
	case <-mg.done:
	default:
		t.Error("done channel not closed")
	}
}

func TestRunGroup_SequentialContextCancelledReturnsStopped(t *testing.T) {
	// An executor that blocks forever until ctx done.
	be := &cancelAfterSleepExecutor{delay: time.Hour}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)

	state := &GroupState{
		ID:       "rg-seq-cancel",
		Strategy: "round_robin",
		Status:   StatusRunning,
		Rounds:   100,
		Participants: []Participant{
			{AgentID: "a", Label: "A"},
			{AgentID: "b", Label: "B"},
		},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		gm.runGroup(ctx, mg)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runGroup did not return after cancel during sequential execute")
	}

	if state.Status != StatusStopped {
		t.Errorf("status = %s, want stopped", state.Status)
	}
}

// publishingExecutor publishes a group.complete-bearing normal flow for the
// "status==StatusError && len(Transcript)>0" branch of the deferred complete.
type partialExecutor struct {
	completeTurn bool
}

func (e *partialExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	e.completeTurn = true
	return "partial-" + req.Speaker, 5, nil
}

func TestRunGroup_ErrorWithPartialTranscriptPublishesComplete(t *testing.T) {
	// Strategy that errors after one speaker so the transcript has content
	// but status becomes error: used to hit the
	// `status==StatusError && len(Transcript)>0` deferred-complete branch.
	// We simulate by using a moderator decider that errors.
	pub := &mockPublisher{}
	pe := &partialExecutor{}
	gm := NewGroupManager(mockResolve, pe.execute, pub.publish)
	// Inject a decider that always errors.
	calls := 0
	gm.SetModeratorDecider(func(*GroupState) (string, bool, error) {
		calls++
		if calls <= 1 {
			return "a", false, nil
		}
		return "", false, fmt.Errorf("decider failed mid-group")
	})

	state := &GroupState{
		ID:       "rg-partial",
		Strategy: "moderator",
		Status:   StatusRunning,
		MaxTurns: 100,
		Participants: []Participant{
			{AgentID: "a", Label: "A"},
		},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	gm.runGroup(context.Background(), mg)

	// After one speaker, decider errors → status error with a transcript.
	if state.Status != StatusError {
		t.Errorf("status = %s, want error", state.Status)
	}
	if len(state.Transcript) != 1 {
		t.Errorf("transcript len = %d, want 1", len(state.Transcript))
	}
	// group.complete should have been published because transcript is non-empty.
	if comp := pub.byEvent("group.complete"); len(comp) != 1 {
		t.Errorf("group.complete events = %d, want 1", len(comp))
	}
	select {
	case <-mg.done:
	default:
		t.Error("done channel not closed")
	}
}
