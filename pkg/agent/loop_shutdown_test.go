package agent

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// --- Shutdown / Stop -------------------------------------------------------

// newShutdownTestLoop builds a minimal AgentLoop for teardown tests. It uses
// NewAgentLoop so the wiring matches production, and the caller only exercises
// the fields Shutdown and Stop touch (running, wg, stopOnce, goalStopCancel,
// stopSessionCleanup, dbStore).
func newShutdownTestLoop(t *testing.T) *AgentLoop {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "agent-shutdown-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: tmpDir,
				Model:     "test-model",
			},
		},
	}
	return NewAgentLoop(cfg, bus.NewMessageBus())
}

// TestAgentLoop_Shutdown_Graceful verifies that Shutdown reports a clean drain
// when no turn is in flight, and that it flips the loop to "not running".
func TestAgentLoop_Shutdown_Graceful(t *testing.T) {
	al := newShutdownTestLoop(t)
	al.running.Store(true)

	if err := al.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() on an idle loop = %v, want nil", err)
	}
	if al.running.Load() {
		t.Error("Expected running to be false after Shutdown")
	}
}

// TestAgentLoop_Shutdown_WaitsForInFlightTurn is the core guarantee of the
// drain: a turn that is still running must be allowed to finish, and Shutdown
// must not return before it does. It also pins the invariant that Shutdown does
// NOT close the store, so the drained turn can still persist its session.
func TestAgentLoop_Shutdown_WaitsForInFlightTurn(t *testing.T) {
	al := newShutdownTestLoop(t)

	// Simulate a turn exactly the way processMessage does: it is tracked on
	// al.wg and inherits the root context, which the gateway only cancels
	// after Shutdown returns.
	turnDone := make(chan struct{})
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		defer close(turnDone)
		time.Sleep(150 * time.Millisecond)
	}()

	start := time.Now()
	if err := al.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v, want nil (drain finished inside the budget)", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("Shutdown returned after %v, before the in-flight turn could finish", elapsed)
	}

	select {
	case <-turnDone:
	default:
		t.Fatal("Shutdown returned while the in-flight turn was still running")
	}

	// Shutdown must leave durable state alone: closing the store here would
	// break the sessions the drained turns are still writing.
	if al.dbStore != nil {
		if err := al.dbStore.DB().Ping(); err != nil {
			t.Errorf("Store was closed by Shutdown (ping: %v); only Stop may close it", err)
		}
	}
}

// TestAgentLoop_Shutdown_Timeout verifies the drain is bounded: when a turn
// outlives the caller's deadline, Shutdown gives up and reports the context
// error instead of hanging the whole teardown.
func TestAgentLoop_Shutdown_Timeout(t *testing.T) {
	al := newShutdownTestLoop(t)

	release := make(chan struct{})
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		<-release
	}()
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := al.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() = %v, want context.DeadlineExceeded", err)
	}
	// The loop must still be marked as not running: new inbound work has to be
	// rejected even though the drain gave up.
	if al.running.Load() {
		t.Error("Expected running to be false after a timed-out Shutdown")
	}
}

// TestAgentLoop_Shutdown_CancelledContext verifies a caller that cancels
// instead of setting a deadline gets its cancellation back promptly, without
// waiting for the stuck turn.
func TestAgentLoop_Shutdown_CancelledContext(t *testing.T) {
	al := newShutdownTestLoop(t)

	release := make(chan struct{})
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		<-release
	}()
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := al.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() = %v, want context.Canceled", err)
	}
}

// TestAgentLoop_StopIsIdempotent guards the sync.Once around Stop's cleanup.
// The pre-existing code closed the session-cleanup stop channel unconditionally,
// so a second call (or Stop after a restart-triggered Stop) panicked; the
// gateway now calls Stop from a path that can be entered twice.
func TestAgentLoop_StopIsIdempotent(t *testing.T) {
	al := newShutdownTestLoop(t)

	// A real cleanup-channel stopper, matching what
	// session.StartCleanupGoroutine returns: func(){ close(stop) }.
	stopped := 0
	cleanupStop := make(chan struct{})
	al.stopSessionCleanup = func() {
		stopped++
		close(cleanupStop)
	}

	al.Stop()
	al.Stop()

	if stopped != 1 {
		t.Errorf("session cleanup stopper called %d times, want 1", stopped)
	}
	select {
	case <-cleanupStop:
	default:
		t.Error("Expected the cleanup stop channel to be closed")
	}
	if al.running.Load() {
		t.Error("Expected running to be false after Stop")
	}
}

// TestAgentLoop_StopAfterShutdown verifies the two-phase teardown the gateway
// relies on: Shutdown drains, then Stop releases the resources. Neither may
// panic or double-run, and Stop must still join a turn that outlived the drain
// budget.
func TestAgentLoop_StopAfterShutdown(t *testing.T) {
	al := newShutdownTestLoop(t)

	finished := make(chan struct{})
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		defer close(finished)
		time.Sleep(120 * time.Millisecond)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := al.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() = %v, want context.DeadlineExceeded", err)
	}

	// Stop is the safety net: it waits unconditionally for the straggler.
	al.Stop()
	select {
	case <-finished:
	default:
		t.Fatal("Stop() returned before the in-flight turn finished")
	}
}

