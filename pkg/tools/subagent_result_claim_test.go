package tools

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// waitForClaim polls cond until it holds or the budget is spent.
func waitForClaim(within time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// taskIDFromSpawnMessage extracts the task id out of the message Spawn returns
// ("Spawned subagent task subagent-1 ...").
func taskIDFromSpawnMessage(t *testing.T, msg string) string {
	t.Helper()
	idx := strings.Index(msg, "subagent-")
	if idx < 0 {
		t.Fatalf("no task id in spawn message %q", msg)
	}
	rest := msg[idx:]
	if end := strings.IndexAny(rest, " '\""); end > 0 {
		return rest[:end]
	}
	return rest
}

// ============================================================================
// Result-claim bookkeeping (A1)
//
// The claim is the single arbiter of "the parent's LLM context saw this result
// exactly once". Two parties contest it: wait_for_subagent (in-turn read) and
// the async injector in pkg/agent (system inbound message). These tests pin the
// primitives; the end-to-end races are covered in pkg/agent.
// ============================================================================

// newClaimTestManager builds a manager with no provider: these tests only
// exercise the bookkeeping, never a run. t.TempDir keeps the workspace off the
// real one.
func newClaimTestManager(t *testing.T) *SubagentManager {
	t.Helper()
	return NewSubagentManager(nil, "test-model", t.TempDir(), nil, 5)
}

// addClaimTestTask registers a task in the given state. It returns the live
// (non-snapshot) task so a test can drive terminal transitions directly.
func addClaimTestTask(t *testing.T, sm *SubagentManager, id, status string) *SubagentTask {
	t.Helper()
	task := &SubagentTask{
		ID:      id,
		Task:    "claim test task",
		Status:  status,
		Created: time.Now().UnixMilli(),
		Updated: time.Now().UnixMilli(),
	}
	sm.AddTaskForTest(task, nil)
	return task
}

// liveTask returns the manager's live task (not a Snapshot) so tests can read
// the claim counters without going through the copy.
func liveTask(t *testing.T, sm *SubagentManager, id string) *SubagentTask {
	t.Helper()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	task, ok := sm.tasks[id]
	if !ok || task == nil {
		t.Fatalf("task %s not found", id)
	}
	return task
}

// resultWaitersOf reads the waiter counter under the task lock.
func resultWaitersOf(t *testing.T, sm *SubagentManager, id string) int {
	t.Helper()
	task := liveTask(t, sm, id)
	if task.mu != nil {
		task.mu.Lock()
		defer task.mu.Unlock()
	}
	return task.resultWaiters
}

// markTerminal simulates a runner writing a terminal outcome: the status and
// result are set under sm.mu exactly as runTaskImpl does, but the done channel
// is deliberately left open so a test can control when (whether) waiters wake.
func markTerminal(t *testing.T, sm *SubagentManager, id, status, summary, result string) {
	t.Helper()
	sm.mu.Lock()
	task, ok := sm.tasks[id]
	if ok && task != nil {
		task.Status = status
		task.Summary = summary
		task.Result = result
		task.Updated = time.Now().UnixMilli()
	}
	sm.mu.Unlock()
	if !ok {
		t.Fatalf("task %s not found", id)
	}
}

// TestClaimResultForInjection_FirstWins proves the claim is winner-takes-all:
// with two goroutines racing, exactly one gets true.
func TestClaimResultForInjection_FirstWins(t *testing.T) {
	sm := newClaimTestManager(t)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusCompleted)

	var wins int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if sm.ClaimResultForInjection("subagent-1") {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&wins); got != 1 {
		t.Fatalf("ClaimResultForInjection winners = %d, want exactly 1", got)
	}
}

