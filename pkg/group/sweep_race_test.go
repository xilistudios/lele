package group

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

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
					if _, err := gm.Wait(ids[j%groups]); err != nil {
						t.Log("wait after eviction:", err)
					}
				case 4:
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
