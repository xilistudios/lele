package channels

// Tests for the WebUI subagents read cache (subagents_cache.go).
//
// Everything here is deterministic: the cache takes an injected clock, so TTL
// expiry is a matter of advancing a counter, never of sleeping, and the fake
// provider exposes a blocking gate so single-flight can be observed without
// racing on wall-clock timing.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeClock is a manually advanced clock: TTL expiry is deterministic.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1700000000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeSubagentsProvider stands in for agentLoop.GetSessionSubagents. It counts
// calls and can be told to block, fail or panic.
type fakeSubagentsProvider struct {
	mu    sync.Mutex
	calls int
	// fn produces the result; nil means "empty result, no error". It runs on
	// the calling goroutine, so a closure over test-local state is safe as long
	// as the test does not mutate that state concurrently.
	fn func() ([]SubagentTaskInfo, error)

	// started is closed when the first call enters the provider.
	started chan struct{}
	// release blocks every call until it is closed, so a test can keep one
	// fill in flight and observe how the cache handles other callers.
	release chan struct{}
}

func (p *fakeSubagentsProvider) fetch() ([]SubagentTaskInfo, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.mu.Unlock()

	if p.started != nil && call == 1 {
		close(p.started)
	}
	if p.release != nil {
		<-p.release
	}
	if p.fn != nil {
		return p.fn()
	}
	return nil, nil
}

func (p *fakeSubagentsProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// entryCount reads the production map size. The tests assert the bound on real
// state rather than trusting the eviction code's own bookkeeping.
func entryCount(c *subagentsCache) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// storedTasks returns a copy of what the cache holds for key.
func storedTasks(c *subagentsCache, key string) []SubagentTaskInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil
	}
	return cloneSubagentTasks(e.tasks)
}

// unsortedTasks is deliberately newest-last; the cache must sort it by Created
// descending before storing.
func unsortedTasks() []SubagentTaskInfo {
	return []SubagentTaskInfo{
		{TaskID: "oldest", Status: "completed", Created: 1000},
		{TaskID: "newest", Status: "running", Created: 3000},
		{TaskID: "middle", Status: "completed", Created: 2000},
	}
}

func taskIDs(tasks []SubagentTaskInfo) []string {
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.TaskID
	}
	return ids
}

func assertSortedNewestFirst(t *testing.T, tasks []SubagentTaskInfo) {
	t.Helper()
	for i := 1; i < len(tasks); i++ {
		if tasks[i-1].Created < tasks[i].Created {
			t.Fatalf("task order not sorted by Created descending: %v", taskIDs(tasks))
		}
	}
}

func newCacheForTest(clock *fakeClock) *subagentsCache {
	return newSubagentsCache(subagentsCacheTTL, subagentsCacheMaxKeys, clock.Now)
}

// cacheReadResult is one cache.get outcome, used to collect results from
// goroutines.
type cacheReadResult struct {
	tasks []SubagentTaskInfo
	err   error
}

// awaitRead receives one cacheReadResult under a deadlock guard: a bug that
// leaves a caller blocked would hang the whole suite otherwise.
func awaitRead(t *testing.T, ch <-chan cacheReadResult) cacheReadResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("cache.get never returned: a caller was left blocked")
		return cacheReadResult{}
	}
}

// readSubagents is the goroutine-side wrapper: get plus a send on ch.
func readSubagents(ctx context.Context, cache *subagentsCache, key string, fetch func() ([]SubagentTaskInfo, error), ch chan<- cacheReadResult) {
	tasks, err := cache.get(ctx, key, fetch)
	ch <- cacheReadResult{tasks, err}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// 10 sequential reads of the same session inside the TTL must reach the
// provider exactly once and return the same, correctly sorted data.
func TestSubagentsCache_RepeatedReadsWithinTTLHitProviderOnce(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		return unsortedTasks(), nil
	}}
	cache := newCacheForTest(clock)

	key := "native:session-1"
	for i := 0; i < 10; i++ {
		got, err := cache.get(context.Background(), key, provider.fetch)
		if err != nil {
			t.Fatalf("call %d: unexpected error = %v", i, err)
		}
		want := []string{"newest", "middle", "oldest"}
		if gotIDs := taskIDs(got); len(gotIDs) != len(want) || gotIDs[0] != want[0] || gotIDs[1] != want[1] || gotIDs[2] != want[2] {
			t.Fatalf("call %d: task ids = %v, want %v", i, gotIDs, want)
		}
	}

	if calls := provider.callCount(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1 for 10 reads inside the TTL", calls)
	}
	if n := entryCount(cache); n != 1 {
		t.Fatalf("cache entries = %d, want 1", n)
	}
}

