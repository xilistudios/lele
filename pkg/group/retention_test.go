package group

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Regression tests for B6: finished groups must not accumulate in memory.
//
// Before the retention sweep, GroupManager.groups never dropped a terminated
// group, so every group ever started — with its full transcript — stayed in RAM
// and kept appearing in List()/AllSnapshots() (unbounded WS welcome payload and
// "/group list"). The rule under test:
//
//   - a group in a terminal status (done | stopped | error) becomes invisible
//     to Start/Status/List/AllSnapshots/Wait once retention has elapsed since
//     finalize stamped finishedAt;
//   - a running group is never evicted, no matter how small retention is;
//   - within the retention window a finished group stays visible, which is what
//     lets the UI welcome snapshot still show the group that just ended.
//
// Retention values are small but not degenerate (200ms) so the "still visible"
// assertions are not timing-flaky, while the eviction assertions only need to
// sleep past the window.
// ---------------------------------------------------------------------------

// releaseStep unblocks a stepExecutor parked inside a turn. Idempotent so a
// test can call it from both the success and the failure path.
func releaseStep(e *stepExecutor) {
	select {
	case <-e.release:
	default:
		close(e.release)
	}
}

// runQuickGroup starts a one-shot group and blocks until it reaches a terminal
// status, returning the group ID.
func runQuickGroup(t *testing.T, gm *GroupManager, id string) string {
	t.Helper()
	groupID, err := gm.Start(context.Background(), id, "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitWithTimeout(t, 5*time.Second, "group to finish", func() {
		if _, err := gm.Wait(groupID); err != nil {
			t.Errorf("Wait: %v", err)
		}
	})
	return groupID
}

func TestRegression_FinishedGroupsEvictedAfterRetention(t *testing.T) {
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, rec.publish)
	gm.SetRetention(200 * time.Millisecond)

	groupID := runQuickGroup(t, gm, "ret-done-1")

	// Inside the window: still tracked, and Wait stays idempotent.
	if _, ok := gm.Status(groupID); !ok {
		t.Fatal("finished group not visible inside retention window")
	}
	waitWithTimeout(t, 2*time.Second, "second Wait on retained finished group", func() {
		if _, err := gm.Wait(groupID); err != nil {
			t.Errorf("second Wait: %v", err)
		}
	})

	time.Sleep(400 * time.Millisecond) // > retention

	if got := gm.List(); len(got) != 0 {
		t.Fatalf("List after retention = %d groups, want 0 (eviction broken)", len(got))
	}
	if _, ok := gm.Status(groupID); ok {
		t.Error("Status after retention = found, want not found")
	}
	if snaps := gm.AllSnapshots(); len(snaps) != 0 {
		t.Errorf("AllSnapshots after retention = %d, want 0", len(snaps))
	}
}

// TestRegression_ActiveGroupsNeverEvicted pins the guard that only terminal
// statuses are swept: with a 1ms retention the in-flight group must stay
// visible, otherwise a long-running group would vanish from the UI mid-run.
func TestRegression_ActiveGroupsNeverEvicted(t *testing.T) {
	rec := newLifecycleRecorder()
	exec := newStepExecutor(1) // turn 1 fast, turn 2 blocks until release
	gm := NewGroupManager(mockResolve, exec.execute, rec.publish)
	gm.SetRetention(time.Millisecond)

	groupID, err := gm.Start(context.Background(), "ret-active-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{MaxTurns: 50}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer releaseStep(exec)
	exec.waitBlockedFor(t, 2)

	// Sleep far beyond the retention window while the group is running.
	time.Sleep(50 * time.Millisecond)

	states := gm.List()
	if len(states) != 1 {
		t.Fatalf("List while running = %d groups, want 1", len(states))
	}
	if states[0].Status != StatusRunning {
		t.Errorf("status = %q, want %q", states[0].Status, StatusRunning)
	}
	if _, ok := gm.Status(groupID); !ok {
		t.Error("Status while running = not found, want found")
	}
	if snaps := gm.AllSnapshots(); len(snaps) != 1 {
		t.Errorf("AllSnapshots while running = %d, want 1", len(snaps))
	}
}

// TestRegression_EvictedWaitError checks the contract of Wait on a group that
// has already been swept: a clear error, never a panic and never a hang.
func TestRegression_EvictedWaitError(t *testing.T) {
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, rec.publish)
	gm.SetRetention(200 * time.Millisecond)

	groupID := runQuickGroup(t, gm, "ret-wait-1")

	time.Sleep(400 * time.Millisecond)

	// List performs the sweep; Wait must then report the group as gone.
	if n := len(gm.List()); n != 0 {
		t.Fatalf("List = %d, want 0 before the Wait check", n)
	}

	var waitErr error
	waitWithTimeout(t, 2*time.Second, "Wait on evicted group to return", func() {
		_, waitErr = gm.Wait(groupID)
	})
	if waitErr == nil {
		t.Fatal("Wait on evicted group = nil error, want a not-found error")
	}
	if !strings.Contains(waitErr.Error(), "not found") {
		t.Errorf("Wait error = %q, want it to mention %q", waitErr, "not found")
	}
}

