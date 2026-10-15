// Tests for the REST layer's live-flow registry (mcp_auth_flows.go).
//
// The registry is the piece that turns a process-local *mcp.AuthFlow into a
// flow id an HTTP client can hold across requests, so these tests pin the four
// rules that make that safe:
//
//  1. one live flow per (agent, server) — a second Start cancels the first;
//  2. agent scoping — an id only resolves for the agent that created it;
//  3. the lazy sweep — an expired lookup drops AND cancels the flow (closing
//     its loopback listener) without a background goroutine;
//  4. the reload claim — exactly one caller ever observes a completed flow.
//
// The flows are REAL: they come from the real engine (mcp.StartAuth) against
// authtest's in-process authorization server, so a loopback listener is really
// bound and really released. Only the one test that needs a Start FAILURE
// substitutes the seam.

package channels

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/mcp/authtest"
)

// mcpAuthEngineFixture is the in-process authorization server (authtest) plus
// the mcp.json layer that points a remote entry at it. Every flow in this file
// is started against it by the real engine, so the fixture is the only place
// that knows the fake AS exists.
type mcpAuthEngineFixture struct {
	f        *mcpFixture
	as       *authtest.Server
	asrv     *httptest.Server
	resource string           // the resource URL the entry points at
	srv      mcp.ServerConfig // the RESOLVED config of entry "remote-srv"
}

// The three entry names every fixture carries: a usable remote OAuth entry, a
// local (stdio) entry, and a remote entry the merge rejects (command + url).
const (
	mcpAuthRemoteName = "remote-srv"
	mcpAuthStdioName  = "stdio-srv"
	mcpAuthBrokenName = "broken-srv"
)

// newMCPAuthEngineFixture starts the fake AS, writes one global mcp.json and
// wires the SAME roots for every given agent id.
func newMCPAuthEngineFixture(t *testing.T, agentIDs ...string) *mcpAuthEngineFixture {
	t.Helper()

	as := authtest.New(authtest.WithScopes("mcp"))
	asrv := httptest.NewServer(as.Handler())
	t.Cleanup(asrv.Close)
	as.SetIssuer(asrv.URL)

	resourceURL := asrv.URL + authtest.DefaultResourcePath
	raw := fmt.Sprintf(`{
  "mcpServers": {
    %q: {"url": %q, "type": "http", "auth": {"type": "oauth"}},
    %q: {"command": "run-stdio"},
    %q: {"command": "run", "url": %q}
  }
}`, mcpAuthRemoteName, resourceURL, mcpAuthStdioName, mcpAuthBrokenName, resourceURL)

	globalDir := t.TempDir()
	writeMCPJSON(t, filepath.Join(globalDir, "mcp.json"), raw)
	paths := mcp.Paths{LeleDir: globalDir, AgentWorkspace: t.TempDir(), Cwd: t.TempDir()}

	ts := newNativeTestServer(t)
	byAgent := make(map[string]mcp.Paths, len(agentIDs))
	for _, id := range agentIDs {
		byAgent[id] = paths
	}
	loop := &mcpTestLoop{nativeTestAgentLoop: ts.loop, paths: byAgent}
	ts.channel.agentLoop = loop

	// Discover reports per-entry validation failures as a WARNING while still
	// returning every entry it could resolve (the handlers ignore that error for
	// the same reason: a broken entry must not blind the API to the good ones).
	// The fixture keeps one broken entry on purpose, so the routes' "known but
	// unresolved" path is reachable.
	merged, _ := mcp.Discover(paths)
	srv, ok := merged.Servers[mcpAuthRemoteName]
	if !ok {
		t.Fatalf("fixture did not resolve %q: %+v", mcpAuthRemoteName, merged)
	}
	// Pin the two other entries the error matrix depends on, so the fixture
	// cannot silently stop exercising them.
	if _, resolved := merged.Servers[mcpAuthBrokenName]; resolved {
		t.Fatalf("fixture %q resolved, want it excluded as invalid", mcpAuthBrokenName)
	}
	if _, resolved := merged.Servers[mcpAuthStdioName]; !resolved {
		t.Fatalf("fixture %q did not resolve: %+v", mcpAuthStdioName, merged)
	}

	return &mcpAuthEngineFixture{
		f:        &mcpFixture{ts: ts, loop: loop, globalDir: globalDir, paths: paths, globalRaw: raw},
		as:       as,
		asrv:     asrv,
		resource: resourceURL,
		srv:      srv,
	}
}

// registry builds a registry bound to the fixture's own lele dir (one store per
// agent), exactly as the channel's accessor does.
func (fx *mcpAuthEngineFixture) registry() *mcpAuthFlows {
	return newMCPAuthFlows(func(agentID string) *mcp.AuthStore {
		if _, known := fx.f.loop.paths[agentID]; !known {
			return nil
		}
		return mcp.NewAuthStore(fx.f.paths.LeleDir)
	})
}