// Just past the TTL the cache must fetch again and serve the fresh payload.
func TestSubagentsCache_ExpiredEntryRefetches(t *testing.T) {
	clock := newFakeClock()
	version := 1
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		// Sequential calls on one goroutine: no race. The provider returns a
		// different label per call so staleness would be visible.
		return []SubagentTaskInfo{{TaskID: "task", Label: fmt.Sprintf("v%d", version), Created: 1}}, nil
	}}
	cache := newCacheForTest(clock)

	key := "native:session-1"
	first, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("first get error = %v", err)
	}
	if first[0].Label != "v1" {
		t.Fatalf("first label = %q, want %q", first[0].Label, "v1")
	}

	// TTL not yet elapsed: still cached.
	clock.advance(subagentsCacheTTL - time.Millisecond)
	cached, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("cached get error = %v", err)
	}
	if cached[0].Label != "v1" || provider.callCount() != 1 {
		t.Fatalf("read inside TTL hit the provider: label=%q calls=%d", cached[0].Label, provider.callCount())
	}

	// TTL + epsilon: refetch.
	version = 2
	clock.advance(time.Millisecond + time.Millisecond)
	fresh, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("fresh get error = %v", err)
	}
	if fresh[0].Label != "v2" {
		t.Fatalf("label after TTL = %q, want %q (stale entry served)", fresh[0].Label, "v2")
	}
	if calls := provider.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2 after TTL expiry", calls)
	}
}

// Different session keys must never share an entry.
func TestSubagentsCache_DistinctKeysNeverShareResults(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		return unsortedTasks(), nil
	}}
	cache := newCacheForTest(clock)

	a, err := cache.get(context.Background(), "native:session-a", provider.fetch)
	if err != nil {
		t.Fatalf("get(a) error = %v", err)
	}
	if len(a) != 3 {
		t.Fatalf("get(a) len = %d, want 3", len(a))
	}

	// A second key must not be served from the first key's entry.
	b, err := cache.get(context.Background(), "native:session-b", provider.fetch)
	if err != nil {
		t.Fatalf("get(b) error = %v", err)
	}
	if len(b) != 3 {
		t.Fatalf("get(b) len = %d, want 3", len(b))
	}
	if calls := provider.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (one per session key)", calls)
	}

	// "native:" is part of the key: a bare key is a different session.
	if _, err := cache.get(context.Background(), "session-a", provider.fetch); err != nil {
		t.Fatalf("get(bare key) error = %v", err)
	}
	if calls := provider.callCount(); calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (normalised and bare keys are distinct)", calls)
	}
}

// 50 goroutines racing for the same session while one fill is in flight must
// produce exactly one provider call, and every caller must get the same data.
// Run under -race.
func TestSubagentsCache_ConcurrentIdenticalCallsCollapseIntoOne(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
		fn: func() ([]SubagentTaskInfo, error) {
			return unsortedTasks(), nil
		},
	}
	cache := newCacheForTest(clock)

	const goroutines = 50
	key := "native:session-stampede"

	results := make([][]SubagentTaskInfo, goroutines)
	errs := make([]error, goroutines)
	// queued is buffered to goroutines, so the sends never block and the
	// reader below can prove all 50 callers were launched before release.
	queued := make(chan struct{}, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			queued <- struct{}{}
			results[i], errs[i] = cache.get(context.Background(), key, provider.fetch)
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		<-queued
	}
	// The provider call is now in flight and cannot finish until we release it,
	// so the remaining callers must be joining it instead of fetching.
	<-provider.started
	close(provider.release)
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: error = %v", i, errs[i])
		}
		want := []string{"newest", "middle", "oldest"}
		if gotIDs := taskIDs(results[i]); len(gotIDs) != 3 || gotIDs[0] != want[0] || gotIDs[1] != want[1] || gotIDs[2] != want[2] {
			t.Fatalf("goroutine %d: task ids = %v, want %v", i, gotIDs, want)
		}
	}

	if calls := provider.callCount(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1 for %d concurrent identical reads", calls, goroutines)
	}
	if n := entryCount(cache); n != 1 {
		t.Fatalf("cache entries = %d, want 1", n)
	}
}