// TestClaimResultForInjection_BlockedByWaiter proves the injector yields to a
// registered wait_for_subagent waiter, and reclaims the chance once it leaves.
func TestClaimResultForInjection_BlockedByWaiter(t *testing.T) {
	sm := newClaimTestManager(t)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusCompleted)

	sm.AddResultWaiter("subagent-1")
	if sm.ClaimResultForInjection("subagent-1") {
		t.Fatal("ClaimResultForInjection = true with a waiter registered, want false")
	}

	sm.ReleaseResultWaiter("subagent-1")
	if !sm.ClaimResultForInjection("subagent-1") {
		t.Fatal("ClaimResultForInjection = false after the waiter left, want true")
	}
}

// TestClaimResultForInjection_UnknownTask pins the unguarded-injection guard:
// with no task there is no dedup, so the injector must not claim.
func TestClaimResultForInjection_UnknownTask(t *testing.T) {
	sm := newClaimTestManager(t)
	if sm.ClaimResultForInjection("does-not-exist") {
		t.Fatal("ClaimResultForInjection(unknown) = true, want false")
	}
}

// TestClaimResult_Idempotent proves both claim entry points are idempotent and
// report their outcome distinctly: ClaimResult says "already claimed", while
// ClaimResultForInjection says "I won".
func TestClaimResult_Idempotent(t *testing.T) {
	sm := newClaimTestManager(t)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusCompleted)
	addClaimTestTask(t, sm, "subagent-2", SubagentStatusCompleted)

	if sm.ClaimResult("subagent-1") {
		t.Error("first ClaimResult = true, want false (this call claimed it)")
	}
	if !sm.ClaimResult("subagent-1") {
		t.Error("second ClaimResult = false, want true (already claimed)")
	}

	if !sm.ClaimResultForInjection("subagent-2") {
		t.Error("first ClaimResultForInjection = false, want true")
	}
	if sm.ClaimResultForInjection("subagent-2") {
		t.Error("second ClaimResultForInjection = true, want false (idempotent)")
	}
}

// TestSealResult_IgnoresWaiters pins the /stop path: a registered waiter must
// not be able to block the seal, or a cancelled task could still resume the
// parent with a "cancelled" turn.
func TestSealResult_IgnoresWaiters(t *testing.T) {
	sm := newClaimTestManager(t)
	task := addClaimTestTask(t, sm, "subagent-1", SubagentStatusCancelled)

	sm.AddResultWaiter("subagent-1")
	sm.SealResult("subagent-1")

	if !task.ResultClaimed() {
		t.Fatal("ResultClaimed() = false after SealResult with a waiter registered, want true")
	}
	if sm.ClaimResultForInjection("subagent-1") {
		t.Fatal("ClaimResultForInjection = true after SealResult, want false")
	}
}

// TestContinueTask_ResetsClaimAndWaiters covers R9, the easiest thing to forget:
// a resumed task must be injectable again, with no stale waiter count.
func TestContinueTask_ResetsClaimAndWaiters(t *testing.T) {
	provider := &scriptedSubagentProvider{responses: []string{
		"STATUS: completed\nSUMMARY: Resumed\nDETAILS:\nResumed and finished.",
	}}
	sm := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusNeedsContext)

	sm.ClaimResult("subagent-1")
	sm.AddResultWaiter("subagent-1")
	if _, err := sm.ContinueTask(context.Background(), "subagent-1", "here is the missing context", nil); err != nil {
		t.Fatalf("ContinueTask failed: %v", err)
	}

	task := liveTask(t, sm, "subagent-1")
	if task.ResultClaimed() {
		t.Error("ResultClaimed() = true after ContinueTask, want false (resumed runs must be injectable)")
	}
	if got := resultWaitersOf(t, sm, "subagent-1"); got != 0 {
		t.Errorf("resultWaiters = %d after ContinueTask, want 0", got)
	}

	waitForSubagentDone(t, task)
}

