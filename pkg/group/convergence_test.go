package group

import "testing"

func TestStopReason_MaxTurns(t *testing.T) {
	state := &GroupState{
		MaxTurns: 3,
		Transcript: []Turn{
			{Content: "a"},
			{Content: "b"},
			{Content: "c"},
		},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "max_turns" {
		t.Errorf("reason = %q, want %q", reason, "max_turns")
	}
}

func TestStopReason_MaxTurns_NotReached(t *testing.T) {
	state := &GroupState{
		MaxTurns: 5,
		Transcript: []Turn{
			{Content: "a"},
			{Content: "b"},
		},
	}
	stop, reason := StopReason(state)
	if stop {
		t.Errorf("expected stop=false, got stop=true reason=%q", reason)
	}
}

func TestStopReason_TokenBudget(t *testing.T) {
	state := &GroupState{
		TotalTokenBudget: 100,
		TotalTokens:      100,
		Transcript:       []Turn{{Content: "hello"}},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "token_budget" {
		t.Errorf("reason = %q, want %q", reason, "token_budget")
	}
}

func TestStopReason_TokenBudget_OverBudget(t *testing.T) {
	state := &GroupState{
		TotalTokenBudget: 100,
		TotalTokens:      150,
		Transcript:       []Turn{{Content: "hello"}},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "token_budget" {
		t.Errorf("reason = %q, want %q", reason, "token_budget")
	}
}

func TestStopReason_StopKeyword(t *testing.T) {
	state := &GroupState{
		StopKeywords: []string{"CONSENSUS", "FINAL"},
		Transcript: []Turn{
			{Content: "We have reached CONSENSUS on this topic."},
		},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "stop_keyword:CONSENSUS" {
		t.Errorf("reason = %q, want %q", reason, "stop_keyword:CONSENSUS")
	}
}

func TestStopReason_StopKeyword_CaseInsensitive(t *testing.T) {
	state := &GroupState{
		StopKeywords: []string{"final"},
		Transcript: []Turn{
			{Content: "This is the FINAL answer."},
		},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "stop_keyword:final" {
		t.Errorf("reason = %q, want %q", reason, "stop_keyword:final")
	}
}

func TestStopReason_StopKeyword_NoMatch(t *testing.T) {
	state := &GroupState{
		StopKeywords: []string{"CONSENSUS"},
		Transcript: []Turn{
			{Content: "I think we should keep going."},
		},
	}
	stop, _ := StopReason(state)
	if stop {
		t.Error("expected stop=false when no keyword matches")
	}
}

func TestStopReason_ConvergedRepetition(t *testing.T) {
	state := &GroupState{
		Transcript: []Turn{
			{Content: "The answer is 42."},
			{Content: "The answer is 42."},
			{Content: "the answer is 42.  "},
		},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "converged_repetition" {
		t.Errorf("reason = %q, want %q", reason, "converged_repetition")
	}
}

func TestStopReason_ConvergedRepetition_NotEnough(t *testing.T) {
	state := &GroupState{
		Transcript: []Turn{
			{Content: "same"},
			{Content: "same"},
		},
	}
	stop, _ := StopReason(state)
	if stop {
		t.Error("expected stop=false with fewer than 3 turns")
	}
}

func TestStopReason_ConvergedRepetition_DifferentContent(t *testing.T) {
	state := &GroupState{
		Transcript: []Turn{
			{Content: "a"},
			{Content: "b"},
			{Content: "a"},
		},
	}
	stop, _ := StopReason(state)
	if stop {
		t.Error("expected stop=false when last 3 turns are not identical")
	}
}

func TestStopReason_ConvergedRepetition_EmptyContent(t *testing.T) {
	state := &GroupState{
		Transcript: []Turn{
			{Content: ""},
			{Content: ""},
			{Content: ""},
		},
	}
	stop, _ := StopReason(state)
	if stop {
		t.Error("expected stop=false when content is empty (normalize returns empty)")
	}
}

func TestStopReason_NoStopConditions(t *testing.T) {
	state := &GroupState{
		MaxTurns:   10,
		Transcript: []Turn{{Content: "first"}, {Content: "second"}},
	}
	stop, reason := StopReason(state)
	if stop {
		t.Errorf("expected stop=false, got stop=true reason=%q", reason)
	}
}

func TestStopReason_Priority_MaxTurnsFirst(t *testing.T) {
	// MaxTurns and token budget both exceeded; max_turns should win (checked first).
	state := &GroupState{
		MaxTurns:         2,
		TotalTokenBudget: 100,
		TotalTokens:      200,
		StopKeywords:     []string{"STOP"},
		Transcript: []Turn{
			{Content: "STOP"},
			{Content: "STOP"},
		},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "max_turns" {
		t.Errorf("reason = %q, want %q (max_turns has highest priority)", reason, "max_turns")
	}
}

func TestStopReason_Priority_TokenBudgetSecond(t *testing.T) {
	state := &GroupState{
		MaxTurns:         10,
		TotalTokenBudget: 50,
		TotalTokens:      50,
		StopKeywords:     []string{"STOP"},
		Transcript:       []Turn{{Content: "STOP"}},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "token_budget" {
		t.Errorf("reason = %q, want %q", reason, "token_budget")
	}
}

func TestStopReason_Priority_KeywordThird(t *testing.T) {
	state := &GroupState{
		MaxTurns:     10,
		StopKeywords: []string{"STOP"},
		Transcript: []Turn{
			{Content: "no"},
			{Content: "no"},
			{Content: "STOP"},
		},
	}
	stop, reason := StopReason(state)
	if !stop {
		t.Fatal("expected stop=true")
	}
	if reason != "stop_keyword:STOP" {
		t.Errorf("reason = %q, want %q", reason, "stop_keyword:STOP")
	}
}