// A failing provider must not be cached: the next call retries and can succeed.
func TestSubagentsCache_ProviderErrorNotCached(t *testing.T) {
	clock := newFakeClock()
	providerErr := errors.New("backend down")
	failing := true
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		if failing {
			return nil, providerErr
		}
		return unsortedTasks(), nil
	}}
	cache := newCacheForTest(clock)

	key := "native:session-1"
	if _, err := cache.get(context.Background(), key, provider.fetch); !errors.Is(err, providerErr) {
		t.Fatalf("error = %v, want %v", err, providerErr)
	}
	if n := entryCount(cache); n != 0 {
		t.Fatalf("cache entries = %d after a failure, want 0 (failures must not be cached)", n)
	}

	// Still failing on retry: retried, not served from a cached failure.
	if _, err := cache.get(context.Background(), key, provider.fetch); !errors.Is(err, providerErr) {
		t.Fatalf("retry error = %v, want %v", err, providerErr)
	}
	if calls := provider.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (a failure must not be cached)", calls)
	}

	// The provider recovers.
	failing = false
	got, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("recovery get error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("recovery len = %d, want 3", len(got))
	}
	if calls := provider.callCount(); calls != 3 {
		t.Fatalf("provider calls = %d, want 3", calls)
	}
	// ...and the recovered value is cached like any success.
	if _, err := cache.get(context.Background(), key, provider.fetch); err != nil {
		t.Fatalf("cached get error = %v", err)
	}
	if calls := provider.callCount(); calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (success must be cached)", calls)
	}
}

// A panicking provider must be converted into an error for every participant of
// the fill — including concurrent waiters, which must never be left blocked on
// the entry's channel — and nothing may be cached. The recovered value must
// reach the caller (and the operator: the cache logs it with a stack).
func TestSubagentsCache_ProviderPanicReleasesWaitersAndIsNotCached(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	panicking := true
	provider.fn = func() ([]SubagentTaskInfo, error) {
		if panicking {
			panic("provider exploded")
		}
		return unsortedTasks(), nil
	}
	cache := newCacheForTest(clock)

	key := "native:session-panic"
	leader := make(chan cacheReadResult, 1)
	go readSubagents(context.Background(), cache, key, provider.fetch, leader)
	<-provider.started

	waiter := make(chan cacheReadResult, 1)
	go readSubagents(context.Background(), cache, key, provider.fetch, waiter)

	close(provider.release)

	for i, o := range []cacheReadResult{awaitRead(t, leader), awaitRead(t, waiter)} {
		if o.err == nil {
			t.Fatalf("caller %d: expected an error from the panicking provider, got tasks %v", i, o.tasks)
		}
		// The recovered value must be carried into the error the caller sees
		// (net/http never logs it: the panic is recovered inside the cache).
		if !strings.Contains(o.err.Error(), "provider exploded") {
			t.Fatalf("caller %d: error = %v, want it to name the recovered panic value", i, o.err)
		}
		if o.tasks != nil {
			t.Fatalf("caller %d: expected nil tasks alongside the error, got %v", i, o.tasks)
		}
	}
	if n := entryCount(cache); n != 0 {
		t.Fatalf("cache entries = %d after a panic, want 0 (failures must not be cached)", n)
	}

	// The next request retries and succeeds.
	panicking = false
	got, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("retry after panic: error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("retry after panic: len = %d, want 3", len(got))
	}
	if calls := provider.callCount(); calls < 2 {
		t.Fatalf("provider calls = %d, want a retry after the panic", calls)
	}
}

