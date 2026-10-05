package channels

// native_loop_lifecycle_test.go — regression tests for issue #369.
//
// Manager.ReloadConfig retires a channel with channel.Stop(m.runCtx) and builds a
// replacement, but m.runCtx is the process context: it is never cancelled on a
// reload. Every loop NativeChannel.Start launched under that context therefore
// kept running against a channel nobody references anymore — one idle upload
// sweeper per reload (quiet, hourly), and since #361 one 15-second ticker
// reading SQLite per reload, which is not quiet at all and is trivially
// reachable from the WebUI/desktop saving its config.
//
// These tests pin the two halves of the fix: Stop ends the loops and waits for
// them, and it waits WITHOUT holding n.mu, because a reconciliation tick takes
// that same lock to kick a revoked WebSocket.

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

func newLoopLifecycleChannel(t *testing.T) *NativeChannel {
	t.Helper()

	cfg := config.DefaultConfig()
	cfg.Channels.Native.Enabled = true
	cfg.Channels.Native.Port = 0
	cfg.Channels.Native.LeleDir = t.TempDir()

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)

	native, err := NewNativeChannel(cfg, msgBus, newNativeTestAgentLoop(cfg), NewApprovalManager())
	if err != nil {
		t.Fatalf("NewNativeChannel() error = %v", err)
	}
	return native
}

// TestNativeChannelStopReapsBackgroundLoops is the leak itself: with the process
// context still alive, only Stop can end the loops.
func TestNativeChannelStopReapsBackgroundLoops(t *testing.T) {
	n := newLoopLifecycleChannel(t)

	// The context a gateway hands Start — background is exactly it: nothing in a
	// config reload ever cancels it.
	processCtx := context.Background()

	if err := n.Start(processCtx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := n.activeLoops.Load(); got != 2 {
		t.Fatalf("activeLoops after Start = %d, want 2 (upload sweep + revocation reconcile)", got)
	}
	if n.loopCtx == nil || n.loopCtx.Err() != nil {
		t.Fatal("loopCtx must be live while the channel runs")
	}

	if err := n.Stop(processCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := n.activeLoops.Load(); got != 0 {
		t.Fatalf("activeLoops after Stop = %d, want 0: Stop returned with loops still running, "+
			"which is the #369 leak", got)
	}
	if err := n.loopCtx.Err(); err == nil {
		t.Fatal("loopCtx was not cancelled: a retired channel's ticker would still be polling")
	}
	if n.loopCancel != nil {
		t.Error("loopCancel must be cleared by Stop so a second Stop cannot wait on a dead context")
	}

	// A channel must be restartable after Stop (the manager hands it a fresh
	// context on the next Start).
	if err := n.Start(processCtx); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}
	if n.loopCtx.Err() != nil {
		t.Error("Start reused the cancelled loopCtx instead of a fresh one")
	}
	if err := n.Stop(processCtx); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if got := n.activeLoops.Load(); got != 0 {
		t.Fatalf("activeLoops after restart cycle = %d, want 0", got)
	}
}

// TestNativeChannelReloadCyclesDoNotAccumulateLoops walks the path the issue
// names: repeated ReloadConfig-shaped Start/Stop pairs on one live process
// context. Before the fix each pair left two goroutines (and the whole retired
// channel) behind forever.
func TestNativeChannelReloadCyclesDoNotAccumulateLoops(t *testing.T) {
	n := newLoopLifecycleChannel(t)
	processCtx := context.Background()

	settle := func(label string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for n.activeLoops.Load() > 0 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		if got := n.activeLoops.Load(); got != 0 {
			t.Fatalf("%s: activeLoops = %d, want 0", label, got)
		}
	}

	runtime.GC()
	before := runtime.NumGoroutine()

	for i := 0; i < 10; i++ {
		if err := n.Start(processCtx); err != nil {
			t.Fatalf("Start #%d: %v", i, err)
		}
		if err := n.Stop(processCtx); err != nil {
			t.Fatalf("Stop #%d: %v", i, err)
		}
		settle("after reload cycle")
	}

	runtime.GC()
	// The channel's own loops are accounted for by activeLoops; the goroutine
	// count is the belt-and-braces check that nothing else outlived the reload.
	// A margin covers unrelated runtime housekeeping on a shared machine.
	if grew := runtime.NumGoroutine() - before; grew > 5 {
		t.Fatalf("goroutines grew by %d across 10 reload cycles: loops are surviving Stop", grew)
	}
}

// TestNativeChannelStopDoesNotDeadlockDuringReconcile pins the lock ordering.
// dropWSClientsForAuth takes n.mu from inside the reconcile loop, so a Stop that
// cancelled and joined its loops while holding the lock would block the very
// loop it waits for and stall a config reload for the whole join timeout.
//
// The fixture is a loop that repeatedly takes n.mu the way a tick that drops a
// revoked client does, and exits on context cancellation. Correct ordering: Stop
// releases n.mu, cancels, the loop wakes and leaves — milliseconds. Wrong
// ordering: Stop holds n.mu, the loop cannot get it, the join runs out its whole
// budget. Duration is what tells them apart, so that is what is asserted.
func TestNativeChannelStopDoesNotDeadlockDuringReconcile(t *testing.T) {
	n := newLoopLifecycleChannel(t)

	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	entered := make(chan struct{}, 1024)
	// The loop checks for cancellation only INSIDE the critical section, which is
	// what makes the ordering observable: a reconcile tick that is on its way to
	// dropWsClientsForAuth cannot see ctx.Done() until it has the lock. Correct
	// ordering hands the lock back first, so it gets in, sees the cancellation and
	// leaves. Wrong ordering keeps it parked on n.mu for the whole join budget.
	n.startLoop(n.loopCtx, func(ctx context.Context) {
		for {
			n.mu.Lock()
			done := ctx.Err() != nil
			if !done {
				select {
				case entered <- struct{}{}:
				default:
				}
			}
			n.mu.Unlock()
			if done {
				return
			}
			time.Sleep(50 * time.Microsecond)
		}
	})

	// Make sure the loop is live and cycling on the lock before stopping.
	for i := 0; i < 3; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("test fixture: loop never reached the critical section")
		}
	}

	started := time.Now()
	if err := n.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	elapsed := time.Since(started)

	// The join budget is 2s; a Stop that took most of it was waiting on a loop
	// it was itself blocking.
	if elapsed >= loopJoinTimeout/2 {
		t.Fatalf("Stop took %v (join budget is %v): it waited for its loops while "+
			"holding n.mu, which is the deadlock #369 warns about", elapsed, loopJoinTimeout)
	}
	if got := n.activeLoops.Load(); got != 0 {
		t.Fatalf("activeLoops after Stop = %d, want 0", got)
	}
}

