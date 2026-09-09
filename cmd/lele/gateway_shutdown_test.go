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

// TestRunGracefulShutdownOrder pins the ordering guarantees the gateway's
// teardown exists for: hooks run LIFO, and the root context is cancelled only
// after every hook has returned. Cancelling earlier is what used to kill
// in-flight turns mid-request.
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
	// The cancel now lives inside the once (it must run before the join, and
	// only the first teardown performs the join), so a repeat call is fully
	// inert: the context was already released by the first pass.
	if cancels != 1 {
		t.Errorf("cancel called %d times, want 1: the teardown runs exactly once", cancels)
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

// fakeTurnJoiner records when the gateway performs its final join, and can be
// made to wait for something the teardown is supposed to do first. It stands in
// for the AgentLoop's in-flight-turn tracking, which is unexported.
type fakeTurnJoiner struct {
	// release, when non-nil, is closed by the test's cancel func and StopWithin
	// blocks until it is. This reproduces the real dependency - an in-flight
	// turn can only end once the root context is cancelled - without reaching
	// into the loop's unexported WaitGroup.
	release chan struct{}
	// joined counts the joins so a test can assert the teardown ran exactly one.
	joined int
}

func (f *fakeTurnJoiner) StopWithin(time.Duration) error {
	if f.release != nil {
		<-f.release
	}
	f.joined++
	return nil
}

// TestRunGracefulShutdownCancelsBeforeJoin is the regression test for the
// self-update restart hang.
//
// A turn that outlived the agent-drain budget runs on the gateway's root
// context and is tracked on the loop's WaitGroup, so the teardown can only
// finish if it cancels that context before joining: joining first waits for the
// turn, the turn waits for the cancel, and the cancel sits behind the join.
// That cycle is what made systemd hit TimeoutStopUSec and SIGKILL the service
// during an update - and, on the self-exec path, what kept the parent from ever
// exiting so the replacement child gave up on the instance lock and died with
// "already running".
//
// The fake joiner below blocks until the cancel fires, so a teardown that
// cancels after joining hangs and this test fails by timeout.
func TestRunGracefulShutdownCancelsBeforeJoin(t *testing.T) {
	silenceGatewayOutput(t)

	var mu sync.Mutex
	var events []string
	record := func(name string) {
		mu.Lock()
		events = append(events, name)
		mu.Unlock()
	}

	joiner := &fakeTurnJoiner{release: make(chan struct{})}
	coord := update.NewShutdownCoordinator(5 * time.Second)
	// The drain hook loses the race against the turn, as it does in production
	// for any turn longer than its budget.
	coord.Register("agent-drain", time.Second, func(context.Context) error {
		record("hook")
		return nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		runGracefulShutdown(coord, joiner, func() {
			record("cancel")
			close(joiner.release)
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runGracefulShutdown deadlocked: it joined the in-flight turn before cancelling the root context")
	}

	mu.Lock()
	defer mu.Unlock()
	if got := strings.Join(events, ","); got != "hook,cancel" {
		t.Errorf("teardown order = %q, want %q", got, "hook,cancel")
	}
	// The join ran, and it could only run after the cancel: StopWithin blocks
	// until the cancel func closes joiner.release, so runGracefulShutdown
	// returning at all is the proof that the root context was released first.
	if joiner.joined != 1 {
		t.Errorf("final join ran %d times, want 1", joiner.joined)
	}
}

// TestRunGracefulShutdownJoinsOnlyOnceAcrossTriggers pins that the restart path
// and the signal path share one teardown, so a turn is never joined twice and
// the store is never closed under a second Stop.
func TestRunGracefulShutdownJoinsOnlyOnceAcrossTriggers(t *testing.T) {
	silenceGatewayOutput(t)

	joiner := &fakeTurnJoiner{}
	coord := update.NewShutdownCoordinator(5 * time.Second)
	hooks := 0
	coord.Register("hook", time.Second, func(context.Context) error {
		hooks++
		return nil
	})

	cancelled := 0
	cancel := func() { cancelled++ }

	runGracefulShutdown(coord, joiner, cancel)
	runGracefulShutdown(coord, joiner, cancel)

	if cancelled != 1 {
		t.Errorf("cancel called %d times, want 1", cancelled)
	}
	if hooks != 1 {
		t.Errorf("hooks ran %d times, want 1", hooks)
	}
	if joiner.joined != 1 {
		t.Errorf("final join ran %d times, want 1", joiner.joined)
	}
}
