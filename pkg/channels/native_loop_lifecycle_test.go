package channels

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

func TestNativeChannelStopReapsBackgroundLoops(t *testing.T) {
	n := newLoopLifecycleChannel(t)

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
	if grew := runtime.NumGoroutine() - before; grew > 5 {
		t.Fatalf("goroutines grew by %d across 10 reload cycles: loops are surviving Stop", grew)
	}
}

func TestNativeChannelStopDoesNotDeadlockDuringReconcile(t *testing.T) {
	n := newLoopLifecycleChannel(t)

	if err := n.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	entered := make(chan struct{}, 1024)
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

	if elapsed >= loopJoinTimeout/2 {
		t.Fatalf("Stop took %v (join budget is %v): it waited for its loops while "+
			"holding n.mu, which is the deadlock #369 warns about", elapsed, loopJoinTimeout)
	}
	if got := n.activeLoops.Load(); got != 0 {
		t.Fatalf("activeLoops after Stop = %d, want 0", got)
	}
}

func TestNativeChannelStopWithoutStartIsInert(t *testing.T) {
	n := newLoopLifecycleChannel(t)

	if err := n.Stop(context.Background()); err != nil {
		t.Fatalf("Stop on a never-started channel: %v", err)
	}
	if got := n.activeLoops.Load(); got != 0 {
		t.Fatalf("activeLoops = %d, want 0", got)
	}
}

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

	if got := n.activeLoops.Load(); got != 0 {
		t.Fatalf("activeLoops after Stop = %d, want 0", got)
	}
	if err := n.loopCtx.Err(); err == nil {
		t.Error("loopCtx still live after Stop: the reconcile ticker would keep polling " +
			"the store from a channel nobody references")
	}
}
