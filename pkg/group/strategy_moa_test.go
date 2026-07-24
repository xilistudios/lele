package group

import "testing"

func TestMoAStrategy_Name(t *testing.T) {
	s := &MoAStrategy{}
	if s.Name() != "moa" {
		t.Errorf("Name() = %q, want %q", s.Name(), "moa")
	}
}

func TestMoAProposers_WithExplicitRoles(t *testing.T) {
	state := &GroupState{
		Participants: []Participant{
			{AgentID: "P1", Role: RoleProposer},
			{AgentID: "P2", Role: RoleProposer},
			{AgentID: "MOD", Role: RoleAggregator},
		},
	}
	proposers := MoAProposers(state)
	if len(proposers) != 2 {
		t.Fatalf("len(proposers) = %d, want 2", len(proposers))
	}
	if proposers[0].AgentID != "P1" || proposers[1].AgentID != "P2" {
		t.Errorf("proposers = %v, want [P1, P2]", proposers)
	}
}

func TestMoAProposers_FallbackAllExceptModerator(t *testing.T) {
	state := &GroupState{
		Moderator: "MOD",
		Participants: []Participant{
			{AgentID: "A"},
			{AgentID: "B"},
			{AgentID: "MOD"},
		},
	}
	proposers := MoAProposers(state)
	if len(proposers) != 2 {
		t.Fatalf("len(proposers) = %d, want 2", len(proposers))
	}
	for _, p := range proposers {
		if p.AgentID == "MOD" {
			t.Error("moderator should not be in proposers")
		}
	}
}

func TestMoAAggregator_ModeratorField(t *testing.T) {
	state := &GroupState{
		Moderator: "X",
		Participants: []Participant{
			{AgentID: "A", Role: RoleProposer},
			{AgentID: "X"},
		},
	}
	if got := MoAAggregator(state); got != "X" {
		t.Errorf("MoAAggregator() = %q, want %q", got, "X")
	}
}

func TestMoAAggregator_RoleAggregator(t *testing.T) {
	state := &GroupState{
		Participants: []Participant{
			{AgentID: "A"},
			{AgentID: "B", Role: RoleAggregator},
		},
	}
	if got := MoAAggregator(state); got != "B" {
		t.Errorf("MoAAggregator() = %q, want %q", got, "B")
	}
}

func TestMoAAggregator_FallbackFirst(t *testing.T) {
	state := &GroupState{
		Participants: []Participant{
			{AgentID: "Z"},
			{AgentID: "Y"},
		},
	}
	if got := MoAAggregator(state); got != "Z" {
		t.Errorf("MoAAggregator() = %q, want %q", got, "Z")
	}
}

func TestMoACurrentLayer_Empty(t *testing.T) {
	state := &GroupState{
		Participants: []Participant{
			{AgentID: "P1", Role: RoleProposer},
			{AgentID: "MOD", Role: RoleAggregator},
		},
	}
	if got := MoACurrentLayer(state); got != 0 {
		t.Errorf("MoACurrentLayer() = %d, want 0 (empty transcript)", got)
	}
}

func TestMoACurrentLayer_AfterLayer0Complete(t *testing.T) {
	state := &GroupState{
		Participants: []Participant{
			{AgentID: "P1", Role: RoleProposer},
			{AgentID: "MOD", Role: RoleAggregator},
		},
		Transcript: []Turn{
			{Speaker: "P1", Layer: 0},
			{Speaker: "MOD", Layer: 0},
		},
	}
	if got := MoACurrentLayer(state); got != 1 {
		t.Errorf("MoACurrentLayer() = %d, want 1", got)
	}
}

