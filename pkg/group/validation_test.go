package group

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Regression tests for B2 and B3.
//
// B2 (critical): an unresolvable speaker used to be logged and skipped, so the
// transcript never grew and any transcript-driven strategy (round_robin, moa,
// moderator) kept re-requesting the same speaker until the loop burned its
// whole budget — up to maxGroupIterations iterations for a single turn.
//
// B3 (high): Start validated participants and strategy but never that
// opts.Moderator was one of them. MoAAggregator then falls back to a speaker
// that can never talk, and synthesisLocked returned the last *proposer's* turn
// as if it were the synthesis. config.GroupProfile.Validate() has always
// required moderator ∈ participants; Start() now enforces the same rule, and
// synthesisLocked flags any surviving "aggregator never spoke" result instead
// of hiding it.
// ---------------------------------------------------------------------------

// --- test doubles -----------------------------------------------------------

// toggleResolver resolves agents listed in `alive` and can revoke one of them
// mid-run, emulating an agent unregistered after Start's validation pass — the
// window B2 lived in.
type toggleResolver struct {
	mu    sync.Mutex
	alive map[string]bool
}

func newToggleResolver(ids ...string) *toggleResolver {
	alive := make(map[string]bool, len(ids))
	for _, id := range ids {
		alive[id] = true
	}
	return &toggleResolver{alive: alive}
}

func (r *toggleResolver) resolve(agentID string) (AgentContext, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.alive[agentID] {
		return AgentContext{}, false
	}
	return AgentContext{
		AgentID:      agentID,
		Name:         "Agent " + agentID,
		SystemPrompt: "persona of " + agentID,
	}, true
}

// revoke makes agentID unresolvable from now on.
func (r *toggleResolver) revoke(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.alive, agentID)
}

// scriptedExecutor records every attempt and answers from per-speaker rules. It
// can park selected speakers until a channel is closed — so a test can change
// the world in the middle of a turn — and can fail selected speakers outright.
type scriptedExecutor struct {
	mu      sync.Mutex
	calls   []string
	gate    map[string]chan struct{}
	content map[string]string
	errFor  map[string]error
}

func (e *scriptedExecutor) execute(ctx context.Context, req TurnRequest) (string, int, error) {
	// Record the attempt before gating so "calls" means "turns requested".
	e.mu.Lock()
	e.calls = append(e.calls, req.Speaker)
	e.mu.Unlock()

	if ch, ok := e.gate[req.Speaker]; ok {
		select {
		case <-ch:
		case <-ctx.Done():
			return "", 0, ctx.Err()
		}
	}
	if err, ok := e.errFor[req.Speaker]; ok && err != nil {
		return "", 0, err
	}

	if c, ok := e.content[req.Speaker]; ok {
		return c, 10, nil
	}
	return "turn-" + req.Speaker, 10, nil
}

