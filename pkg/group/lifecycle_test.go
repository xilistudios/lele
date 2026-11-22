package group

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

func isTerminalStatus(s string) bool {
	return s == StatusDone || s == StatusStopped || s == StatusError
}

type lifecycleEvent struct {
	event  string
	status string
}

type lifecycleRecorder struct {
	mu      sync.Mutex
	byGroup map[string][]lifecycleEvent
	all     []bus.OutboundMessage
}

func newLifecycleRecorder() *lifecycleRecorder {
	return &lifecycleRecorder{byGroup: make(map[string][]lifecycleEvent)}
}

func (r *lifecycleRecorder) publish(msg bus.OutboundMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.all = append(r.all, msg)
	if msg.Event != "group.status" && msg.Event != "group.complete" {
		return
	}
	id := msg.Metadata["group_id"]
	r.byGroup[id] = append(r.byGroup[id], lifecycleEvent{
		event:  msg.Event,
		status: msg.Metadata["status"],
	})
}

func (r *lifecycleRecorder) terminalPair(t *testing.T, groupID string) (statusCount, completeCount int, seq []lifecycleEvent) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	seq = append([]lifecycleEvent(nil), r.byGroup[groupID]...)
	for _, e := range seq {
		switch e.event {
		case "group.status":
			if isTerminalStatus(e.status) {
				statusCount++
			}
		case "group.complete":
			completeCount++
		}
	}
	return statusCount, completeCount, seq
}

func (r *lifecycleRecorder) terminalStatus(t *testing.T, groupID string) string {
	t.Helper()
	n, _, seq := r.terminalPair(t, groupID)
	if n != 1 {
		t.Fatalf("group %s: terminal group.status events = %d, want exactly 1 (seq=%v)", groupID, n, seq)
	}
	for _, e := range seq {
		if e.event == "group.status" && isTerminalStatus(e.status) {
			return e.status
		}
	}
	return ""
}

func assertExactlyOnePair(t *testing.T, r *lifecycleRecorder, groupID string) {
	t.Helper()
	statusCount, completeCount, seq := r.terminalPair(t, groupID)
	if statusCount != 1 {
		t.Errorf("group %s: terminal group.status events = %d, want exactly 1 (seq=%v)", groupID, statusCount, seq)
	}
	if completeCount != 1 {
		t.Errorf("group %s: group.complete events = %d, want exactly 1 (seq=%v)", groupID, completeCount, seq)
	}

	firstStatus, firstComplete := -1, -1
	for i, e := range seq {
		if e.event == "group.status" && isTerminalStatus(e.status) && firstStatus < 0 {
			firstStatus = i
		}
		if e.event == "group.complete" && firstComplete < 0 {
			firstComplete = i
		}
	}
	if firstStatus >= 0 && firstComplete >= 0 && firstStatus > firstComplete {
		t.Errorf("group %s: terminal group.status (idx %d) published after group.complete (idx %d)",
			groupID, firstStatus, firstComplete)
	}
}

func waitWithTimeout(t *testing.T, d time.Duration, desc string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("timed out after %s waiting for %s", d, desc)
	}
}

type stepExecutor struct {
	mu      sync.Mutex
	calls   int
	fast    int
	release chan struct{}
}

func newStepExecutor(fast int) *stepExecutor {
	return &stepExecutor{fast: fast, release: make(chan struct{})}
}

func (e *stepExecutor) execute(ctx context.Context, req TurnRequest) (string, int, error) {
	e.mu.Lock()
	e.calls++
	n := e.calls
	e.mu.Unlock()

	if n <= e.fast {
		return fmt.Sprintf("turn-%d-%s", n, req.Speaker), 10, nil
	}
	select {
	case <-e.release:
		return fmt.Sprintf("turn-%d-%s", n, req.Speaker), 10, nil
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
}

func (e *stepExecutor) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *stepExecutor) waitBlockedFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if e.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("executor never reached %d calls (got %d)", n, e.callCount())
}

