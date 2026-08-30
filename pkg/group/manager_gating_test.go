// Lele - Ultra-lightweight personal AI agent
// Tests for the GroupManager feature gate (B10: single gating point).
// License: MIT

package group

import (
	"context"
	"errors"
	"testing"
)

// TestStart_RejectedWhenDisabledHookFalse: with an enabled hook that returns
// false, Start must refuse with ErrGroupsDisabled and create no state.
func TestStart_RejectedWhenDisabledHookFalse(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	gm.SetEnabledHook(func() bool { return false })

	id, err := gm.Start(context.Background(), "gate-off-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "test", "0")

	if !errors.Is(err, ErrGroupsDisabled) {
		t.Fatalf("expected ErrGroupsDisabled, got %v", err)
	}
	if id != "" {
		t.Errorf("expected empty group id, got %q", id)
	}
	// No state created, nothing executed, nothing published.
	if _, ok := gm.Status("gate-off-1"); ok {
		t.Error("group must not exist after rejected Start")
	}
	if len(gm.List()) != 0 {
		t.Errorf("expected no groups registered, got %d", len(gm.List()))
	}
	if exec.callCount != 0 {
		t.Errorf("executor must not be called when disabled, calls=%d", exec.callCount)
	}
	if pub.count() != 0 {
		t.Errorf("no events must be published when disabled, got %d", pub.count())
	}
}

// TestStart_AllowedWhenHookNil: managers constructed directly (as the ~30
// existing tests do) must keep working — nil hook means allowed.
func TestStart_AllowedWhenHookNil(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	// No SetEnabledHook call at all.

	id, err := gm.Start(context.Background(), "gate-nil-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "test", "0")
	if err != nil {
		t.Fatalf("nil hook must allow Start, got %v", err)
	}
	if id != "gate-nil-1" {
		t.Errorf("expected group id gate-nil-1, got %q", id)
	}
	if _, err := gm.Wait(id); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
}

// TestStart_HookEvaluatedPerCall: toggling the hook flips the decision on
// subsequent Start calls (config reload semantics).
func TestStart_HookEvaluatedPerCall(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)
	enabled := false
	gm.SetEnabledHook(func() bool { return enabled })

	if _, err := gm.Start(context.Background(), "gate-t-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "test", "0"); !errors.Is(err, ErrGroupsDisabled) {
		t.Fatalf("expected disabled error first, got %v", err)
	}

	enabled = true
	id, err := gm.Start(context.Background(), "gate-t-2", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "test", "0")
	if err != nil {
		t.Fatalf("expected Start allowed after enabling, got %v", err)
	}
	if id != "gate-t-2" {
		t.Errorf("unexpected id %q", id)
	}
	if _, err := gm.Wait(id); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}

	// Resetting the hook restores the permissive default.
	gm.SetEnabledHook(nil)
	enabled = false // hook is gone; the flag value must be ignored
	if _, err := gm.Start(context.Background(), "gate-t-3", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "test", "0"); err != nil {
		t.Fatalf("nil hook must always allow, got %v", err)
	}
	if _, err := gm.Wait("gate-t-3"); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
}
