package main

// M5: the gateway's mcp-stop hook must be exactly-once even when the drain
// eats the shutdown budget, and it must run AFTER the config watcher stopped.
//
// runGateway is not callable from a unit test (it owns signal handling and
// the whole service graph), so the test has two halves:
//
//   - a static half that reads gateway.go — the effective shutdown order is
//     decided by WHERE a hook is registered, so the registration kind
//     (Register vs RegisterCritical) and the source position of mcp-stop
//     relative to services-stop/lock-release are asserted on the real source
//     (the same technique as indexOfRegistrationOrder in
//     gateway_durable_outbound_test.go);
//   - a behavioral half that replays the relevant registrations against a
//     recording coordinator, proving what RegisterCritical + LIFO actually
//     deliver: mcp-stop after services-stop with budget to spare, and
//     mcp-stop STILL RUN when the budgeted pass is exhausted.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/update"
)

// mcpStopSourcePositions returns the byte offsets of the mcp-stop,
// services-stop and lock-release registrations in gateway.go's source.
func mcpStopSourcePositions(t *testing.T) (mcpStop, servicesStop, lockRelease int) {
	t.Helper()
	src, err := os.ReadFile("gateway.go")
	if err != nil {
		t.Fatalf("read gateway.go: %v", err)
	}
	text := string(src)
	mcpStop = strings.Index(text, `coord.RegisterCritical("mcp-stop"`)
	servicesStop = strings.Index(text, `coord.Register("services-stop"`)
	lockRelease = strings.Index(text, `coord.RegisterCritical("lock-release"`)
	if mcpStop < 0 {
		t.Error("gateway.go must register mcp-stop via coord.RegisterCritical " +
			"(a spent budget may not skip it: that orphans stdio children)")
	}
	if servicesStop < 0 || lockRelease < 0 {
		t.Fatalf("gateway.go registrations not found (services-stop=%d lock-release=%d)",
			servicesStop, lockRelease)
	}
	if strings.Contains(text, `coord.Register("mcp-stop"`) {
		t.Error("mcp-stop registered as a budgeted hook again (Register) — " +
			"budget exhaustion would skip it")
	}
	return mcpStop, servicesStop, lockRelease
}

// TestMCPStopRegistrationCriticalAndOrdered pins the static half of M5: the
// hook is critical, registered before services-stop (documenting that it runs
// after the config watcher stopped) and after lock-release (so the lock is
// still released last among the criticals).
func TestMCPStopRegistrationCriticalAndOrdered(t *testing.T) {
	mcpStop, servicesStop, lockRelease := mcpStopSourcePositions(t)
	if mcpStop > servicesStop {
		t.Errorf("gateway.go registers mcp-stop (%d) AFTER services-stop (%d): "+
			"the source order must keep mcp-stop ahead of services-stop",
			mcpStop, servicesStop)
	}
	if lockRelease > mcpStop {
		t.Errorf("gateway.go registers lock-release (%d) AFTER mcp-stop (%d): "+
			"the lock must stay the last critical hook to run (LIFO)",
			lockRelease, mcpStop)
	}
}

// TestMCPStopRunsAfterServicesStop pins the behavioral half of M5 (order):
// replaying the gateway's relevant registrations must run mcp-stop after the
// drain AND after services-stop, with lock-release last.
func TestMCPStopRunsAfterServicesStop(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(n string) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			order = append(order, n)
			mu.Unlock()
			return nil
		}
	}

	coord := update.NewShutdownCoordinator(5 * time.Second)
	coord.RegisterCritical("lock-release", time.Second, record("lock-release"))
	coord.RegisterCritical("mcp-stop", time.Second, record("mcp-stop"))
	coord.Register("services-stop", time.Second, record("services-stop"))
	coord.Register("agent-drain", time.Second, record("agent-drain"))

	for hook, err := range coord.RunAll(context.Background()) {
		if err != nil {
			t.Errorf("hook %q: %v", hook, err)
		}
	}

	mu.Lock()
	got := strings.Join(order, " → ")
	mu.Unlock()
	want := "agent-drain → services-stop → mcp-stop → lock-release"
	if got != want {
		t.Errorf("effective order = %q, want %q", got, want)
	}
}

// TestMCPStopSurvivesExhaustedBudget pins the other half of M5: after a slow
// agent-drain spends the whole budget, the budgeted services-stop is skipped
// but the critical mcp-stop still runs (and lock-release with it) — the
// stdio children can never be orphaned by a blown budget.
func TestMCPStopSurvivesExhaustedBudget(t *testing.T) {
	var mu sync.Mutex
	var order []string
	record := func(n string, fn func()) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			order = append(order, n)
			mu.Unlock()
			if fn != nil {
				fn()
			}
			return nil
		}
	}

	coord := update.NewShutdownCoordinator(60 * time.Millisecond)
	coord.RegisterCritical("lock-release", time.Second, record("lock-release", nil))
	coord.RegisterCritical("mcp-stop", time.Second, record("mcp-stop", nil))
	coord.Register("services-stop", time.Second, record("services-stop", nil))
	// The drain eats the whole budget (its own timeout is clamped to the
	// remaining budget).
	coord.Register("agent-drain", time.Second, record("agent-drain", func() {
		time.Sleep(400 * time.Millisecond)
	}))

	results := coord.RunAll(context.Background())

	if err, ok := results["mcp-stop"]; !ok || err != nil {
		t.Errorf("mcp-stop result = %v, want a clean run despite the spent budget", err)
	}
	if err, ok := results["lock-release"]; !ok || err != nil {
		t.Errorf("lock-release result = %v, want a clean run despite the spent budget", err)
	}
	if err, ok := results["services-stop"]; !ok || !errors.Is(err, update.ErrShutdownBudgetExceeded) {
		t.Errorf("services-stop result = %v, want ErrShutdownBudgetExceeded", err)
	}
	if err, ok := results["agent-drain"]; !ok || err == nil {
		t.Errorf("agent-drain result = %v, want a timeout error (it spent the budget)", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != "agent-drain" || order[1] != "mcp-stop" || order[2] != "lock-release" {
		t.Errorf("ran hooks = %v, want [agent-drain mcp-stop lock-release] "+
			"(services-stop skipped by the budget, mcp-stop guaranteed)", order)
	}
}
