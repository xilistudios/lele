// The REST layer's registry of LIVE MCP OAuth flows (contract routes 2-6 of
// .lele/tasks/mcp-oauth-rest-contract.md).
//
// Why this file exists: the interactive OAuth engine (pkg/mcp/auth.go) hands
// out an *mcp.AuthFlow, but an HTTP client cannot hold a Go pointer — it holds
// a flow ID across the start / poll / complete / cancel sequence, which may
// span several requests and several connections. This registry is that bridge:
// a small in-memory map from a flow id to the one live flow of an
// (agent, server) pair.
//
// Four rules shape every line:
//
//  1. ONE LIVE FLOW PER (agent, server). A second Start for the same pair
//     cancels the previous flow and starts a new one, so the old id stops
//     resolving (invariant 1). A flow that is STILL LIVE is released before the
//     new one starts, because pkg/mcp pins the loopback redirect URI in the
//     auth store and the new flow would have to bind the same port; a terminal
//     flow holds no port and is replaced only once the new flow exists. The old
//     listener and the old flow goroutine are released by the cancel, never
//     leaked.
//  2. NO BACKGROUND LOOP. There is no sweeper goroutine: EVERY lookup first
//     drops (and cancels) the entries whose deadline has passed (invariant 3).
//  3. NO SECRET IS EVER STORED OR RETURNED. An entry holds an *AuthFlow, the
//     server NAME, the resolved ServerConfig the engine was given, the agent id
//     and a deadline — never a code, a token, a client id, a client secret or a
//     verifier. It cannot leak what it never holds; the flow's own token store
//     is internal to pkg/mcp and is unreachable from here.
//  4. AGENT-SCOPED. A flow id only resolves with the agent_id that created it
//     (invariant 4). Ids are 128-bit crypto/rand hex (generateID, the same
//     generator the approval manager uses) — never a counter, never derived
//     from the server name.

package channels

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/mcp"
)

const (
	// mcpAuthFlowTTL is how long a REST-started flow stays resolvable. It
	// matches pkg/mcp's own defaultAuthTimeout (5m) on purpose: the registry
	// deadline is what ExpiresAt reports, and a listed expiry that outlives
	// the engine's budget would make the API lie about how long the auth_url
	// is usable.
	mcpAuthFlowTTL = 5 * time.Minute

	// mcpAuthFlowIDLen is the length of a generateID() id: 16 crypto/rand
	// bytes hex-encoded. Validating the shape (not just "unknown id") lets a
	// malformed id be refused as invalid_request before any lookup, which is
	// what the contract's error table asks for.
	mcpAuthFlowIDLen = 32
)

// startMCPAuthFlow is the flow-start seam (contract "Test seams"): a
// package-level var so a test can substitute a scripted flow without a
// network. It always points at the real engine otherwise.
var startMCPAuthFlow = mcp.StartAuth

// errMCPAuthFlowNotFound is returned by the registry when no live entry
// resolves for an (agent, flow id) pair: unknown, expired (swept), or owned by
// another agent. Callers map it to 404 mcp_auth_flow_not_found.
var errMCPAuthFlowNotFound = errors.New("mcp auth flow not found")

// mcpAuthEntry is one live flow plus the bookkeeping the REST layer owes it.
//
// serverConfig is the RESOLVED config the engine was handed (the winner of the
// mcp.json merge): the status endpoint rebuilds the auth report from it through
// mcp.CheckAuth, so the answer comes from the same config the login used.
// reloaded is set exactly once, under the registry mutex, by MarkReloaded.
type mcpAuthEntry struct {
	agentID      string
	name         string
	flowID       string
	serverConfig mcp.ServerConfig
	flow         *mcp.AuthFlow
	deadline     time.Time
	reloaded     bool
}

// ServerConfig returns the resolved config the flow was started with. It is
// structural (url, declared auth, header KEY NAMES/count) or already-expanded
// config the engine itself holds; nothing derived from it crosses the API —
// see mcpAuthStatusFor, which projects booleans only.
func (e *mcpAuthEntry) ServerConfig() mcp.ServerConfig { return e.serverConfig }