func TestRegression_StopEmitsExactlyOneTerminalPair(t *testing.T) {
	rec := newLifecycleRecorder()
	exec := newStepExecutor(1)
	gm := NewGroupManager(mockResolve, exec.execute, rec.publish)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	groupID, err := gm.Start(ctx, "life-stop-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a")}, GroupOptions{MaxTurns: 50}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	exec.waitBlockedFor(t, 2)

	if !gm.Stop(groupID) {
		t.Fatal("Stop returned false")
	}

	var waitErr error
	waitWithTimeout(t, 5*time.Second, "Wait to return after Stop", func() {
		_, waitErr = gm.Wait(groupID)
	})
	if waitErr != nil {
		t.Errorf("Wait err = %v, want nil (stop is not an error)", waitErr)
	}

	assertExactlyOnePair(t, rec, groupID)
	if got := rec.terminalStatus(t, groupID); got != StatusStopped {
		t.Errorf("terminal status = %q, want %q", got, StatusStopped)
	}

	st, ok := gm.Status(groupID)
	if !ok {
		t.Fatal("group not tracked after Wait")
	}
	if st.Status != StatusStopped {
		t.Errorf("state.Status = %q, want %q", st.Status, StatusStopped)
	}
}

func TestRegression_CtxCancelEmitsTerminalPair(t *testing.T) {
	rec := newLifecycleRecorder()
	exec := newStepExecutor(1)
	gm := NewGroupManager(mockResolve, exec.execute, rec.publish)

	parentCtx, cancelParent := context.WithCancel(context.Background())

	groupID, err := gm.Start(parentCtx, "life-cancel-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a"), plainParticipant("b")},
		GroupOptions{MaxTurns: 50}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	exec.waitBlockedFor(t, 2)
	cancelParent()

	waitWithTimeout(t, 5*time.Second, "Wait to return after parent cancel", func() {
		_, _ = gm.Wait(groupID)
	})

	assertExactlyOnePair(t, rec, groupID)
	if got := rec.terminalStatus(t, groupID); got != StatusStopped {
		t.Errorf("terminal status = %q, want %q", got, StatusStopped)
	}
}

func startManaged(gm *GroupManager, mg *managedGroup) {
	gm.mu.Lock()
	gm.groups[mg.state.ID] = mg
	gm.mu.Unlock()
}

func newManaged(state *GroupState) *managedGroup {
	_, cancel := context.WithCancel(context.Background())
	return &managedGroup{
		state:      state,
		originCh:   "ch",
		originChat: "chat",
		cancel:     cancel,
		done:       make(chan struct{}),
	}
}

func TestRegression_InvalidStrategyEmitsTerminalPair(t *testing.T) {
	rec := newLifecycleRecorder()
	exec := newStepExecutor(0)
	gm := NewGroupManager(mockResolve, exec.execute, rec.publish)

	state := &GroupState{
		ID:           "life-badstrategy-1",
		Task:         "task",
		Participants: []Participant{plainParticipant("a")},
		Strategy:     "nope",
		Status:       StatusRunning,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		MaxTurns:     4,
	}
	mg := newManaged(state)
	startManaged(gm, mg)

	go gm.runGroup(context.Background(), mg)

	var waitErr error
	waitWithTimeout(t, 5*time.Second, "Wait to return for invalid strategy", func() {
		_, waitErr = gm.Wait(state.ID)
	})
	if waitErr == nil {
		t.Error("Wait err = nil, want the NewStrategy error")
	}

	assertExactlyOnePair(t, rec, state.ID)
	if got := rec.terminalStatus(t, state.ID); got != StatusError {
		t.Errorf("terminal status = %q, want %q", got, StatusError)
	}
	if exec.callCount() != 0 {
		t.Errorf("executor calls = %d, want 0 (loop must not run)", exec.callCount())
	}
}

type panicExecutor struct{}

func (panicExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	panic("boom in turn for " + req.Speaker)
}

func TestRegression_PanicInTurnIsContained(t *testing.T) {
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, panicExecutor{}.execute, rec.publish)

	groupID, err := gm.Start(context.Background(), "life-panic-1", "p1", "task", "round_robin",
		[]Participant{plainParticipant("a"), plainParticipant("b")},
		GroupOptions{MaxTurns: 10}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var waitErr error
	waitWithTimeout(t, 5*time.Second, "Wait to return after panic", func() {
		_, waitErr = gm.Wait(groupID)
	})
	if waitErr == nil {
		t.Fatal("Wait err = nil, want a 'group panic' error")
	}
	if !strings.Contains(waitErr.Error(), "group panic") {
		t.Errorf("Wait err = %v, want it to mention 'group panic'", waitErr)
	}

	assertExactlyOnePair(t, rec, groupID)
	if got := rec.terminalStatus(t, groupID); got != StatusError {
		t.Errorf("terminal status = %q, want %q", got, StatusError)
	}
}

