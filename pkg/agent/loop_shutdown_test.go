package agent

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/tools"
)

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

func TestAgentLoop_Shutdown_WaitsForInFlightTurn(t *testing.T) {
	al := newShutdownTestLoop(t)

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

	if al.dbStore != nil {
		if err := al.dbStore.DB().Ping(); err != nil {
			t.Errorf("Store was closed by Shutdown (ping: %v); only Stop may close it", err)
		}
	}
}

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
	if al.running.Load() {
		t.Error("Expected running to be false after a timed-out Shutdown")
	}
}

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

func TestAgentLoop_StopIsIdempotent(t *testing.T) {
	al := newShutdownTestLoop(t)

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

	al.Stop()
	select {
	case <-finished:
	default:
		t.Fatal("Stop() returned before the in-flight turn finished")
	}
}

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

func TestAgentLoop_StopWithin_TimeoutAbandonsJoin(t *testing.T) {
	al := newShutdownTestLoop(t)

	release := make(chan struct{})
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		<-release
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
	if al.dbStore != nil {
		if perr := al.dbStore.DB().Ping(); perr != nil {
			t.Errorf("Store was closed while a turn was still in flight: %v", perr)
		}
	}
}

func TestAgentLoop_StopWithin_SharesOneDeadlineAcrossStages(t *testing.T) {
	al := newShutdownTestLoop(t)

	for _, wired := range al.toolCoordinator.GetSubagents() {
		wired.StopRetentionCleanup()
	}

	sm := tools.NewSubagentManager(nil, "test-model", t.TempDir(), nil, 10)
	sm.SetRetentionPeriod(time.Millisecond)
	addOldTerminalTaskForTest(sm, time.Hour)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseSweeper := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseSweeper)
	sm.SetSessionEvictCallback(func(string) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	stopSweeper := sm.StartRetentionCleanup(time.Millisecond)
	t.Cleanup(stopSweeper)
	al.toolCoordinator = newToolCoordinatorWithSubagents(al, map[string]*tools.SubagentManager{"main": sm}, nil, nil)

	waitUntil := time.Now().Add(3 * time.Second)
	for {
		select {
		case <-entered:
		default:
			if time.Now().After(waitUntil) {
				t.Fatal("the sweeper never entered the evict callback")
			}
			time.Sleep(time.Millisecond)
			continue
		}
		break
	}

	turnRelease := make(chan struct{})
	t.Cleanup(func() { close(turnRelease) })
	al.wg.Add(1)
	go func() {
		defer al.wg.Done()
		<-turnRelease
	}()

	const grace = 300 * time.Millisecond
	go func() {
		time.Sleep(grace * 2 / 3)
		releaseSweeper()
	}()

	start := time.Now()
	err := al.StopWithin(grace)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("StopWithin() = nil, want an error: the turn never observes cancellation, so the join must be abandoned")
	}
	if elapsed > grace+100*time.Millisecond {
		t.Errorf("StopWithin took %s with a %s budget; each stage must consume the shared deadline, not get its own (per-stage budgeting returns at ≈%s)",
			elapsed, grace, grace+grace*2/3)
	}
	t.Logf("teardown returned in %s with a %s budget: %v", elapsed, grace, err)

	if al.dbStore != nil {
		if perr := al.dbStore.DB().Ping(); perr != nil {
			t.Errorf("Store was closed while a turn was still in flight: %v", perr)
		}
	}
}

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
	al.Stop()

	if stopped != 1 {
		t.Errorf("session cleanup stopper called %d times, want 1", stopped)
	}
}

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
		<-ctx.Done()
	}()

	drainCtx, drainCancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer drainCancel()
	if err := al.Shutdown(drainCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain Shutdown() = %v, want context.DeadlineExceeded", err)
	}

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
	if al.dbStore != nil {
		if err := al.dbStore.DB().Ping(); err == nil {
			t.Error("store still open after a completed join")
		}
	}
}
