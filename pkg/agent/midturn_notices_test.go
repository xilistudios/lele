// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"runtime"
	"sync"
	"testing"
)

// ============================================================================
// Mid-turn notice queue semantics.
//
// The queue is the whole point of the fix: while a parent turn runs, a subagent
// completion must be handed to THAT turn instead of becoming an inbound that
// blocks on the per-session semaphore until the turn is over. These tests pin
// the state machine (inactive/active, drain vs. drain-or-deactivate, flush on
// abnormal exit) that makes delivery exactly-once — see the file-level comment
// in midturn_notices.go for the invariant being tested.
//
// The consumer side of the queue is a POINTER captured by
// activateMidTurnNotices and never re-resolved: these tests therefore hold on
// to the returned queue instead of addressing it by session key.
// ============================================================================

// newMidTurnQueueTestLoop returns a bare loop: the queue methods only need the
// session-key resolver, so a real NewAgentLoop (store, providers, channels) is
// unnecessary here.
func newMidTurnQueueTestLoop() *AgentLoop {
	return &AgentLoop{}
}

func TestMidTurnNotices_EnqueueRequiresActiveQueue(t *testing.T) {
	al := newMidTurnQueueTestLoop()
	const key = "session-notice"

	if al.enqueueMidTurnNotice(key, "result", "subagent-1") {
		t.Fatal("enqueue on an inactive queue returned true; an idle parent must use the inbound path")
	}

	queue := al.activateMidTurnNotices(key)
	if queue == nil {
		t.Fatal("activateMidTurnNotices returned no queue for a real session key")
	}
	if notices := queue.drain(); len(notices) != 0 {
		t.Fatalf("drained %d notices from an empty queue, want 0", len(notices))
	}
	if !al.enqueueMidTurnNotice(key, "result", "subagent-1") {
		t.Fatal("enqueue on an active queue returned false")
	}
}

func TestMidTurnNotices_DrainKeepsQueueActive(t *testing.T) {
	al := newMidTurnQueueTestLoop()
	const key = "session-drain"
	queue := al.activateMidTurnNotices(key)

	if !al.enqueueMidTurnNotice(key, "first", "subagent-1") {
		t.Fatal("enqueue failed on an active queue")
	}
	notices := queue.drain()
	if len(notices) != 1 {
		t.Fatalf("drained %d notices, want 1", len(notices))
	}
	if notices[0].content != "first" || notices[0].taskID != "subagent-1" {
		t.Errorf("drained notice = %+v, want {content:first task_id:subagent-1}", notices[0])
	}
	if got := queue.drain(); len(got) != 0 {
		t.Fatalf("second drain returned %d notices, want 0", len(got))
	}

	// A drain is a mid-turn read, not an exit: the turn keeps running, so
	// later completions must still be injectable.
	if !al.enqueueMidTurnNotice(key, "second", "subagent-2") {
		t.Fatal("enqueue after a plain drain returned false; the queue must stay active")
	}
}

func TestMidTurnNotices_DrainOrDeactivate(t *testing.T) {
	t.Run("with items keeps the queue active", func(t *testing.T) {
		al := newMidTurnQueueTestLoop()
		const key = "session-drain-items"
		queue := al.activateMidTurnNotices(key)
		if !al.enqueueMidTurnNotice(key, "payload", "subagent-1") {
			t.Fatal("enqueue failed on an active queue")
		}

		notices := queue.drainOrDeactivate()
		if len(notices) != 1 || notices[0].content != "payload" {
			t.Fatalf("drainOrDeactivate = %+v, want one notice with content payload", notices)
		}
		if !al.enqueueMidTurnNotice(key, "later", "subagent-2") {
			t.Fatal("drainOrDeactivate deactivated a non-empty queue; the loop would lose later notices")
		}
	})

	t.Run("empty deactivates", func(t *testing.T) {
		al := newMidTurnQueueTestLoop()
		const key = "session-drain-empty"
		queue := al.activateMidTurnNotices(key)

		if notices := queue.drainOrDeactivate(); len(notices) != 0 {
			t.Fatalf("drainOrDeactivate returned %d notices, want 0", len(notices))
		}
		// This is the atomicity the fallback relies on: once the exit check
		// reports "nothing left", no notice can be added until the next turn.
		if al.enqueueMidTurnNotice(key, "late", "subagent-3") {
			t.Fatal("enqueue succeeded after drainOrDeactivate reported the queue empty: a claimed result could be lost")
		}
	})
}