// TestRegression_StoppedGroupEvicted covers the other terminal statuses: Stop
// ends the group, so it must age out exactly like a done group.
func TestRegression_StoppedGroupEvicted(t *testing.T) {
	rec := newLifecycleRecorder()
	exec := newStepExecutor(1)
	gm := NewGroupManager(mockResolve, exec.execute, rec.publish)
	gm.SetRetention(200 * time.Millisecond)

	groupID, err := gm.Start(context.Background(), "ret-stopped-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{MaxTurns: 50}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer releaseStep(exec)
	exec.waitBlockedFor(t, 2)

	if !gm.Stop(groupID) {
		t.Fatal("Stop returned false")
	}
	waitWithTimeout(t, 5*time.Second, "stopped group to finalize", func() {
		_, _ = gm.Wait(groupID)
	})
	releaseStep(exec)

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("stopped group not visible inside retention window")
	}
	if st.Status != StatusStopped {
		t.Fatalf("status = %q, want %q", st.Status, StatusStopped)
	}

	time.Sleep(400 * time.Millisecond)
	if n := len(gm.List()); n != 0 {
		t.Errorf("List after retention = %d, want 0 (stopped groups must age out too)", n)
	}
	if _, ok := gm.Status(groupID); ok {
		t.Error("Status after retention = found, want not found")
	}
}

// TestRegression_DefaultRetentionKeepsFinishedGroupVisible guards the default:
// with no SetRetention call a just-finished group stays listed (the welcome
// payload depends on it), so the sweep cannot be over-aggressive.
func TestRegression_DefaultRetentionKeepsFinishedGroupVisible(t *testing.T) {
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, rec.publish)

	groupID := runQuickGroup(t, gm, "ret-default-1")

	if gm.retention != 0 {
		t.Fatalf("retention = %v, want 0 (unset) to exercise the default", gm.retention)
	}
	states := gm.List()
	if len(states) != 1 {
		t.Fatalf("List = %d groups, want 1 (default retention is %s)", len(states), DefaultGroupRetention)
	}
	if states[0].ID != groupID {
		t.Errorf("listed id = %q, want %q", states[0].ID, groupID)
	}
	if _, ok := gm.Status(groupID); !ok {
		t.Error("Status = not found, want found within default retention")
	}
	// Wait on a finished, retained group must still resolve.
	waitWithTimeout(t, 2*time.Second, "Wait on retained finished group", func() {
		if _, err := gm.Wait(groupID); err != nil {
			t.Errorf("Wait: %v", err)
		}
	})
}

// TestRegression_ReStartAfterEviction checks that eviction frees the ID slot:
// reusing a group ID after the old group aged out is a fresh start, not an
// "already exists" collision with a zombie entry.
func TestRegression_ReStartAfterEviction(t *testing.T) {
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, rec.publish)
	gm.SetRetention(200 * time.Millisecond)

	runQuickGroup(t, gm, "ret-reuse-1")
	time.Sleep(400 * time.Millisecond)

	if n := len(gm.List()); n != 0 {
		t.Fatalf("List = %d, want 0 before restart", n)
	}
	runQuickGroup(t, gm, "ret-reuse-1")
}
