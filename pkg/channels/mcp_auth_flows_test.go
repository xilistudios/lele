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

type mcpAuthEngineFixture struct {
	f        *mcpFixture
	as       *authtest.Server
	asrv     *httptest.Server
	resource string
	srv      mcp.ServerConfig
}

const (
	mcpAuthRemoteName = "remote-srv"
	mcpAuthStdioName  = "stdio-srv"
	mcpAuthBrokenName = "broken-srv"
)

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

	merged, _ := mcp.Discover(paths)
	srv, ok := merged.Servers[mcpAuthRemoteName]
	if !ok {
		t.Fatalf("fixture did not resolve %q: %+v", mcpAuthRemoteName, merged)
	}
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

func (fx *mcpAuthEngineFixture) registry() *mcpAuthFlows {
	return newMCPAuthFlows(func(agentID string) *mcp.AuthStore {
		if _, known := fx.f.loop.paths[agentID]; !known {
			return nil
		}
		return mcp.NewAuthStore(fx.f.paths.LeleDir)
	})
}

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

func (r *mcpAuthFlows) entryCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *mcpAuthFlows) pairCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pairs)
}

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

func TestMCPAuthFlows_SecondStartReplacesThePairsFlow(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow1, id1, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)

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

	state, ferr := flow1.Status()
	if state != "error" || !errors.Is(ferr, mcp.ErrAuthCanceled) {
		t.Fatalf("replaced flow status = (%q, %v), want (error, ErrAuthCanceled)", state, ferr)
	}
	if !flow1.Done() {
		t.Fatal("replaced flow is not Done(), want a terminal outcome")
	}

	if state, _ := flow2.Status(); state != "pending" {
		t.Fatalf("new flow status = %q, want pending", state)
	}
}

