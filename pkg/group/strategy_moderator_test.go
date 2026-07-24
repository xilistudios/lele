package group

import (
	"fmt"
	"testing"
)

func TestModeratorStrategy_Name(t *testing.T) {
	s := &ModeratorStrategy{}
	if s.Name() != "moderator" {
		t.Errorf("Name() = %q, want %q", s.Name(), "moderator")
	}
}

func TestModeratorStrategy_DelegatesToDecider(t *testing.T) {
	calls := 0
	decider := func(state *GroupState) (string, bool, error) {
		calls++
		switch calls {
		case 1:
			return "A", false, nil
		case 2:
			return "B", false, nil
		default:
			return "", true, nil
		}
	}

	state := &GroupState{
		ID: "mod-test",
		Participants: []Participant{
			{AgentID: "A"},
			{AgentID: "B"},
		},
	}

	s := &ModeratorStrategy{Decider: decider}

	// First call: decider says A.
	speakers, done, err := s.Next(state)
	if err != nil {
		t.Fatalf("call 1: error: %v", err)
	}
	if done {
		t.Fatal("call 1: done=true prematurely")
	}
	if len(speakers) != 1 || speakers[0] != "A" {
		t.Fatalf("call 1: speakers = %v, want [A]", speakers)
	}
	state.AddTurn(Turn{Index: 0, Speaker: "A", Content: "x"})

	// Second call: decider says B.
	speakers, done, err = s.Next(state)
	if err != nil {
		t.Fatalf("call 2: error: %v", err)
	}
	if done {
		t.Fatal("call 2: done=true prematurely")
	}
	if len(speakers) != 1 || speakers[0] != "B" {
		t.Fatalf("call 2: speakers = %v, want [B]", speakers)
	}
	state.AddTurn(Turn{Index: 1, Speaker: "B", Content: "y"})

	// Third call: decider says done.
	_, done, err = s.Next(state)
	if err != nil {
		t.Fatalf("call 3: error: %v", err)
	}
	if !done {
		t.Fatal("call 3: expected done=true")
	}
}

func TestModeratorStrategy_MaxTurnsHardStop(t *testing.T) {
	// The decider never says done, but MaxTurns should hard-stop.
	decider := func(state *GroupState) (string, bool, error) {
		return "A", false, nil // never done
	}

	state := &GroupState{
		ID:       "mod-max",
		MaxTurns: 2,
		Participants: []Participant{
			{AgentID: "A"},
		},
	}

	s := &ModeratorStrategy{Decider: decider}

	// Turn 0
	speakers, done, _ := s.Next(state)
	if done {
		t.Fatal("turn 0: done=true prematurely")
	}
	state.AddTurn(Turn{Index: 0, Speaker: speakers[0], Content: "x"})

	// Turn 1
	speakers, done, _ = s.Next(state)
	if done {
		t.Fatal("turn 1: done=true prematurely")
	}
	state.AddTurn(Turn{Index: 1, Speaker: speakers[0], Content: "x"})

	// Turn 2: MaxTurns=2, len(transcript)=2 → hard stop.
	_, done, _ = s.Next(state)
	if !done {
		t.Fatal("turn 2: expected done=true (MaxTurns hard-stop)")
	}
}

func TestModeratorStrategy_DeciderNilError(t *testing.T) {
	state := &GroupState{
		ID: "mod-nil",
		Participants: []Participant{
			{AgentID: "A"},
		},
	}

	s := &ModeratorStrategy{Decider: nil}
	_, _, err := s.Next(state)
	if err == nil {
		t.Fatal("expected error when Decider is nil")
	}
}

func TestModeratorStrategy_DeciderError(t *testing.T) {
	decider := func(state *GroupState) (string, bool, error) {
		return "", false, fmt.Errorf("LLM unavailable")
	}

	state := &GroupState{
		ID: "mod-err",
		Participants: []Participant{
			{AgentID: "A"},
		},
	}

	s := &ModeratorStrategy{Decider: decider}
	_, _, err := s.Next(state)
	if err == nil {
		t.Fatal("expected error from decider")
	}
}

func TestModeratorStrategy_StopKeywordHardStop(t *testing.T) {
	// Even though decider never says done, stop keyword should trigger.
	decider := func(state *GroupState) (string, bool, error) {
		return "A", false, nil
	}

	state := &GroupState{
		ID:           "mod-kw",
		StopKeywords: []string{"DONE"},
		Participants: []Participant{
			{AgentID: "A"},
		},
		Transcript: []Turn{
			{Speaker: "A", Content: "This is DONE now"},
		},
	}

	s := &ModeratorStrategy{Decider: decider}
	_, done, _ := s.Next(state)
	if !done {
		t.Fatal("expected done=true (stop keyword)")
	}
}