// Repeated panics in a row are the case where a non-idempotent publication
// would show up as a poisoned entry or a second close of the ready channel:
// each panic must be an error, leave nothing cached, and the next request must
// still be served normally.
func TestSubagentsCache_RepeatedPanicsNeverPoisonTheCache(t *testing.T) {
	clock := newFakeClock()
	remainingPanics := 2
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		if remainingPanics > 0 {
			remainingPanics--
			panic("provider exploded")
		}
		return unsortedTasks(), nil
	}}
	cache := newCacheForTest(clock)
	key := "native:session-double-panic"

	// Two fills, two panics: the entry must be dropped both times (a poisoned
	// entry would be served as an empty, successful list).
	for i := 0; i < 2; i++ {
		tasks, err := cache.get(context.Background(), key, provider.fetch)
		if err == nil {
			t.Fatalf("panic %d: expected an error, got tasks %v", i, tasks)
		}
		if tasks != nil {
			t.Fatalf("panic %d: expected nil tasks, got %v", i, tasks)
		}
		if n := entryCount(cache); n != 0 {
			t.Fatalf("panic %d: cache entries = %d, want 0", i, n)
		}
	}
	if calls := provider.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2 (neither panic may be cached)", calls)
	}

	// Third request: the provider recovers and the cache serves it normally.
	got, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("recovery get error = %v", err)
	}
	assertSortedNewestFirst(t, got)
	if ids := taskIDs(got); len(ids) != 3 || ids[0] != "newest" {
		t.Fatalf("recovery task ids = %v, want newest/middle/oldest", ids)
	}

	// ...and that value is cached like any other success.
	if _, err := cache.get(context.Background(), key, provider.fetch); err != nil {
		t.Fatalf("cached get error = %v", err)
	}
	if calls := provider.callCount(); calls != 3 {
		t.Fatalf("provider calls = %d, want 3 (the recovered value must be cached)", calls)
	}
}

// publish must be exactly-once per entry. The second call stands for a panic
// that lands after the value was already handed to waiters: it must not close
// ready a second time (closing a closed channel panics, and from a deferred
// function that would crash the process rather than fail one request) and must
// not overwrite the value the waiters already received.
func TestSubagentsCache_PublishIsIdempotent(t *testing.T) {
	clock := newFakeClock()
	cache := newCacheForTest(clock)
	key := "native:session-idempotent"
	want := []SubagentTaskInfo{{TaskID: "only", Status: "running", Created: 7}}

	e := &subagentsCacheEntry{ready: make(chan struct{})}
	cache.mu.Lock()
	cache.entries[key] = e
	cache.mu.Unlock()

	cache.publish(key, e, want, nil)
	// The late panic: a no-op. Without the done guard this panics with
	// "close of closed channel".
	cache.publish(key, e, nil, errors.New("late panic"))

	if e.err != nil {
		t.Fatalf("published entry err = %v, want nil (the late failure must not be recorded)", e.err)
	}
	if ids := taskIDs(e.tasks); len(ids) != 1 || ids[0] != "only" {
		t.Fatalf("published entry tasks = %v, want the first outcome", ids)
	}

	// The entry is still a valid, fresh cache hit: no provider call.
	got, err := cache.get(context.Background(), key, func() ([]SubagentTaskInfo, error) {
		t.Fatal("provider must not be called: the entry is published and fresh")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("get error = %v", err)
	}
	if ids := taskIDs(got); len(ids) != 1 || ids[0] != "only" {
		t.Fatalf("cached tasks = %v, want the published outcome", ids)
	}
}

// Inserting far more keys than the cap must not grow the map past the cap, and
// expired entries are the first to go.
func TestSubagentsCache_EvictionBoundsEntryCount(t *testing.T) {
	clock := newFakeClock()
	const capLimit = 8
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		return unsortedTasks(), nil
	}}
	cache := newSubagentsCache(subagentsCacheTTL, capLimit, clock.Now)

	for i := 0; i < 50; i++ {
		if _, err := cache.get(context.Background(), fmt.Sprintf("native:session-%d", i), provider.fetch); err != nil {
			t.Fatalf("get %d error = %v", i, err)
		}
		if n := entryCount(cache); n > capLimit {
			t.Fatalf("after insert %d: cache entries = %d, want <= %d", i, n, capLimit)
		}
	}
	if n := entryCount(cache); n != capLimit {
		t.Fatalf("cache entries = %d, want %d after 50 inserts", n, capLimit)
	}

	// Once the held entries expire, a new insert evicts them instead of
	// holding dead weight.
	clock.advance(subagentsCacheTTL + time.Millisecond)
	if _, err := cache.get(context.Background(), "native:session-fresh", provider.fetch); err != nil {
		t.Fatalf("get(fresh) error = %v", err)
	}
	if n := entryCount(cache); n != 1 {
		t.Fatalf("cache entries = %d after expiry, want 1 (expired entries must be pruned first)", n)
	}
}

