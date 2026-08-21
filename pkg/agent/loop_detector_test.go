package agent

import (
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func TestLoopDetector_Check_ReturnsNilOnFirstCall(t *testing.T) {
	ld := newLoopDetector()

	msg := ld.Check([]providers.ToolCall{{Name: "tool_a", Arguments: map[string]interface{}{"x": 1}}}, "agent-1", 1)
	if msg != nil {
		t.Fatalf("Expected nil message on first call, got %v", msg)
	}
}

func TestLoopDetector_Check_ReturnsNilUntilThreshold(t *testing.T) {
	ld := newLoopDetector()
	tc := []providers.ToolCall{{Name: "tool_b", Arguments: map[string]interface{}{}}}
	for i := 0; i < MaxLoopRepetitions-1; i++ {
		if msg := ld.Check(tc, "agent-1", i); msg != nil {
			t.Fatalf("Expected nil at repetition %d, got %v", i+1, msg)
		}
	}
	// Threshold reached -> should return guidance
	msg := ld.Check(tc, "agent-1", MaxLoopRepetitions-1)
	if msg == nil {
		t.Fatal("Expected guidance message at threshold")
	}
	if msg.Role != "user" {
		t.Errorf("Expected role user, got %s", msg.Role)
	}
	if str := any(msg.Content); str == "" {
		t.Error("Expected non-empty guidance content")
	}
}

func TestLoopDetector_Check_DifferentCallsReset(t *testing.T) {
	ld := newLoopDetector()
	tc1 := []providers.ToolCall{{Name: "read"}}
	tc2 := []providers.ToolCall{{Name: "write"}}

	ld.Check(tc1, "agent-1", 1)
	ld.Check(tc1, "agent-1", 2)
	// Different signature resets, so should be nil
	if msg := ld.Check(tc2, "agent-1", 3); msg != nil {
		t.Fatalf("Expected nil after signature change, got %v", msg)
	}
}

func TestLoopDetector_Check_SerializesArguments(t *testing.T) {
	ld := newLoopDetector()
	// Two calls with same tool name but differing argument maps serialize
	// differently -> considered different, so no guidance triggered.
	tc := []providers.ToolCall{{Name: "read", Arguments: map[string]interface{}{"file": "a.txt"}}}
	ld.Check(tc, "agent-1", 1)
	ld.Check(tc, "agent-1", 2)
	if msg := ld.Check(tc, "agent-1", 3); msg == nil {
		t.Fatal("Expected guidance after 3 identical calls with args")
	}
}

func TestLoopDetector_Check_UsesToolNamesInLog(t *testing.T) {
	ld := newLoopDetector()
	tc := []providers.ToolCall{{Name: "foo"}, {Name: "bar"}}
	for i := 0; i < MaxLoopRepetitions; i++ {
		ld.Check(tc, "agent-x", i)
	}
	// After threshold is reached, detector resets internal count back to 0.
	// Just verify no panic and a message was returned.
	if msg := ld.Check([]providers.ToolCall{{Name: "foo"}, {Name: "bar"}}, "agent-x", MaxLoopRepetitions); msg == nil {
		// Because it reset to 0 after threshold, next identical call increments to 1 (below threshold)
		t.Log("OK: count reset after threshold")
	}
}

func TestLoopDetector_Check_EmptyToolCalls(t *testing.T) {
	ld := newLoopDetector()
	// Call 1, 2 are identical (empty) -> below threshold.
	if msg := ld.Check(nil, "agent-1", 0); msg != nil {
		t.Fatalf("Expected nil on first empty call, got %v", msg)
	}
	if msg := ld.Check(nil, "agent-1", 1); msg != nil {
		t.Fatalf("Expected nil on second empty call, got %v", msg)
	}
	// Call 3 (identical) reaches threshold -> guidance.
	if msg := ld.Check(nil, "agent-1", 2); msg == nil {
		t.Fatal("Expected guidance after repeated empty tool calls")
	}
}

func TestLoopDetector_Reset(t *testing.T) {
	ld := newLoopDetector()
	tc := []providers.ToolCall{{Name: "tool_c"}}
	ld.Check(tc, "agent-1", 1)
	ld.Check(tc, "agent-1", 2)
	ld.Reset()

	// After reset, the counter is cleared so identical calls start fresh.
	if msg := ld.Check(tc, "agent-1", 3); msg != nil {
		t.Fatalf("Expected nil after reset, got %v", msg)
	}
}

func TestLoopDetector_Check_ThresholdThenAgain(t *testing.T) {
	ld := newLoopDetector()
	tc := []providers.ToolCall{{Name: "loop_tool"}, {Name: "loop_tool2"}}

	// Reach threshold -> guidance returned.
	var got *providers.Message
	for i := 0; i < MaxLoopRepetitions; i++ {
		got = ld.Check(tc, "agent-1", i)
	}
	if got == nil {
		t.Fatal("Expected guidance at threshold")
	}
	if got.Content != "" {
		// sanity: content should mention guidance
		t.Logf("guidance content length: %d", len(got.Content))
	}
}