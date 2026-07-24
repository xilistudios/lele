package group

import (
	"testing"
	"time"
)

func TestConstants(t *testing.T) {
	// Status constants
	if StatusRunning != "running" {
		t.Errorf("StatusRunning = %q, want %q", StatusRunning, "running")
	}
	if StatusDone != "done" {
		t.Errorf("StatusDone = %q, want %q", StatusDone, "done")
	}
	if StatusStopped != "stopped" {
		t.Errorf("StatusStopped = %q, want %q", StatusStopped, "stopped")
	}
	if StatusError != "error" {
		t.Errorf("StatusError = %q, want %q", StatusError, "error")
	}

	// Role constants
	if RoleProposer != "proposer" {
		t.Errorf("RoleProposer = %q, want %q", RoleProposer, "proposer")
	}
	if RoleAggregator != "aggregator" {
		t.Errorf("RoleAggregator = %q, want %q", RoleAggregator, "aggregator")
	}
	if RoleModerator != "moderator" {
		t.Errorf("RoleModerator = %q, want %q", RoleModerator, "moderator")
	}
	if RoleCritic != "critic" {
		t.Errorf("RoleCritic = %q, want %q", RoleCritic, "critic")
	}
}

func TestAddTurn(t *testing.T) {
	g := &GroupState{
		ID:        "test-group",
		Status:    StatusRunning,
		CreatedAt: time.Now(),
	}

	before := time.Now()

	t1 := Turn{Index: 0, Layer: 0, Speaker: "alice", Label: "Alice", Content: "hello", Tokens: 10}
	g.AddTurn(t1)

	if len(g.Transcript) != 1 {
		t.Fatalf("len(Transcript) = %d, want 1", len(g.Transcript))
	}
	if g.Transcript[0].Content != "hello" {
		t.Errorf("Transcript[0].Content = %q, want %q", g.Transcript[0].Content, "hello")
	}
	if g.TotalTokens != 10 {
		t.Errorf("TotalTokens = %d, want 10", g.TotalTokens)
	}
	if g.UpdatedAt.Before(before) {
		t.Errorf("UpdatedAt was not updated (is before AddTurn call)")
	}

	// Add a second turn — tokens should accumulate
	updatedAtAfterFirst := g.UpdatedAt
	time.Sleep(time.Millisecond) // ensure time advances

	t2 := Turn{Index: 1, Layer: 0, Speaker: "bob", Label: "Bob", Content: "world", Tokens: 20}
	g.AddTurn(t2)

	if len(g.Transcript) != 2 {
		t.Fatalf("len(Transcript) = %d, want 2", len(g.Transcript))
	}
	if g.TotalTokens != 30 {
		t.Errorf("TotalTokens = %d, want 30 (10+20)", g.TotalTokens)
	}
	if !g.UpdatedAt.After(updatedAtAfterFirst) {
		t.Errorf("UpdatedAt was not updated after second AddTurn")
	}
}

func TestLastTurn_Empty(t *testing.T) {
	g := &GroupState{ID: "empty"}
	turn, ok := g.LastTurn()
	if ok {
		t.Error("LastTurn() on empty transcript returned ok=true, want false")
	}
	if turn.Content != "" {
		t.Errorf("LastTurn() on empty transcript returned non-zero turn")
	}
}

func TestLastTurn_WithData(t *testing.T) {
	g := &GroupState{ID: "test"}
	g.AddTurn(Turn{Index: 0, Speaker: "a", Content: "first", Tokens: 5})
	g.AddTurn(Turn{Index: 1, Speaker: "b", Content: "second", Tokens: 8})

	turn, ok := g.LastTurn()
	if !ok {
		t.Fatal("LastTurn() returned ok=false, want true")
	}
	if turn.Content != "second" {
		t.Errorf("LastTurn().Content = %q, want %q", turn.Content, "second")
	}
	if turn.Tokens != 8 {
		t.Errorf("LastTurn().Tokens = %d, want 8", turn.Tokens)
	}
}

func TestParticipantByAgent_Found(t *testing.T) {
	g := &GroupState{
		ID: "test",
		Participants: []Participant{
			{AgentID: "alice", Role: RoleProposer, Label: "Alice"},
			{AgentID: "bob", Role: RoleAggregator, Label: "Bob"},
		},
	}

	p, ok := g.ParticipantByAgent("bob")
	if !ok {
		t.Fatal("ParticipantByAgent(bob) returned ok=false, want true")
	}
	if p.Label != "Bob" {
		t.Errorf("Label = %q, want %q", p.Label, "Bob")
	}
	if p.Role != RoleAggregator {
		t.Errorf("Role = %q, want %q", p.Role, RoleAggregator)
	}
}