// --- StopWithin ------------------------------------------------------------

// TestAgentLoop_StopWithin_Graceful verifies the bounded join returns nil and
// closes the store when the in-flight turn exits inside the grace.
func TestAgentLoop_StopWithin_Graceful(t *testing.T) {
	al := newShutdownTestLoop(t)

	finished := make(chan struct{})
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		defer close(finished)
		time.Sleep(80 * time.Millisecond)
	}()

	if err := al.StopWithin(5 * time.Second); err != nil {
		t.Fatalf("StopWithin() = %v, want nil (turn exited inside the grace)", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("StopWithin returned before the in-flight turn finished")
	}
	if al.dbStore != nil {
		if err := al.dbStore.DB().Ping(); err == nil {
			t.Error("Store is still open after a graceful StopWithin; the join must close it")
		}
	}
}

// TestAgentLoop_StopWithin_TimeoutAbandonsJoin is the guard against the 90 s
// systemd SIGKILL: a turn that ignores cancellation must NOT be able to hold
// the teardown hostage. StopWithin gives up after the grace, reports it, and
// leaves the store open for the abandoned turn instead of closing it mid-write.
func TestAgentLoop_StopWithin_TimeoutAbandonsJoin(t *testing.T) {
	al := newShutdownTestLoop(t)

	release := make(chan struct{})
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		<-release // never observes any context: the worst-case tool
	}()
	t.Cleanup(func() { close(release) })

	start := time.Now()
	err := al.StopWithin(50 * time.Millisecond)
	if err == nil {
		t.Fatal("StopWithin() = nil, want an error: the join was abandoned")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("StopWithin took %v; the abandoned join must return at the grace", elapsed)
	}
	if al.running.Load() {
		t.Error("Expected running to be false after a timed-out StopWithin")
	}
	// The abandoned turn may still write: the store must stay open.
	if al.dbStore != nil {
		if perr := al.dbStore.DB().Ping(); perr != nil {
			t.Errorf("Store was closed while a turn was still in flight: %v", perr)
		}
	}
}

// TestAgentLoop_StopWithin_Idempotent pins that Stop and StopWithin share one
// stopOnce: the gateway's bounded join and any later unbounded Stop (defer in
// the TUI, a second teardown trigger) must not double-close channels or panic.
func TestAgentLoop_StopWithin_Idempotent(t *testing.T) {
	al := newShutdownTestLoop(t)

	cleanupStop := make(chan struct{})
	stopped := 0
	al.stopSessionCleanup = func() {
		stopped++
		close(cleanupStop)
	}

	if err := al.StopWithin(time.Second); err != nil {
		t.Fatalf("StopWithin() = %v, want nil on an idle loop", err)
	}
	al.Stop() // the unbounded form must be a no-op afterwards

	if stopped != 1 {
		t.Errorf("session cleanup stopper called %d times, want 1", stopped)
	}
}

// TestAgentLoop_GatewayTeardownSequenceReleasesTurn is the end-to-end shape of
// the self-update restart hang, run against a real loop and a real WaitGroup.
//
// It replays the exact sequence cmd/lele's runGracefulShutdown performs:
// Shutdown with a drain budget the turn does not fit, then cancel the root
// context the turn runs on, then the bounded final join. The turn below is a
// faithful stand-in for processMessage: it is tracked on al.wg and can only be
// ended by the root context, which is precisely the dependency that deadlocked
// when the gateway joined before cancelling.
func TestAgentLoop_GatewayTeardownSequenceReleasesTurn(t *testing.T) {
	al := newShutdownTestLoop(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	turnFinished := make(chan struct{})
	al.running.Store(true)
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		defer close(turnFinished)
		<-ctx.Done() // what processMessage does between LLM/tool iterations
	}()

	// 1. The drain hook loses the race, as it does for any turn longer than its
	//    budget. This is the state in which the hang used to begin.
	drainCtx, drainCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer drainCancel()
	if err := al.Shutdown(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain Shutdown() = %v, want context.DeadlineExceeded", err)
	}

	// 2+3. Cancel, then join. Done in the other order this blocks forever.
	cancel()
	joined := make(chan error, 1)
	go func() { joined <- al.StopWithin(5 * time.Second) }()

	select {
	case err := <-joined:
		if err != nil {
			t.Fatalf("StopWithin() = %v, want nil: the cancelled turn should have exited", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("teardown hung: the turn was not released by the root cancel")
	}

	select {
	case <-turnFinished:
	default:
		t.Error("the in-flight turn was abandoned instead of released")
	}
	// A clean join means the store was closed normally, not abandoned open.
	if al.dbStore != nil {
		if err := al.dbStore.DB().Ping(); err == nil {
			t.Error("store still open after a completed join")
		}
	}
}