func (e *scriptedExecutor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func (e *scriptedExecutor) spoke(speaker string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range e.calls {
		if s == speaker {
			return true
		}
	}
	return false
}

// waitGroup runs gm.Wait in a goroutine and returns its result, failing the test
// if the group never reaches a terminal state.
func waitGroup(t *testing.T, gm *GroupManager, groupID string, d time.Duration) (string, error) {
	t.Helper()
	type outcome struct {
		res string
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		res, err := gm.Wait(groupID)
		ch <- outcome{res, err}
	}()
	select {
	case o := <-ch:
		return o.res, o.err
	case <-time.After(d):
		t.Fatalf("group %s: Wait did not return within %s", groupID, d)
		return "", nil
	}
}

// --- B2: an unresolvable speaker must fail the turn -------------------------

// TestRegression_UnresolvableParticipantFailsFast covers the public path: the
// group starts with every participant resolvable, one agent disappears while
// another turn is in flight, and the run must fail fast through
// finalize(StatusError) instead of skipping the dead speaker forever.
func TestRegression_UnresolvableParticipantFailsFast(t *testing.T) {
	res := newToggleResolver("a", "b", "agg")
	hold := make(chan struct{})
	exec := &scriptedExecutor{gate: map[string]chan struct{}{"a": hold}}
	rec := newLifecycleRecorder()
	gm := NewGroupManager(res.resolve, exec.execute, rec.publish)

	participants := []Participant{proposer("a"), proposer("b"), aggregator("agg")}
	groupID, err := gm.Start(context.Background(), "b2-start-1", "p1", "solve X", "moa",
		participants, GroupOptions{Rounds: 1, Moderator: "agg"}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// a's turn is parked; drop b, then let a finish. b is asked for next, so it
	// is unresolvable exactly when its turn comes up.
	res.revoke("b")
	close(hold)

	start := time.Now()
	synthesis, waitErr := waitGroup(t, gm, groupID, 5*time.Second)
	elapsed := time.Since(start)

	if waitErr == nil {
		t.Fatalf("Wait err = nil, want a 'not resolvable' error (synthesis=%q)", synthesis)
	}
	if want := fmt.Sprintf("participant %q not resolvable", "b"); !strings.Contains(waitErr.Error(), want) {
		t.Errorf("Wait err = %v, want it to contain %q", waitErr, want)
	}
	// Pre-fix the loop ran to the end of its budget; that error must never
	// surface now.
	if strings.Contains(waitErr.Error(), "iterations exhausted") {
		t.Errorf("Wait err = %v, must not be the iteration-exhaustion error", waitErr)
	}
	if elapsed > 2*time.Second {
		t.Errorf("group took %s to fail, want < 2s (fail fast, not %d loops)", elapsed, maxGroupIterations)
	}
	// Only a's turn may run; the loop stops at b.
	if exec.callCount() != 1 {
		t.Errorf("executor calls = %d, want 1 (b's turn must abort the run)", exec.callCount())
	}

	assertExactlyOnePair(t, rec, groupID)
	if got := rec.terminalStatus(t, groupID); got != StatusError {
		t.Errorf("terminal status = %q, want %q", got, StatusError)
	}
	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not tracked after Wait")
	}
	if st.Status != StatusError {
		t.Errorf("state.Status = %q, want %q", st.Status, StatusError)
	}
}

// TestRegression_UnresolvableSpeakerFromLoopStart covers a state that never went
// through Start's validation — a group rehydrated from the store after its agent
// disappeared, or any caller that builds a GroupState directly. Before B2 this
// burned all maxGroupIterations iterations to produce a single turn.
func TestRegression_UnresolvableSpeakerFromLoopStart(t *testing.T) {
	res := newToggleResolver("a", "agg") // "b" is unknown from the start
	exec := &scriptedExecutor{}
	rec := newLifecycleRecorder()
	gm := NewGroupManager(res.resolve, exec.execute, rec.publish)

	state := &GroupState{
		ID:           "b2-direct-1",
		Task:         "solve X",
		Strategy:     "moa",
		Status:       StatusRunning,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Rounds:       1,
		MaxTurns:     MaxGroupTurnsCeiling,
		Moderator:    "agg",
		Participants: []Participant{proposer("a"), proposer("b"), aggregator("agg")},
	}
	mg := newManaged(state)
	startManaged(gm, mg)

	start := time.Now()
	go gm.runGroup(context.Background(), mg)
	_, waitErr := waitGroup(t, gm, state.ID, 5*time.Second)
	elapsed := time.Since(start)

	if waitErr == nil {
		t.Fatal("Wait err = nil, want a 'not resolvable' error")
	}
	if want := fmt.Sprintf("participant %q not resolvable", "b"); !strings.Contains(waitErr.Error(), want) {
		t.Errorf("Wait err = %v, want it to contain %q", waitErr, want)
	}
	if elapsed > 2*time.Second {
		t.Errorf("group took %s to fail, want < 2s", elapsed)
	}
	if exec.callCount() != 1 {
		t.Errorf("executor calls = %d, want 1 (one turn for a, then abort)", exec.callCount())
	}
	assertExactlyOnePair(t, rec, state.ID)
	if got := rec.terminalStatus(t, state.ID); got != StatusError {
		t.Errorf("terminal status = %q, want %q", got, StatusError)
	}
}

// TestRegression_UnresolvableSpeakerFailsFastForEveryStrategy pins that the rule
// lives in the runner, not in one strategy: all four derive their speakers from
// the transcript, so all of them used to loop on a dead speaker.
func TestRegression_UnresolvableSpeakerFailsFastForEveryStrategy(t *testing.T) {
	for _, strategy := range []string{"round_robin", "pipeline", "moderator", "moa"} {
		t.Run(strategy, func(t *testing.T) {
			res := newToggleResolver("a") // only "a" exists
			exec := &scriptedExecutor{}
			rec := newLifecycleRecorder()
			gm := NewGroupManager(res.resolve, exec.execute, rec.publish)

			state := &GroupState{
				ID:        "b2-" + strategy,
				Task:      "solve X",
				Strategy:  strategy,
				Status:    StatusRunning,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
				Rounds:    3,
				MaxTurns:  MaxGroupTurnsCeiling,
				// "b" is a participant but never resolvable, and it speaks first.
				Participants: []Participant{plainParticipant("b"), plainParticipant("a")},
			}
			mg := newManaged(state)
			startManaged(gm, mg)

			go gm.runGroup(context.Background(), mg)
			_, waitErr := waitGroup(t, gm, state.ID, 5*time.Second)

			if waitErr == nil {
				t.Fatal("Wait err = nil, want a 'not resolvable' error")
			}
			if want := fmt.Sprintf("participant %q not resolvable", "b"); !strings.Contains(waitErr.Error(), want) {
				t.Errorf("Wait err = %v, want it to contain %q", waitErr, want)
			}
			// At most one healthy turn may be spent before the abort: the loop
			// must not grind through its iteration budget.
			if exec.callCount() > 1 {
				t.Errorf("executor calls = %d, want at most 1", exec.callCount())
			}
			assertExactlyOnePair(t, rec, state.ID)
			if got := rec.terminalStatus(t, state.ID); got != StatusError {
				t.Errorf("terminal status = %q, want %q", got, StatusError)
			}
		})
	}
}

// --- B3: moderator membership ------------------------------------------------

// TestRegression_StartRejectsModeratorNotInParticipants asserts Start refuses a
// moderator/aggregator outside the participants list and launches nothing. This
// is the old audit repro B (ghost moderator): it must now fail in Start instead
// of running a group whose "synthesis" is really a raw proposal.
func TestRegression_StartRejectsModeratorNotInParticipants(t *testing.T) {
	pub := &mockPublisher{}
	exec := &scriptedExecutor{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	participants := []Participant{proposer("a"), proposer("b")}

	_, err := gm.Start(context.Background(), "b3-ghost-1", "p1", "solve X", "moa",
		participants, GroupOptions{Rounds: 1, Moderator: "ghost"}, "ch", "chat")
	if err == nil {
		t.Fatal("Start err = nil, want rejection of moderator outside participants")
	}
	if !strings.Contains(err.Error(), "not in participants") {
		t.Errorf("Start err = %v, want it to mention 'not in participants'", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("Start err = %v, want it to name the offending moderator", err)
	}

	// Nothing may have been registered, executed or announced.
	if _, ok := gm.Status("b3-ghost-1"); ok {
		t.Error("group was registered despite Start returning an error")
	}
	if exec.callCount() != 0 {
		t.Errorf("executor calls = %d, want 0", exec.callCount())
	}
	if n := len(pub.byEvent("group.status")); n != 0 {
		t.Errorf("group.status events = %d, want 0", n)
	}
	if n := len(pub.byEvent("group.complete")); n != 0 {
		t.Errorf("group.complete events = %d, want 0", n)
	}
	if _, err := gm.Wait("b3-ghost-1"); err == nil {
		t.Error("Wait on a rejected group returned nil error, want 'not found'")
	}

	// The same call with a valid moderator must succeed and synthesise normally.
	okID, err := gm.Start(context.Background(), "b3-valid-1", "p1", "solve X", "moa",
		[]Participant{proposer("a"), proposer("b"), aggregator("agg")},
		GroupOptions{Rounds: 1, Moderator: "agg"}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start with valid moderator: %v", err)
	}
	if okID != "b3-valid-1" {
		t.Errorf("groupID = %q, want %q", okID, "b3-valid-1")
	}
	res, waitErr := waitGroup(t, gm, okID, 5*time.Second)
	if waitErr != nil {
		t.Fatalf("Wait for valid group: %v", waitErr)
	}
	if strings.HasPrefix(res, unsynthesizedPrefix) {
		t.Errorf("synthesis = %q, want the aggregator's real synthesis", res)
	}
	if !strings.Contains(res, "turn-agg") {
		t.Errorf("synthesis = %q, want it to come from the aggregator", res)
	}
}

// TestRegression_ModeratorStrategySameRule asserts the "moderator" strategy is
// covered by the same Start validation — its decider is custom, which is the
// path where a stray moderator is easiest to introduce.
func TestRegression_ModeratorStrategySameRule(t *testing.T) {
	pub := &mockPublisher{}
	exec := &scriptedExecutor{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	gm.SetModeratorDecider(func(state *GroupState) (string, bool, error) {
		if len(state.Transcript) >= 2 {
			return "", true, nil
		}
		return state.Participants[0].AgentID, false, nil
	})

	participants := []Participant{plainParticipant("a"), plainParticipant("b")}

	_, err := gm.Start(context.Background(), "b3-mod-1", "p1", "solve X", "moderator",
		participants, GroupOptions{MaxTurns: 2, Moderator: "ghost"}, "ch", "chat")
	if err == nil {
		t.Fatal("Start err = nil, want rejection of moderator outside participants")
	}
	if !strings.Contains(err.Error(), "not in participants") {
		t.Errorf("Start err = %v, want it to mention 'not in participants'", err)
	}
	if exec.callCount() != 0 {
		t.Errorf("executor calls = %d, want 0", exec.callCount())
	}
	if n := len(pub.byEvent("group.status")); n != 0 {
		t.Errorf("group.status events = %d, want 0 (a rejected group stays silent)", n)
	}

	// A moderator that IS a participant is accepted.
	if _, err := gm.Start(context.Background(), "b3-mod-2", "p1", "solve X", "moderator",
		participants, GroupOptions{MaxTurns: 2, Moderator: "b"}, "ch", "chat"); err != nil {
		t.Fatalf("Start with in-participants moderator: %v", err)
	}
	if _, err := waitGroup(t, gm, "b3-mod-2", 5*time.Second); err != nil {
		t.Errorf("Wait for b3-mod-2: %v", err)
	}
}

// TestRegression_EmptyModeratorStillAllowed pins the boundary: an empty
// moderator is legal for every strategy (mirrors GroupProfile.Validate, which
// only checks membership when Moderator != "").
func TestRegression_EmptyModeratorStillAllowed(t *testing.T) {
	for _, strategy := range []string{"moa", "moderator", "round_robin", "pipeline"} {
		gm := NewGroupManager(mockResolve, (&scriptedExecutor{}).execute, (&mockPublisher{}).publish)
		id := "b3-empty-mod-" + strategy
		if _, err := gm.Start(context.Background(), id, "p1", "solve X", strategy,
			[]Participant{proposer("a"), aggregator("agg")},
			GroupOptions{MaxTurns: 2, Moderator: ""}, "ch", "chat"); err != nil {
			t.Errorf("Start(%s) with empty moderator: %v", strategy, err)
			continue
		}
		if _, err := waitGroup(t, gm, id, 5*time.Second); err != nil {
			t.Errorf("Wait(%s): %v", strategy, err)
		}
	}
}

// --- B3: "aggregator never spoke" safety net --------------------------------

// TestRegression_MoAFlagsUnsynthesizedOnConvergedStop is the case that stays
// reachable through Start: the group converges on a stop keyword after the
// proposers speak, so the aggregator never gets a turn. Convergence is a
// legitimate completion — mg.err stays nil — but the fallback text is a raw
// proposal, so it must be marked instead of being presented as the synthesis.
func TestRegression_MoAFlagsUnsynthesizedOnConvergedStop(t *testing.T) {
	pub := &mockPublisher{}
	exec := &scriptedExecutor{content: map[string]string{
		"a":   "raw proposal from a",
		"b":   "raw proposal from b FINAL",
		"agg": "the real synthesis",
	}}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

	groupID, err := gm.Start(context.Background(), "b3-unsynth-1", "p1", "solve X", "moa",
		[]Participant{proposer("a"), proposer("b"), aggregator("agg")},
		GroupOptions{Rounds: 2, Moderator: "agg", StopKeywords: []string{"FINAL"}}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	synthesis, waitErr := waitGroup(t, gm, groupID, 5*time.Second)
	if waitErr != nil {
		t.Fatalf("Wait err = %v, want nil (a converged stop is not an error)", waitErr)
	}
	if !strings.HasPrefix(synthesis, unsynthesizedPrefix) {
		t.Errorf("synthesis = %q, want it to start with %q", synthesis, unsynthesizedPrefix)
	}
	if !strings.Contains(synthesis, "raw proposal from b") {
		t.Errorf("synthesis = %q, want it to carry the fallback turn text", synthesis)
	}
	if exec.spoke("agg") {
		t.Error("aggregator spoke; the test no longer exercises the fallback")
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not tracked")
	}
	if st.Status != StatusDone {
		t.Errorf("state.Status = %q, want %q", st.Status, StatusDone)
	}

	// The marker must reach clients through group.complete as well, not only
	// through Wait's return value.
	complete := pub.byEvent("group.complete")
	if len(complete) != 1 {
		t.Fatalf("group.complete events = %d, want 1", len(complete))
	}
	if !strings.HasPrefix(complete[0].Content, unsynthesizedPrefix) {
		t.Errorf("group.complete Content = %q, want the unsynthesized marker", complete[0].Content)
	}
}

// TestRegression_MoAFailedAggregatorStillReportsError documents what happens
// when the aggregator's own turn fails: the run aborts with an error (it does
// NOT continue and quietly return a proposal), and the partial result that
// finalize still publishes is marked as unsynthesized.
func TestRegression_MoAFailedAggregatorStillReportsError(t *testing.T) {
	aggErr := fmt.Errorf("provider exploded")
	exec := &scriptedExecutor{errFor: map[string]error{"agg": aggErr}}
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, exec.execute, rec.publish)

	groupID, err := gm.Start(context.Background(), "b3-aggfail-1", "p1", "solve X", "moa",
		[]Participant{proposer("a"), aggregator("agg")},
		GroupOptions{Rounds: 1, Moderator: "agg"}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	synthesis, waitErr := waitGroup(t, gm, groupID, 5*time.Second)
	if waitErr == nil {
		t.Fatal("Wait err = nil, want the aggregator's turn error")
	}
	if !strings.Contains(waitErr.Error(), "provider exploded") {
		t.Errorf("Wait err = %v, want it to wrap the aggregator failure", waitErr)
	}
	if !strings.HasPrefix(synthesis, unsynthesizedPrefix) {
		t.Errorf("synthesis = %q, want the unsynthesized marker on the partial result", synthesis)
	}
	assertExactlyOnePair(t, rec, groupID)
	if got := rec.terminalStatus(t, groupID); got != StatusError {
		t.Errorf("terminal status = %q, want %q", got, StatusError)
	}
}

// TestSynthesisLocked_FlagsGhostModeratorState covers the residual B3 shape — a
// Moderator outside participants — which Start now refuses. It can only arrive
// through a state built elsewhere (a persisted group rehydrated after its
// moderator was renamed), so the check is exercised directly on finalize to keep
// the safety net under test rather than unreachable-by-construction.
func TestSynthesisLocked_FlagsGhostModeratorState(t *testing.T) {
	exec := &scriptedExecutor{}
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, exec.execute, rec.publish)

	state := &GroupState{
		ID:           "b3-legacy-1",
		Task:         "solve X",
		Strategy:     "moa",
		Status:       StatusRunning,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Rounds:       1,
		Moderator:    "ghost", // not a participant: Start would reject this
		Participants: []Participant{proposer("a"), aggregator("agg")},
		Transcript: []Turn{
			{Index: 0, Layer: 0, Speaker: "a", Content: "proposal from a"},
		},
	}
	mg := newManaged(state)
	startManaged(gm, mg)

	// MoAAggregator resolves to "ghost", which has no turn, so the raw
	// proposal must be flagged rather than returned as the synthesis.
	gm.finalize(mg, StatusDone, nil)

	synthesis, waitErr := waitGroup(t, gm, state.ID, 5*time.Second)
	if waitErr != nil {
		t.Errorf("Wait err = %v, want nil", waitErr)
	}
	if synthesis != unsynthesizedPrefix+"proposal from a" {
		t.Errorf("synthesis = %q, want %q", synthesis, unsynthesizedPrefix+"proposal from a")
	}
	assertExactlyOnePair(t, rec, state.ID)
}

// TestSynthesisLocked_MarksOnlyWhenAggregatorSilent keeps the marker narrow: an
// aggregator that did speak wins even when a later proposer turn exists, and
// non-moa strategies are never marked.
func TestSynthesisLocked_MarksOnlyWhenAggregatorSilent(t *testing.T) {
	// A publisher is required: finalize always emits the terminal pair.
	gm := NewGroupManager(mockResolve, nil, (&mockPublisher{}).publish)

	t.Run("aggregator turn wins over later proposer turn", func(t *testing.T) {
		mg := newManaged(&GroupState{
			ID:        "synth-1",
			Strategy:  "moa",
			Status:    StatusRunning,
			Moderator: "agg",
			Participants: []Participant{
				proposer("a"), aggregator("agg"),
			},
			Transcript: []Turn{
				{Index: 0, Layer: 0, Speaker: "agg", Content: "synthesis"},
				{Index: 1, Layer: 1, Speaker: "a", Content: "later proposal"},
			},
		})
		gm.finalize(mg, StatusDone, nil)
		if got := mg.result; got != "synthesis" {
			t.Errorf("result = %q, want %q", got, "synthesis")
		}
	})

	t.Run("non-moa strategies are never marked", func(t *testing.T) {
		mg := newManaged(&GroupState{
			ID:           "synth-2",
			Strategy:     "round_robin",
			Status:       StatusRunning,
			Moderator:    "ghost",
			Participants: []Participant{plainParticipant("a")},
			Transcript:   []Turn{{Index: 0, Speaker: "a", Content: "last word"}},
		})
		gm.finalize(mg, StatusDone, nil)
		if got := mg.result; got != "last word" {
			t.Errorf("result = %q, want %q", got, "last word")
		}
	})

	t.Run("empty transcript stays empty", func(t *testing.T) {
		mg := newManaged(&GroupState{
			ID:           "synth-3",
			Strategy:     "moa",
			Status:       StatusRunning,
			Participants: []Participant{aggregator("agg")},
		})
		gm.finalize(mg, StatusError, fmt.Errorf("boom"))
		if got := mg.result; got != "" {
			t.Errorf("result = %q, want empty", got)
		}
	})
}