// TestReleaseResultWaiterAndClaim_ClaimsTerminalOnLastWaiter covers the release
// primitive directly: the last waiter out claims a terminal, unclaimed result.
func TestReleaseResultWaiterAndClaim_ClaimsTerminalOnLastWaiter(t *testing.T) {
	sm := newClaimTestManager(t)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusRunning)
	sm.AddResultWaiter("subagent-1")

	// Still running: nothing to hand back, and the registration is dropped.
	if task := sm.ReleaseResultWaiterAndClaim("subagent-1"); task != nil {
		t.Fatalf("ReleaseResultWaiterAndClaim on a running task = %+v, want nil", task)
	}
	if got := resultWaitersOf(t, sm, "subagent-1"); got != 0 {
		t.Fatalf("resultWaiters = %d after release, want 0", got)
	}

	// The task turns terminal after the release: a later injector claims it.
	markTerminal(t, sm, "subagent-1", SubagentStatusCompleted, "Done", "the answer")

	sm.AddResultWaiter("subagent-1")
	task := sm.ReleaseResultWaiterAndClaim("subagent-1")
	if task == nil {
		t.Fatal("ReleaseResultWaiterAndClaim on a terminal unclaimed task = nil, want the task back")
	}
	if task.Result != "the answer" {
		t.Errorf("handed-back task Result = %q, want %q", task.Result, "the answer")
	}
	if !liveTask(t, sm, "subagent-1").ResultClaimed() {
		t.Error("ResultClaimed() = false after the release claimed it, want true")
	}
	if sm.ClaimResultForInjection("subagent-1") {
		t.Error("ClaimResultForInjection = true after the release claimed it, want false")
	}
}

// TestReleaseResultWaiterAndClaim_KeepsRegistrationUntilLastWaiter proves the
// clamp/last-waiter rule: an inner waiter leaving must not open the injection
// window while another waiter is still registered.
func TestReleaseResultWaiterAndClaim_KeepsRegistrationUntilLastWaiter(t *testing.T) {
	sm := newClaimTestManager(t)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusCompleted)
	markTerminal(t, sm, "subagent-1", SubagentStatusCompleted, "Done", "the answer")

	sm.AddResultWaiter("subagent-1")
	sm.AddResultWaiter("subagent-1")

	if task := sm.ReleaseResultWaiterAndClaim("subagent-1"); task != nil {
		t.Fatal("first release handed back a result while a waiter remained registered")
	}
	if got := resultWaitersOf(t, sm, "subagent-1"); got != 1 {
		t.Fatalf("resultWaiters = %d, want 1", got)
	}
	if task := sm.ReleaseResultWaiterAndClaim("subagent-1"); task == nil {
		t.Fatal("last release did not claim the terminal result")
	}
	// A spurious extra release must not go negative nor re-claim.
	if task := sm.ReleaseResultWaiterAndClaim("subagent-1"); task != nil {
		t.Fatal("extra release re-handed the result, want nil")
	}
	if got := resultWaitersOf(t, sm, "subagent-1"); got != 0 {
		t.Fatalf("resultWaiters = %d after extra release, want 0 (clamped)", got)
	}
}

// ============================================================================
// wait_for_subagent × the claim
// ============================================================================

// gatedSubagentProvider blocks inside Chat until release is closed, so a test
// can hold a real subagent in the running state while it inspects the waiter
// bookkeeping.
type gatedSubagentProvider struct {
	release chan struct{}
	once    sync.Once
}

func (p *gatedSubagentProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string, options map[string]interface{}) (*providers.LLMResponse, error) {
	<-p.release
	return &providers.LLMResponse{Content: "STATUS: completed\nSUMMARY: Done\nDETAILS:\nGated result"}, nil
}

func (p *gatedSubagentProvider) GetDefaultModel() string { return "test-model" }
func (p *gatedSubagentProvider) SupportsTools() bool     { return false }
func (p *gatedSubagentProvider) GetContextWindow() int   { return 4096 }

func (p *gatedSubagentProvider) unblock() {
	p.once.Do(func() { close(p.release) })
}