// start starts a flow for the fixture's remote entry and fails the test when
// the real engine refuses it.
func (fx *mcpAuthEngineFixture) start(t *testing.T, r *mcpAuthFlows, agentID, name string) (*mcp.AuthFlow, string, time.Time) {
	t.Helper()
	flow, id, deadline, err := r.Start(context.Background(), agentID, name, fx.srv)
	if err != nil {
		t.Fatalf("Start(%s, %s): %v", agentID, name, err)
	}
	if flow == nil || id == "" {
		t.Fatalf("Start(%s, %s) = flow %v id %q, want both set", agentID, name, flow, id)
	}
	return flow, id, deadline
}

// entryCount reports how many live flows the registry holds (white box: the
// concurrency tests are about the INDEXES staying consistent).
func (r *mcpAuthFlows) entryCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// pairCount reports how many pairs are indexed.
func (r *mcpAuthFlows) pairCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pairs)
}

// expire forces the entry's deadline into the past: the sweep is lazy, so the
// NEXT lookup is what must notice. Only deadline is touched — never the flow.
func (r *mcpAuthFlows) expire(t *testing.T, flowID string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[flowID]
	if !ok {
		t.Fatalf("expire(%s): no such entry", flowID)
	}
	e.deadline = time.Now().Add(-time.Second)
}

// --- one live flow per pair (contract invariant 1) -------------------------

// TestMCPAuthFlows_SecondStartReplacesThePairsFlow pins invariant 1: the same
// (agent, server) never has two live flows. The second Start returns a NEW id,
// the first id stops resolving, and the first flow is CANCELLED — which is also
// what releases the loopback port the replacement has to bind.
func TestMCPAuthFlows_SecondStartReplacesThePairsFlow(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow1, id1, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)

	// The replacement binds the SAME port: the engine pins the first login's
	// redirect URI in the auth store, so a second start is only possible once
	// the first listener is closed. A successful second Start is therefore the
	// proof that the previous flow was released, and the URI equality below
	// proves the test would have hit EADDRINUSE without that release.
	flow2, id2, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	if flow2.RedirectURI() != flow1.RedirectURI() {
		t.Fatalf("second start bound %q, want the pinned %q (the test would then not exercise the port conflict)",
			flow2.RedirectURI(), flow1.RedirectURI())
	}
	if id2 == id1 {
		t.Fatalf("second Start reused flow id %q, want a fresh id (the old flow is cancelled)", id1)
	}
	if len(id2) != mcpAuthFlowIDLen {
		t.Fatalf("flow id %q has length %d, want %d", id2, len(id2), mcpAuthFlowIDLen)
	}

	if _, ok := r.Get("agent1", id1); ok {
		t.Fatal("the replaced flow id still resolves, want it dropped")
	}
	if _, ok := r.Get("agent1", id2); !ok {
		t.Fatal("the new flow id does not resolve")
	}
	if got := r.entryCount(); got != 1 {
		t.Fatalf("registry holds %d flows for one pair, want 1", got)
	}

	// The replaced flow is terminal with the cancel sentinel...
	state, ferr := flow1.Status()
	if state != "error" || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Fatalf("replaced flow status = (%q, %v), want (error, ErrAuthCanceled)", state, ferr)
	}
	if !flow1.Done() {
		t.Fatal("replaced flow is not Done(), want a terminal outcome")
	}

	// The new flow is the pending one.
	if state, _ := flow2.Status(); state != "pending" {
		t.Fatalf("new flow status = %q, want pending", state)
	}
}

// TestMCPAuthFlows_DifferentPairsDoNotShareFlows: the pair key is
// (agent, server) — two agents, or two servers of one agent, coexist.
//
// Each pair uses its own server NAME: the auth store (and with it the pinned
// loopback redirect URI) is keyed by name inside ONE lele dir, so two agents
// logging into the same name would share — and contend for — the same port.
// That is the engine's storage rule, not a property of this registry, and the
// test does not depend on it.
func TestMCPAuthFlows_DifferentPairsDoNotShareFlows(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")
	r := fx.registry()

	_, idA1, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	_, idA2, _ := fx.start(t, r, "agent2", "remote-srv-b")
	// A second SERVER for the same agent, proving the key is not just the agent
	// (the registry never reads the config; it is the caller's resolved entry).
	_, idB1, _ := fx.start(t, r, "agent1", "remote-srv-alias")

	if len(map[string]string{idA1: "", idA2: "", idB1: ""}) != 3 {
		t.Fatalf("ids %q %q %q must be distinct", idA1, idA2, idB1)
	}
	if got := r.entryCount(); got != 3 {
		t.Fatalf("registry holds %d flows, want 3", got)
	}
	if got := r.pairCount(); got != 3 {
		t.Fatalf("registry holds %d pair keys, want 3", got)
	}
}

// --- agent scoping (contract invariant 4) ---------------------------------

