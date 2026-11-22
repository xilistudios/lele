package group

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

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

func (r *toggleResolver) revoke(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.alive, agentID)
}

type scriptedExecutor struct {
	mu      sync.Mutex
	calls   []string
	gate    map[string]chan struct{}
	content map[string]string
	errFor  map[string]error
}

func (e *scriptedExecutor) execute(ctx context.Context, req TurnRequest) (string, int, error) {
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
	if strings.Contains(waitErr.Error(), "iterations exhausted") {
		t.Errorf("Wait err = %v, must not be the iteration-exhaustion error", waitErr)
	}
	if elapsed > 2*time.Second {
		t.Errorf("group took %s to fail, want < 2s (fail fast, not %d loops)", elapsed, maxGroupIterations)
	}
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

func TestRegression_UnresolvableSpeakerFromLoopStart(t *testing.T) {
	res := newToggleResolver("a", "agg")
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

func TestRegression_UnresolvableSpeakerFailsFastForEveryStrategy(t *testing.T) {
	for _, strategy := range []string{"round_robin", "pipeline", "moderator", "moa"} {
		t.Run(strategy, func(t *testing.T) {
			res := newToggleResolver("a")
			exec := &scriptedExecutor{}
			rec := newLifecycleRecorder()
			gm := NewGroupManager(res.resolve, exec.execute, rec.publish)

			state := &GroupState{
				ID:           "b2-" + strategy,
				Task:         "solve X",
				Strategy:     strategy,
				Status:       StatusRunning,
				CreatedAt:    time.Now(),
				UpdatedAt:    time.Now(),
				Rounds:       3,
				MaxTurns:     MaxGroupTurnsCeiling,
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

	if _, err := gm.Start(context.Background(), "b3-mod-2", "p1", "solve X", "moderator",
		participants, GroupOptions{MaxTurns: 2, Moderator: "b"}, "ch", "chat"); err != nil {
		t.Fatalf("Start with in-participants moderator: %v", err)
	}
	if _, err := waitGroup(t, gm, "b3-mod-2", 5*time.Second); err != nil {
		t.Errorf("Wait for b3-mod-2: %v", err)
	}
}

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

	complete := pub.byEvent("group.complete")
	if len(complete) != 1 {
		t.Fatalf("group.complete events = %d, want 1", len(complete))
	}
	if !strings.HasPrefix(complete[0].Content, unsynthesizedPrefix) {
		t.Errorf("group.complete Content = %q, want the unsynthesized marker", complete[0].Content)
	}
}

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
		Moderator:    "ghost",
		Participants: []Participant{proposer("a"), aggregator("agg")},
		Transcript: []Turn{
			{Index: 0, Layer: 0, Speaker: "a", Content: "proposal from a"},
		},
	}
	mg := newManaged(state)
	startManaged(gm, mg)

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

func TestSynthesisLocked_MarksOnlyWhenAggregatorSilent(t *testing.T) {
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