func TestMidTurnNotices_DeactivateFlushesRemaining(t *testing.T) {
	al := newMidTurnQueueTestLoop()
	const key = "session-flush"
	queue := al.activateMidTurnNotices(key)

	if !al.enqueueMidTurnNotice(key, "first", "subagent-1") {
		t.Fatal("enqueue failed on an active queue")
	}
	// A mid-turn drain consumed nothing here on purpose: the turn is exiting
	// abnormally, so everything still queued must come back to the caller.
	remaining := queue.deactivate()
	if len(remaining) != 1 || remaining[0].content != "first" {
		t.Fatalf("deactivate = %+v, want the queued notice", remaining)
	}
	if al.enqueueMidTurnNotice(key, "after", "subagent-2") {
		t.Fatal("enqueue succeeded after deactivate")
	}
	if again := queue.deactivate(); len(again) != 0 {
		t.Fatalf("second deactivate returned %d notices, want 0", len(again))
	}
} // TestMidTurnNotices_CapturedQueueSurvivesAliasRemap is the regression test for
// the split-identity bug: the turn captures its queue at activation, a /new
// remaps the alias while the turn runs, and the turn must still drain the queue
// it activated (not the queue the new alias points at). Otherwise a notice the
// producer accepted into the captured queue — and therefore did NOT publish as
// an inbound — would be stranded forever.
func TestMidTurnNotices_CapturedQueueSurvivesAliasRemap(t *testing.T) {
	al := newMidTurnQueueTestLoop()
	const baseKey = "base-session"
	al.sessionAliases.Store(baseKey, "base-session:chat:1")

	// The turn starts while the alias points at chat:1.
	queue := al.activateMidTurnNotices(baseKey)
	if queue == nil {
		t.Fatal("activateMidTurnNotices returned no queue")
	}

	// "/new" mid-turn: the alias now points at a different conversation key.
	al.setSessionAlias(baseKey, "base-session:chat:2")

	// A subagent completing after the remap resolves the NEW alias, whose
	// queue is inactive -> enqueue must report false so the producer publishes
	// the inbound fallback instead of stranding the result.
	if al.enqueueMidTurnNotice(baseKey, "post-remap result", "subagent-2") {
		t.Fatal("enqueue landed in the post-remap queue, which no running turn drains")
	}

	// The turn's captured identity still owns the pre-remap queue and drains
	// it by pointer, no matter what the alias says now.
	if !al.enqueueMidTurnNotice("base-session:chat:1", "in-flight result", "subagent-1") {
		t.Fatal("enqueue through the activated key did not reach the captured queue")
	}
	notices := queue.drain()
	if len(notices) != 1 || notices[0].content != "in-flight result" {
		t.Fatalf("drain through the captured queue = %+v, want the in-flight notice", notices)
	}
}

func TestMidTurnNotices_RejectsEmptyInput(t *testing.T) {
	al := newMidTurnQueueTestLoop()

	if al.enqueueMidTurnNotice("", "payload", "subagent-1") {
		t.Error("enqueue accepted an empty session key")
	}
	al.activateMidTurnNotices("session-empty-content")
	if al.enqueueMidTurnNotice("session-empty-content", "", "subagent-1") {
		t.Error("enqueue accepted an empty content")
	}
	if queue := al.activateMidTurnNotices(""); queue != nil {
		t.Error("activate returned a queue for an empty session key")
	}
	// A turn without a usable key has no queue at all; every consumer
	// operation must degrade to a no-op instead of panicking.
	var noQueue *midTurnNoticeQueue
	if notices := noQueue.drain(); len(notices) != 0 {
		t.Errorf("drain on a nil queue returned %d notices", len(notices))
	}
	if notices := noQueue.drainOrDeactivate(); len(notices) != 0 {
		t.Errorf("drainOrDeactivate on a nil queue returned %d notices", len(notices))
	}
	if notices := noQueue.deactivate(); len(notices) != 0 {
		t.Errorf("deactivate on a nil queue returned %d notices", len(notices))
	}
}

// TestMidTurnNotice_MessageContent pins the canonical synthetic marker. The
// inbound fallback lets processSystemMessage add the prefix, so the stored
// content is raw; the injection path renders the prefix itself. Both must end
// up byte-identical, which is what this test asserts.
func TestMidTurnNotice_MessageContent(t *testing.T) {
	notice := midTurnNotice{content: "the subagent result", taskID: "subagent-1"}
	if got, want := midTurnNoticePrefix+notice.content, "[System: subagent] the subagent result"; got != want {
		t.Errorf("rendered notice = %q, want %q", got, want)
	}
	// processSystemMessage formats the inbound as "[System: <SenderID>] ...";
	// the sender the flush uses must be the one in the prefix.
	if got, want := midTurnNoticePrefix, "[System: "+midTurnNoticeSender+"] "; got != want {
		t.Errorf("prefix = %q, want %q", got, want)
	}
} // TestMidTurnNotices_StressNoNoticeLost hammers enqueue/drain/deactivate from
// several goroutines. The invariant is accounting, not ordering: every enqueue
// that reported success is later observed by exactly one drain (or by the final
// flush), so a claimed subagent result can never vanish. Run with -race.
func TestMidTurnNotices_StressNoNoticeLost(t *testing.T) {
	al := newMidTurnQueueTestLoop()
	const (
		key       = "session-stress"
		producers = 4
		perProd   = 50
	)

	queue := al.activateMidTurnNotices(key)

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		delivered int
		accepted  int
	)

	// Consumer: drains concurrently with the producers, exactly like a running
	// turn does at its iteration boundaries.
	stop := make(chan struct{})
	var consumerWG sync.WaitGroup
	consumerWG.Add(1)
	go func() {
		defer consumerWG.Done()
		for {
			select {
			case <-stop:
				// Final sweep: whatever is left must come out here.
				remaining := queue.deactivate()
				mu.Lock()
				delivered += len(remaining)
				mu.Unlock()
				return
			default:
				notices := queue.drain()
				if len(notices) > 0 {
					mu.Lock()
					delivered += len(notices)
					mu.Unlock()
					continue
				}
				// Yield so the producers get a chance to enqueue.
				runtime.Gosched()
			}
		}
	}()

	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < perProd; i++ {
				if al.enqueueMidTurnNotice(key, "notice", "subagent") {
					mu.Lock()
					accepted++
					mu.Unlock()
				}
			}
		}(p)
	}
	wg.Wait()
	close(stop)
	consumerWG.Wait()

	mu.Lock()
	defer mu.Unlock()
	if accepted != delivered {
		t.Fatalf("accepted %d notices but delivered %d: a claimed result was lost", accepted, delivered)
	}
	if accepted == 0 {
		t.Fatal("no enqueue succeeded; the test exercised nothing")
	}
}