// mcpAuthFlows is the per-channel registry. It is safe for concurrent use:
// every field below is read/written under mu, and no user code (an engine call
// such as Cancel) ever runs while mu is held. Starts of ONE pair additionally
// hold that pair's own lock (startLocks), which is what keeps a double-fired
// client request from racing for the pair's single loopback port.
type mcpAuthFlows struct {
	mu sync.Mutex
	// entries is indexed by flow id: the id is the API's handle.
	entries map[string]*mcpAuthEntry
	// pairs maps "agentID\x00name" → flow id, the "one live flow per pair"
	// index. It is kept in step with entries at every insert and removal.
	pairs map[string]string
	// startLocks holds one lock per pair a Start is currently running for.
	// Refcounted (pairStartLock.refs), so the map only ever holds the pairs
	// being started right now and cannot grow with the number of servers.
	startLocks map[string]*pairStartLock
	// storeFor resolves the agent's auth store. It returns nil when the agent
	// has no root (StartAuth then reports ErrAuthStoreDisabled → 503): the
	// registry never guesses a store path of its own.
	storeFor func(agentID string) *mcp.AuthStore
}

// pairStartLock serializes the Starts of ONE pair without blocking any other
// pair. refs counts the goroutines that hold (or are about to hold) mu, so the
// registry can drop the lock the moment the last one is done with it.
type pairStartLock struct {
	mu   sync.Mutex
	refs int
}

// newMCPAuthFlows builds an empty registry. storeFor may be nil.
func newMCPAuthFlows(storeFor func(agentID string) *mcp.AuthStore) *mcpAuthFlows {
	return &mcpAuthFlows{
		entries:    make(map[string]*mcpAuthEntry),
		pairs:      make(map[string]string),
		startLocks: make(map[string]*pairStartLock),
		storeFor:   storeFor,
	}
}

// lockPair takes the start lock of one pair and returns its release function
// (never nil, idempotent-safe to call once).
//
// Why a lock at all: pkg/mcp PINS the loopback redirect URI of a login in the
// auth store, so two flows for the same server want the SAME port and only one
// of them can bind it. Serializing the pair means a concurrent second start (a
// client that fires twice, a retry) releases the previous listener before it
// binds, instead of losing a race against its own earlier request.
func (r *mcpAuthFlows) lockPair(key string) func() {
	r.mu.Lock()
	l := r.startLocks[key]
	if l == nil {
		l = &pairStartLock{}
		r.startLocks[key] = l
	}
	l.refs++
	r.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		r.mu.Lock()
		l.refs--
		if l.refs == 0 {
			// No one references it any more: a later start creates a fresh one.
			delete(r.startLocks, key)
		}
		r.mu.Unlock()
	}
}

// mcpAuthPairKey is the (agent, server) index key. NUL cannot appear in an
// agent id or a server name (both come from a JSON object/layer with
// path-and-name validation), so the two parts can never be confused.
func mcpAuthPairKey(agentID, name string) string { return agentID + "\x00" + name }