func TestMCPAuthFlows_DifferentPairsDoNotShareFlows(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")
	r := fx.registry()

	_, idA1, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	_, idA2, _ := fx.start(t, r, "agent2", "remote-srv-b")
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

func TestMCPAuthFlows_ExpiredLookupCancelsAndUnindexes(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, deadline := fx.start(t, r, "agent1", mcpAuthRemoteName)
	port := portOf(t, flow.RedirectURI())

	if d := time.Until(deadline); d <= 0 || d > mcpAuthFlowTTL+time.Minute {
		t.Fatalf("ExpiresAt is %v away, want within (0, %v]", d, mcpAuthFlowTTL+time.Minute)
	}

	if _, ok := r.Get("agent1", id); !ok {
		t.Fatal("the flow did not resolve before its deadline")
	}
	if state, _ := flow.Status(); state != "pending" {
		t.Fatalf("flow status = %q before the deadline, want pending", state)
	}

	r.expire(t, id)

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

	_, id2, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	if id2 == id {
		t.Fatalf("the new flow reused the swept id %q", id)
	}
	if r.MarkReloaded("agent1", id) {
		t.Fatal("MarkReloaded on a swept id reported the reload claim")
	}
}

func TestMCPAuthFlows_ExpiredFlowIsReapedByAnyLookup(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")
	r := fx.registry()

	flowA, idA, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
	_, idB, _ := fx.start(t, r, "agent2", "remote-srv-b")
	r.expire(t, idA)

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

func TestMCPAuthFlows_ReloadIsClaimedExactlyOnce(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)

	if r.MarkReloaded("agent1", id) {
		t.Fatal("MarkReloaded on a PENDING flow claimed the reload (the flow has no credential yet)")
	}
	if r.MarkReloaded("agent1", id) {
		t.Fatal("MarkReloaded on a pending flow claimed twice")
	}

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

func TestMCPAuthFlows_ConcurrentReloadClaimHasOneWinner(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()
	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
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
			<-start
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

	if err := r.Cancel("agent1", id); err != nil {
		t.Fatalf("second Cancel() = %v, want nil", err)
	}
	if _, ok := r.Get("agent1", id); !ok {
		t.Fatal("a cancelled-but-unexpired flow id stopped resolving, want it to keep resolving until the TTL")
	}

	if err := r.Cancel("agent1", "abababababababababababababababab"); !errors.Is(err, errMCPAuthFlowNotFound) {
		t.Fatalf("Cancel(unknown) = %v, want errMCPAuthFlowNotFound", err)
	}

	r.expire(t, id)
	if err := r.Cancel("agent1", id); !errors.Is(err, errMCPAuthFlowNotFound) {
		t.Fatalf("Cancel(swept) = %v, want errMCPAuthFlowNotFound", err)
	}
}

func TestMCPAuthFlows_SweepReleasesEveryListener(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1", "agent2")
	r := fx.registry()

	ports := make([]int, 0, 3)
	ids := make([]string, 0, 3)
	for _, pair := range []struct{ agent, name string }{
		{"agent1", mcpAuthRemoteName},
		{"agent2", "remote-srv-b"},
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
	if _, ok := r.Get("agent1", id); !ok {
		t.Fatal("CancelPair removed the entry, want it kept until its deadline")
	}

	if err := r.CancelPair("agent1", mcpAuthRemoteName); err != nil {
		t.Fatalf("second CancelPair() = %v, want nil", err)
	}
	if err := r.CancelPair("agent1", "never-started"); err != nil {
		t.Fatalf("CancelPair(absent) = %v, want nil", err)
	}
}

func TestMCPAuthFlows_CompleteUnknownFlowIsNotFound(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	for _, id := range []string{
		"abababababababababababababababab",
		"",
		"short",
	} {
		if err := r.Complete(context.Background(), "agent1", id, "code", ""); !errors.Is(err, errMCPAuthFlowNotFound) {
			t.Fatalf("Complete(%q) = %v, want errMCPAuthFlowNotFound", id, err)
		}
	}
}

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

	status := mcp.CheckAuth(mcp.NewAuthStore(fx.f.paths.LeleDir), mcpAuthRemoteName, fx.srv)
	if !status.Authenticated {
		t.Fatalf("CheckAuth after Complete = %+v, want authenticated", status)
	}
	if err := r.Complete(context.Background(), "agent1", id, code, state); err != nil {
		t.Fatalf("second Complete() = %v, want nil (terminal flows are idempotent)", err)
	}
}

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
	code, st := mcpAuthAuthorizeCode(t, flow.URL())
	if err := r.Complete(context.Background(), "agent1", id, code, st); err != nil {
		t.Fatalf("retry Complete() = %v, want nil", err)
	}
	if got, _ := flow.Status(); got != "done" {
		t.Fatalf("status after the retry = %q, want done (previous error %v)", got, ferr)
	}
}

func TestMCPAuthFlows_StartFailureKeepsATerminalFlow(t *testing.T) {
	fx := newMCPAuthEngineFixture(t, "agent1")
	r := fx.registry()

	flow, id, _ := fx.start(t, r, "agent1", mcpAuthRemoteName)
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

	if _, _, _, err := r.Start(context.Background(), "ghost-agent", mcpAuthRemoteName, fx.srv); !errors.Is(err, mcp.ErrAuthStoreDisabled) {
		t.Fatalf("Start(ghost) = %v, want ErrAuthStoreDisabled", err)
	}
	if len(asked) != 1 || asked[0] != "ghost-agent" {
		t.Fatalf("storeFor calls = %v, want [ghost-agent]", asked)
	}

	fx.start(t, r, "agent1", mcpAuthRemoteName)
	if len(asked) != 2 || asked[1] != "agent1" {
		t.Fatalf("storeFor calls = %v, want the agent id of every Start", asked)
	}
	if r.storeFor == nil {
		t.Fatal("storeFor was cleared")
	}
}

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

	if err := r.Cancel("agent1", ids[live]); err != nil {
		t.Fatalf("Cancel(survivor) = %v", err)
	}
	assertPortClosed(t, port)
}

func mustGet(t *testing.T, r *mcpAuthFlows, agentID, flowID string) *mcpAuthEntry {
	t.Helper()
	e, ok := r.Get(agentID, flowID)
	if !ok {
		t.Fatalf("Get(%s, %s): not found", agentID, flowID)
	}
	return e
}

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
			return
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("port %d still accepts connections, want the loopback listener released", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

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