// TestWaitForSubagent_RegistersWaiterBeforeBlocking is the C12 guard: the
// waiter registration must be visible BEFORE the wait blocks, so the injector
// sees "the parent is about to read this in-turn" instead of racing the
// completion callback.
func TestWaitForSubagent_RegistersWaiterBeforeBlocking(t *testing.T) {
	provider := &gatedSubagentProvider{release: make(chan struct{})}
	t.Cleanup(provider.unblock)
	sm := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)

	msg, err := sm.Spawn(context.Background(), "hold on", "gated", "", "telegram", "chat-1", nil)
	if err != nil {
		t.Fatalf("Spawn failed: %v", err)
	}
	taskID := taskIDFromSpawnMessage(t, msg)

	waited := make(chan *ToolResult, 1)
	go func() {
		tool := NewWaitForSubagentTool(sm)
		waited <- tool.Execute(context.Background(), map[string]interface{}{
			"task_id":         taskID,
			"timeout_seconds": float64(10),
		})
	}()

	if !waitForClaim(3*time.Second, func() bool { return resultWaitersOf(t, sm, taskID) == 1 }) {
		t.Fatalf("resultWaiters never reached 1 while the wait was blocking (got %d)", resultWaitersOf(t, sm, taskID))
	}
	if sm.ClaimResultForInjection(taskID) {
		t.Fatal("ClaimResultForInjection = true while a waiter was registered, want false")
	}

	provider.unblock()

	var result *ToolResult
	select {
	case result = <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("wait_for_subagent did not return after the provider was released")
	}
	if result == nil || result.IsError {
		t.Fatalf("wait result = %+v, want a success result", result)
	}
	if !strings.Contains(result.ForLLM, "Gated result") {
		t.Errorf("wait result = %q, want it to contain the subagent result", result.ForLLM)
	}
	if got := resultWaitersOf(t, sm, taskID); got != 0 {
		t.Errorf("resultWaiters = %d after the wait returned, want 0", got)
	}
	if !liveTask(t, sm, taskID).ResultClaimed() {
		t.Error("ResultClaimed() = false after the wait returned the result, want true")
	}
}

// TestWaitForSubagent_TimeoutReleasesWaiterWithoutClaiming proves nothing is
// lost when a wait gives up on a STILL-RUNNING task: the registration is
// dropped without claiming, so a later completion is injectable.
func TestWaitForSubagent_TimeoutReleasesWaiterWithoutClaiming(t *testing.T) {
	sm := newClaimTestManager(t)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusRunning)

	tool := NewWaitForSubagentTool(sm)
	result := tool.Execute(context.Background(), map[string]interface{}{
		"task_id":         "subagent-1",
		"timeout_seconds": float64(1),
	})

	if result == nil || !result.IsError {
		t.Fatalf("wait result = %+v, want a timeout error", result)
	}
	if got := resultWaitersOf(t, sm, "subagent-1"); got != 0 {
		t.Errorf("resultWaiters = %d after the timeout, want 0", got)
	}
	if liveTask(t, sm, "subagent-1").ResultClaimed() {
		t.Error("ResultClaimed() = true after a timeout on a running task, want false")
	}
	if !sm.ClaimResultForInjection("subagent-1") {
		t.Error("ClaimResultForInjection = false after the timeout released the waiter, want true (the completion must still be injectable)")
	}
}