// TestMCPAuthFlows_LookupIsAgentScoped: a flow id resolves only for its own
// agent. The owner still resolves it afterwards (a foreign lookup must not
// consume or cancel the entry).
func TestMCPAuthFlows_LookupIsAgentScoped(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)

	if _, ok := r.Get("agent2", id); ok {
		t.Fatal("agent2 resolved agent1's flow id, want not found")
	}
	if err := r.Cancel("agent2", id); !errors.Is(err, errMCPAuthFlowNotFound) {
		t.Fatalf("Cancel(agent2) = %v, want errMCPAuthFlowNotFound", err)
	}
	if r.MarkReloaded("agent2", id) {
		t.Fatal("agent2 claimed the reload of agent1's flow")
	}
	if err := r.Complete(context.Background(), "agent2", id, "code", ""); !errors.Is(err, errMCPAuthFlowNotFound) {
		t.Fatalf("Complete(agent2) = %v, want errMCPAuthFlowNotFound", err)
	}

	// The owner is unaffected: still resolvable, still pending.
	entry, ok := r.Get("agent1", id)
	if !ok {
		t.Fatal("the owner lost its flow after foreign lookups")
	}
	if entry.flow != flow {
		t.Fatal("Get returned a different flow than Start")
	}
	if state, _ := flow.Status(); state != "pending" {
		t.Fatalf("flow status = %q, want pending (no foreign lookup may touch it)", state)
	}
}

// --- the lazy sweep (contract invariant 3) --------------------------------

// TestMCPAuthFlows_ExpiredLookupCancelsAndUnindexes: the deadline is what makes
// a forgotten flow release its loopback port. Nothing sweeps in the background,
// so the FIRST lookup after the deadline must drop the entry, cancel the flow
// (closing the listener) and free the pair — after which the same pair can be
// started again.
func TestMCPAuthFlows_ExpiredLookupCancelsAndUnindexes(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, deadline := fx.start(t, r, "agent1", mcpAuthRemoteName)
	port := portOf(t, flow.RedirectURI())

	// The reported deadline is the registry's own TTL, not a placeholder.
	if d := time.Until(deadline); d <= 0 || d > mcpAuthFlowTTL+time.Minute {
		t.Fatalf("ExpiresAt is %v away, want within (0, %v]", d, mcpAuthFlowTTL+time.Minute)
	}

	// Nothing expires early: a lookup well before the deadline keeps the flow.
	if _, ok := r.Get("agent1", id); !ok {
		t.Fatal("the flow did not resolve before its deadline")
	}
	if state, _ := flow.Status(); state != "pending" {
		t.Fatalf("flow status = %q before the deadline, want pending", state)
	}

	r.expire(t, id)

	// Every entry point sweeps: use Get here, MarkReloaded below.
	if _, ok := r.Get("agent1", id); ok {
		t.Fatal("the expired flow id still resolves, want it dropped")
	}
	if state, ferr := flow.Status(); state != "error" || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Fatalf("expired flow status = (%q, %v), want (error, ErrAuthCanceled)", state, ferr)
	}
	assertPortClosed(t, port)
	if got := r.entryCount(); got != 0 {
		t.Fatalf("registry holds %d entries after the sweep, want 0", got)
	}
	if got := r.pairCount(); got != 0 {
		t.Fatalf("registry holds %d pair keys after the sweep, want 0", got)
	}

	// A swept pair is startable again, and it does not resurrect the old id.
	_, id2, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	if id2 == id {
		t.Fatalf("the new flow reused the swept id %q", id)
	}
	if r.MarkReloaded("agent1", id) {
		t.Fatal("MarkReloaded on a swept id reported the reload claim")
	}
}

// TestMCPAuthFlows_ExpiredFlowDoesNotLeakIntoAnotherPair: the sweep is global —
// a lookup for pair B must reap pair A's expired flow too (otherwise a pair
// nobody asks about again would pin its listener for the life of the process).
func TestMCPAuthFlows_ExpiredFlowIsReapedByAnyLookup(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")
	r := fx.registry()

	flowA, idA, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	_, idB, _ := fx.start(t, r, "agent2", "remote-srv-b") // its own name: its own pinned port
	r.expire(t, idA)

	// Look up B: A's flow must be reaped as a side effect.
	if _, ok := r.Get("agent2", idB); !ok {
		t.Fatal("agent2's live flow did not resolve")
	}
	if state, ferr := flowA.Status(); state != "error" || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Fatalf("agent1's expired flow = (%q, %v), want it cancelled by agent2's lookup", state, ferr)
	}
	if got := r.entryCount(); got != 1 {
		t.Fatalf("registry holds %d entries, want 1 (only agent2's)", got)
	}
}

// --- the reload claim (contract invariant 2) -------------------------------