// isValidMCPFlowID reports whether s has the shape generateID produces: 32
// lowercase hex characters. It is a cheap shape check, NOT authentication —
// the agent scoping in Get is what makes an id unforgeable.
func isValidMCPFlowID(s string) bool {
	if len(s) != mcpAuthFlowIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// sweepLocked removes (and returns) every entry whose deadline has passed.
// Removal drops BOTH indexes so a swept pair can be started again immediately.
// The caller cancels the returned flows AFTER releasing mu: cancellation is
// user code and must not run under the lock.
func (r *mcpAuthFlows) sweepLocked(now time.Time) []*mcp.AuthFlow {
	var expired []*mcp.AuthFlow
	for id, e := range r.entries {
		if now.Before(e.deadline) {
			continue
		}
		delete(r.entries, id)
		key := mcpAuthPairKey(e.agentID, e.name)
		if cur, ok := r.pairs[key]; ok && cur == id {
			delete(r.pairs, key)
		}
		expired = append(expired, e.flow)
	}
	return expired
}

// cancelFlows releases the flows a sweep expired: Cancel is idempotent and
// closes the loopback listener, so a reaped flow stops holding a port.
func cancelFlows(flows []*mcp.AuthFlow) {
	for _, f := range flows {
		if f != nil {
			_ = f.Cancel()
		}
	}
}

// Start begins a flow for (agentID, name).
//
// ctx is the context the FLOW runs under, not merely the request's: pass a
// context that outlives the HTTP request which starts it (the REST handler
// passes context.Background()). Binding it to a request context would make
// pkg/mcp's run goroutine observe a cancelled context the moment the handler
// returns — every poll would then report a terminal "error"/"canceled". The
// flow's own 5m budget bounds the goroutine.
//
// Replacing an existing flow for the pair is ordered by the LOOPBACK PORT.
// pkg/mcp PINS the redirect URI of the first login in the auth store
// (resolveRedirectURI reuses the record's URI), so every later flow for the
// same server asks for the SAME port; a listener that is still bound makes that
// bind fail with EADDRINUSE. A still-live flow for the pair is therefore
// cancelled and unindexed BEFORE the new one starts — which is what makes
// invariant 1's "the response is always a working auth_url" true on a retry.
//
// A flow that is already TERMINAL holds no listener and is not released up
// front: it stays resolvable until the replacement actually starts, so a Start
// that fails (an unreachable authorization server, a disabled store) leaves it
// exactly as it was. (When the previous flow was still live the pair is left
// empty by a failed Start: its port had to be released first, and re-starting
// the old flow is not possible with the same auth URL.)
//
// The whole release → start → index sequence runs under the PAIR's lock, so
// concurrent Starts for one server are serialized instead of racing for its
// pinned port: each one releases the previous listener before binding, which is
// what makes "a working auth_url per call" true for a double-fired client too.
// Different servers never block each other.
//
// Returns the engine flow, the flow id and the registry deadline.
func (r *mcpAuthFlows) Start(ctx context.Context, agentID, name string, srv mcp.ServerConfig) (*mcp.AuthFlow, string, time.Time, error) {
	key := mcpAuthPairKey(agentID, name)
	unlock := r.lockPair(key)
	defer unlock()

	// Step 1: release the pair's live flow (see the port rule above).
	r.mu.Lock()
	expired := r.sweepLocked(time.Now())
	var released *mcp.AuthFlow
	if id, ok := r.pairs[key]; ok {
		if prev := r.entries[id]; prev != nil && !prev.flow.Done() {
			delete(r.entries, id)
			delete(r.pairs, key)
			released = prev.flow
		}
	}
	r.mu.Unlock()
	cancelFlows(append(expired, released))

	var store *mcp.AuthStore
	if r.storeFor != nil {
		store = r.storeFor(agentID)
	}
	// OpenURL is deliberately nil (contract invariant 5): the gateway may be
	// headless, so it must never spawn a browser. The client shows auth_url.
	flow, err := startMCPAuthFlow(ctx, name, srv, store, mcp.AuthOptions{})
	if err != nil {
		return nil, "", time.Time{}, err
	}

	id := generateID()
	deadline := time.Now().Add(mcpAuthFlowTTL)
	entry := &mcpAuthEntry{
		agentID:      agentID,
		name:         name,
		flowID:       id,
		serverConfig: srv,
		flow:         flow,
		deadline:     deadline,
	}

	// Step 2: index the new flow, replacing and cancelling whatever the pair
	// still holds. Only a TERMINAL predecessor can be found here (a live one was
	// released in step 1, and the pair's lock keeps other Starts out), but the
	// pair index and entries are updated together so they cannot drift.
	r.mu.Lock()
	// Ids are 128-bit crypto/rand hex, so a collision is a 2^-128 event — but
	// the registry makes it impossible rather than merely improbable, because a
	// colliding id would be silently OVERWRITTEN here: the entry it replaced
	// would lose its only handle while its pair index kept pointing at the
	// survivor, orphaning a live flow and its loopback listener. The loop costs
	// one map lookup in the common case (it runs its body zero times).
	for r.entries[id] != nil {
		id = generateID()
		entry.flowID = id
	}
	var replaced *mcp.AuthFlow
	if prevID, ok := r.pairs[key]; ok {
		if prev := r.entries[prevID]; prev != nil {
			replaced = prev.flow
		}
		delete(r.entries, prevID)
		delete(r.pairs, key)
	}
	r.entries[id] = entry
	r.pairs[key] = id
	r.mu.Unlock()

	cancelFlows([]*mcp.AuthFlow{replaced})
	return flow, id, deadline, nil
}

// Get resolves a flow id for one agent, sweeping expired entries first. ok is
// false for an unknown, expired or foreign id — the caller answers 404
// mcp_auth_flow_not_found for all three (telling them apart would leak whether
// another agent owns the id).
func (r *mcpAuthFlows) Get(agentID, flowID string) (*mcpAuthEntry, bool) {
	r.mu.Lock()
	expired := r.sweepLocked(time.Now())
	e, ok := r.entries[flowID]
	if ok && e.agentID != agentID {
		// Invariant 4: never resolvable across agents. The entry is left in
		// place: it belongs to the agent that created it.
		e, ok = nil, false
	}
	r.mu.Unlock()
	cancelFlows(expired)
	if !ok {
		return nil, false
	}
	return e, true
}

// Complete submits a code obtained out of band (the manual flow): the WebUI
// shows auth_url, the user completes the login in the browser, and the AS
// redirects to a redirect URI this gateway cannot serve, so the client pastes
// the code back here.
//
// A failure does NOT remove the flow: pkg/mcp records a retryable failure
// (status "error" with the flow still open) precisely so a second attempt with
// a fresh code can succeed. The caller maps the error; errMCPAuthFlowNotFound
// means the id did not resolve.
func (r *mcpAuthFlows) Complete(ctx context.Context, agentID, flowID, code, state string) error {
	e, ok := r.Get(agentID, flowID)
	if !ok {
		return errMCPAuthFlowNotFound
	}
	return e.flow.Submit(ctx, code, state)
}

// Cancel stops a flow and releases its listener. It is
// best-effort-idempotent (invariant 7): cancelling an already-terminal flow
// still returns nil while the entry is in the registry, and only a swept or
// unknown id reports errMCPAuthFlowNotFound.
func (r *mcpAuthFlows) Cancel(agentID, flowID string) error {
	e, ok := r.Get(agentID, flowID)
	if !ok {
		return errMCPAuthFlowNotFound
	}
	return e.flow.Cancel()
}

// MarkReloaded claims the ONE reload a completed flow is owed (invariant 2:
// the credential only becomes visible to the manager at fingerprint time, so
// without the reload the cached connection stays unauthenticated for the life
// of the process).
//
// It returns true for exactly one caller — the first to observe the flow after
// it reached "done" — and false for every other poll. The claim is atomic
// under the registry mutex, which is what makes concurrent polls safe.
//
// A flow that is still pending, or that ended in an error (cancel, timeout,
// failed exchange) can never claim: invariant 2 fires the reload on the
// pending → done transition only. Enforcing that HERE means a caller that
// forgets to check cannot fire the manager's re-wiring pass for a login that
// never produced a credential.
func (r *mcpAuthFlows) MarkReloaded(agentID, flowID string) bool {
	r.mu.Lock()
	expired := r.sweepLocked(time.Now())
	e := r.entries[flowID]
	first := false
	if e != nil && e.agentID == agentID && !e.reloaded {
		// Status() takes the FLOW's own mutex: safe here, because pkg/mcp
		// never calls back into this package, so the two locks cannot be taken
		// in the opposite order anywhere.
		state, _ := e.flow.Status()
		first = state == mcpAuthStateDone
	}
	if first {
		e.reloaded = true
	}
	r.mu.Unlock()
	cancelFlows(expired)
	return first
}

// CancelPair cancels the live flow of an (agent, server) pair, if any. It is
// the logout path (contract route 6): a login that completes after the user
// logged the server out would re-store the credential they just removed, so
// the pair's flow — and with it its listener — must go first.
//
// Absence is not an error: logout is idempotent by definition, and the pair may
// legitimately have no live flow.
func (r *mcpAuthFlows) CancelPair(agentID, name string) error {
	key := mcpAuthPairKey(agentID, name)
	r.mu.Lock()
	expired := r.sweepLocked(time.Now())
	id := r.pairs[key]
	entry := r.entries[id]
	r.mu.Unlock()
	cancelFlows(expired)
	if entry == nil {
		return nil
	}
	return entry.flow.Cancel()
}

// --- channel wiring --------------------------------------------------------

// mcpAuthFlowRegistry returns the channel's registry, creating it on first use.
//
// It is LAZY for the same reason subagentsCache is: a bare &NativeChannel{}
// literal (every test fixture that does not need MCP auth) must not require
// wiring, and building the registry allocates two maps.
func (n *NativeChannel) mcpAuthFlowRegistry() *mcpAuthFlows {
	n.mcpAuthFlowsOnce.Do(func() {
		n.mcpAuthFlows = newMCPAuthFlows(n.mcpAuthStoreFor)
	})
	return n.mcpAuthFlows
}

// mcpAuthStoreFor resolves an agent's auth store from its OWN roots, or nil
// when the agent is unknown or has no resolvable root. A nil store is not an
// error here: pkg/mcp.StartAuth reports ErrAuthStoreDisabled, which the routes
// map to 503 mcp_auth_disabled — the same verdict the CLI gives when there is
// no lele directory to persist a login in.
//
// It resolves the roots through the same optional capability mcpPathsFor uses
// (mcpPathsSource) but without a ResponseWriter: it is called from the flow
// registry, never while an error response is being formed, and the caller has
// already validated agent_id through mcpPathsFor.
func (n *NativeChannel) mcpAuthStoreFor(agentID string) *mcp.AuthStore {
	source, ok := n.agentLoop.(mcpPathsSource)
	if !ok {
		return nil
	}
	paths, ok := source.MCPPathsFor(agentID)
	if !ok {
		return nil
	}
	return mcp.NewAuthStore(paths.LeleDir)
}
