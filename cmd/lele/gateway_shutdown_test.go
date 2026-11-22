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

func silenceGatewayOutput(t *testing.T) {
	t.Helper()
	orig := gatewayOut
	gatewayOut = io.Discard
	t.Cleanup(func() { gatewayOut = orig })
}

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
	if cancels != 1 {
		t.Errorf("cancel called %d times, want 1: the teardown runs exactly once", cancels)
	}
}

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

	runGracefulShutdown(coord, newGatewayTestLoop(t), func() {})

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ran, ",") != "broken,lock-like" {
		t.Errorf("hooks that ran = %v, want both: a failure must not abort the teardown", ran)
	}
}

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
	runGracefulShutdown(coord, al, cancel)

	mu.Lock()
	defer mu.Unlock()
	if ran != 1 {
		t.Errorf("hook ran %d times, want 1 across restart + signal triggers", ran)
	}
}

type fakeTurnJoiner struct {
	release chan struct{}
	joined  int
}

func (f *fakeTurnJoiner) StopWithin(time.Duration) error {
	if f.release != nil {
		<-f.release
	}
	f.joined++
	return nil
}

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
	if joiner.joined != 1 {
		t.Errorf("final join ran %d times, want 1", joiner.joined)
	}
}

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