// TestMCPAuthFlows_ReloadIsClaimedExactlyOnce: the manager must be re-wired
// once per completed flow, no matter how many times a client polls — and never
// for a flow that has not produced a credential.
func TestMCPAuthFlows_ReloadIsClaimedExactlyOnce(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)

	// Pending: nothing to reload yet, however often it is asked.
	if r.MarkReloaded("agent1", id) {
		t.Fatal("MarkReloaded on a PENDING flow claimed the reload (the flow has no credential yet)")
	}
	if r.MarkReloaded("agent1", id) {
		t.Fatal("MarkReloaded on a pending flow claimed twice")
	}

	// Complete it: now the claim is owed exactly once.
	code, state := mcpAuthAuthorizeCode(t, flow.URL())
	if err := r.Complete(context.Background(), "agent1", id, code, state); err != nil {
		t.Fatalf("Complete() = %v", err)
	}
	if st, _ := flow.Status(); st != "done" {
		t.Fatalf("status after Complete = %q, want done", st)
	}
	if !r.MarkReloaded("agent1", id) {
		t.Fatal("the first claim after the flow completed was refused")
	}
	for i := 0; i < 3; i++ {
		if r.MarkReloaded("agent1", id) {
			t.Fatalf("poll %d claimed the reload again", i)
		}
	}
}

// TestMCPAuthFlows_FailedFlowNeverClaimsReload: invariant 2 fires the manager's
// re-wiring pass on the pending → done transition ONLY, so a cancelled flow —
// which is equally terminal — must never claim it.
func TestMCPAuthFlows_FailedFlowNeverClaimsReload(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	if err := r.Cancel("agent1", id); err != nil {
		t.Fatalf("Cancel() = %v", err)
	}
	if !flow.Done() {
		t.Fatal("cancelled flow is not Done()")
	}
	if r.MarkReloaded("agent1", id) {
		t.Fatal("a CANCELLED flow claimed the reload, want the reload reserved for pending → done")
	}
}

// TestMCPAuthFlows_ConcurrentReloadClaimHasOneWinner: the claim is the reason
// the registry has a mutex — N concurrent polls of one completed flow must
// reload once, not N times.
func TestMCPAuthFlows_ConcurrentReloadClaimHasOneWinner(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()
	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	// The flow must be DONE before the race: that is the only state in which a
	// claim is honored at all.
	code, state := mcpAuthAuthorizeCode(t, flow.URL())
	if err := r.Complete(context.Background(), "agent1", id, code, state); err != nil {
		t.Fatalf("Complete() = %v", err)
	}

	const pollers = 32
	var claims atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < pollers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // maximize overlap
			if r.MarkReloaded("agent1", id) {
				claims.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := claims.Load(); got != 1 {
		t.Fatalf("%d of %d concurrent polls claimed the reload, want exactly 1", got, pollers)
	}
}

// --- cancel (contract invariant 7) ----------------------------------------

// TestMCPAuthFlows_CancelIsBestEffortIdempotent: 204 semantics — cancelling a
// live flow works, cancelling an already-terminal one works again (while the id
// is still in the registry), and only an id that does not resolve reports
// not-found.
func TestMCPAuthFlows_CancelIsBestEffortIdempotent(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	port := portOf(t, flow.RedirectURI())

	if err := r.Cancel("agent1", id); err != nil {
		t.Fatalf("Cancel() = %v, want nil", err)
	}
	if state, ferr := flow.Status(); state != "error" || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Fatalf("status after Cancel = (%q, %v), want (error, ErrAuthCanceled)", state, ferr)
	}
	assertPortClosed(t, port)

	// Idempotent: the entry is still resolvable until its deadline.
	if err := r.Cancel("agent1", id); err != nil {
		t.Fatalf("second Cancel() = %v, want nil", err)
	}
	if _, ok := r.Get("agent1", id); !ok {
		t.Fatal("a cancelled-but-unexpired flow id stopped resolving, want it to keep resolving until the TTL")
	}

	// Unknown shape-valid id → not found.
	if err := r.Cancel("agent1", "abababababababababababababababab"); !errors.Is(err, errMCPAuthFlowNotFound) {
		t.Fatalf("Cancel(unknown) = %v, want errMCPAuthFlowNotFound", err)
	}

	// After the sweep, the cancelled flow's id is gone for good.
	r.expire(t, id)
	if err := r.Cancel("agent1", id); !errors.Is(err, errMCPAuthFlowNotFound) {
		t.Fatalf("Cancel(swept) = %v, want errMCPAuthFlowNotFound", err)
	}
}

// TestMCPAuthFlows_SweepReleasesEveryListener: the port is the resource being
// leaked, so a bulk expiry must release all of them.
func TestMCPAuthFlows_SweepReleasesEveryListener(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")
	r := fx.registry()

	ports := make([]int, 0, 3)
	ids := make([]string, 0, 3)
	for _, pair := range []struct{ agent, name string }{
		{"agent1", mcpAuthRemoteName},
		{"agent2", "remote-srv-b"}, // its own name: its own pinned port
	} {
		flow, id, _ := fx.start(t, r, pair.agent, pair.name)
		ports = append(ports, portOf(t, flow.RedirectURI()))
		ids = append(ids, id)
	}
	if ports[0] == ports[1] {
		t.Fatalf("two flows bound the same port %d; the fixture cannot observe a release", ports[0])
	}
	for _, id := range ids {
		r.expire(t, id)
	}
	// One lookup reaps everything that expired.
	if _, ok := r.Get("agent1", ids[0]); ok {
		t.Fatal("expired id still resolves")
	}
	for i, port := range ports {
		assertPortClosed(t, port)
		_ = i
	}
	if got := r.pairCount(); got != 0 {
		t.Fatalf("%d pair keys left after the sweep, want 0", got)
	}
}

