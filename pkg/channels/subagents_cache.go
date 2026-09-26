package channels

// Server-side read cache for GET /api/v1/chat/sessions/{k}/subagents.
//
// Why this exists (defence in depth)
// ---------------------------------
// A stale WebUI bundle was observed issuing ~250 GET requests per second to
// that endpoint. Every request runs agentLoop.GetSessionSubagents, which scans
// every subagent manager of every agent and calls
// SessionManager.FindSubagentSessions for each of them: that takes the session
// manager write lock and can load full subagent sessions from disk through a
// single connection. The result is multi-second stalls on ordinary endpoints on
// the live gateway. Fixing the client is necessary but not sufficient: users may
// keep a cached bundle in their browser after a deploy, so the server must not
// let one misbehaving read client multiply its load.
//
// Note what is *not* on that expensive path: the handler's
// validateSessionOwnership, which runs before every cache lookup, only touches
// in-memory state (auth.GetClient and, for subagent keys,
// GetSubagentParentSessionKey). FindSubagentSessions and the session manager
// write lock are reached by the provider call (GetSessionSubagents) alone.
//
// Design notes
// ------------
//   - Keyed by the *normalised* session key (the handler's "native:" prefixing
//     happens before the key is handed to the cache), so different sessions can
//     never share an entry.
//   - Successful reads only. A fetch that returns an error or panics stores
//     nothing, so the next request retries.
//   - Bounded: the map holds at most subagentsCacheMaxKeys entries at rest.
//     In-flight fills may overshoot that cap transiently (evicting one would
//     break single-flight); the next completed fill evicts back down.
//   - Single-flight: concurrent identical calls collapse into one fetch. Every
//     participant waits on a per-entry channel that is closed exactly once,
//     including on panic, so no waiter can block forever.
//   - Waiters honour the caller's context: a disconnected client stops waiting
//     (see get). The fill itself is never abandoned, so the other waiters still
//     receive a value.
//   - Invalidated on subagent lifecycle events (spawn tool result, subagent
//     result, see native.go), so a refresh triggered by those events is never
//     served a list that predates them. It is NOT a blanket "never stale"
//     guarantee: the WebUI also refreshes on `tool.executing` with
//     tool === 'spawn' (web/src/hooks/event-handlers/tools.ts), which fires
//     before the spawn has produced a tool result, so that refresh can still
//     observe a pre-spawn list — bounded by subagentsCacheTTL, which is the
//     only staleness the client can see for an event the server does not
//     invalidate on.
//   - Authorisation is *not* part of this cache: the handler runs
//     validateSessionOwnership before every cache lookup, so changing the TTL
//     can never change auth semantics. The cache only affects latency.

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

const (
	// subagentsCacheTTL is how long one successful read may be reused.
	//
	// One second collapses the observed 250 req/s client into at most one
	// provider call per session per second while keeping the payload at most a
	// second stale. Subagent lifecycle events invalidate the entry immediately
	// (see invalidate), so the TTL only covers quiet periods. Live subagent
	// progress reaches the browser over SSE, so this endpoint only needs to be
	// roughly current; a sub-second staleness is imperceptible in the sidebar.
	subagentsCacheTTL = time.Second

	// subagentsCacheMaxKeys bounds memory. One entry holds a shallow copy of a
	// session's task list (value structs), so 256 entries stay in the tens of
	// kilobytes even on a gateway with many sessions, while still covering a
	// realistic number of concurrently open browser sessions.
	subagentsCacheMaxKeys = 256
)

// subagentsCache is a bounded, TTL-collapsing, single-flight cache of
// GetSessionSubagents results. It is safe for concurrent use.
type subagentsCache struct {
	// now is injected so tests are deterministic: no sleeping to expire an
	// entry, and no dependence on wall-clock granularity.
	now func() time.Time
	ttl time.Duration
	max int

	mu      sync.Mutex
	entries map[string]*subagentsCacheEntry
}

// subagentsCacheEntry is either an in-flight fill (done still false) or a
// completed value (done true).
//
// Outcome fields are written once by the single publisher of a fill and read by
// waiters only after ready is closed, which is a happens-before edge; that is
// why they need no lock of their own. done is the exception: it is the
// exactly-once flag, so it is written and read under subagentsCache.mu.
type subagentsCacheEntry struct {
	ready chan struct{} // closed exactly once, when the fill is published

	tasks     []SubagentTaskInfo
	err       error
	expiresAt time.Time

	// done is true once this entry's outcome has been published. The cache
	// mutex guards it, and an entry in the map with done == false is a fill in
	// progress.
	done bool
}

