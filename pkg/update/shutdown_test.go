package update

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunAllLIFOOrder(t *testing.T) {
	c := NewShutdownCoordinator(5 * time.Second)

	var mu sync.Mutex
	var order []string
	record := func(name string) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return nil
		}
	}
	c.Register("first", time.Second, record("first"))
	c.Register("second", time.Second, record("second"))
	c.Register("third", time.Second, record("third"))

	results := c.RunAll(context.Background())

	want := []string{"third", "second", "first"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("execution order = %v, want %v (LIFO)", order, want)
	}
	if len(results) != 3 {
		t.Fatalf("results has %d entries, want 3", len(results))
	}
	for _, name := range want {
		if err, ok := results[name]; !ok {
			t.Errorf("results missing hook %q", name)
		} else if err != nil {
			t.Errorf("hook %q returned error %v, want nil", name, err)
		}
	}
}

func TestRunAllIdempotent(t *testing.T) {
	c := NewShutdownCoordinator(5 * time.Second)

	var calls int
	c.Register("counter", time.Second, func(context.Context) error {
		calls++
		return errors.New("boom")
	})

	first := c.RunAll(context.Background())
	if calls != 1 {
		t.Fatalf("hook ran %d times after first RunAll, want 1", calls)
	}
	if first["counter"] == nil || first["counter"].Error() != "boom" {
		t.Fatalf("first RunAll results = %v, want counter=boom", first)
	}

	second := c.RunAll(context.Background())
	if calls != 1 {
		t.Fatalf("hook ran %d times after second RunAll, want 1 (RunAll must be idempotent)", calls)
	}
	if second["counter"] == nil || second["counter"].Error() != "boom" {
		t.Fatalf("second RunAll results = %v, want the recorded counter=boom", second)
	}

	c.Register("late", time.Second, func(context.Context) error {
		t.Error("late hook must never run")
		return nil
	})
	if got := c.RunAll(context.Background()); len(got) != 1 {
		t.Fatalf("results after late Register = %v, want only the original hook", got)
	}
	if !c.HasRun() {
		t.Fatal("HasRun() = false after RunAll, want true")
	}
	if c.Total() != 5*time.Second {
		t.Fatalf("Total() = %v, want 5s", c.Total())
	}
}

func TestRunAllHookTimeout(t *testing.T) {
	c := NewShutdownCoordinator(10 * time.Second)

	nextRan := make(chan struct{})
	c.Register("fast", time.Second, func(context.Context) error {
		close(nextRan)
		return nil
	})
	c.Register("stuck", 100*time.Millisecond, func(context.Context) error {
		time.Sleep(5 * time.Second)
		return nil
	})

	started := time.Now()
	results := c.RunAll(context.Background())
	elapsed := time.Since(started)

	err := results["stuck"]
	if err == nil {
		t.Fatal("stuck hook returned nil error, want timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stuck hook error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("stuck hook error = %v, want it to mention the timeout", err)
	}

	select {
	case <-nextRan:
	case <-time.After(time.Second):
		t.Fatal("fast hook did not run after the stuck hook timed out")
	}
	if results["fast"] != nil {
		t.Fatalf("fast hook error = %v, want nil", results["fast"])
	}
	if elapsed > 2*time.Second {
		t.Fatalf("RunAll took %v; the hanging hook blocked shutdown", elapsed)
	}
}

func TestRunAllBudgetExceeded(t *testing.T) {
	budget := 200 * time.Millisecond
	c := NewShutdownCoordinator(budget)

	var mu sync.Mutex
	ran := []string{}
	slow := func(name string, d time.Duration) func(context.Context) error {
		return func(ctx context.Context) error {
			mu.Lock()
			ran = append(ran, name)
			mu.Unlock()
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
			return nil
		}
	}
	c.Register("c", time.Second, slow("c", 10*time.Millisecond))
	c.Register("b", time.Second, slow("b", 10*time.Second))
	c.Register("a", time.Second, slow("a", 10*time.Millisecond))

	started := time.Now()
	results := c.RunAll(context.Background())
	elapsed := time.Since(started)

	if err := results["a"]; err != nil {
		t.Fatalf("hook \"a\" error = %v, want nil (it ran within the budget)", err)
	}
	if err := results["b"]; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hook \"b\" error = %v, want a deadline-exceeded timeout", err)
	}
	if err := results["c"]; !errors.Is(err, ErrShutdownBudgetExceeded) {
		t.Fatalf("hook \"c\" error = %v, want ErrShutdownBudgetExceeded", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ran, ",") != "a,b" {
		t.Fatalf("hooks that ran = %v, want [a b]: c must be skipped", ran)
	}
	if elapsed > 2*budget {
		t.Errorf("RunAll took %v with a %v budget; the budget does not bound the teardown", elapsed, budget)
	}
}