// TestMCPAuthFlows_CancelPairStopsTheLogoutRace: logout cancels the pair's
// flow (a login that completed after the logout would re-store the credential
// the user just dropped). Absence is not an error.
func TestMCPAuthFlows_CancelPairStopsTheLogoutRace(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	port := portOf(t, flow.RedirectURI())

	if err := r.CancelPair("agent1", mcpAuthRemoteName); err != nil {
		t.Fatalf("CancelPair() = %v, want nil", err)
	}
	if state, ferr := flow.Status(); state != "error" || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Fatalf("status after CancelPair = (%q, %v), want (error, ErrAuthCanceled)", state, ferr)
	}
	assertPortClosed(t, port)
	// The entry stays resolvable (logout does not delete state the client may
	// still be polling), but the flow behind it is dead.
	if _, ok := r.Get("agent1", id); !ok {
		t.Fatal("CancelPair removed the entry, want it kept until its deadline")
	}

	// Idempotent, and a pair with no flow is not an error.
	if err := r.CancelPair("agent1", mcpAuthRemoteName); err != nil {
		t.Fatalf("second CancelPair() = %v, want nil", err)
	}
	if err := r.CancelPair("agent1", "never-started"); err != nil {
		t.Fatalf("CancelPair(absent) = %v, want nil", err)
	}
}

// --- completion -----------------------------------------------------------

// TestMCPAuthFlows_CompleteUnknownFlowIsNotFound: the complete route's 404 path
// (unknown, expired or foreign id) never reaches the engine.
func TestMCPAuthFlows_CompleteUnknownFlowIsNotFound(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	for _, id := range []string{
		"abababababababababababababababab", // well-formed, unknown
		"",                                 // empty
		"short",                            // wrong shape (the handler refuses it earlier)
	} {
		if err := r.Complete(context.Background(), "agent1", id, "code", ""); !errors.Is(err, errMCPAuthFlowNotFound) {
			t.Fatalf("Complete(%q) = %v, want errMCPAuthFlowNotFound", id, err)
		}
	}
}

// TestMCPAuthFlows_CompleteWithTheAuthorizationServersCode drives the one path
// that must really work: the code the fake AS issues, submitted out of band,
// completes the flow, stores the credential and leaves the flow resolvable as
// "done" — and the engine then reports the server as authenticated.
//
// It is the registry-level half of the REST complete test (the REST layer only
// adds routing, DTO projection and the reload claim on top of it).
func TestMCPAuthFlows_CompleteStoresTheCredential(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	code, state := mcpAuthAuthorizeCode(t, flow.URL())

	if err := r.Complete(context.Background(), "agent1", id, code, state); err != nil {
		t.Fatalf("Complete() = %v, want nil", err)
	}
	entry, ok := r.Get("agent1", id)
	if !ok {
		t.Fatal("the completed flow stopped resolving, want it kept until its deadline")
	}
	if got, _ := entry.flow.Status(); got != "done" {
		t.Fatalf("status after Complete = %q, want done", got)
	}
	if !entry.flow.Done() {
		t.Fatal("completed flow is not Done()")
	}

	// The credential is in the REAL store (the engine's own TokenStore), and
	// CheckAuth — the projection the status DTO uses — sees it.
	status := mcp.CheckAuth(mcp.NewAuthStore(fx.f.paths.LeleDir), mcpAuthRemoteName, fx.srv)
	if !status.Authenticated {
		t.Fatalf("CheckAuth after Complete = %+v, want authenticated", status)
	}
	// A second Complete on a terminal flow is idempotent (nil), which is what
	// makes a retried request harmless.
	if err := r.Complete(context.Background(), "agent1", id, code, state); err != nil {
		t.Fatalf("second Complete() = %v, want nil (terminal flows are idempotent)", err)
	}
}

// TestMCPAuthFlows_FailedCompleteLeavesTheFlowOpen: a wrong code is retryable —
// the flow stays pending-capable ("error", not Done), which is the state the
// WebUI's paste box depends on.
func TestMCPAuthFlows_FailedCompleteLeavesTheFlowOpen(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)

	if err := r.Complete(context.Background(), "agent1", id, "not-a-real-code", ""); err == nil {
		t.Fatal("Complete() with a bogus code = nil, want a token-exchange failure")
	}
	state, ferr := flow.Status()
	if state != "error" {
		t.Fatalf("status after a failed exchange = %q, want error", state)
	}
	if flow.Done() {
		t.Fatal("a failed token exchange must leave the flow open and retryable")
	}
	// Retry with the real code: the SAME flow recovers.
	code, st := mcpAuthAuthorizeCode(t, flow.URL())
	if err := r.Complete(context.Background(), "agent1", id, code, st); err != nil {
		t.Fatalf("retry Complete() = %v, want nil", err)
	}
	if got, _ := flow.Status(); got != "done" {
		t.Fatalf("status after the retry = %q, want done (previous error %v)", got, ferr)
	}
}