// TestNativeChannelStopWithoutStartIsInert covers the deferred join running on a
// channel that never had loops to reap.
func TestNativeChannelStopWithoutStartIsInert(t *testing.T) {
	n := newLoopLifecycleChannel(t)

	if err := n.Stop(context.Background()); err != nil {
		t.Fatalf("Stop on a never-started channel: %v", err)
	}
	if got := n.activeLoops.Load(); got != 0 {
		t.Fatalf("activeLoops = %d, want 0", got)
	}
}

// TestNativeChannelReconcileLoopTicksAndStopsWithTheChannel exercises the wiring
// the #327 function-level tests cannot: that Start really runs the loop, that it
// runs it at the channel's interval, and that Stop takes the polling with it.
// The production interval is 15s, which no test can wait on, hence
// authReconcileEvery.
func TestNativeChannelReconcileLoopTicksAndStopsWithTheChannel(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "lele.db")

	n := newLoopLifecycleChannel(t)
	gwAuth, gwStore := openServerAuthManager(t, tmpDir, dbPath)
	defer gwStore.Close()
	n.auth = gwAuth
	n.authReconcileEvery = 5 * time.Millisecond

	revoked, revokedToken, _ := pairClientOn(t, gwAuth, "revoked-phone")

	cliAuth, cliStore := openCLIAuthManager(t, tmpDir, dbPath)
	defer cliStore.Close()

	// While the channel runs, a revocation made by another process dies here on
	// its own: nothing calls reconcile in this test's foreground.
	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := cliAuth.RemoveClient(revoked.ClientID); err != nil {
		t.Fatalf("CLI RemoveClient: %v", err)
	}
	if _, ok := gwAuth.ValidateToken(revokedToken); ok {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, ok := gwAuth.ValidateToken(revokedToken); !ok {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if _, ok := gwAuth.ValidateToken(revokedToken); ok {
			t.Fatal("Start did not run the revocation loop: a client revoked by the CLI " +
				"is still authenticating (that is #327's impact, unfixed)")
		}
	}

	if err := n.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// And the loop goes away with the channel: no goroutine is left holding a
	// ticker against a store this process no longer supervises. Proving "stopped
	// polling" from the token map is deliberately NOT done here — any accessor
	// can take the reload-on-miss path #327 added, which adopts the deletion by
	// itself, so that assertion would be a coin flip rather than a test.
	if got := n.activeLoops.Load(); got != 0 {
		t.Fatalf("activeLoops after Stop = %d, want 0", got)
	}
	if err := n.loopCtx.Err(); err == nil {
		t.Error("loopCtx still live after Stop: the reconcile ticker would keep polling " +
			"the store from a channel nobody references")
	}
}