func TestParticipantByAgent_NotFound(t *testing.T) {
	g := &GroupState{
		ID: "test",
		Participants: []Participant{
			{AgentID: "alice", Role: RoleProposer, Label: "Alice"},
		},
	}

	_, ok := g.ParticipantByAgent("charlie")
	if ok {
		t.Error("ParticipantByAgent(charlie) returned ok=true, want false")
	}
}

func TestGroupStateString(t *testing.T) {
	g := &GroupState{
		ID:           "g1",
		Status:       StatusRunning,
		Participants: []Participant{{AgentID: "a"}, {AgentID: "b"}},
		TotalTokens:  42,
	}
	g.AddTurn(Turn{Tokens: 42})

	s := g.String()
	if s == "" {
		t.Error("String() returned empty")
	}
	// Just verify it doesn't panic and contains the ID
	if len(s) < 5 {
		t.Errorf("String() = %q, seems too short", s)
	}
}

func TestGroupState_Snapshot_NilReceiver(t *testing.T) {
	var s *GroupState
	if s.Snapshot() != nil {
		t.Error("Snapshot() on nil receiver should return nil")
	}
}

func TestGroupState_Snapshot_IsDeepCopy(t *testing.T) {
	original := &GroupState{
		ID:        "g1",
		ProfileID: "p1",
		Task:      "solve it",
		Participants: []Participant{
			{AgentID: "a", Role: RoleProposer, Label: "Agent A"},
			{AgentID: "b", Role: RoleAggregator, Label: "Agent B"},
		},
		Strategy: "round_robin",
		Transcript: []Turn{
			{Index: 0, Layer: 0, Speaker: "a", Label: "Agent A", Content: "hello", CreatedAt: time.Now(), Tokens: 10},
		},
		Status:       StatusRunning,
		TotalTokens:  10,
		Rounds:       2,
		MaxTurns:     4,
		StopKeywords: []string{"stop", "halt"},
	}

	snap := original.Snapshot()
	if snap == nil {
		t.Fatal("Snapshot() returned nil")
	}

	// Verify values match.
	if snap.ID != original.ID {
		t.Errorf("ID mismatch: %s vs %s", snap.ID, original.ID)
	}
	if len(snap.Participants) != 2 {
		t.Errorf("Participants len = %d, want 2", len(snap.Participants))
	}
	if len(snap.Transcript) != 1 {
		t.Errorf("Transcript len = %d, want 1", len(snap.Transcript))
	}
	if len(snap.StopKeywords) != 2 {
		t.Errorf("StopKeywords len = %d, want 2", len(snap.StopKeywords))
	}
	if snap.TotalTokens != 10 {
		t.Errorf("TotalTokens = %d, want 10", snap.TotalTokens)
	}

	// Mutate original slices — snapshot must NOT change.
	original.Transcript = append(original.Transcript, Turn{Index: 1, Speaker: "b", Content: "world", Tokens: 20})
	original.Participants = append(original.Participants, Participant{AgentID: "c", Role: RoleCritic, Label: "C"})
	original.StopKeywords = append(original.StopKeywords, "quit")
	original.TotalTokens = 999
	original.Status = StatusDone

	if len(snap.Transcript) != 1 {
		t.Errorf("snapshot Transcript changed after original append: len=%d, want 1", len(snap.Transcript))
	}
	if len(snap.Participants) != 2 {
		t.Errorf("snapshot Participants changed after original append: len=%d, want 2", len(snap.Participants))
	}
	if len(snap.StopKeywords) != 2 {
		t.Errorf("snapshot StopKeywords changed after original append: len=%d, want 2", len(snap.StopKeywords))
	}
	if snap.TotalTokens != 10 {
		t.Errorf("snapshot TotalTokens changed: %d, want 10", snap.TotalTokens)
	}
	if snap.Status != StatusRunning {
		t.Errorf("snapshot Status changed: %s, want running", snap.Status)
	}

	// Mutate snapshot slices — original must NOT change.
	snap.Transcript[0].Content = "MUTATED"
	if original.Transcript[0].Content == "MUTATED" {
		t.Error("mutating snapshot Transcript entry changed original")
	}

	snap.Participants[0].Label = "MUTATED"
	if original.Participants[0].Label == "MUTATED" {
		t.Error("mutating snapshot Participant entry changed original")
	}

	snap.StopKeywords[0] = "MUTATED"
	if original.StopKeywords[0] == "MUTATED" {
		t.Error("mutating snapshot StopKeywords entry changed original")
	}
}