// --- the seam, and the shape of what Start returns ------------------------

// TestMCPAuthFlows_StartFailureKeepsATerminalFlow: a failed Start (dead server,
// disabled store) must not tear down what the pair already had — as long as
// that flow holds no loopback port (a terminal flow has released it). The seam
// is substituted so the failure is deterministic.
func TestMCPAuthFlows_StartFailureKeepsATerminalFlow(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	// Cancel it FIRST: terminal, listener released, still resolvable.
	if err := r.Cancel("agent1", id); err != nil {
		t.Fatalf("Cancel() = %v", err)
	}
	if !flow.Done() {
		t.Fatal("Cancel() left the flow live")
	}

	wantErr := errors.New("seam: authorization server unreachable")
	var calls atomic.Int32
	restore := startMCPAuthFlow
	startMCPAuthFlow = func(ctx context.Context, name string, srv mcp.ServerConfig, store *mcp.AuthStore, opts mcp.AuthOptions) (*mcp.AuthFlow, error) {
		calls.Add(1)
		// The seam receives the config the REST layer resolved and the store of
		// the CALLING agent — the two things the registry owes the engine.
		if srv.URL != fx.srv.URL {
			t.Errorf("seam got url %q, want the resolved %q", srv.URL, fx.srv.URL)
		}
		if store == nil {
			t.Error("seam got a nil store for a known agent")
		}
		return nil, wantErr
	}
	t.Cleanup(func() { startMCPAuthFlow = restore })

	_, newID, _, err := r.Start(context.Background(), "agent1", mcpAuthRemoteName, fx.srv)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start() = %v, want the seam error", err)
	}
	if newID != "" {
		t.Fatalf("failed Start returned id %q, want \"\"", newID)
	}
	if calls.Load() != 1 {
		t.Fatalf("seam called %d times, want 1", calls.Load())
	}
	if entry, ok := r.Get("agent1", id); !ok || entry.flow != flow {
		t.Fatal("the pair's terminal flow was dropped by a failed Start")
	}
}

// TestMCPAuthFlows_StartFailureAfterALiveFlowLeavesThePairEmpty documents the
// deliberate consequence of the port rule: a flow that is still LIVE has to
// release its loopback port BEFORE the new flow can bind it, so a Start that
// then fails leaves the pair empty. The alternative — binding first — is not
// available: the store pins the port, so the new flow could never bind while
// the old listener is open.
func TestMCPAuthFlows_StartFailureAfterALiveFlowLeavesThePairEmpty(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	port := portOf(t, flow.RedirectURI())

	restore := startMCPAuthFlow
	startMCPAuthFlow = func(context.Context, string, mcp.ServerConfig, *mcp.AuthStore, mcp.AuthOptions) (*mcp.AuthFlow, error) {
		return nil, errors.New("seam: authorization server unreachable")
	}
	t.Cleanup(func() { startMCPAuthFlow = restore })

	if _, _, _, err := r.Start(context.Background(), "agent1", mcpAuthRemoteName, fx.srv); err == nil {
		t.Fatal("Start() = nil, want the seam error")
	}

	// The old flow was released to free the port, and no new one was indexed.
	if state, ferr := flow.Status(); state != "error" || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Fatalf("previous flow = (%q, %v), want it cancelled to free the port", state, ferr)
	}
	assertPortClosed(t, port)
	if _, ok := r.Get("agent1", id); ok {
		t.Fatal("the released flow id still resolves")
	}
	if got := r.entryCount(); got != 0 {
		t.Fatalf("registry holds %d entries, want 0", got)
	}
}

// TestMCPAuthFlows_StoreFactoryIsPerAgentAndNilWhenUnknown: the registry never
// guesses a store, and an unknown agent (no roots) yields nil, which the engine
// reports as a disabled store.
func TestMCPAuthFlows_StoreFactoryIsPerAgentAndNilWhenUnknown(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	var asked []string
	r.storeFor = func(agentID string) *mcp.AuthStore {
		asked = append(asked, agentID)
		if agentID != "agent1" {
			return nil
		}
		return mcp.NewAuthStore(fx.f.paths.LeleDir)
	}

	// A nil store is what the engine sees as ErrAuthStoreDisabled — the REST
	// layer's 503. The registry must forward it, not invent a path.
	if _, _, _, err := r.Start(context.Background(), "ghost-agent", mcpAuthRemoteName, fx.srv); !errors.Is(err, mcp.ErrAuthStoreDisabled) {
		t.Fatalf("Start(ghost) = %v, want ErrAuthStoreDisabled", err)
	}
	if len(asked) != 1 || asked[0] != "ghost-agent" {
		t.Fatalf("storeFor calls = %v, want [ghost-agent]", asked)
	}

	// And the working agent still starts.
	fx.start(t, r, "agent1", mcpAuthRemoteName)
	if len(asked) != 2 || asked[1] != "agent1" {
		t.Fatalf("storeFor calls = %v, want the agent id of every Start", asked)
	}
	if r.storeFor == nil {
		t.Fatal("storeFor was cleared")
	}
}

