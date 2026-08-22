package group

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ===========================================================================
// Manager setters (0% coverage)
// ===========================================================================

func TestSetStoreDir_Direct(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	dir := t.TempDir()
	gm.SetStoreDir(dir)

	gm.mu.Lock()
	got := gm.storeDir
	gm.mu.Unlock()
	if got != dir {
		t.Errorf("storeDir = %q, want %q", got, dir)
	}
}

func TestSetStoreDir_PersistsOnStart(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	dir := t.TempDir()
	gm.SetStoreDir(dir)

	participants := []Participant{plainParticipant("a")}
	groupID, err := gm.Start(context.Background(), "setdir-1", "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 1}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// The started state should have been persisted as JSON in dir.
	found := false
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			found = true
		}
	}
	if !found {
		t.Error("expected a JSON persistence file in storeDir")
	}
}

func TestSetStore_UsesRepo(t *testing.T) {
	s := openSQLiteStore(t)
	gm := NewGroupManager(mockResolve, nil, nil)
	gm.SetStore(s.Groups())
	t.Cleanup(func() { UseStore(nil) })

	state := &GroupState{
		ID:        "group:setstore",
		Task:      "x",
		Status:    StatusRunning,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	// dir is ignored when a repo is configured; use empty dir.
	if err := SaveGroup("", state); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}

	_, found, err := s.Groups().GetGroupState(state.ID)
	if err != nil {
		t.Fatalf("GetGroupState: %v", err)
	}
	if !found {
		t.Error("expected state to be persisted in the SQLite repo")
	}
}

