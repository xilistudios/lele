package group

import (
	"context"
	"errors"
	"testing"
)

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

func TestStart_AllowedWhenHookNil(t *testing.T) {
	exec := &mockExecutor{}
	pub := &mockPublisher{}
	gm := NewGroupManager(mockResolve, exec.execute, pub.publish)

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

	gm.SetEnabledHook(nil)
	enabled = false
	if _, err := gm.Start(context.Background(), "gate-t-3", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "test", "0"); err != nil {
		t.Fatalf("nil hook must always allow, got %v", err)
	}
	if _, err := gm.Wait("gate-t-3"); err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
}
