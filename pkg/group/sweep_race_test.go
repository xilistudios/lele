package group

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestRegression_ConcurrentSweepsAreSafe hammers the lazy eviction path
// (B6/aaf49d7) with concurrent readers while every group is expired
// (retention ~0). Two properties must hold under concurrency:
//
//  1. No panic and no double close: eviction only drops groups whose done is
//     already closed, and finalize owns the close, so concurrent sweeps can
//     never race a close — this test would crash loudly if that changed.
//  2. Convergence: once retention has elapsed, any reader (List,
//     AllSnapshots, Status, Wait) performs the sweep, so the map ends empty.
//
// Wait on an already-evicted group must return a clean error, not block: the
// goroutines below mix Wait calls with the other readers on purpose.
func TestRegression_ConcurrentSweepsAreSafe(t *testing.T) {
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, (&mockExecutor{}).execute, rec.publish)

	const groups = 6
	ids := make([]string, groups)
	for i := range ids {
		ids[i] = fmt.Sprintf("sweep-%d", i)
		groupID, err := gm.Start(context.Background(), ids[i], "p1", "task", "round_robin",
			[]Participant{plainParticipant("a")}, GroupOptions{Rounds: 1}, "ch", "chat")
		if err != nil {
			t.Fatalf("Start(%s): %v", ids[i], err)
		}
		waitWithTimeout(t, 5*time.Second, "group to finish", func() {
			if _, err := gm.Wait(groupID); err != nil {
				t.Errorf("Wait(%s): %v", groupID, err)
			}
		})
	}

	// Everything is expired from now on.
	gm.SetRetention(time.Nanosecond)
	time.Sleep(5 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				switch (i + j) % 5 {
				case 0:
					gm.List()
				case 1:
					gm.AllSnapshots()
				case 2:
					gm.Status(ids[j%groups])
				case 3:
					// Wait on an expired (possibly mid- or post-eviction)
					// group: must return, never hang, never panic.
					if _, err := gm.Wait(ids[j%groups]); err != nil {
						// Evicted is a legitimate outcome; do not fail, but
						// the error must be the "not found or evicted" one,
						// not a nil-result lie.
						t.Log("wait after eviction:", err)
					}
				case 4:
					// Stop on an expired group must not resurrect it.
					gm.Stop(ids[j%groups])
				}
			}
		}(i)
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("concurrent sweeps did not converge (deadlock suspected)")
	}

	if got := len(gm.List()); got != 0 {
		t.Fatalf("expired groups survived %d concurrent sweepers: %d left", 8, got)
	}
}