func TestSetModeratorDecider_UsedByModeratorStrategy(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	used := false
	decider := func(state *GroupState) (string, bool, error) {
		used = true
		return "a", true, nil // done immediately, no turns
	}
	gm.SetModeratorDecider(decider)

	groupID, err := gm.Start(context.Background(), "mod-dec-1", "p1", "task", "moderator",
		[]Participant{plainParticipant("a")}, GroupOptions{}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	synthesis, err := gm.Wait(groupID)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if synthesis != "" {
		t.Errorf("synthesis = %q, want empty (no turns executed)", synthesis)
	}
	if !used {
		t.Error("custom moderator decider was not used")
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status != StatusDone {
		t.Errorf("status = %s, want done", st.Status)
	}
}

func TestSetModeratorDecider_NilResetsToDefault(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	gm.SetModeratorDecider(func(state *GroupState) (string, bool, error) {
		return "a", true, nil
	})
	gm.SetModeratorDecider(nil) // reset

	// Default decider cycles through participants; 1 participant, 1 round.
	groupID, err := gm.Start(context.Background(), "mod-reset-1", "p1", "task", "moderator",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

// ===========================================================================
// Manager lifecycle edge cases
// ===========================================================================

func TestStatus_NotFound(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	st, ok := gm.Status("does-not-exist")
	if ok {
		t.Error("Status returned ok=true for nonexistent group")
	}
	if st != nil {
		t.Errorf("Status returned %v, want nil", st)
	}
}

func TestStop_NotFound(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	if gm.Stop("does-not-exist") {
		t.Error("Stop returned true for nonexistent group")
	}
}

func TestStop_AlreadyDone_StaysDone(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	groupID, err := gm.Start(context.Background(), "stop-done-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := gm.Wait(groupID); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Group is already done; Stop should still return true but not flip status.
	if !gm.Stop(groupID) {
		t.Error("Stop on finished group returned false")
	}
	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not found")
	}
	if st.Status != StatusDone {
		t.Errorf("status = %s, want done (must not regress to stopped)", st.Status)
	}
}

func TestSaveStateBestEffort_EmptyDir(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	mg := &managedGroup{
		state: &GroupState{ID: "se-1", Status: StatusRunning},
		done:  make(chan struct{}),
	}
	gm.saveStateBestEffort(mg) // storeDir empty → no-op, no panic
}

func TestSaveStateBestEffort_ErrorLogged(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)

	// storeDir points to a path that is actually a file → MkdirAll fails.
	d := t.TempDir()
	filePath := filepath.Join(d, "notdir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	gm.storeDir = filePath

	mg := &managedGroup{
		state: &GroupState{ID: "se-2", Status: StatusRunning},
		done:  make(chan struct{}),
	}
	gm.saveStateBestEffort(mg) // should log failure, not return error
}

// ===========================================================================
// runGroup direct tests (unexported, same package)
// ===========================================================================

func TestRunGroup_UnknownStrategy(t *testing.T) {
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, nil, pub.publish)

	state := &GroupState{ID: "rg-1", Strategy: "no_such_strategy_zzz", Status: StatusRunning}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	gm.runGroup(context.Background(), mg)

	if state.Status != StatusError {
		t.Errorf("status = %s, want error", state.Status)
	}
	if mg.err == nil {
		t.Error("expected err to be set for unknown strategy")
	}
	select {
	case <-mg.done:
	default:
		t.Error("done channel not closed")
	}

	// Should publish a group.status error event.
	if ev := pub.byEvent("group.status"); len(ev) == 0 || ev[0].Metadata["status"] != "error" {
		t.Errorf("expected group.status error event, got %v", ev)
	}
}

func TestRunGroup_CancelledBeforeStart(t *testing.T) {
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, nil, pub.publish)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	state := &GroupState{ID: "rg-2", Strategy: "round_robin", Status: StatusRunning}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	gm.runGroup(ctx, mg)

	if state.Status != StatusStopped {
		t.Errorf("status = %s, want stopped", state.Status)
	}
	select {
	case <-mg.done:
	default:
		t.Error("done channel not closed")
	}
}

func TestRunGroup_ModeratorWithEmptyParticipants_StopsDone(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	gm.SetModeratorDecider(nil) // ensure default decider

	state := &GroupState{
		ID:           "rg-3",
		Strategy:     "moderator",
		Status:       StatusRunning,
		Participants: []Participant{}, // empty → default decider returns done
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	gm.runGroup(context.Background(), mg)

	if state.Status != StatusDone {
		t.Errorf("status = %s, want done", state.Status)
	}
	select {
	case <-mg.done:
	default:
		t.Error("done channel not closed")
	}
}

// ===========================================================================
// executeSpeaker / executeSequential edge cases
// ===========================================================================

func TestExecuteSpeaker_UnresolvableSpeaker(t *testing.T) {
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, nil, pub.publish)

	state := &GroupState{
		ID:       "sp-1",
		Strategy: "round_robin",
		Status:   StatusRunning,
		Participants: []Participant{
			{AgentID: "ghost", Label: "Ghost"},
		},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	err := gm.executeSpeaker(context.Background(), mg, "ghost", 0)
	if err != nil {
		t.Errorf("expected nil error for unresolvable speaker, got %v", err)
	}
	// No turn should be appended.
	if len(state.Transcript) != 0 {
		t.Errorf("unexpected turns appended: %d", len(state.Transcript))
	}
}

func TestExecuteSpeaker_EmptyLabelFallsBackToID(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	state := &GroupState{
		ID:           "sp-2",
		Strategy:     "round_robin",
		Status:       StatusRunning,
		Participants: []Participant{{AgentID: "a"}}, // no label
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	err := gm.executeSpeaker(context.Background(), mg, "a", 0)
	if err != nil {
		t.Fatalf("executeSpeaker: %v", err)
	}
	if len(state.Transcript) != 1 {
		t.Fatalf("transcript len = %d, want 1", len(state.Transcript))
	}
	// Label should fall back to agent name from resolve ("Agent A").
	if state.Transcript[0].Label == "" {
		t.Error("label should not be empty")
	}
	// Speaker should be the agent ID.
	if state.Transcript[0].Speaker != "a" {
		t.Errorf("speaker = %s, want a", state.Transcript[0].Speaker)
	}
}

func TestExecuteSpeaker_ExecutorError(t *testing.T) {
	badExec := func(ctx context.Context, req TurnRequest) (string, int, error) {
		return "", 0, fmt.Errorf("boom")
	}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, badExec, pub.publish)

	state := &GroupState{
		ID:           "sp-3",
		Strategy:     "round_robin",
		Status:       StatusRunning,
		Participants: []Participant{{AgentID: "a", Label: "A"}},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	err := gm.executeSpeaker(context.Background(), mg, "a", 0)
	if err == nil {
		t.Fatal("expected error from executor")
	}
	if !strings.Contains(err.Error(), "a") {
		t.Errorf("error should mention speaker, got %v", err)
	}
	// No turn should be appended on error.
	if len(state.Transcript) != 0 {
		t.Errorf("unexpected turns after error: %d", len(state.Transcript))
	}
}

func TestExecuteSequential_ContextCancelled(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	state := &GroupState{
		ID:           "seq-1",
		Strategy:     "round_robin",
		Status:       StatusRunning,
		Participants: []Participant{{AgentID: "a", Label: "A"}, {AgentID: "b", Label: "B"}},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel between speakers isn't deterministic here; instead we verify
	// that a cancelled context makes executeSequential return immediately.
	cancel()

	err := gm.executeSequential(ctx, mg, []string{"a", "b"}, 0)
	if err == nil {
		t.Error("expected context-cancelled error")
	}
}

func TestExecuteParallel_WithToolCalls(t *testing.T) {
	// Parallel execution with OnToolCall being invoked from concurrent
	// goroutines to exercise the tcMu-protected upsert logic.
	exec := &toolCallExecutor{
		toolID:   "call-par-1",
		toolName: "read_file",
		args:     `{"path":"/x"}`,
		result:   "ok",
	}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	state := &GroupState{
		ID:           "par-1",
		Strategy:     "moa",
		Status:       StatusRunning,
		Parallel:     true,
		Participants: []Participant{{AgentID: "a", Role: RoleProposer, Label: "A"}, {AgentID: "b", Role: RoleProposer, Label: "B"}},
	}
	mg := &managedGroup{state: state, done: make(chan struct{})}

	err := gm.executeParallel(context.Background(), mg, []string{"a", "b"}, 0)
	if err != nil {
		t.Fatalf("executeParallel: %v", err)
	}
	if len(state.Transcript) != 2 {
		t.Fatalf("transcript len = %d, want 2", len(state.Transcript))
	}
	for _, turn := range state.Transcript {
		if len(turn.ToolCalls) != 1 {
			t.Errorf("turn %s tool calls = %d, want 1", turn.Speaker, len(turn.ToolCalls))
		}
	}
}

// ===========================================================================
// synthesisLocked / instructionFor
// ===========================================================================

func TestSynthesisLocked_EmptyTranscript(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	mg := &managedGroup{state: &GroupState{ID: "syn-1", Strategy: "round_robin"}}
	if s := gm.synthesisLocked(mg); s != "" {
		t.Errorf("synthesis = %q, want empty", s)
	}
}

func TestSynthesisLocked_RoundRobinLastTurn(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	state := &GroupState{
		ID:       "syn-2",
		Strategy: "round_robin",
		Transcript: []Turn{
			{Speaker: "a", Content: "first"},
			{Speaker: "b", Content: "second"},
		},
	}
	mg := &managedGroup{state: state}
	if s := gm.synthesisLocked(mg); s != "second" {
		t.Errorf("synthesis = %q, want %q", s, "second")
	}
}

func TestSynthesisLocked_MoA_FallsBackToLastTurnWhenNoAggregatorTurn(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	state := &GroupState{
		ID:        "syn-3",
		Strategy:  "moa",
		Moderator: "agg",
		Participants: []Participant{
			{AgentID: "p1", Role: RoleProposer},
			{AgentID: "agg", Role: RoleAggregator},
		},
		// Aggregator never spoke → walk-back finds nothing → last turn content.
		Transcript: []Turn{
			{Speaker: "p1", Layer: 0, Content: "proposal"},
		},
	}
	mg := &managedGroup{state: state}
	if s := gm.synthesisLocked(mg); s != "proposal" {
		t.Errorf("synthesis = %q, want %q", s, "proposal")
	}
}

func TestSynthesisLocked_MoA_UsesAggregatorLastTurn(t *testing.T) {
	gm := NewGroupManager(mockResolve, nil, nil)
	state := &GroupState{
		ID:       "syn-4",
		Strategy: "moa",
		Participants: []Participant{
			{AgentID: "p1", Role: RoleProposer},
			{AgentID: "agg", Role: RoleAggregator},
		},
		Transcript: []Turn{
			{Speaker: "p1", Layer: 0, Content: "proposal"},
			{Speaker: "agg", Layer: 0, Content: "aggregated result"},
		},
	}
	mg := &managedGroup{state: state}
	if s := gm.synthesisLocked(mg); s != "aggregated result" {
		t.Errorf("synthesis = %q, want %q", s, "aggregated result")
	}
}

func TestInstructionFor(t *testing.T) {
	cases := []struct {
		name     string
		state    *GroupState
		self     Participant
		layer    int
		contains string
	}{
		{
			name:     "aggregator role",
			state:    &GroupState{Strategy: "moa"},
			self:     Participant{AgentID: "agg", Role: RoleAggregator},
			contains: "Synthesize",
		},
		{
			name:     "moderator role",
			state:    &GroupState{Strategy: "moderator"},
			self:     Participant{AgentID: "m", Role: RoleModerator},
			contains: "Synthesize",
		},
		{
			name:     "moa proposer",
			state:    &GroupState{Strategy: "moa"},
			self:     Participant{AgentID: "p", Role: RoleProposer},
			contains: "Propose",
		},
		{
			name:     "moa aggregator by field",
			state:    &GroupState{Strategy: "moa", Moderator: "agg"},
			self:     Participant{AgentID: "agg"},
			contains: "Synthesize",
		},
		{
			name:     "moa non-aggregator fallback proposer",
			state:    &GroupState{Strategy: "moa", Moderator: "agg"},
			self:     Participant{AgentID: "other"},
			contains: "Propose",
		},
		{
			name:     "default",
			state:    &GroupState{Strategy: "round_robin"},
			self:     Participant{AgentID: "x"},
			contains: "Contribute",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := instructionFor(tc.state, tc.self, tc.layer)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("instructionFor = %q, want substring %q", got, tc.contains)
			}
		})
	}
}

// ===========================================================================
// defaultModeratorDecider
// ===========================================================================

func TestDefaultModeratorDecider_NoParticipants(t *testing.T) {
	state := &GroupState{}
	speaker, done, err := defaultModeratorDecider(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if speaker != "" {
		t.Errorf("speaker = %q, want empty", speaker)
	}
	if !done {
		t.Error("expected done=true with no participants")
	}
}

func TestDefaultModeratorDecider_CyclesAndMaxTurns(t *testing.T) {
	state := &GroupState{
		MaxTurns: 3,
		Participants: []Participant{
			{AgentID: "a"},
			{AgentID: "b"},
		},
	}
	// Turn 0 → a
	speaker, done, err := defaultModeratorDecider(state)
	if err != nil || done {
		t.Fatalf("turn 0: speaker=%q done=%v err=%v", speaker, done, err)
	}
	if speaker != "a" {
		t.Errorf("turn 0 speaker = %q, want a", speaker)
	}
	state.AddTurn(Turn{Speaker: "a"})

	// Turn 1 → b
	speaker, done, _ = defaultModeratorDecider(state)
	if speaker != "b" {
		t.Errorf("turn 1 speaker = %q, want b", speaker)
	}
	state.AddTurn(Turn{Speaker: "b"})

	// Turn 2 → a
	speaker, done, _ = defaultModeratorDecider(state)
	if speaker != "a" {
		t.Errorf("turn 2 speaker = %q, want a", speaker)
	}
	state.AddTurn(Turn{Speaker: "a"})

	// Turn 3: len(transcript)=3 >= MaxTurns=3 → done.
	_, done, err = defaultModeratorDecider(state)
	if err != nil {
		t.Fatalf("turn 3 error: %v", err)
	}
	if !done {
		t.Error("expected done=true once MaxTurns reached")
	}
}
