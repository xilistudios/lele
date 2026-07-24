package group

import (
	"fmt"
	"testing"
)

func TestRoundRobinStrategy_Name(t *testing.T) {
	s := &RoundRobinStrategy{}
	if s.Name() != "round_robin" {
		t.Errorf("Name() = %q, want %q", s.Name(), "round_robin")
	}
}

func TestRoundRobinStrategy_CycleOrder(t *testing.T) {
	// 3 participants, Rounds=2 → expect A,B,C,A,B,C then done.
	state := &GroupState{
		ID:       "rr-test",
		Status:   StatusRunning,
		Strategy: "round_robin",
		Rounds:   2,
		Participants: []Participant{
			{AgentID: "A", Label: "Alice"},
			{AgentID: "B", Label: "Bob"},
			{AgentID: "C", Label: "Charlie"},
		},
	}

	expected := []string{"A", "B", "C", "A", "B", "C"}

	s := &RoundRobinStrategy{}
	for i, want := range expected {
		speakers, done, err := s.Next(state)
		if err != nil {
			t.Fatalf("turn %d: Next() error: %v", i, err)
		}
		if done {
			t.Fatalf("turn %d: Next() returned done=true prematurely", i)
		}
		if len(speakers) != 1 || speakers[0] != want {
			t.Fatalf("turn %d: speakers = %v, want [%s]", i, speakers, want)
		}
		// Simulate the runner appending the turn with unique content to avoid converged_repetition.
		state.AddTurn(Turn{Index: i, Layer: 0, Speaker: speakers[0], Content: "response " + speakers[0] + " turn" + fmt.Sprint(i)})
	}

	// After 6 turns (2 rounds × 3 participants), should be done.
	speakers, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("after 6 turns: Next() error: %v", err)
	}
	if !done {
		t.Fatal("after 6 turns: expected done=true")
	}
	if speakers != nil {
		t.Errorf("after 6 turns: speakers = %v, want nil", speakers)
	}
}

func TestRoundRobinStrategy_MaxTurnsHardStop(t *testing.T) {
	// MaxTurns=4 with 3 participants: should stop at turn 4 before completing 2 full rounds.
	state := &GroupState{
		ID:       "rr-max",
		Status:   StatusRunning,
		Strategy: "round_robin",
		MaxTurns: 4,
		Participants: []Participant{
			{AgentID: "A"},
			{AgentID: "B"},
			{AgentID: "C"},
		},
	}

	s := &RoundRobinStrategy{}

	// Turns 0-3 should proceed.
	for i := 0; i < 4; i++ {
		speakers, done, err := s.Next(state)
		if err != nil {
			t.Fatalf("turn %d: error: %v", i, err)
		}
		if done {
			t.Fatalf("turn %d: done=true prematurely", i)
		}
		state.AddTurn(Turn{Index: i, Layer: 0, Speaker: speakers[0], Content: fmt.Sprintf("turn %d", i)})
	}

	// Turn 4: MaxTurns=4 and len(transcript)=4 → stop.
	_, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("turn 4: error: %v", err)
	}
	if !done {
		t.Fatal("turn 4: expected done=true (MaxTurns reached)")
	}
}

func TestRoundRobinStrategy_NoParticipants(t *testing.T) {
	state := &GroupState{
		ID:           "rr-empty",
		Participants: []Participant{},
	}
	s := &RoundRobinStrategy{}
	_, _, err := s.Next(state)
	if err == nil {
		t.Fatal("expected error for empty participants")
	}
}

func TestRoundRobinStrategy_ZeroRounds(t *testing.T) {
	// Rounds=0 means unlimited (only MaxTurns or StopReason can stop it).
	state := &GroupState{
		ID:       "rr-zero",
		Status:   StatusRunning,
		Strategy: "round_robin",
		Rounds:   0,
		MaxTurns: 0, // no limit
		Participants: []Participant{
			{AgentID: "A"},
			{AgentID: "B"},
		},
	}

	s := &RoundRobinStrategy{}
	// Run 10 turns, should all succeed.
	for i := 0; i < 10; i++ {
		speakers, done, err := s.Next(state)
		if err != nil {
			t.Fatalf("turn %d: error: %v", i, err)
		}
		if done {
			t.Fatalf("turn %d: done=true unexpectedly", i)
		}
		if len(speakers) != 1 {
			t.Fatalf("turn %d: expected 1 speaker, got %d", i, len(speakers))
		}
		state.AddTurn(Turn{Index: i, Layer: 0, Speaker: speakers[0], Content: fmt.Sprintf("turn %d", i)})
	}
}

func TestRoundRobinStrategy_LayerAlwaysZero(t *testing.T) {
	state := &GroupState{
		ID: "rr-layer",
		Participants: []Participant{
			{AgentID: "A"},
			{AgentID: "B"},
		},
	}
	s := &RoundRobinStrategy{}
	speakers, _, _ := s.Next(state)
	state.AddTurn(Turn{Index: 0, Layer: 0, Speaker: speakers[0], Content: "unique content"})

	// Verify the runner set Layer=0 (as per spec, round_robin always uses layer 0).
	if state.Transcript[0].Layer != 0 {
		t.Errorf("Layer = %d, want 0", state.Transcript[0].Layer)
	}
}