// TestWaitForSubagent_TimeoutRaceWithCompletion_ClaimOnRelease is the AM1
// regression test for the loss window: the injector refuses to publish while a
// waiter is registered, so a waiter that times out on a task that became
// terminal in the meantime must claim the result on its way out and return it
// as a SUCCESS. The model asked a direct question and must get the answer.
func TestWaitForSubagent_TimeoutRaceWithCompletion_ClaimOnRelease(t *testing.T) {
	sm := newClaimTestManager(t)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusRunning)

	waited := make(chan *ToolResult, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		tool := NewWaitForSubagentTool(sm)
		waited <- tool.Execute(context.Background(), map[string]interface{}{
			"task_id":         "subagent-1",
			"timeout_seconds": float64(1),
		})
	}()
	<-started

	if !waitForClaim(3*time.Second, func() bool { return resultWaitersOf(t, sm, "subagent-1") == 1 }) {
		t.Fatalf("resultWaiters never reached 1 (got %d)", resultWaitersOf(t, sm, "subagent-1"))
	}

	// The task completes while the waiter is registered and before its timer
	// fires — exactly the window in which the injector gives up (a waiter is
	// registered) and the waiter used to give up too (it only checked once).
	// The done channel is intentionally NOT closed: the release path must be
	// the one that notices.
	markTerminal(t, sm, "subagent-1", SubagentStatusCompleted, "Done", "the answer that raced the timeout")

	var result *ToolResult
	select {
	case result = <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("wait_for_subagent did not return after the task went terminal")
	}

	if result == nil {
		t.Fatal("wait returned no result")
	}
	if result.IsError {
		t.Fatalf("wait returned an error instead of the raced result: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "the answer that raced the timeout") {
		t.Errorf("wait result = %q, want the terminal result text", result.ForLLM)
	}
	if !liveTask(t, sm, "subagent-1").ResultClaimed() {
		t.Error("ResultClaimed() = false after the release handed the result back, want true")
	}
	if sm.ClaimResultForInjection("subagent-1") {
		t.Error("ClaimResultForInjection = true after the release claimed it, want false")
	}
	if got := resultWaitersOf(t, sm, "subagent-1"); got != 0 {
		t.Errorf("resultWaiters = %d after the claim, want 0", got)
	}
}

// TestWaitForSubagent_ImmediateTerminal_ClaimsOnce pins the fast path: a task
// that is already terminal is claimed (so the injector will not also publish
// it) but still answered to the model.
func TestWaitForSubagent_ImmediateTerminal_ClaimsOnce(t *testing.T) {
	sm := newClaimTestManager(t)
	addClaimTestTask(t, sm, "subagent-1", SubagentStatusCompleted)
	markTerminal(t, sm, "subagent-1", SubagentStatusCompleted, "Done", "already finished")

	tool := NewWaitForSubagentTool(sm)
	result := tool.Execute(context.Background(), map[string]interface{}{"task_id": "subagent-1"})

	if result == nil || result.IsError {
		t.Fatalf("wait result = %+v, want a success result", result)
	}
	if !strings.Contains(result.ForLLM, "already finished") {
		t.Errorf("wait result = %q, want it to contain the result", result.ForLLM)
	}
	if !liveTask(t, sm, "subagent-1").ResultClaimed() {
		t.Error("ResultClaimed() = false after an immediate-terminal wait, want true")
	}
	if got := resultWaitersOf(t, sm, "subagent-1"); got != 0 {
		t.Errorf("resultWaiters = %d after the wait returned, want 0", got)
	}
}

// ============================================================================
// One callback per task run (C10)
// ============================================================================

// recordingCallback collects the payloads delivered to the async callback.
type recordingCallback struct {
	mu      sync.Mutex
	results []*ToolResult
}

func (r *recordingCallback) fn(_ context.Context, result *ToolResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, result)
}

func (r *recordingCallback) snapshot() []*ToolResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*ToolResult(nil), r.results...)
}

// newCallbackTestTask mirrors what SpawnWithOptions builds: the retry policy
// lives on the task, and runTask is invoked directly so the test can observe
// the callback without going through the manager's goroutine plumbing.
func newCallbackTestTask(id string, maxRetries int) *SubagentTask {
	return &SubagentTask{
		ID:               id,
		Task:             "do the thing",
		AgentID:          "agent",
		OriginChannel:    "native",
		OriginChatID:     "chat-1",
		OriginSessionKey: "native:chat-1",
		Status:           SubagentStatusPending,
		MaxRetries:       maxRetries,
	}
}

