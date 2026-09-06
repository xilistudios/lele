package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/update"
)

// silenceGatewayOutput keeps the "Shutting down..." banner out of test logs.
func silenceGatewayOutput(t *testing.T) {
	t.Helper()
	orig := gatewayOut
	gatewayOut = io.Discard
	t.Cleanup(func() { gatewayOut = orig })
}

// newGatewayTestLoop builds a real AgentLoop in a throwaway workspace so the
// teardown helpers run against production wiring instead of a partial struct.
func newGatewayTestLoop(t *testing.T) *agent.AgentLoop {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "gateway-shutdown-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Workspace: tmpDir, Model: "test-model"},
		},
	}
	return agent.NewAgentLoop(cfg, bus.NewMessageBus())
}

// TestRunGracefulShutdownOrder pins the two ordering guarantees the gateway's
// teardown exists for: hooks run LIFO, and the root context is cancelled only
// after every hook has returned. Cancelling earlier is precisely what used to
// kill in-flight turns mid-request.
func TestRunGracefulShutdownOrder(t *testing.T) {
	silenceGatewayOutput(t)

	var mu sync.Mutex
	var events []string
	record := func(name string) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			events = append(events, name)
			mu.Unlock()
			return nil
		}
	}

	coord := update.NewShutdownCoordinator(5 * time.Second)
	coord.Register("first", time.Second, record("first"))
	coord.Register("second", time.Second, record("second"))

	done := make(chan struct{})
	go func() {
		defer close(done)
		runGracefulShutdown(coord, newGatewayTestLoop(t), func() {
			mu.Lock()
			events = append(events, "cancel")
			mu.Unlock()
		})
	}()
	<-done

	mu.Lock()
	defer mu.Unlock()
	want := "second,first,cancel"
	if got := strings.Join(events, ","); got != want {
		t.Errorf("teardown order = %q, want %q", got, want)
	}
}

// TestRunGracefulShutdownIsIdempotent covers the case the whole design depends
// on: the SIGTERM path and the self-restart path both call it, so the second
// call must not re-run hooks or panic inside AgentLoop.Stop.
func TestRunGracefulShutdownIsIdempotent(t *testing.T) {
	silenceGatewayOutput(t)

	var mu sync.Mutex
	ran := 0
	cancels := 0

	coord := update.NewShutdownCoordinator(5 * time.Second)
	coord.Register("counter", time.Second, func(context.Context) error {
		mu.Lock()
		ran++
		mu.Unlock()
		return nil
	})

	al := newGatewayTestLoop(t)
	cancel := func() {
		mu.Lock()
		cancels++
		mu.Unlock()
	}

	runGracefulShutdown(coord, al, cancel)
	runGracefulShutdown(coord, al, cancel)

	mu.Lock()
	defer mu.Unlock()
	if ran != 1 {
		t.Errorf("hooks ran %d times, want 1 (RunAll is idempotent)", ran)
	}
	if cancels != 2 {
		t.Errorf("cancel called %d times, want 2: a CancelFunc is safe to call twice", cancels)
	}
}

// TestRunGracefulShutdownContinuesAfterFailingHook asserts a hook that errors is
// recorded and the rest of the teardown still runs: one bad stop step must never
// leave the process holding the instance lock. Hooks run LIFO, so "broken"
// (registered last) runs before "lock-like" (registered first).
func TestRunGracefulShutdownContinuesAfterFailingHook(t *testing.T) {
	silenceGatewayOutput(t)

	wanted := errors.New("stop failed")
	coord := update.NewShutdownCoordinator(5 * time.Second)

	var mu sync.Mutex
	var ran []string
	coord.Register("lock-like", time.Second, func(context.Context) error {
		mu.Lock()
		ran = append(ran, "lock-like")
		mu.Unlock()
		return nil
	})
	coord.Register("broken", time.Second, func(context.Context) error {
		mu.Lock()
		ran = append(ran, "broken")
		mu.Unlock()
		return wanted
	})

	// Nothing may panic and the function must return even though a hook failed.
	runGracefulShutdown(coord, newGatewayTestLoop(t), func() {})

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ran, ",") != "broken,lock-like" {
		t.Errorf("hooks that ran = %v, want both: a failure must not abort the teardown", ran)
	}
}

// TestRestarterOnRestartRunsTeardownOnce exercises the actual wiring contract
// between the restarter and the gateway: Restart calls OnRestart before exiting
// the parent, and OnRestart must drive the same coordinator the signal path
// uses - never a second, competing one.
func TestRestarterOnRestartRunsTeardownOnce(t *testing.T) {
	silenceGatewayOutput(t)
	t.Setenv("LELE_RESTART_DRY_RUN", "1")

	var mu sync.Mutex
	ran := 0
	coord := update.NewShutdownCoordinator(5 * time.Second)
	coord.Register("hook", time.Second, func(context.Context) error {
		mu.Lock()
		ran++
		mu.Unlock()
		return nil
	})

	al := newGatewayTestLoop(t)
	cancel := func() {}

	r := update.NewRestarter()
	r.Detect = func() update.Supervisor { return update.SupervisorNone }
	r.Exit = func(int) {}
	r.OnRestart = func(string) { runGracefulShutdown(coord, al, cancel) }

	if _, err := r.Restart(); err != nil {
		t.Fatalf("Restart() = %v", err)
	}
	// The signal path fires right after the restart path in the worst case.
	runGracefulShutdown(coord, al, cancel)

	mu.Lock()
	defer mu.Unlock()
	if ran != 1 {
		t.Errorf("hook ran %d times, want 1 across restart + signal triggers", ran)
	}
}