// TestMCPAuthFlows_ConcurrentStartLeavesOneLiveFlow: N concurrent Starts for
// one pair must leave exactly ONE live flow, cancel the rest (or their loopback
// listeners are leaked), and — the harder half — SUCCEED for every caller.
//
// Every one of these starts wants the pair's single PINNED port (they all ask
// for the same redirect URI, asserted below), so this is the test that pins the
// pair's serialization: without it, the losers of the bind race fail with
// EADDRINUSE and a client that fired twice gets a spurious 502. Run under -race
// it also covers the two indexes being mutated from many goroutines.
func TestMCPAuthFlows_ConcurrentStartLeavesOneLiveFlow(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	const starters = 8
	var wg sync.WaitGroup
	ids := make([]string, starters)
	flows := make([]*mcp.AuthFlow, starters)
	errs := make([]error, starters)
	start := make(chan struct{})
	for i := 0; i < starters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			flows[i], ids[i], _, errs[i] = r.Start(context.Background(), "agent1", mcpAuthRemoteName, fx.srv)
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[string]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Start %d: %v", i, err)
		}
		if seen[ids[i]] {
			t.Fatalf("two Starts returned the same flow id %q", ids[i])
		}
		seen[ids[i]] = true
		if flows[i].RedirectURI() != flows[0].RedirectURI() {
			t.Fatalf("start %d got redirect %q, want the pinned %q — the test would then not exercise the port contention",
				i, flows[i].RedirectURI(), flows[0].RedirectURI())
		}
	}

	if got := r.entryCount(); got != 1 {
		t.Fatalf("registry holds %d flows after %d concurrent Starts, want 1", got, starters)
	}
	if got := r.pairCount(); got != 1 {
		t.Fatalf("registry holds %d pair keys, want 1", got)
	}

	// Exactly one id survives, and it is one of the returned ones.
	live := -1
	for i, id := range ids {
		if _, ok := r.Get("agent1", id); ok {
			if live >= 0 {
				t.Fatalf("flow ids %q and %q both resolve, want one live flow per pair", ids[live], id)
			}
			live = i
		}
	}
	if live < 0 {
		t.Fatal("none of the flow ids resolves, want the last started flow live")
	}
	if mustGet(t, r, "agent1", ids[live]).flow != flows[live] {
		t.Fatal("the surviving id resolves to a flow that is not the one Start returned")
	}

	// The losers are terminal and cancelled — that is what frees the port for
	// the winner — and the winner still holds it.
	for i, flow := range flows {
		if i == live {
			continue
		}
		if !flow.Done() {
			t.Fatalf("replaced flow %d is not Done(), want the pair's losers cancelled", i)
		}
	}
	if state, _ := flows[live].Status(); state != "pending" {
		t.Fatalf("surviving flow status = %q, want pending", state)
	}
	port := portOf(t, flows[live].RedirectURI())
	assertPortOpen(t, port)

	// And the pair's ONE listener is released with its flow.
	if err := r.Cancel("agent1", ids[live]); err != nil {
		t.Fatalf("Cancel(survivor) = %v", err)
	}
	assertPortClosed(t, port)
}

// mustGet resolves a flow id or fails the test.
func mustGet(t *testing.T, r *mcpAuthFlows, agentID, flowID string) *mcpAuthEntry {
	t.Helper()
	e, ok := r.Get(agentID, flowID)
	if !ok {
		t.Fatalf("Get(%s, %s): not found", agentID, flowID)
	}
	return e
}

// mcpAuthAuthorizeCode plays the browser's half of the flow: GET the
// authorization URL, stop at the AS's 302 and return the code and state it
// carries. The redirect itself is NOT followed (the REST layer's manual flow
// never receives it — the user pastes the code instead).
func mcpAuthAuthorizeCode(t *testing.T, authURL string) (code, state string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(authURL)
	if err != nil {
		t.Fatalf("GET authorization URL: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorization status = %d, want 302", resp.StatusCode)
	}
	loc, err := resp.Request.URL.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse authorization redirect: %v", err)
	}
	q := loc.Query()
	if e := q.Get("error"); e != "" {
		t.Fatalf("authorization server refused: %s", e)
	}
	if q.Get("code") == "" {
		t.Fatalf("authorization redirect carried no code: %s", loc)
	}
	if q.Get("state") == "" {
		t.Fatalf("authorization redirect carried no state: %s", loc)
	}
	return q.Get("code"), q.Get("state")
}