func TestRegression_PanicInParallelTurnIsContained(t *testing.T) {
	rec := newLifecycleRecorder()
	gm := NewGroupManager(mockResolve, panicExecutor{}.execute, rec.publish)

	groupID, err := gm.Start(context.Background(), "life-panic-par-1", "p1", "task", "moa",
		[]Participant{proposer("a"), proposer("b"), aggregator("agg")},
		GroupOptions{Rounds: 1, Parallel: true}, "ch", "chat")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var waitErr error
	waitWithTimeout(t, 5*time.Second, "Wait to return after parallel panic", func() {
		_, waitErr = gm.Wait(groupID)
	})
	if waitErr == nil {
		t.Fatal("Wait err = nil, want a panic error from the worker goroutine")
	}
	if !strings.Contains(waitErr.Error(), "panic") {
		t.Errorf("Wait err = %v, want it to mention the panic", waitErr)
	}

	assertExactlyOnePair(t, rec, groupID)
	if got := rec.terminalStatus(t, groupID); got != StatusError {
		t.Errorf("terminal status = %q, want %q", got, StatusError)
	}
}

type jitterExecutor struct {
	mu  sync.Mutex
	rnd *rand.Rand
	min time.Duration
	max time.Duration
}

func newJitterExecutor(min, max time.Duration) *jitterExecutor {
	return &jitterExecutor{rnd: rand.New(rand.NewSource(time.Now().UnixNano())), min: min, max: max}
}

func (e *jitterExecutor) execute(_ context.Context, req TurnRequest) (string, int, error) {
	e.mu.Lock()
	d := e.min + time.Duration(e.rnd.Int63n(int64(e.max-e.min+1)))
	e.mu.Unlock()
	time.Sleep(d)
	return fmt.Sprintf("turn-%s", req.Speaker), 10, nil
}

func TestRegression_NoDoubleTerminalOnStopRace(t *testing.T) {
	const groups = 50

	rec := newLifecycleRecorder()
	exec := newJitterExecutor(200*time.Microsecond, 2*time.Millisecond)
	gm := NewGroupManager(mockResolve, exec.execute, rec.publish)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ids := make([]string, 0, groups)
	for i := 0; i < groups; i++ {
		id := fmt.Sprintf("race-%d", i)
		_, err := gm.Start(ctx, id, "p1", "task", "round_robin",
			[]Participant{plainParticipant("a"), plainParticipant("b")},
			GroupOptions{MaxTurns: 4}, "ch", "chat")
		if err != nil {
			t.Fatalf("Start %s: %v", id, err)
		}
		ids = append(ids, id)
	}

	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(2)
		go func(gid string) {
			defer wg.Done()
			_, _ = gm.Wait(gid)
		}(id)
		delay := time.Duration(rand.Int63n(int64(6 * time.Millisecond)))
		go func(gid string, d time.Duration) {
			defer wg.Done()
			time.Sleep(d)
			gm.Stop(gid)
		}(id, delay)
	}

	waitWithTimeout(t, 20*time.Second, "all racers to finish", wg.Wait)

	time.Sleep(50 * time.Millisecond)

	var stopped, done, errored int
	for _, id := range ids {
		assertExactlyOnePair(t, rec, id)
		got := rec.terminalStatus(t, id)
		switch got {
		case StatusStopped:
			stopped++
		case StatusDone:
			done++
		case StatusError:
			errored++
		default:
			t.Errorf("group %s: terminal status = %q, want done|stopped|error", id, got)
		}
		st, ok := gm.Status(id)
		if !ok {
			t.Errorf("group %s not tracked", id)
			continue
		}
		if st.Status != got {
			t.Errorf("group %s: state.Status = %q but published terminal status = %q", id, st.Status, got)
		}
	}

	t.Logf("terminal outcomes: stopped=%d done=%d error=%d", stopped, done, errored)
}