func TestRunAllCriticalHookRunsAfterBudget(t *testing.T) {
	c := NewShutdownCoordinator(100 * time.Millisecond)

	var mu sync.Mutex
	ran := []string{}
	slow := func(name string, d time.Duration) func(context.Context) error {
		return func(ctx context.Context) error {
			mu.Lock()
			ran = append(ran, name)
			mu.Unlock()
			select {
			case <-time.After(d):
			case <-ctx.Done():
			}
			return nil
		}
	}

	c.RegisterCritical("lock-release", time.Second, slow("lock", 10*time.Millisecond))
	c.Register("burn", time.Second, slow("burn", 5*time.Second))

	results := c.RunAll(context.Background())

	if err := results["burn"]; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hook \"burn\" error = %v, want a deadline-exceeded timeout", err)
	}
	if err := results["lock-release"]; err != nil {
		t.Fatalf("critical hook \"lock-release\" error = %v, want nil (must run regardless of budget)", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if strings.Join(ran, ",") != "burn,lock" {
		t.Fatalf("hooks that ran = %v, want [burn lock]: the critical hook must not be skipped", ran)
	}
}

func TestRunAllSurvivesPanic(t *testing.T) {
	c := NewShutdownCoordinator(5 * time.Second)

	var mu sync.Mutex
	ran := []string{}
	c.Register("after-panic", time.Second, func(context.Context) error {
		mu.Lock()
		ran = append(ran, "after-panic")
		mu.Unlock()
		return nil
	})
	c.Register("panicker", time.Second, func(context.Context) error {
		panic("kaboom")
	})
	c.Register("last", time.Second, func(context.Context) error {
		mu.Lock()
		ran = append(ran, "last")
		mu.Unlock()
		return nil
	})

	results := c.RunAll(context.Background())

	err := results["panicker"]
	if err == nil {
		t.Fatal("panicking hook returned nil error, want a recorded panic error")
	}
	if !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("panic error = %v, want it to include the recovered value", err)
	}
	if results["last"] != nil || results["after-panic"] != nil {
		t.Fatalf("neighbouring hooks failed: %v", results)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 2 || ran[0] != "last" || ran[1] != "after-panic" {
		t.Fatalf("hooks that ran = %v, want [last after-panic] in LIFO order", ran)
	}
}

func TestRunAllIgnoresCancelledParentCtx(t *testing.T) {
	c := NewShutdownCoordinator(5 * time.Second)

	var mu sync.Mutex
	ran := map[string]error{}
	ctxFn := func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		ran["hook"] = nil
		return nil
	}
	c.Register("hook", 2*time.Second, ctxFn)

	parent, cancel := context.WithCancel(context.Background())
	cancel()

	results := c.RunAll(parent)

	mu.Lock()
	defer mu.Unlock()
	if _, ok := ran["hook"]; !ok {
		t.Fatalf("hook did not run with a cancelled parent ctx; results = %v", results)
	}
	if err := results["hook"]; err != nil {
		t.Fatalf("hook error = %v, want nil (hook ctx must not inherit the cancellation)", err)
	}
}

func TestRunAllConcurrentCallersSeeResults(t *testing.T) {
	c := NewShutdownCoordinator(5 * time.Second)

	var calls int
	var mu sync.Mutex
	c.Register("hook", time.Second, func(context.Context) error {
		mu.Lock()
		calls++
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
		return errors.New("recorded")
	})

	var wg sync.WaitGroup
	got := make([]map[string]error, 4)
	for i := range got {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			got[idx] = c.RunAll(context.Background())
		}(i)
	}
	wg.Wait()

	mu.Lock()
	if calls != 1 {
		t.Fatalf("hook ran %d times, want 1", calls)
	}
	mu.Unlock()

	for i, res := range got {
		if err := res["hook"]; err == nil || err.Error() != "recorded" {
			t.Fatalf("caller %d results = %v, want hook=recorded", i, res)
		}
	}
}

func TestRegisterNilFuncIgnored(t *testing.T) {
	c := NewShutdownCoordinator(time.Second)
	c.Register("nil", time.Second, nil)
	if results := c.RunAll(context.Background()); len(results) != 0 {
		t.Fatalf("results = %v, want empty map", results)
	}
}

func TestNewShutdownCoordinatorDefaultsTotal(t *testing.T) {
	if got := NewShutdownCoordinator(0).Total(); got != DefaultShutdownBudget {
		t.Fatalf("Total() = %v, want default %v", got, DefaultShutdownBudget)
	}
	if got := NewShutdownCoordinator(-time.Second).Total(); got != DefaultShutdownBudget {
		t.Fatalf("Total() = %v, want default %v", got, DefaultShutdownBudget)
	}
}