// Ordering is applied once, before storing: the provider's slice is not
// reordered in place, what the cache holds is sorted, and callers receive a
// copy they can mutate freely.
func TestSubagentsCache_SortsBeforeStoringAndHandsOutCopies(t *testing.T) {
	clock := newFakeClock()
	raw := unsortedTasks()
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		return raw, nil
	}}
	cache := newCacheForTest(clock)

	key := "native:session-1"
	first, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("get error = %v", err)
	}
	assertSortedNewestFirst(t, first)
	if gotIDs := taskIDs(first); gotIDs[0] != "newest" || gotIDs[2] != "oldest" {
		t.Fatalf("first result ids = %v, want newest/middle/oldest", gotIDs)
	}

	// The provider's own slice must be left alone.
	if raw[0].TaskID != "oldest" {
		t.Fatalf("provider slice was reordered in place: %v", taskIDs(raw))
	}

	// The stored payload is the sorted order.
	stored := storedTasks(cache, key)
	assertSortedNewestFirst(t, stored)
	if gotIDs := taskIDs(stored); gotIDs[0] != "newest" || gotIDs[2] != "oldest" {
		t.Fatalf("stored ids = %v, want newest/middle/oldest", gotIDs)
	}

	// Mutating a handed-out slice must not corrupt the cache.
	reverse(first)
	if gotIDs := taskIDs(first); gotIDs[0] != "oldest" {
		t.Fatalf("test setup: reverse failed, ids = %v", gotIDs)
	}
	second, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("second get error = %v", err)
	}
	assertSortedNewestFirst(t, second)
	if gotIDs := taskIDs(second); gotIDs[0] != "newest" || gotIDs[2] != "oldest" {
		t.Fatalf("second result ids = %v, want newest/middle/oldest (cache was corrupted by the caller)", gotIDs)
	}
	if calls := provider.callCount(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (the sorted value must be cached)", calls)
	}
}

func reverse(tasks []SubagentTaskInfo) {
	for i, j := 0, len(tasks)-1; i < j; i, j = i+1, j-1 {
		tasks[i], tasks[j] = tasks[j], tasks[i]
	}
}

// ---------------------------------------------------------------------------
// Invalidation (subagent lifecycle events)
// ---------------------------------------------------------------------------

// invalidate must force the next read to call the provider again, even though
// the entry is still well inside its TTL.
func TestSubagentsCache_InvalidateRefetches(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		return unsortedTasks(), nil
	}}
	cache := newCacheForTest(clock)
	key := "native:session-invalidate"

	// Two reads inside the TTL: the second is served from the cache, which is
	// what makes the post-invalidate call count meaningful.
	for i := 0; i < 2; i++ {
		if _, err := cache.get(context.Background(), key, provider.fetch); err != nil {
			t.Fatalf("read %d error = %v", i, err)
		}
	}
	if calls := provider.callCount(); calls != 1 {
		t.Fatalf("provider calls = %d before invalidating, want 1", calls)
	}

	cache.invalidate(key)
	if n := entryCount(cache); n != 0 {
		t.Fatalf("cache entries after invalidate = %d, want 0", n)
	}

	got, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("read after invalidate error = %v", err)
	}
	assertSortedNewestFirst(t, got)
	if calls := provider.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d after invalidate, want 2: an event must not be masked by the TTL", calls)
	}
}

// Invalidating a key the cache does not hold is a no-op: it must not disturb a
// live entry belonging to another session.
func TestSubagentsCache_InvalidateUnknownKeyIsNoOp(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{fn: func() ([]SubagentTaskInfo, error) {
		return unsortedTasks(), nil
	}}
	cache := newCacheForTest(clock)
	key := "native:session-known"

	if _, err := cache.get(context.Background(), key, provider.fetch); err != nil {
		t.Fatalf("seed error = %v", err)
	}

	cache.invalidate("native:never-seen")
	cache.invalidate("")
	cache.invalidate("session-known") // the un-normalised spelling is a different key

	if n := entryCount(cache); n != 1 {
		t.Fatalf("cache entries after invalidating unknown keys = %d, want 1", n)
	}
	if _, err := cache.get(context.Background(), key, provider.fetch); err != nil {
		t.Fatalf("read after unknown-key invalidates error = %v", err)
	}
	if calls := provider.callCount(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1: invalidating another key must not evict a live entry", calls)
	}
}

