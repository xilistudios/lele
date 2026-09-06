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
