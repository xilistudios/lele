// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package group

import (
	"context"
	"testing"
	"time"
)

// --- StopByAgent tests ---
//
// StopByAgent exists for the config-reload path: when an agent leaves the
// registry, running groups that included it as a speaker must be stopped,
// while groups made only of surviving agents keep running.

func startBlockingGroup(t *testing.T, gm *GroupManager, id string, be *blockingExecutor, participants []Participant, moderator string) {
	t.Helper()
	_, err := gm.Start(context.Background(), id, "p1", "task", "round_robin",
		participants, GroupOptions{Rounds: 100, Moderator: moderator}, "ch", "chat-"+id)
	if err != nil {
		t.Fatalf("Start %s: %v", id, err)
	}
	// Let the run goroutine enter the executor before asserting on it.
	time.Sleep(50 * time.Millisecond)
}

func groupStatus(t *testing.T, gm *GroupManager, id string) string {
	t.Helper()
	st, ok := gm.Status(id)
	if !ok {
		t.Fatalf("group %s not tracked", id)
	}
	return st.Status
}

// waitGroupStatus polls until the group reaches want or the deadline expires.
// Stop only signals cancellation; the run goroutine writes the terminal status
// when it observes it, so assertions must not assume it is synchronous.
func waitGroupStatus(t *testing.T, gm *GroupManager, id, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if groupStatus(t, gm, id) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("group %s status = %q, want %q (timeout)", id, groupStatus(t, gm, id), want)
}

func TestStopByAgent_StopsGroupContainingAgent(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)
	t.Cleanup(func() { close(be.unblockCh) })

	startBlockingGroup(t, gm, "g-hit", be, []Participant{plainParticipant("a"), plainParticipant("b")}, "")
	startBlockingGroup(t, gm, "g-miss", be, []Participant{plainParticipant("b"), plainParticipant("c")}, "")

	if got := gm.StopByAgent("a"); got != 1 {
		t.Fatalf("StopByAgent(a) = %d, want 1", got)
	}

	waitGroupStatus(t, gm, "g-hit", StatusStopped)
	if s := groupStatus(t, gm, "g-miss"); s != StatusRunning {
		t.Errorf("g-miss status = %q, want %q (agent a is not a participant)", s, StatusRunning)
	}
	gm.Stop("g-miss") // cleanup: don't leak the running goroutine
	waitGroupStatus(t, gm, "g-miss", StatusStopped)
}

// TestGroupIncludesAgent_ModeratorIsAlsoSpokenFor covers the membership rule
// StopByAgent uses. It is exercised directly because Start rejects a moderator
// that is not among the participants, so a moderator-only membership cannot be
// built through the public API.
func TestGroupIncludesAgent_ModeratorIsAlsoSpokenFor(t *testing.T) {
	state := &GroupState{
		Participants: []Participant{plainParticipant("a")},
		Moderator:    "agg",
	}
	if !groupIncludesAgent(state, "agg") {
		t.Error("groupIncludesAgent(moderator) = false, want true")
	}
	if !groupIncludesAgent(state, "a") {
		t.Error("groupIncludesAgent(participant) = false, want true")
	}
	if groupIncludesAgent(state, "c") {
		t.Error("groupIncludesAgent(stranger) = true, want false")
	}
	if groupIncludesAgent(nil, "a") {
		t.Error("groupIncludesAgent(nil) = true, want false")
	}
}

func TestStopByAgent_IgnoresUnknownAgentAndFinishedGroups(t *testing.T) {
	be := &blockingExecutor{unblockCh: make(chan struct{})}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, be.execute, pub.publish)
	t.Cleanup(func() { close(be.unblockCh) })

	if got := gm.StopByAgent(""); got != 0 {
		t.Errorf("StopByAgent(empty) = %d, want 0", got)
	}
	if got := gm.StopByAgent("nobody"); got != 0 {
		t.Errorf("StopByAgent(unknown) = %d, want 0", got)
	}

	// A finished group must not be counted or re-stopped.
	startBlockingGroup(t, gm, "g-done", be, []Participant{plainParticipant("a")}, "")
	gm.Stop("g-done")
	waitGroupStatus(t, gm, "g-done", StatusStopped)
	if got := gm.StopByAgent("a"); got != 0 {
		t.Errorf("StopByAgent after group finished = %d, want 0", got)
	}
}