// The channel-level helper is the caller-facing wrapper: it must never create
// the cache just to delete nothing from an empty key.
func TestNativeChannel_InvalidateSubagentsCacheEmptyKeyIsNoOp(t *testing.T) {
	channel := &NativeChannel{}

	channel.invalidateSubagentsCache("")

	if channel.subagentsCached != nil {
		t.Fatal("invalidateSubagentsCache(\"\") built the cache: an empty key can never match an entry")
	}
}

// Invalidating while a fill is in flight must:
//   - release that fill's waiters (nothing is orphaned: invalidate drops the map
//     entry, and the waiters hold a reference to the entry itself);
//   - let the pre-event fill finish without resurrecting its value (it must not
//     re-insert itself, otherwise the event would be masked again);
//   - make the next read fetch again instead of joining the pre-event fill.
func TestSubagentsCache_InvalidateDuringInFlightFill(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
		fn: func() ([]SubagentTaskInfo, error) {
			return unsortedTasks(), nil
		},
	}
	cache := newCacheForTest(clock)
	key := "native:session-inflight"

	leader := make(chan cacheReadResult, 1)
	go readSubagents(context.Background(), cache, key, provider.fetch, leader)
	<-provider.started

	// get has already inserted its entry (it does so before calling the
	// provider), so this is the entry every concurrent reader is waiting on.
	cache.mu.Lock()
	inFlight := cache.entries[key]
	cache.mu.Unlock()
	if inFlight == nil {
		t.Fatal("test setup: the in-flight fill is not in the map")
	}
	// Stand in for a waiter that joined before the event. Its wait is exactly
	// "ready is closed" (get's single-flight branch, pinned end to end by
	// TestSubagentsCache_ConcurrentIdenticalCallsCollapseIntoOne); driving the
	// same channel directly keeps the ordering deterministic.
	waiterReleased := make(chan struct{})
	go func() {
		<-inFlight.ready
		close(waiterReleased)
	}()

	// The lifecycle event arrives while the fill is still running.
	cache.invalidate(key)
	if n := entryCount(cache); n != 0 {
		t.Fatalf("cache entries after invalidating an in-flight fill = %d, want 0", n)
	}

	// The pre-event fill still completes and releases its waiters.
	close(provider.release)
	select {
	case <-waiterReleased:
	case <-time.After(5 * time.Second):
		t.Fatal("invalidating an in-flight fill orphaned its waiters: ready was never closed")
	}
	if r := awaitRead(t, leader); r.err != nil || len(r.tasks) != 3 {
		t.Fatalf("leader outcome = (%d tasks, %v), want 3 tasks and no error", len(r.tasks), r.err)
	}

	if n := entryCount(cache); n != 0 {
		t.Fatalf("cache entries after the invalidated fill published = %d, want 0 (a pre-event list must not come back)", n)
	}

	// The read that follows the event must reach the provider again.
	got, err := cache.get(context.Background(), key, provider.fetch)
	if err != nil {
		t.Fatalf("read after the event error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("read after the event: len = %d, want 3", len(got))
	}
	if calls := provider.callCount(); calls != 2 {
		t.Fatalf("provider calls = %d, want 2: the post-event read must not join the pre-event fill", calls)
	}
}

// ---------------------------------------------------------------------------
// Caller context
// ---------------------------------------------------------------------------

