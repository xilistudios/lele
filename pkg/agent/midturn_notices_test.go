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
	if notices := al.drainMidTurnNotices(key); len(notices) != 0 {
		t.Fatalf("drained %d notices from an inactive queue, want 0", len(notices))
	}

	al.activateMidTurnNotices(key)
	if !al.enqueueMidTurnNotice(key, "result", "subagent-1") {
		t.Fatal("enqueue on an active queue returned false")
	}
}

func TestMidTurnNotices_DrainKeepsQueueActive(t *testing.T) {
	al := newMidTurnQueueTestLoop()
	const key = "session-drain"
	al.activateMidTurnNotices(key)

	if !al.enqueueMidTurnNotice(key, "first", "subagent-1") {
		t.Fatal("enqueue failed on an active queue")
	}
	notices := al.drainMidTurnNotices(key)
	if len(notices) != 1 {
		t.Fatalf("drained %d notices, want 1", len(notices))
	}
	if notices[0].content != "first" || notices[0].taskID != "subagent-1" {
		t.Errorf("drained notice = %+v, want {content:first task_id:subagent-1}", notices[0])
	}
	if got := al.drainMidTurnNotices(key); len(got) != 0 {
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
		al.activateMidTurnNotices(key)
		if !al.enqueueMidTurnNotice(key, "payload", "subagent-1") {
			t.Fatal("enqueue failed on an active queue")
		}

		notices := al.drainOrDeactivateMidTurnNotices(key)
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
		al.activateMidTurnNotices(key)

		if notices := al.drainOrDeactivateMidTurnNotices(key); len(notices) != 0 {
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
	al.activateMidTurnNotices(key)

	if !al.enqueueMidTurnNotice(key, "first", "subagent-1") {
		t.Fatal("enqueue failed on an active queue")
	}
	// A mid-turn drain consumed nothing here on purpose: the turn is exiting
	// abnormally, so everything still queued must come back to the caller.
	remaining := al.deactivateMidTurnNotices(key)
	if len(remaining) != 1 || remaining[0].content != "first" {
		t.Fatalf("deactivateMidTurnNotices = %+v, want the queued notice", remaining)
	}
	if al.enqueueMidTurnNotice(key, "after", "subagent-2") {
		t.Fatal("enqueue succeeded after deactivate")
	}
	if again := al.deactivateMidTurnNotices(key); len(again) != 0 {
		t.Fatalf("second deactivate returned %d notices, want 0", len(again))
	}
}

// TestMidTurnNotices_ResolvesSessionAlias pins the reason every method resolves
// the key: a /new alias switch moves the conversation to a new active key, and
// the activation, the completion callback and the drain must still agree on the
// same queue.
func TestMidTurnNotices_ResolvesSessionAlias(t *testing.T) {
	al := newMidTurnQueueTestLoop()
	al.sessionAliases.Store("base-session", "base-session:chat:2")

	al.activateMidTurnNotices("base-session")
	if !al.enqueueMidTurnNotice("base-session:chat:2", "payload", "subagent-1") {
		t.Fatal("the aliased key did not reach the queue activated through the base key")
	}
	notices := al.drainMidTurnNotices("base-session")
	if len(notices) != 1 {
		t.Fatalf("drained %d notices through the base key, want 1", len(notices))
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
	if notices := al.drainMidTurnNotices(""); len(notices) != 0 {
		t.Errorf("drain on an empty session key returned %d notices", len(notices))
	}
	if notices := al.deactivateMidTurnNotices(""); len(notices) != 0 {
		t.Errorf("deactivate on an empty session key returned %d notices", len(notices))
	}
}

// TestMidTurnNotice_MessageContent pins the canonical synthetic marker. The
// inbound fallback lets processSystemMessage add the prefix, so the stored
// content is raw; the injection path renders the prefix itself. Both must end
// up byte-identical, which is what this test asserts.
func TestMidTurnNotice_MessageContent(t *testing.T) {
	notice := midTurnNotice{content: "the subagent result", taskID: "subagent-1"}
	if got, want := notice.messageContent(), "[System: subagent] the subagent result"; got != want {
		t.Errorf("messageContent() = %q, want %q", got, want)
	}
	// processSystemMessage formats the inbound as "[System: <SenderID>] ...";
	// the sender the flush uses must be the one in the prefix.
	if got, want := notice.messageContent(), "[System: "+midTurnNoticeSender+"] "+notice.content; got != want {
		t.Errorf("messageContent() = %q, want %q", got, want)
	}
}

// TestMidTurnNotices_StressNoNoticeLost hammers enqueue/drain/deactivate from
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

	al.activateMidTurnNotices(key)

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
				remaining := al.deactivateMidTurnNotices(key)
				mu.Lock()
				delivered += len(remaining)
				mu.Unlock()
				return
			default:
				notices := al.drainMidTurnNotices(key)
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