// newSubagentsCache builds a cache with an injectable clock. A ttl <= 0 makes
// every entry immediately stale, i.e. effectively no caching; max <= 0 falls
// back to subagentsCacheMaxKeys.
func newSubagentsCache(ttl time.Duration, max int, now func() time.Time) *subagentsCache {
	if now == nil {
		now = time.Now
	}
	if max <= 0 {
		max = subagentsCacheMaxKeys
	}
	return &subagentsCache{
		now:     now,
		ttl:     ttl,
		max:     max,
		entries: make(map[string]*subagentsCacheEntry),
	}
}

// subagentsReadCache returns the channel's subagents cache, creating it on first
// use. NewNativeChannel already builds it, but several tests (and anything that
// builds a bare &NativeChannel{} literal, as the rate-limit tests do) skip the
// constructor, so the lazy fallback keeps every code path working. The mutex
// covers only construction: once built the pointer never changes.
func (n *NativeChannel) subagentsReadCache() *subagentsCache {
	n.subagentsCacheMu.Lock()
	defer n.subagentsCacheMu.Unlock()
	if n.subagentsCached == nil {
		n.subagentsCached = newSubagentsCache(subagentsCacheTTL, subagentsCacheMaxKeys, time.Now)
	}
	return n.subagentsCached
}

// invalidateSubagentsCache drops the cached list for one session key, so the
// next read calls the provider again. It is called from the outbound dispatch
// path (dispatchOutboundMessage) on the same subagent lifecycle events the TUI
// invalidates on, so a WebSocket-driven refresh cannot be served a list that
// predates the event that triggered it.
//
// An empty key matches no entry (get is never called with one), and is skipped
// rather than reaching the cache - on a channel built without NewNativeChannel
// it also avoids constructing the cache just to delete nothing.
func (n *NativeChannel) invalidateSubagentsCache(sessionKey string) {
	if sessionKey == "" {
		return
	}
	n.subagentsReadCache().invalidate(sessionKey)
}

// get returns the subagent tasks for key, running fetch at most once per TTL
// window and collapsing concurrent identical calls into a single fetch.
//
// ctx must be non-nil. It is consulted only while this caller waits on someone
// else's fill: a cancelled context returns ctx.Err() promptly instead of
// blocking the handler goroutine, while the fill keeps running for its other
// waiters (abandoning it would strand them). With a context that never expires
// (context.Background) behaviour is exactly as if the context were not there.
//
// Failures (errors and panics) are never cached: the entry is dropped and the
// next request retries. The returned slice is a fresh shallow copy of the
// cached one, so a caller may reorder, truncate or overwrite its elements
// without corrupting the cache; the elements are value structs of scalars and
// immutable strings, so the copy shares nothing mutable with the cache.
func (c *subagentsCache) get(ctx context.Context, key string, fetch func() ([]SubagentTaskInfo, error)) ([]SubagentTaskInfo, error) {
	c.mu.Lock()
	e, ok := c.entries[key]

	// Fast path: a completed entry that is still fresh.
	if ok && e.done {
		if c.now().Before(e.expiresAt) {
			tasks := cloneSubagentTasks(e.tasks)
			c.mu.Unlock()
			return tasks, nil
		}
		// Stale: drop it and refill below.
		delete(c.entries, key)
		ok = false
	}

	// Single-flight: a fill for this key is already running (done entries
	// returned or were dropped above, so ok now means exactly "in flight").
	// Wait for it and serve its outcome instead of issuing a second provider
	// call.
	if ok {
		c.mu.Unlock()
		select {
		case <-e.ready:
			if e.err != nil {
				return nil, e.err
			}
			return cloneSubagentTasks(e.tasks), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// No usable entry: this caller becomes the leader for key.
	e = &subagentsCacheEntry{ready: make(chan struct{})}
	c.entries[key] = e
	c.evictLocked()
	c.mu.Unlock()

	return c.fill(key, e, fetch)
}

// invalidate drops the entry for key so the next read calls the provider again.
// It is a no-op when there is no entry (unknown key, or an entry that already
// expired).
//
// An in-flight fill is removed from the map as well, deliberately: leaving it
// would let a read that arrives *after* the lifecycle event join a fetch that
// started *before* it and be served the pre-event list, which is the staleness
// this is here to prevent. Only the map entry goes; the fill keeps its own
// reference to the entry, so its waiters are still released (nothing is
// orphaned) and it never re-inserts itself (publish writes through the entry it
// was handed, it does not touch the map except to drop a failed entry of its
// own). The price is that a read arriving after an invalidate may start a
// second, concurrent fill for the same key - acceptable, and bounded by the
// number of events, not by client request rate.
func (c *subagentsCache) invalidate(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// fill runs fetch as the leader of an entry and publishes the outcome to every
// waiter. A panic is converted into an error (never cached) so that waiters are
// always released; it is also logged, because net/http never sees it (see the
// recovery below).
func (c *subagentsCache) fill(key string, e *subagentsCacheEntry, fetch func() ([]SubagentTaskInfo, error)) (tasks []SubagentTaskInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			// net/http's "panic serving ..." log never happens for a panic
			// recovered here, so without this the operator would see a 500 and
			// nothing else. Log the recovered value with a stack, in the same
			// structured style as the rest of pkg/channels.
			logger.ErrorCF("native", "Subagents provider panicked", map[string]interface{}{
				"session_key": key,
				"panic":       fmt.Sprint(r),
				"stack":       string(debug.Stack()),
			})
			panicErr := fmt.Errorf("subagents provider panicked: %v", r)
			// Idempotent: if this fill had already published, the entry keeps the
			// outcome its waiters received and only this caller sees the panic.
			c.publish(key, e, nil, panicErr)
			tasks, err = nil, panicErr
		}
	}()

	fetched, err := fetch()
	if err != nil {
		c.publish(key, e, nil, err)
		return nil, err
	}

	// Sort once, here, before storing: ordering is part of the cached value, so
	// no caller ever has to mutate what it was handed (the REST handler used to
	// sort in place). Sorting a private copy also leaves the provider's slice
	// untouched.
	sorted := cloneSubagentTasks(fetched)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Created > sorted[j].Created })

	c.publish(key, e, sorted, nil)

	return cloneSubagentTasks(sorted), nil
}