// A cancelled caller must return ctx.Err() instead of blocking on someone
// else's fill, and the fill itself must still complete: abandoning it would
// strand every other waiter. The read that was cancelled must not start a fill
// of its own either.
func TestSubagentsCache_CancelledWaiterDoesNotCancelTheFill(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
		fn: func() ([]SubagentTaskInfo, error) {
			return unsortedTasks(), nil
		},
	}
	cache := newCacheForTest(clock)
	key := "native:session-ctx"

	leader := make(chan cacheReadResult, 1)
	go readSubagents(context.Background(), cache, key, provider.fetch, leader)
	<-provider.started

	// The entry is in the map (get inserts it before calling the provider) and
	// these readers cannot become leaders, whatever the interleaving: the
	// cancelled one joins the running fill and returns ctx.Err() immediately,
	// the other waits for the fill like any follower.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cancelled := make(chan cacheReadResult, 1)
	other := make(chan cacheReadResult, 1)
	go readSubagents(ctx, cache, key, provider.fetch, cancelled)
	go readSubagents(context.Background(), cache, key, provider.fetch, other)
	cancel()

	// "Promptly" is provable here without timing: the fill is still gated, so a
	// return before the gate opens can only be the ctx.Err() one.
	if r := awaitRead(t, cancelled); !errors.Is(r.err, context.Canceled) {
		t.Fatalf("cancelled waiter outcome = (%d tasks, %v), want %v", len(r.tasks), r.err, context.Canceled)
	}

	// The cancelled caller did not cancel the fill: the leader (and the other
	// follower) still get their value once the provider returns.
	close(provider.release)
	if r := awaitRead(t, leader); r.err != nil || len(r.tasks) != 3 {
		t.Fatalf("leader outcome = (%d tasks, %v), want 3 tasks and no error", len(r.tasks), r.err)
	}
	if r := awaitRead(t, other); r.err != nil || len(r.tasks) != 3 {
		t.Fatalf("other waiter outcome = (%d tasks, %v), want 3 tasks and no error", len(r.tasks), r.err)
	}

	if calls := provider.callCount(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1: neither the cancelled read nor the follower may start a fill", calls)
	}
	// The successful fill is cached as usual.
	if _, err := cache.get(context.Background(), key, provider.fetch); err != nil {
		t.Fatalf("cached read error = %v", err)
	}
	if calls := provider.callCount(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1 (the fill must have been published)", calls)
	}
}

// A context without a deadline must change nothing: the follower still gets the
// leader's value (there is simply no Done to select on).
func TestSubagentsCache_ContextWithoutDeadlineStillWaits(t *testing.T) {
	clock := newFakeClock()
	provider := &fakeSubagentsProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
		fn: func() ([]SubagentTaskInfo, error) {
			return unsortedTasks(), nil
		},
	}
	cache := newCacheForTest(clock)
	key := "native:session-ctx-no-deadline"

	leader := make(chan cacheReadResult, 1)
	follower := make(chan cacheReadResult, 1)
	go readSubagents(context.Background(), cache, key, provider.fetch, leader)
	<-provider.started
	go readSubagents(context.Background(), cache, key, provider.fetch, follower)

	close(provider.release)
	if r := awaitRead(t, leader); r.err != nil || len(r.tasks) != 3 {
		t.Fatalf("leader outcome = (%d tasks, %v), want 3 tasks", len(r.tasks), r.err)
	}
	if r := awaitRead(t, follower); r.err != nil || len(r.tasks) != 3 {
		t.Fatalf("follower outcome = (%d tasks, %v), want 3 tasks", len(r.tasks), r.err)
	}
	if calls := provider.callCount(); calls != 1 {
		t.Fatalf("provider calls = %d, want 1", calls)
	}
}

// ---------------------------------------------------------------------------
// Handler wiring
// ---------------------------------------------------------------------------

// countingSubagentLoop wraps the shared test agent loop to observe how many
// times the REST handler reaches the provider.
type countingSubagentLoop struct {
	*nativeTestAgentLoop
	calls atomic.Int64
}

func (c *countingSubagentLoop) GetSessionSubagents(sessionKey string) []SubagentTaskInfo {
	c.calls.Add(1)
	return c.nativeTestAgentLoop.GetSessionSubagents(sessionKey)
}

// subagentListURL builds the read endpoint URL for a (possibly un-normalised)
// session key.
func subagentListURL(ts *nativeTestServer, key string) string {
	return ts.server.URL + "/api/v1/chat/sessions/" + url.PathEscape(key) + "/subagents"
}

// fetchSubagentList performs an authenticated GET against the read endpoint and
// returns the decoded payload. It fails the test on anything but 200.
func fetchSubagentList(t *testing.T, ts *nativeTestServer, key string) *SessionSubagentsResponse {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, subagentListURL(ts, key), nil)
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var payload SessionSubagentsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Decode error = %v", err)
	}
	return &payload
}

