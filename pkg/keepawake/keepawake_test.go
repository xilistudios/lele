package keepawake

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingStarter struct {
	mu        sync.Mutex
	starts    int
	releases  int
	failNext  bool
	failErr   error
	activeGen int
	released  map[int]bool
	t         *testing.T
}

func newCountingStarter(t *testing.T) *countingStarter {
	t.Helper()
	return &countingStarter{t: t, released: make(map[int]bool)}
}

func (c *countingStarter) failf(format string, args ...any) {
	if c.t != nil {
		c.t.Errorf(format, args...)
	}
}

func (c *countingStarter) starter() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.starts++
	if c.failNext {
		c.failNext = false
		return nil, c.failErr
	}
	gen := c.activeGen
	c.activeGen++
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.released[gen] {
			return
		}
		c.released[gen] = true
		c.releases++
	}, nil
}

func (c *countingStarter) counts() (starts, releases int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starts, c.releases
}

func TestStartStopDeliversEachReleaseOnce(t *testing.T) {
	c := newCountingStarter(t)
	inh := NewWithStarter(c.starter)

	if err := inh.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	inh.Stop()
	inh.Stop()
	if err := inh.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	inh.Stop()

	starts, releases := c.counts()
	if starts != 2 || releases != 2 {
		t.Fatalf("starts=%d releases=%d, want 2/2 (no double release, no leak)", starts, releases)
	}
}

func TestStartRejectsNilReleaseFunc(t *testing.T) {
	inh := NewWithStarter(func() (func(), error) { return nil, nil })

	err := inh.Start()
	if err == nil {
		t.Fatal("Start with a (nil, nil) starter returned nil, want an explicit error")
	}
	if !strings.Contains(err.Error(), "no release func") {
		t.Fatalf("error %q must say the starter returned no release func", err)
	}
	if inh.Active() {
		t.Fatal("Active() = true after a rejected (nil, nil) starter")
	}
	if err := inh.Start(); err == nil {
		t.Fatal("subsequent Start must still call the (broken) starter, not latch success")
	}
}

func TestStartReleasesOnStarterError(t *testing.T) {
	wantErr := errors.New("acquired then failed")
	released := make(chan struct{})
	inh := NewWithStarter(func() (func(), error) {
		return func() { close(released) }, wantErr
	})

	err := inh.Start()
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start error = %v, want the starter error", err)
	}
	select {
	case <-released:
	default:
		t.Fatal("starter returned a release closure with an error; Start must call it")
	}
	if inh.Active() {
		t.Fatal("Active() = true after a failed Start")
	}
}

func TestStartStopHappyPath(t *testing.T) {
	c := newCountingStarter(t)
	inh := NewWithStarter(c.starter)

	if inh.Active() {
		t.Fatal("fresh Inhibitor must not be active")
	}
	if err := inh.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !inh.Active() {
		t.Fatal("Active() = false after successful Start")
	}
	inh.Stop()
	if inh.Active() {
		t.Fatal("Active() = true after Stop")
	}
	starts, releases := c.counts()
	if starts != 1 || releases != 1 {
		t.Fatalf("starts=%d releases=%d, want 1/1", starts, releases)
	}
}

func TestStartTwiceStopTwiceAndRestart(t *testing.T) {
	c := newCountingStarter(t)
	inh := NewWithStarter(c.starter)

	if err := inh.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := inh.Start(); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if starts, _ := c.counts(); starts != 1 {
		t.Fatalf("starter called %d times, want 1", starts)
	}

	inh.Stop()
	inh.Stop()
	if _, releases := c.counts(); releases != 1 {
		t.Fatalf("release called %d times, want 1", releases)
	}

	if err := inh.Start(); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}
	if !inh.Active() {
		t.Fatal("Active() = false after re-Start")
	}
	if starts, _ := c.counts(); starts != 2 {
		t.Fatalf("starter called %d times after restart, want 2", starts)
	}
	inh.Stop()
}

func TestStartErrorLeavesInactiveAndRetries(t *testing.T) {
	wantErr := errors.New("mechanism exploded")
	c := newCountingStarter(t)
	c.failNext = true
	c.failErr = wantErr
	inh := NewWithStarter(c.starter)

	err := inh.Start()
	if err == nil {
		t.Fatal("Start with failing starter returned nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error %q does not wrap starter error", err)
	}
	if err != wantErr {
		t.Fatalf("Start returned %q, want the starter error unchanged", err)
	}
	if inh.Active() {
		t.Fatal("Active() = true after failed Start")
	}

	if err := inh.Start(); err != nil {
		t.Fatalf("retry Start: %v", err)
	}
	if starts, _ := c.counts(); starts != 2 {
		t.Fatalf("starter called %d times, want 2", starts)
	}
	inh.Stop()
}

func TestStopWithoutStartIsNoop(t *testing.T) {
	c := newCountingStarter(t)
	inh := NewWithStarter(c.starter)

	inh.Stop()
	if inh.Active() {
		t.Fatal("Active() = true after Stop with no Start")
	}
	if starts, releases := c.counts(); starts != 0 || releases != 0 {
		t.Fatalf("starter touched: starts=%d releases=%d, want 0/0", starts, releases)
	}
}

func TestZeroValueFallsBackToPlatformStart(t *testing.T) {
	var inh Inhibitor
	if inh.Active() {
		t.Fatal("zero value must not be active")
	}
	inh.Stop()

	if inh.starter != nil {
		t.Fatal("zero value must have nil starter")
	}
}

func TestConcurrentStartStop(t *testing.T) {
	c := newCountingStarter(t)
	inh := NewWithStarter(c.starter)

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	var failures atomic.Int64
	deadline := time.Now().Add(time.Second)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for n := 0; n < iterations; n++ {
				switch (g + n) % 3 {
				case 0, 1:
					if err := inh.Start(); err != nil {
						failures.Add(1)
					}
				case 2:
					inh.Stop()
				}
				if time.Now().After(deadline) {
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if failures.Load() != 0 {
		t.Fatalf("%d Start calls failed", failures.Load())
	}
	inh.Stop()
	if inh.Active() {
		t.Fatal("still active after final Stop")
	}
	if err := inh.Start(); err != nil {
		t.Fatalf("Start after concurrent churn: %v", err)
	}
	if !inh.Active() {
		t.Fatal("not active after final Start")
	}
	inh.Stop()
}