// publish records a fill's outcome and releases its waiters, exactly once per
// entry: on success, on provider error and on recovered panic alike. A second
// call is a no-op.
//
// The guard is not decoration. A panic recovered *after* the value was already
// published (unreachable in practice today, but the deferred recovery wraps the
// whole of fill) would otherwise close e.ready a second time, and closing a
// closed channel panics inside a deferred function - i.e. it would take down
// the process instead of returning a 500. Publishing twice must therefore be
// harmless, and the second attempt must not touch the already-valid entry.
func (c *subagentsCache) publish(key string, e *subagentsCacheEntry, tasks []SubagentTaskInfo, err error) {
	c.mu.Lock()
	if e.done {
		c.mu.Unlock()
		return
	}
	e.done = true
	e.err = err
	if err != nil {
		// Failures are never cached: drop the entry so the next read refills
		// it. The identity check matters when the entry was invalidated
		// mid-flight: it must never evict a newer entry created since.
		if cur, ok := c.entries[key]; ok && cur == e {
			delete(c.entries, key)
		}
	} else {
		e.tasks = tasks
		e.expiresAt = c.now().Add(c.ttl)
	}
	c.mu.Unlock()
	close(e.ready)
}

// evictLocked keeps the map bounded. Expired entries go first; if the map is
// still over capacity, the entry with the earliest expiry is dropped (the one
// that will be useless soonest). In-flight entries are never evicted: removing
// one would let a second fill start for the same key and break single-flight.
//
// Caller must hold c.mu.
func (c *subagentsCache) evictLocked() {
	if len(c.entries) <= c.max {
		return
	}

	now := c.now()
	for k, e := range c.entries {
		if e.done && !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}

	for len(c.entries) > c.max {
		var victim string
		var victimExpiry time.Time
		found := false
		for k, e := range c.entries {
			if !e.done {
				continue
			}
			if !found || e.expiresAt.Before(victimExpiry) {
				victim, victimExpiry, found = k, e.expiresAt, true
			}
		}
		if !found {
			// Every entry is mid-flight: allow a temporary overshoot rather
			// than break single-flight. The next completed fill evicts again.
			return
		}
		delete(c.entries, victim)
	}
}

// cloneSubagentTasks returns a shallow copy of tasks. SubagentTaskInfo holds
// only scalars and strings, so copying the backing array already prevents a
// caller from mutating cached state (reordering, truncating, overwriting
// elements); the strings themselves are immutable.
func cloneSubagentTasks(tasks []SubagentTaskInfo) []SubagentTaskInfo {
	if tasks == nil {
		return nil
	}
	return append([]SubagentTaskInfo(nil), tasks...)
}