// statusOf performs a GET against the read endpoint and returns only the status
// code, so auth rejections can be pinned exactly.
func statusOf(t *testing.T, ts *nativeTestServer, key, bearer string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, subagentListURL(ts, key), nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// The read endpoint must serve repeated requests from the cache (one provider
// call), keep the response shape and ordering, and still reject unauthorised
// requests without touching the provider.
func TestHandleSessionSubagents_HandlerUsesCache(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.loop.sessionSubagents[sessionKey] = unsortedTasks()

	counter := &countingSubagentLoop{nativeTestAgentLoop: ts.loop}
	ts.channel.agentLoop = counter

	wantIDs := []string{"newest", "middle", "oldest"}
	for i := 0; i < 10; i++ {
		payload := fetchSubagentList(t, ts, sessionKey)
		if payload.SessionKey != sessionKey {
			t.Fatalf("request %d: session_key = %q, want %q", i, payload.SessionKey, sessionKey)
		}
		if len(payload.Subagents) != 3 {
			t.Fatalf("request %d: subagents = %d, want 3", i, len(payload.Subagents))
		}
		for j, id := range wantIDs {
			if payload.Subagents[j].TaskID != id {
				t.Fatalf("request %d: subagent[%d] = %q, want %q", i, j, payload.Subagents[j].TaskID, id)
			}
		}
	}

	if got := counter.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 for 10 identical endpoint requests", got)
	}

	// Authorisation still runs on every request: a call with no credentials is
	// rejected with 401 by the auth middleware (pinned exactly, like the 403
	// case below) and never reaches the provider.
	if status := statusOf(t, ts, sessionKey, ""); status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", status, http.StatusUnauthorized)
	}

	orphanKey := "native:orphan:subagent-1"
	if status := statusOf(t, ts, orphanKey, ts.token); status != http.StatusForbidden {
		t.Fatalf("orphan subagent key status = %d, want %d", status, http.StatusForbidden)
	}

	if got := counter.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d after rejected requests, want 1 (auth runs before the cache)", got)
	}
}

// The outbound dispatch path must invalidate the parent session's cached list on
// exactly the events that can change it - a spawn tool result (a new task now
// exists) and a subagent result (a task reached a terminal status) - and on no
// others, so the WebUI's refresh on those events cannot be served a pre-event
// list. Mirrors pkg/tui/handlers_events.go.
func TestDispatchOutboundMessage_InvalidatesSubagentsCache(t *testing.T) {
	ts := newNativeTestServer(t)

	sessionKey := "native:" + ts.clientID
	ts.loop.sessionSubagents[sessionKey] = unsortedTasks()

	counter := &countingSubagentLoop{nativeTestAgentLoop: ts.loop}
	ts.channel.agentLoop = counter

	// Seed the cache and prove the second read is served from it.
	fetchSubagentList(t, ts, sessionKey)
	fetchSubagentList(t, ts, sessionKey)
	if got := counter.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 before any event", got)
	}

	cases := []struct {
		name           string
		msg            bus.OutboundMessage
		wantInvalidate bool
	}{
		{
			name: "message.stream is not a subagent lifecycle event",
			msg: bus.OutboundMessage{
				Event:    "message.stream",
				ChatID:   sessionKey,
				Content:  "chunk",
				Metadata: map[string]string{"done": "false"},
			},
		},
		{
			name: "a non-spawn tool result is not a subagent lifecycle event",
			msg: bus.OutboundMessage{
				Event:    "tool.result",
				ChatID:   sessionKey,
				Metadata: map[string]string{"tool": "read_file"},
			},
		},
		{
			name: "a completed spawn means a new subagent task exists",
			msg: bus.OutboundMessage{
				Event:  "tool.result",
				ChatID: sessionKey,
				Metadata: map[string]string{
					"tool":                 "spawn",
					"subagent_session_key": sessionKey + ":subagent-1",
				},
			},
			wantInvalidate: true,
		},
		{
			name: "a subagent result carries a terminal status",
			msg: bus.OutboundMessage{
				Event:    "subagent.result",
				ChatID:   sessionKey,
				Metadata: map[string]string{"task_id": "subagent-1"},
			},
			wantInvalidate: true,
		},
	}

	wantCalls := int64(1)
	for _, tc := range cases {
		ts.channel.dispatchOutboundMessage(tc.msg)
		if tc.wantInvalidate {
			wantCalls++
		}
		payload := fetchSubagentList(t, ts, sessionKey)
		if len(payload.Subagents) != 3 {
			t.Fatalf("%s: subagents = %d, want 3", tc.name, len(payload.Subagents))
		}
		if got := counter.calls.Load(); got != wantCalls {
			t.Fatalf("%s: provider calls = %d, want %d", tc.name, got, wantCalls)
		}
	}
}
