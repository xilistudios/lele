package group

import "testing"

func TestPipelineStrategy_Name(t *testing.T) {
	s := &PipelineStrategy{}
	if s.Name() != "pipeline" {
		t.Errorf("Name() = %q, want %q", s.Name(), "pipeline")
	}
}

func TestPipelineStrategy_SequentialOrder(t *testing.T) {
	state := &GroupState{
		ID:       "pipe-test",
		Status:   StatusRunning,
		Strategy: "pipeline",
		Participants: []Participant{
			{AgentID: "A", Label: "Architect"},
			{AgentID: "B", Label: "Builder"},
			{AgentID: "C", Label: "Checker"},
		},
	}

	expected := []string{"A", "B", "C"}

	s := &PipelineStrategy{}
	for i, want := range expected {
		speakers, done, err := s.Next(state)
		if err != nil {
			t.Fatalf("turn %d: error: %v", i, err)
		}
		if done {
			t.Fatalf("turn %d: done=true prematurely", i)
		}
		if len(speakers) != 1 || speakers[0] != want {
			t.Fatalf("turn %d: speakers = %v, want [%s]", i, speakers, want)
		}
		state.AddTurn(Turn{Index: i, Layer: 0, Speaker: speakers[0], Content: "output"})
	}

	// After all 3 participants have spoken, should be done.
	speakers, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("after 3 turns: error: %v", err)
	}
	if !done {
		t.Fatal("after 3 turns: expected done=true")
	}
	if speakers != nil {
		t.Errorf("after 3 turns: speakers = %v, want nil", speakers)
	}
}

func TestPipelineStrategy_SingleParticipant(t *testing.T) {
	state := &GroupState{
		ID:           "pipe-single",
		Participants: []Participant{{AgentID: "X"}},
	}

	s := &PipelineStrategy{}
	speakers, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if done {
		t.Fatal("done=true before first turn")
	}
	if len(speakers) != 1 || speakers[0] != "X" {
		t.Fatalf("speakers = %v, want [X]", speakers)
	}
	state.AddTurn(Turn{Index: 0, Layer: 0, Speaker: "X", Content: "done"})

	_, done, err = s.Next(state)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !done {
		t.Fatal("expected done=true after single participant spoke")
	}
}

func TestPipelineStrategy_MaxTurnsStop(t *testing.T) {
	state := &GroupState{
		ID:       "pipe-max",
		MaxTurns: 2,
		Participants: []Participant{
			{AgentID: "A"},
			{AgentID: "B"},
			{AgentID: "C"},
		},
	}

	s := &PipelineStrategy{}
	// Turn 0: A speaks.
	speakers, done, _ := s.Next(state)
	state.AddTurn(Turn{Index: 0, Speaker: speakers[0], Content: "x"})
	if done {
		t.Fatal("done=true prematurely at turn 0")
	}

	// Turn 1: B speaks.
	speakers, done, _ = s.Next(state)
	state.AddTurn(Turn{Index: 1, Speaker: speakers[0], Content: "x"})
	if done {
		t.Fatal("done=true prematurely at turn 1")
	}

	// Now len(transcript)=2 >= MaxTurns=2 → StopReason triggers.
	_, done, _ = s.Next(state)
	if !done {
		t.Fatal("expected done=true (MaxTurns hit)")
	}
}

func TestPipelineStrategy_EmptyParticipants(t *testing.T) {
	state := &GroupState{
		ID:           "pipe-empty",
		Participants: []Participant{},
	}
	s := &PipelineStrategy{}
	speakers, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 0 participants, 0 turns → turns >= n (0 >= 0) → done.
	if !done {
		t.Fatal("expected done=true for empty participants")
	}
	if speakers != nil {
		t.Errorf("speakers = %v, want nil", speakers)
	}
}