// portOf extracts the port from a loopback redirect URI, so a test can prove
// the listener behind it was released.
func portOf(t *testing.T, redirectURI string) int {
	t.Helper()
	u, err := url.Parse(redirectURI)
	if err != nil {
		t.Fatalf("parse redirect uri %q: %v", redirectURI, err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port == 0 {
		t.Fatalf("redirect uri %q carries no usable port (err=%v)", redirectURI, err)
	}
	return port
}

// assertPortClosed fails when the port still accepts connections. It is the
// only way to observe that a cancelled/expired flow really released its
// loopback listener rather than leaving a half-dead server behind.
// assertPortOpen is the positive twin of assertPortClosed: the loopback
// listener behind a live flow must really accept connections.
func assertPortOpen(t *testing.T, port int) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if err != nil {
		t.Fatalf("port %d refuses connections, want the live flow's loopback listener: %v", port, err)
	}
	_ = conn.Close()
}

func assertPortClosed(t *testing.T, port int) {
	t.Helper()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err != nil {
			return // refused: the listener is gone
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("port %d still accepts connections, want the loopback listener released", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMCPAuthFlows_NoTokenBeforeCompletion is a negative guard on the
// registry's "no secret stored" rule: no token exists anywhere until a code is
// exchanged, so a started-but-uncompleted flow must leave no access token in
// the store — only the client registration the flow needs to persist.
func TestMCPAuthFlows_NoTokenBeforeCompletion(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	_, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	if _, ok := r.Get("agent1", id); !ok {
		t.Fatal("flow did not resolve")
	}

	if mcp.CheckAuth(mcp.NewAuthStore(fx.f.paths.LeleDir), mcpAuthRemoteName, fx.srv).Authenticated {
		t.Fatal("a started-but-uncompleted flow reports an authenticated server (a token was stored too early)")
	}
	rec, ok := mcp.NewAuthStore(fx.f.paths.LeleDir).Record(mcpAuthRemoteName)
	if !ok {
		t.Fatal("no registration record was persisted, so the flow could not have completed")
	}
	if rec.Token != nil {
		t.Fatal("the store holds a token before any code was exchanged")
	}

	// The store file itself is the secret: it must not be word-readable, and
	// no token may be findable in it.
	path := filepath.Join(fx.f.paths.LeleDir, "mcp-auth.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("%s mode = %o, want 600", path, perm)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if bytes.Contains(data, []byte("access_token")) {
		t.Fatalf("a token already exists before completion: %s", data)
	}
}

// TestMCPAuthStoreFor_NilWhenTheAgentIsUnresolvable pins the channel's store
// factory directly (finding 3 of the adversarial review). It is the thing that
// decides whether mcp.StartAuth sees a usable store at all: a nil store and a
// DISABLED store both become ErrAuthStoreDisabled → 503 mcp_auth_disabled, so
// neither may be a guess.
//
//   - no agent loop at all (a bare &NativeChannel{}, i.e. every channel fake
//     that does not opt into the optional mcpPathsSource capability): nil, never
//     a process-relative path;
//   - the capability is present but does not know the agent: nil;
//   - a known agent: a store rooted in THAT agent's lele dir and Enabled;
//   - a known agent with an EMPTY lele dir: a store that is present but
//     DISABLED — the reachable store-less path of the start route (see
//     TestMCPAuthStart_StoreDisabledIs503 in rest_mcp_auth_test.go).
//
// Neither nil return is reachable THROUGH the routes: mcpPathsFor refuses the
// same two conditions one step earlier (500 mcp_unavailable, 404
// agent_not_found). This is the direct test.
//
// Contract row pinned: mcp_auth_disabled/503 (the disabled-store cell).
func TestMCPAuthStoreFor_NilWhenTheAgentIsUnresolvable(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")

	if store := (&NativeChannel{}).mcpAuthStoreFor("agent1"); store != nil {
		t.Fatalf("store for a channel without an agent loop = %v, want nil", store)
	}
	if store := fx.f.ts.channel.mcpAuthStoreFor("ghost"); store != nil {
		t.Fatalf("store for an unknown agent = %v, want nil", store)
	}

	store := fx.f.ts.channel.mcpAuthStoreFor("agent1")
	if store == nil || !store.Enabled() {
		t.Fatalf("store for a known agent = %v (Enabled=%v), want a store rooted in its lele dir", store, store == nil)
	}

	mcpAuthAddRootlessAgent(t, fx, "rootless")
	store = fx.f.ts.channel.mcpAuthStoreFor("rootless")
	if store == nil {
		t.Fatal("store for a rootless agent = nil, want a store that is present but disabled")
	}
	if store.Enabled() {
		t.Fatal("a store built from an empty lele dir reports Enabled, want disabled (it must never create a process-relative file)")
	}
}