func TestMoAStrategy_FullTwoRounds(t *testing.T) {
	// 2 proposers (P1, P2) + 1 moderator/aggregator (MOD), Rounds=2.
	// Expected flow:
	//   Next → [P1, P2]  (layer 0 proposers)
	//   Next → [MOD]     (layer 0 aggregator)
	//   Next → [P1, P2]  (layer 1 proposers)
	//   Next → [MOD]     (layer 1 aggregator)
	//   Next → done
	state := &GroupState{
		ID:       "moa-test",
		Status:   StatusRunning,
		Strategy: "moa",
		Rounds:   2,
		Participants: []Participant{
			{AgentID: "P1", Role: RoleProposer, Label: "Proposer 1"},
			{AgentID: "P2", Role: RoleProposer, Label: "Proposer 2"},
			{AgentID: "MOD", Role: RoleAggregator, Label: "Moderator"},
		},
	}

	s := &MoAStrategy{}

	// Helper: simulate the runner's behavior of appending turns.
	addTurns := func(speakers []string) {
		layer := MoACurrentLayer(state)
		for _, sp := range speakers {
			state.AddTurn(Turn{
				Index:   len(state.Transcript),
				Layer:   layer,
				Speaker: sp,
				Content: "response from " + sp,
			})
		}
	}

	// Iteration 1: proposers for layer 0.
	speakers, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("iter 1: error: %v", err)
	}
	if done {
		t.Fatal("iter 1: done=true prematurely")
	}
	assertSpeakers(t, speakers, "P1", "P2")
	addTurns(speakers)
	if got := MoACurrentLayer(state); got != 0 {
		t.Fatalf("after iter 1: MoACurrentLayer=%d, want 0 (aggregator hasn't spoken)", got)
	}

	// Iteration 2: aggregator for layer 0.
	speakers, done, err = s.Next(state)
	if err != nil {
		t.Fatalf("iter 2: error: %v", err)
	}
	if done {
		t.Fatal("iter 2: done=true prematurely")
	}
	assertSpeakers(t, speakers, "MOD")
	addTurns(speakers)
	if got := MoACurrentLayer(state); got != 1 {
		t.Fatalf("after iter 2: MoACurrentLayer=%d, want 1", got)
	}

	// Iteration 3: proposers for layer 1.
	speakers, done, err = s.Next(state)
	if err != nil {
		t.Fatalf("iter 3: error: %v", err)
	}
	if done {
		t.Fatal("iter 3: done=true prematurely")
	}
	assertSpeakers(t, speakers, "P1", "P2")
	addTurns(speakers)
	if got := MoACurrentLayer(state); got != 1 {
		t.Fatalf("after iter 3: MoACurrentLayer=%d, want 1 (aggregator hasn't spoken for layer 1)", got)
	}

	// Iteration 4: aggregator for layer 1.
	speakers, done, err = s.Next(state)
	if err != nil {
		t.Fatalf("iter 4: error: %v", err)
	}
	if done {
		t.Fatal("iter 4: done=true prematurely")
	}
	assertSpeakers(t, speakers, "MOD")
	addTurns(speakers)
	if got := MoACurrentLayer(state); got != 2 {
		t.Fatalf("after iter 4: MoACurrentLayer=%d, want 2", got)
	}

	// Iteration 5: Rounds=2, layer=2 → done.
	_, done, err = s.Next(state)
	if err != nil {
		t.Fatalf("iter 5: error: %v", err)
	}
	if !done {
		t.Fatal("iter 5: expected done=true (Rounds reached)")
	}

	// Verify transcript order and layer stamps.
	expectedSeq := []struct {
		speaker string
		layer   int
	}{
		{"P1", 0}, {"P2", 0}, {"MOD", 0},
		{"P1", 1}, {"P2", 1}, {"MOD", 1},
	}
	if len(state.Transcript) != len(expectedSeq) {
		t.Fatalf("transcript length = %d, want %d", len(state.Transcript), len(expectedSeq))
	}
	for i, exp := range expectedSeq {
		if state.Transcript[i].Speaker != exp.speaker {
			t.Errorf("transcript[%d].Speaker = %q, want %q", i, state.Transcript[i].Speaker, exp.speaker)
		}
		if state.Transcript[i].Layer != exp.layer {
			t.Errorf("transcript[%d].Layer = %d, want %d", i, state.Transcript[i].Layer, exp.layer)
		}
	}
}

func TestMoAStrategy_MaxTurnsHardStop(t *testing.T) {
	state := &GroupState{
		ID:       "moa-max",
		MaxTurns: 2,
		Participants: []Participant{
			{AgentID: "P1", Role: RoleProposer},
			{AgentID: "P2", Role: RoleProposer},
			{AgentID: "MOD", Role: RoleAggregator},
		},
	}

	s := &MoAStrategy{}

	// Turn 0: P1, P2
	speakers, done, _ := s.Next(state)
	layer := MoACurrentLayer(state)
	for _, sp := range speakers {
		state.AddTurn(Turn{Index: len(state.Transcript), Layer: layer, Speaker: sp, Content: "x"})
	}
	if done {
		t.Fatal("turn 0: done=true prematurely")
	}

	// Turn 1 (now 2 turns in transcript → MaxTurns=2 triggers)
	_, done, _ = s.Next(state)
	if !done {
		t.Fatal("expected done=true (MaxTurns reached)")
	}
}

func TestMoAStrategy_StopKeyword(t *testing.T) {
	state := &GroupState{
		StopKeywords: []string{"CONSENSUS"},
		Participants: []Participant{
			{AgentID: "P1", Role: RoleProposer},
			{AgentID: "MOD", Role: RoleAggregator},
		},
		Transcript: []Turn{
			{Speaker: "P1", Layer: 0, Content: "We have CONSENSUS on this."},
		},
	}

	s := &MoAStrategy{}
	_, done, _ := s.Next(state)
	if !done {
		t.Fatal("expected done=true (stop keyword in last turn)")
	}
}

// assertSpeakers checks that speakers contains exactly the expected AgentIDs (order matters).
func assertSpeakers(t *testing.T, speakers []string, expected ...string) {
	t.Helper()
	if len(speakers) != len(expected) {
		t.Fatalf("speakers = %v (len %d), want %v (len %d)", speakers, len(speakers), expected, len(expected))
	}
	for i, want := range expected {
		if speakers[i] != want {
			t.Errorf("speakers[%d] = %q, want %q", i, speakers[i], want)
		}
	}
}