// TestRunTask_CallbackFiresOnlyForFinalAttempt is the C10 guard: a transient
// failure that is about to be retried must NOT be delivered, or the parent
// would resume on "failed [rate_limit]" and the claim would suppress the
// successful retry.
func TestRunTask_CallbackFiresOnlyForFinalAttempt(t *testing.T) {
	withNoRetrySleep(t)

	provider := &flakySubagentProvider{
		// Burn the inner per-call retry budget so the OUTER runTask retry is
		// what recovers the task.
		failAt:  DefaultRetryConfig().MaxRetries + 1,
		failErr: transportErr(),
		final:   "STATUS: completed\nSUMMARY: Done\nDETAILS:\nCompleted after retry",
	}
	sm := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)
	sm.SetDefaultMaxRetries(2)

	task := newCallbackTestTask("subagent-callback-retry", 2)
	cb := &recordingCallback{}
	sm.runTask(context.Background(), task, cb.fn)

	results := cb.snapshot()
	if len(results) != 1 {
		t.Fatalf("callback fired %d times, want exactly 1 (the final attempt only)", len(results))
	}
	if results[0] == nil || results[0].IsError {
		t.Fatalf("callback payload = %+v, want the successful attempt's result", results[0])
	}
	if snap := task.Snapshot(); snap.Status != SubagentStatusCompleted {
		t.Errorf("task status = %q, want %q", snap.Status, SubagentStatusCompleted)
	}
}

// TestRunTask_CallbackFiresOnceOnTerminalFailure pins the non-retried path: a
// terminal failure is the task's outcome and must be delivered exactly once.
func TestRunTask_CallbackFiresOnceOnTerminalFailure(t *testing.T) {
	withNoRetrySleep(t)

	provider := &flakySubagentProvider{
		failAt: 99,
		failErr: &providers.FailoverError{
			Reason:   providers.FailoverFormat,
			Provider: "test",
			Wrapped:  errors.New("invalid request format"),
		},
		final: "STATUS: completed\nSUMMARY: unreachable\nDETAILS:\n-",
	}
	sm := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)
	sm.SetDefaultMaxRetries(2)

	task := newCallbackTestTask("subagent-callback-terminal", 2)
	cb := &recordingCallback{}
	sm.runTask(context.Background(), task, cb.fn)

	results := cb.snapshot()
	if len(results) != 1 {
		t.Fatalf("callback fired %d times, want exactly 1", len(results))
	}
	if results[0] == nil || !results[0].IsError {
		t.Fatalf("callback payload = %+v, want an error result", results[0])
	}
	if snap := task.Snapshot(); snap.RetryCount != 0 {
		t.Errorf("RetryCount = %d, want 0 (terminal failure must not retry)", snap.RetryCount)
	}
}

// TestRunTask_CallbackFiresOnceOnExhaustedRetries pins the retry-exhaustion
// path: only the last attempt's failure may be delivered.
func TestRunTask_CallbackFiresOnceOnExhaustedRetries(t *testing.T) {
	withNoRetrySleep(t)

	provider := &flakySubagentProvider{
		failAt:  99,
		failErr: transportErr(),
		final:   "STATUS: completed\nSUMMARY: unreachable\nDETAILS:\n-",
	}
	sm := NewSubagentManager(provider, "test-model", t.TempDir(), nil, 5)
	sm.SetDefaultMaxRetries(1)

	task := newCallbackTestTask("subagent-callback-exhausted", 1)
	cb := &recordingCallback{}
	sm.runTask(context.Background(), task, cb.fn)

	results := cb.snapshot()
	if len(results) != 1 {
		t.Fatalf("callback fired %d times, want exactly 1 (last attempt only)", len(results))
	}
	if results[0] == nil || !results[0].IsError {
		t.Fatalf("callback payload = %+v, want the final error result", results[0])
	}
	if snap := task.Snapshot(); snap.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1 (one retry then exhausted)", snap.RetryCount)
	}
}
