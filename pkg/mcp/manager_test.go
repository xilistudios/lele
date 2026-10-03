package mcp

import (
	"bytes"
	"context"
	"errors"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tools"
)

// --- shared test doubles ----------------------------------------------------

// fakeConn is an in-memory ClientConn whose failures can be scripted: the
// *Errs slices are consumed in order, after which calls succeed.
type fakeConn struct {
	mu sync.Mutex

	listTools  []mcp.Tool
	listErrs   []error
	callResult *mcp.CallToolResult
	callErrs   []error
	lastName   string
	lastArgs   map[string]interface{}

	listCalls int
	callCalls int
	closes    int
}

func (f *fakeConn) ListTools(context.Context) ([]mcp.Tool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if len(f.listErrs) > 0 {
		err := f.listErrs[0]
		f.listErrs = f.listErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	return f.listTools, nil
}

func (f *fakeConn) CallTool(_ context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCalls++
	f.lastName = name
	f.lastArgs = args
	if len(f.callErrs) > 0 {
		err := f.callErrs[0]
		f.callErrs = f.callErrs[1:]
		if err != nil {
			return nil, err
		}
	}
	return f.callResult, nil
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

// stats snapshots the counters (safe against concurrent test goroutines).
func (f *fakeConn) stats() (listCalls, callCalls, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls, f.callCalls, f.closes
}

// fakeDialer implements Dialer: scripted per-dial failures first, then
// success with a fresh fakeConn each time. setup(n, conn) configures the
// n-th connection (n starts at 1).
type fakeDialer struct {
	mu    sync.Mutex
	errs  []error
	setup func(n int, conn *fakeConn)
	delay time.Duration

	dials int
	cfgs  []ServerConfig
	conns []*fakeConn
}

func (d *fakeDialer) Dial(ctx context.Context, cfg ServerConfig) (ClientConn, error) {
	if d.delay > 0 {
		select {
		case <-time.After(d.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	d.cfgs = append(d.cfgs, cfg)
	if len(d.errs) > 0 {
		err := d.errs[0]
		d.errs = d.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	conn := &fakeConn{}
	if d.setup != nil {
		d.setup(d.dials, conn)
	}
	d.conns = append(d.conns, conn)
	return conn, nil
}

func (d *fakeDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

func (d *fakeDialer) connAt(n int) *fakeConn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conns[n]
}

// cfgAt returns the ServerConfig the n-th Dial received (asserts that a
// config edit actually reaches the dialer).
func (d *fakeDialer) cfgAt(n int) ServerConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfgs[n]
}

// mcpFixture writes a global-layer mcp.json with the given server bodies and
// returns Paths rooted at the temporary directory.
func mcpFixture(t *testing.T, serversJSON string) Paths {
	t.Helper()
	dir := t.TempDir()
	writeFileAt(t, dir+"/mcp.json", serversJSON)
	return Paths{LeleDir: dir}
}

// basicFixture returns a Paths with a single active stdio server "srv".
func basicFixture(t *testing.T) Paths {
	t.Helper()
	return mcpFixture(t, `{"mcpServers": {"srv": {"command": "srv-bin", "description": "a server"}}}`)
}

// remoteTool builds a simple remote tool declaration.
func remoteTool(name string) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: "desc of " + name,
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]interface{}{
				"q": map[string]interface{}{"type": "string"},
			},
		},
	}
}

// transportFail returns the mcp-go error shape for a dead connection.
func transportFail(reason string) error {
	return transport.NewError(errors.New(reason))
}

// --- T5: Servers() ----------------------------------------------------------

// TestManagerServersSortsAndExcludesDisabled pins the system-prompt source.
func TestManagerServersSortsAndExcludesDisabled(t *testing.T) {
	paths := mcpFixture(t, `{
  "mcpServers": {
    "zeta":  {"command": "a", "description": "Z desc"},
    "alpha": {"command": "b", "description": "A desc"},
    "off":   {"command": "c", "disabled": true}
  }
}`)
	mgr := NewManager(paths, &fakeDialer{})
	infos := mgr.Servers()

	if len(infos) != 2 {
		t.Fatalf("Servers() = %+v, want 2 entries (disabled excluded)", infos)
	}
	if infos[0].Name != "alpha" || infos[1].Name != "zeta" {
		t.Errorf("names not sorted: %q, %q", infos[0].Name, infos[1].Name)
	}
	if infos[0].Description != "A desc" {
		t.Errorf("description = %q", infos[0].Description)
	}
	if infos[0].Layer != LayerGlobal {
		t.Errorf("layer = %q, want %q", infos[0].Layer, LayerGlobal)
	}
	for _, info := range infos {
		if info.Name == "off" {
			t.Error("the disabled server must be excluded from Servers()")
		}
	}
}

// --- T5: LoadServer ---------------------------------------------------------

// TestLoadServerIdempotent: two sequential loads dial once and list once.
func TestLoadServerIdempotent(t *testing.T) {
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listTools = []mcp.Tool{remoteTool("read"), remoteTool("write")}
	}}
	mgr := NewManager(basicFixture(t), d)

	first, err := mgr.LoadServer(context.Background(), "srv")
	if err != nil {
		t.Fatalf("LoadServer: %v", err)
	}
	second, err := mgr.LoadServer(context.Background(), "srv")
	if err != nil {
		t.Fatalf("LoadServer again: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("specs differ between calls:\n%+v\n%+v", first, second)
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1", d.dialCount())
	}
	if listCalls, _, _ := d.connAt(0).stats(); listCalls != 1 {
		t.Errorf("ListTools calls = %d, want 1", listCalls)
	}
	if len(first) != 2 {
		t.Fatalf("specs = %+v, want 2", first)
	}
	if first[0].Name != "mcp_srv_read" || first[1].Name != "mcp_srv_write" {
		t.Errorf("registered names = %q, %q; want mcp_srv_read, mcp_srv_write", first[0].Name, first[1].Name)
	}
	if first[0].RemoteName != "read" {
		t.Errorf("RemoteName = %q, want read", first[0].RemoteName)
	}
	if first[0].InputSchema["type"] != "object" {
		t.Errorf("InputSchema type = %v, want object", first[0].InputSchema["type"])
	}
}

// TestLoadServerConcurrentSingleflight: N concurrent loads ⇒ exactly one dial,
// everyone gets the same specs.
func TestLoadServerConcurrentSingleflight(t *testing.T) {
	d := &fakeDialer{
		delay: 20 * time.Millisecond, // widen the race window
		setup: func(_ int, conn *fakeConn) {
			conn.listTools = []mcp.Tool{remoteTool("ping")}
		},
	}
	mgr := NewManager(basicFixture(t), d)

	const n = 8
	var wg sync.WaitGroup
	results := make([][]tools.LoadedMCPTool, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = mgr.LoadServer(context.Background(), "srv")
		}(i)
	}
	close(start)
	wg.Wait()

	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 (singleflight)", d.dialCount())
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if !reflect.DeepEqual(results[0], results[i]) {
			t.Errorf("goroutine %d got different specs: %+v", i, results[i])
		}
	}
}

// TestLoadServerUnknownAndDisabled: descriptive errors. The known-servers
// list is deliberately NOT part of the manager's error (N4): the loader
// layer (pkg/tools loadError) appends the authoritative list exactly once,
// so a manager error flowing through it never duplicates the list.
func TestLoadServerUnknownAndDisabled(t *testing.T) {
	paths := mcpFixture(t, `{
  "mcpServers": {
    "srv":  {"command": "a"},
    "gone": {"command": "b", "disabled": true}
  }
}`)
	mgr := NewManager(paths, &fakeDialer{})

	_, err := mgr.LoadServer(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected an error for an unknown server")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unknown-server error should say it is not configured, got: %v", err)
	}
	if strings.Contains(err.Error(), "known servers:") {
		t.Errorf("manager error must not carry the known-servers list (loader appends it once), got: %v", err)
	}

	_, err = mgr.LoadServer(context.Background(), "gone")
	if err == nil {
		t.Fatal("expected an error for a disabled server")
	}
	if !strings.Contains(err.Error(), "disabled") || !strings.Contains(err.Error(), LayerGlobal) {
		t.Errorf("disabled-server error should name the layer, got: %v", err)
	}
	if strings.Contains(err.Error(), "known servers:") {
		t.Errorf("manager error must not carry the known-servers list (loader appends it once), got: %v", err)
	}
}

// TestLoadServerDialFailureThenSuccess: a failed dial is not cached.
func TestLoadServerDialFailureThenSuccess(t *testing.T) {
	d := &fakeDialer{
		errs: []error{errors.New("dial exploded")},
		setup: func(_ int, conn *fakeConn) {
			conn.listTools = []mcp.Tool{remoteTool("read")}
		},
	}
	mgr := NewManager(basicFixture(t), d)

	if _, err := mgr.LoadServer(context.Background(), "srv"); err == nil {
		t.Fatal("first load should fail")
	}
	specs, err := mgr.LoadServer(context.Background(), "srv")
	if err != nil {
		t.Fatalf("second load should retry the dial: %v", err)
	}
	if len(specs) != 1 {
		t.Errorf("specs = %+v, want 1", specs)
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2", d.dialCount())
	}
}

// TestLoadServerTransportFailureRedialsOnce: a dead connection during listing
// is dropped, re-dialed and listed again.
func TestLoadServerTransportFailureRedialsOnce(t *testing.T) {
	d := &fakeDialer{setup: func(n int, conn *fakeConn) {
		if n == 1 {
			conn.listErrs = []error{transportFail("stdio child died")}
			conn.listTools = []mcp.Tool{remoteTool("never")}
		} else {
			conn.listTools = []mcp.Tool{remoteTool("after")}
		}
	}}
	mgr := NewManager(basicFixture(t), d)

	specs, err := mgr.LoadServer(context.Background(), "srv")
	if err != nil {
		t.Fatalf("LoadServer should recover via one redial: %v", err)
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2", d.dialCount())
	}
	if _, _, closes := d.connAt(0).stats(); closes != 1 {
		t.Errorf("dead conn closes = %d, want 1", closes)
	}
	if len(specs) != 1 || specs[0].RemoteName != "after" {
		t.Errorf("specs = %+v, want the second connection's tools", specs)
	}
}

// TestLoadServerTransportFailureStopsAfterRetry: still-dead server ⇒ one
// redial, then a clean wrapped error (no retry loop).
func TestLoadServerTransportFailureStopsAfterRetry(t *testing.T) {
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listErrs = []error{transportFail("always dead")}
	}}
	mgr := NewManager(basicFixture(t), d)

	_, err := mgr.LoadServer(context.Background(), "srv")
	if err == nil {
		t.Fatal("expected an error after both attempts failed")
	}
	if !strings.Contains(err.Error(), "always dead") {
		t.Errorf("error should keep the cause, got: %v", err)
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2 (initial + one retry)", d.dialCount())
	}
}

// --- T5: CallRemote ---------------------------------------------------------

// TestCallRemoteReconnectsOnce: the "server died after load" scenario.
func TestCallRemoteReconnectsOnce(t *testing.T) {
	d := &fakeDialer{setup: func(n int, conn *fakeConn) {
		if n == 1 {
			conn.callErrs = []error{transportFail("connection lost")}
		} else {
			conn.callResult = &mcp.CallToolResult{
				Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "pong"}},
			}
		}
	}}
	mgr := NewManager(basicFixture(t), d)

	res, err := mgr.CallRemote(context.Background(), "srv", "ping", nil)
	if err != nil {
		t.Fatalf("CallRemote should reconnect once and succeed: %v", err)
	}
	if res == nil || len(res.Content) != 1 {
		t.Fatalf("result = %+v, want one content part", res)
	}
	if tc, ok := res.Content[0].(mcp.TextContent); !ok || tc.Text != "pong" {
		t.Errorf("content = %+v, want text pong", res.Content[0])
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2", d.dialCount())
	}
	if _, _, closes := d.connAt(0).stats(); closes != 1 {
		t.Errorf("dead conn closes = %d, want 1", closes)
	}
}

// TestCallRemoteTransportFailureAfterRetryFails: two dead attempts ⇒ error,
// exactly two dials.
func TestCallRemoteTransportFailureAfterRetryFails(t *testing.T) {
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.callErrs = []error{transportFail("still dead")}
	}}
	mgr := NewManager(basicFixture(t), d)

	_, err := mgr.CallRemote(context.Background(), "srv", "ping", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "still dead") {
		t.Errorf("error should keep the cause, got: %v", err)
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2", d.dialCount())
	}
}

// TestCallRemoteMCPErrorDoesNotRedial: an application-level (JSON-RPC) error
// is not a connection failure — redialing would be pointless.
func TestCallRemoteMCPErrorDoesNotRedial(t *testing.T) {
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.callErrs = []error{errors.New("invalid arguments")}
	}}
	mgr := NewManager(basicFixture(t), d)

	_, err := mgr.CallRemote(context.Background(), "srv", "ping", nil)
	if err == nil {
		t.Fatal("expected the MCP error to surface")
	}
	if !strings.Contains(err.Error(), "invalid arguments") {
		t.Errorf("error = %v", err)
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 (no reconnect for MCP errors)", d.dialCount())
	}
}

// TestCallRemoteCancelledContextDoesNotRetry: caller cancellation never
// triggers a reconnect.
func TestCallRemoteCancelledContextDoesNotRetry(t *testing.T) {
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.callErrs = []error{transportFail("context canceled")}
	}}
	mgr := NewManager(basicFixture(t), d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := mgr.CallRemote(ctx, "srv", "ping", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 (cancelled context must not redial)", d.dialCount())
	}
}

// TestCallRemoteUnknownServer: same descriptive error as LoadServer — and,
// like it, without the known-servers list (N4: the loader layer owns it).
func TestCallRemoteUnknownServer(t *testing.T) {
	mgr := NewManager(basicFixture(t), &fakeDialer{})
	_, err := mgr.CallRemote(context.Background(), "ghost", "ping", nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("error should say the server is not configured, got: %v", err)
	}
	if strings.Contains(err.Error(), "known servers:") {
		t.Errorf("manager error must not carry the known-servers list, got: %v", err)
	}
}

// --- T5: Close --------------------------------------------------------------

// TestCloseIdempotent: closes every connection once, then becomes a no-op and
// rejects new dials.
func TestCloseIdempotent(t *testing.T) {
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listTools = []mcp.Tool{remoteTool("read")}
	}}
	mgr := NewManager(basicFixture(t), d)
	if _, err := mgr.LoadServer(context.Background(), "srv"); err != nil {
		t.Fatalf("LoadServer: %v", err)
	}

	if err := mgr.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("second Close must be a no-op: %v", err)
	}
	if _, _, closes := d.connAt(0).stats(); closes != 1 {
		t.Errorf("conn closes = %d, want exactly 1", closes)
	}

	if _, err := mgr.LoadServer(context.Background(), "srv"); err == nil {
		t.Error("LoadServer after Close should fail (manager is closed)")
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 (no dial after Close)", d.dialCount())
	}
}

// TestCloseAfterFailedDial: no connection was ever installed, Close stays safe.
func TestCloseAfterFailedDial(t *testing.T) {
	d := &fakeDialer{errs: []error{errors.New("boom")}}
	mgr := NewManager(basicFixture(t), d)

	if _, err := mgr.LoadServer(context.Background(), "srv"); err == nil {
		t.Fatal("expected the dial failure to surface")
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close after failed dial: %v", err)
	}
	if err := mgr.Close(); err != nil {
		t.Fatalf("Close twice after failed dial: %v", err)
	}
}

// TestNewManagerNilDialerWiresRealDialer: the default wiring exists.
func TestNewManagerNilDialerWiresRealDialer(t *testing.T) {
	mgr := NewManager(Paths{}, nil)
	if mgr == nil || mgr.dialer == nil {
		t.Fatal("NewManager must default the dialer")
	}
	if _, ok := mgr.dialer.(*mcpDialer); !ok {
		t.Errorf("default dialer type = %T, want *mcpDialer", mgr.dialer)
	}
}

// --- M1: stale-connection drop (lost update) --------------------------------

// gatedConn is a ClientConn whose individual CallTool invocations can be held
// at a gate, so two CallRemote calls over the same connection overlap
// deterministically. release/errs are keyed by that conn's own 1-based call
// number and are read-only after construction.
type gatedConn struct {
	enter   chan int
	release map[int]chan struct{}
	errs    map[int]error
	ok      *mcp.CallToolResult

	mu     sync.Mutex
	calls  int
	closes int
}

func newGatedConn(release map[int]chan struct{}, errs map[int]error) *gatedConn {
	return &gatedConn{
		enter:   make(chan int, 8),
		release: release,
		errs:    errs,
		ok: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "pong"}},
		},
	}
}

func (g *gatedConn) ListTools(context.Context) ([]mcp.Tool, error) {
	return []mcp.Tool{remoteTool("ping")}, nil
}

func (g *gatedConn) CallTool(_ context.Context, _ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
	g.mu.Lock()
	g.calls++
	n := g.calls
	g.mu.Unlock()
	g.enter <- n
	if gate, ok := g.release[n]; ok {
		<-gate
	}
	if err, ok := g.errs[n]; ok {
		return nil, err
	}
	return g.ok, nil
}

func (g *gatedConn) Close() error {
	g.mu.Lock()
	g.closes++
	g.mu.Unlock()
	return nil
}

func (g *gatedConn) closeCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.closes
}

// scriptedDialer hands out a fixed sequence of connections (no real dialing)
// and records the ServerConfig every Dial received, so a test can assert
// WHICH config a redial used (NEW-1).
type scriptedDialer struct {
	mu    sync.Mutex
	conns []ClientConn
	dials int
	cfgs  []ServerConfig
}

func (d *scriptedDialer) Dial(_ context.Context, cfg ServerConfig) (ClientConn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfgs = append(d.cfgs, cfg)
	if d.dials >= len(d.conns) {
		return nil, errors.New("scripted dialer exhausted")
	}
	conn := d.conns[d.dials]
	d.dials++
	return conn, nil
}

func (d *scriptedDialer) dialCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials
}

// cfgAt returns the ServerConfig the n-th Dial received (zero value when
// fewer dials happened — callers assert the dial count separately).
func (d *scriptedDialer) cfgAt(n int) ServerConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n < 0 || n >= len(d.cfgs) {
		return ServerConfig{}
	}
	return d.cfgs[n]
}

// waitEnter waits until the conn reports call number n (bounded so a
// regression fails instead of hanging the suite).
func waitEnter(t *testing.T, conn *gatedConn, n int) {
	t.Helper()
	select {
	case got := <-conn.enter:
		if got != n {
			t.Fatalf("call entry = #%d, want #%d", got, n)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for call #%d to enter", n)
	}
}

// TestCallRemoteStaleDropLeavesNewerConnAlive is the M1 regression: two
// overlapping transport failures of the SAME connection. The first failure
// redials and installs a newer conn; the second (stale) drop must close only
// the conn that failed — never the newer healthy one, and without forcing a
// third dial. Run under -race (gate 3).
func TestCallRemoteStaleDropLeavesNewerConnAlive(t *testing.T) {
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	conn1 := newGatedConn(
		map[int]chan struct{}{1: releaseA, 2: releaseB},
		map[int]error{1: transportFail("stdio child died"), 2: transportFail("stdio child died")},
	)
	conn2 := newGatedConn(nil, nil)
	d := &scriptedDialer{conns: []ClientConn{conn1, conn2}}
	mgr := NewManager(basicFixture(t), d)

	ctx := context.Background()
	doneA := make(chan error, 1)
	go func() {
		_, err := mgr.CallRemote(ctx, "srv", "ping", nil)
		doneA <- err
	}()

	// A is inside conn1's first call; let B grab the same conn1 too.
	waitEnter(t, conn1, 1)
	doneB := make(chan error, 1)
	go func() {
		_, err := mgr.CallRemote(ctx, "srv", "ping", nil)
		doneB <- err
	}()
	waitEnter(t, conn1, 2)

	// Release A: transport failure → drop conn1 → redial conn2 → success.
	close(releaseA)
	if err := <-doneA; err != nil {
		t.Fatalf("call A (fresh redial) = %v, want nil", err)
	}

	// The stale failure of B lands only AFTER conn2 is installed: its drop
	// must retire conn1 (already retired) and leave conn2 untouched.
	close(releaseB)
	if err := <-doneB; err != nil {
		t.Fatalf("call B (stale drop) = %v, want nil — it must reuse the newer conn", err)
	}

	if closes := conn2.closeCount(); closes != 0 {
		t.Errorf("newer conn closed %d time(s) by a stale drop, want 0", closes)
	}
	if got := d.dialCount(); got != 2 {
		t.Errorf("dials = %d, want 2 (B must reuse the newer conn, not redial)", got)
	}
	if closes := conn1.closeCount(); closes == 0 {
		t.Error("the conn that failed must still be closed by its retiree")
	}
}

// --- M2: config staleness ---------------------------------------------------

// TestConfigChangeRedialsWithNewConfig pins the lazy staleness fix: editing a
// server's mcp.json entry (here: a different command/description) is picked
// up on the next use — the old connection is closed, the cached specs are
// dropped and the dialer receives the NEW ServerConfig. The prompt-facing
// Servers() re-reads the disk on every call, so the new description shows up
// there too.
func TestConfigChangeRedialsWithNewConfig(t *testing.T) {
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"command": "v1", "description": "one"}}}`)
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listTools = []mcp.Tool{remoteTool("ping")}
		conn.callResult = &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "pong"}},
		}
	}}
	mgr := NewManager(paths, d)
	ctx := context.Background()

	if _, err := mgr.LoadServer(ctx, "srv"); err != nil {
		t.Fatalf("first LoadServer: %v", err)
	}
	if d.dialCount() != 1 {
		t.Fatalf("dials = %d, want 1", d.dialCount())
	}
	if got := d.cfgAt(0).Command; got != "v1" {
		t.Fatalf("first dial got command %q, want v1", got)
	}

	// Rewrite the layer with a different command (a real edit, same path).
	writeFileAt(t, paths.LeleDir+"/mcp.json",
		`{"mcpServers": {"srv": {"command": "v2", "description": "two"}}}`)

	if _, err := mgr.CallRemote(ctx, "srv", "ping", nil); err != nil {
		t.Fatalf("CallRemote after config edit: %v", err)
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2 (redial with the new config)", d.dialCount())
	}
	if got := d.cfgAt(1).Command; got != "v2" {
		t.Errorf("second dial got command %q, want v2 (stale config was reused)", got)
	}
	if _, _, closes := d.connAt(0).stats(); closes != 1 {
		t.Errorf("old conn closes = %d, want 1 (retired with its config)", closes)
	}

	// The loaded cache was dropped with the config: the next load re-lists
	// over the new connection without a third dial.
	if _, err := mgr.LoadServer(ctx, "srv"); err != nil {
		t.Fatalf("LoadServer after config edit: %v", err)
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2 (specs re-fetched on the live conn)", d.dialCount())
	}

	// Prompt-facing view re-reads the disk (M2 doc claim).
	infos := mgr.Servers()
	if len(infos) != 1 || infos[0].Description != "two" {
		t.Errorf("Servers() = %+v, want the edited description", infos)
	}
}

// --- minor m1: discovery warning dedup -------------------------------------

// TestDiscoveryWarningLoggedOnlyWhenChanged: discover runs on every remote
// call, so a malformed layer must not emit one warning per call — the same
// text is logged once, and a later change (or a clean re-read that re-arms
// the dedup) logs again.
func TestDiscoveryWarningLoggedOnlyWhenChanged(t *testing.T) {
	var buf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origOut) })
	origLevel := logger.GetLevel()
	logger.SetLevel(logger.DEBUG)
	t.Cleanup(func() { logger.SetLevel(origLevel) })

	broken := `{"mcpServers": {"bad":`
	paths := mcpFixture(t, broken)
	mgr := NewManager(paths, &fakeDialer{})

	for i := 0; i < 5; i++ {
		_ = mgr.Servers()
	}
	if n := strings.Count(buf.String(), "mcp.json discovery reported problems"); n != 1 {
		t.Errorf("warning logged %d times for unchanged text, want 1:\n%s", n, buf.String())
	}

	// A clean read re-arms the dedup; breaking the layer again logs anew.
	writeFileAt(t, paths.LeleDir+"/mcp.json", `{"mcpServers": {"ok": {"command": "x"}}}`)
	_ = mgr.Servers()
	writeFileAt(t, paths.LeleDir+"/mcp.json", broken)
	_ = mgr.Servers()
	if n := strings.Count(buf.String(), "mcp.json discovery reported problems"); n != 2 {
		t.Errorf("warning logged %d times after a clean re-read, want 2:\n%s", n, buf.String())
	}
}

// --- nit: cached load honors a dead context ---------------------------------

// TestLoadServerCancelledContextOnCachedPath: a caller whose context is
// already done gets ctx.Err() back instead of cached specs it cannot use —
// no dial, no listing, no surprise success.
func TestLoadServerCancelledContextOnCachedPath(t *testing.T) {
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listTools = []mcp.Tool{remoteTool("read")}
	}}
	mgr := NewManager(basicFixture(t), d)

	if _, err := mgr.LoadServer(context.Background(), "srv"); err != nil {
		t.Fatalf("warm LoadServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mgr.LoadServer(ctx, "srv"); err == nil {
		t.Fatal("cached load with a cancelled context must return the context error")
	} else if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 (the cached path must not dial)", d.dialCount())
	}
}

// --- N1: mid-flight config change must not re-poison the cache -------------

// listGatedConn is a ClientConn whose ListTools blocks at a gate, so a
// LoadServer flight can be held open while the test edits mcp.json and
// triggers a resolve. entered is closed on the first ListTools call.
type listGatedConn struct {
	entered chan struct{}
	release chan struct{}
	tools   []mcp.Tool
	// listErr, when set, is returned by ListTools once the gate opens: the
	// transport failure that drives the reconnect-once retry (NEW-1).
	listErr error

	once   sync.Once
	mu     sync.Mutex
	closes int
}

func newListGatedConn(remoteTools []mcp.Tool) *listGatedConn {
	return &listGatedConn{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		tools:   remoteTools,
	}
}

func (c *listGatedConn) ListTools(context.Context) ([]mcp.Tool, error) {
	c.once.Do(func() { close(c.entered) })
	<-c.release
	return c.tools, c.listErr
}

func (c *listGatedConn) CallTool(context.Context, string, map[string]interface{}) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "pong"}},
	}, nil
}

func (c *listGatedConn) Close() error {
	c.mu.Lock()
	c.closes++
	c.mu.Unlock()
	return nil
}

// TestLoadServerMidFlightConfigChangeDoesNotRePoisonCache is the N1
// regression: a LoadServer flight captured the v1 config, then a mid-flight
// mcp.json edit advanced the fingerprint (applyConfigChange cleared the
// loaded cache and dropped the connection). When the flight finishes it must
// NOT write its v1-era specs back into the cache — the caller of THIS load
// still gets the specs it asked for (it asked while v1 was current), but the
// next LoadServer must re-dial with v2 and return the new tools. Pre-fix the
// store ran unconditionally (`err == nil && !m.closed`) and re-poisoned the
// cache. Run under -race (gate 3).
func TestLoadServerMidFlightConfigChangeDoesNotRePoisonCache(t *testing.T) {
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"command": "v1", "description": "one"}}}`)
	conn1 := newListGatedConn([]mcp.Tool{remoteTool("v1tool")})
	conn2 := &fakeConn{listTools: []mcp.Tool{remoteTool("v2tool")}}
	d := &scriptedDialer{conns: []ClientConn{conn1, conn2}}
	mgr := NewManager(paths, d)
	ctx := context.Background()

	type result struct {
		specs []tools.LoadedMCPTool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		specs, err := mgr.LoadServer(ctx, "srv")
		done <- result{specs, err}
	}()

	// The flight dialed with v1 and is now inside conn1's ListTools.
	select {
	case <-conn1.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the first ListTools to start")
	}

	// Mid-flight config edit + resolve: advances fps["srv"], clears the
	// loaded cache and drops the live connection.
	writeFileAt(t, paths.LeleDir+"/mcp.json",
		`{"mcpServers": {"srv": {"command": "v2", "description": "two"}}}`)
	if _, err := mgr.resolve("srv"); err != nil {
		t.Fatalf("resolve after the mid-flight edit: %v", err)
	}

	// Let the v1 flight finish.
	close(conn1.release)
	res := <-done
	if res.err != nil {
		t.Fatalf("LoadServer (the caller asked while v1 was current): %v", res.err)
	}
	if len(res.specs) != 1 || res.specs[0].RemoteName != "v1tool" {
		t.Fatalf("this caller must still get the specs it asked for: %+v", res.specs)
	}

	// The stale specs must NOT land in the cache: the flight captured the v1
	// fingerprint before fetching and compares it at store time.
	mgr.mu.Lock()
	_, cached := mgr.loaded["srv"]
	mgr.mu.Unlock()
	if cached {
		t.Fatal("stale v1 specs re-poisoned the loaded cache after a mid-flight config change")
	}

	// A fresh LoadServer re-dials with v2 and returns the new tools.
	specs, err := mgr.LoadServer(ctx, "srv")
	if err != nil {
		t.Fatalf("fresh LoadServer after the config change: %v", err)
	}
	if len(specs) != 1 || specs[0].RemoteName != "v2tool" {
		t.Errorf("fresh specs = %+v, want the v2 tool list", specs)
	}
	if got := d.dialCount(); got != 2 {
		t.Errorf("dials = %d, want 2 (v1 flight + v2 redial)", got)
	}
}

// --- NEW-1: the reconnect-once retry must re-resolve the config -----------

// TestCallRemoteRedialReResolvesConfig is the NEW-1 regression (CallRemote
// path): the call enters with the v1 config, a mid-flight mcp.json edit
// retires that config (fps advanced to v2, the live conn dropped) while the
// call is still gated inside CallTool, and the transport failure then drives
// the reconnect-once retry. Pre-fix the retry redialed with the ENTRY-TIME
// cfg — dial #2 received the retired v1 ServerConfig, and because fps
// already equalled v2 no later resolve ever retired that stale conn again.
// Post-fix the retry re-resolves before dialing: dial #2 receives v2, and a
// subsequent LoadServer serves the v2 tools with NO further edit. Run under
// -race (gate 3).
func TestCallRemoteRedialReResolvesConfig(t *testing.T) {
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"command": "v1", "description": "one"}}}`)
	release := make(chan struct{})
	// conn1: the in-flight call, gated at entry, then a transport failure.
	conn1 := newGatedConn(
		map[int]chan struct{}{1: release},
		map[int]error{1: transportFail("stdio child died")},
	)
	// conn2: what the retry must dial — under the CURRENT (v2) config.
	conn2 := &fakeConn{
		listTools: []mcp.Tool{remoteTool("v2tool")},
		callResult: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "pong"}},
		},
	}
	d := &scriptedDialer{conns: []ClientConn{conn1, conn2}}
	mgr := NewManager(paths, d)
	ctx := context.Background()

	done := make(chan error, 1)
	go func() {
		_, err := mgr.CallRemote(ctx, "srv", "ping", nil)
		done <- err
	}()

	// The call is inside conn1.CallTool, blocked at the gate.
	waitEnter(t, conn1, 1)

	// Mid-flight edit + resolve: retires the v1 config (clears the specs
	// cache, drops the live conn) while the call is still in flight.
	writeFileAt(t, paths.LeleDir+"/mcp.json",
		`{"mcpServers": {"srv": {"command": "v2", "description": "two"}}}`)
	if _, err := mgr.resolve("srv"); err != nil {
		t.Fatalf("resolve after the mid-flight edit: %v", err)
	}

	// Release: the transport failure drives the reconnect-once retry.
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("CallRemote must recover via the reconnect-once retry: %v", err)
	}

	if got := d.dialCount(); got != 2 {
		t.Fatalf("dials = %d, want 2 (initial + exactly one redial)", got)
	}
	if got := d.cfgAt(1).Command; got != "v2" {
		t.Errorf("redial dialed command %q, want v2 — the retry must re-resolve the config before dialing (pre-fix it re-dialed the retired v1 entry)", got)
	}
	if conn1.closeCount() == 0 {
		t.Error("the retired conn must be closed")
	}

	// No second edit: the next load serves the v2 tools over the redialed conn.
	specs, err := mgr.LoadServer(ctx, "srv")
	if err != nil {
		t.Fatalf("LoadServer after the recovered call: %v", err)
	}
	if len(specs) != 1 || specs[0].RemoteName != "v2tool" {
		t.Errorf("specs = %+v, want the v2 tool list without another edit", specs)
	}
	if got := d.dialCount(); got != 2 {
		t.Errorf("dials = %d, want 2 (LoadServer reuses the redialed conn)", got)
	}
}

// TestLoadServerRedialReResolvesConfig is the NEW-1 fetchTools twin: the
// list flight enters with v1, a mid-flight edit retires that config, and the
// transport failure on release drives the reconnect-once retry — which must
// re-resolve and redial with v2. Pre-fix dial #2 received the retired v1
// entry (and the stale conn it installed survived, since fps already held
// v2). Run under -race (gate 3).
func TestLoadServerRedialReResolvesConfig(t *testing.T) {
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"command": "v1", "description": "one"}}}`)
	conn1 := newListGatedConn([]mcp.Tool{remoteTool("v1tool")})
	conn1.listErr = transportFail("stdio child died")
	conn2 := &fakeConn{listTools: []mcp.Tool{remoteTool("v2tool")}}
	d := &scriptedDialer{conns: []ClientConn{conn1, conn2}}
	mgr := NewManager(paths, d)
	ctx := context.Background()

	type result struct {
		specs []tools.LoadedMCPTool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		specs, err := mgr.LoadServer(ctx, "srv")
		done <- result{specs, err}
	}()

	select {
	case <-conn1.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the first ListTools to start")
	}

	// Mid-flight edit + resolve: retires the v1 config under the open flight.
	writeFileAt(t, paths.LeleDir+"/mcp.json",
		`{"mcpServers": {"srv": {"command": "v2", "description": "two"}}}`)
	if _, err := mgr.resolve("srv"); err != nil {
		t.Fatalf("resolve after the mid-flight edit: %v", err)
	}

	close(conn1.release)
	res := <-done
	if res.err != nil {
		t.Fatalf("LoadServer must recover via the reconnect-once retry: %v", res.err)
	}

	if got := d.dialCount(); got != 2 {
		t.Fatalf("dials = %d, want 2 (initial + exactly one redial)", got)
	}
	if got := d.cfgAt(1).Command; got != "v2" {
		t.Errorf("redial dialed command %q, want v2 — the fetchTools retry must re-resolve the config before dialing (pre-fix it re-dialed the retired v1 entry)", got)
	}
	// The retry listed through the redialed (v2) connection.
	if len(res.specs) != 1 || res.specs[0].RemoteName != "v2tool" {
		t.Errorf("specs = %+v, want the v2 tool list", res.specs)
	}
}

// --- NEW-2: specs deliberately NOT cached still bind to their server -------

// TestToolFactoryBindsSpecServerWithoutLoadedCache is the manager-level probe
// of the reviewer's scenario: a LoadServer flight lands across a config edit,
// so the N1 guard keeps its specs OUT of the loaded cache while still
// returning them to this caller. Handing such a spec to ToolFactory must
// yield a LIVE RemoteTool: the binding comes from spec.Server (stamped by
// buildSpecs) and CallRemote re-resolves the current config, dialing lazily.
// Pre-fix the factory recovered the server from the loaded cache (serverOf →
// ""), the RemoteTool executed dead ("not linked to a loaded server") and —
// registration being skip-if-present — that dead instance would permanently
// shadow the correct one after a later successful load. Run under -race
// (gate 3).
func TestToolFactoryBindsSpecServerWithoutLoadedCache(t *testing.T) {
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"command": "v1", "description": "one"}}}`)
	conn1 := newListGatedConn([]mcp.Tool{remoteTool("v1tool")})
	conn2 := &fakeConn{
		listTools: []mcp.Tool{remoteTool("v2tool")},
		callResult: &mcp.CallToolResult{
			Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "pong"}},
		},
	}
	d := &scriptedDialer{conns: []ClientConn{conn1, conn2}}
	mgr := NewManager(paths, d)
	ctx := context.Background()

	type result struct {
		specs []tools.LoadedMCPTool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		specs, err := mgr.LoadServer(ctx, "srv")
		done <- result{specs, err}
	}()

	select {
	case <-conn1.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the first ListTools to start")
	}

	// Mid-flight edit + resolve: cache cleared, conn1 retired under the flight.
	writeFileAt(t, paths.LeleDir+"/mcp.json",
		`{"mcpServers": {"srv": {"command": "v2", "description": "two"}}}`)
	if _, err := mgr.resolve("srv"); err != nil {
		t.Fatalf("resolve after the mid-flight edit: %v", err)
	}
	close(conn1.release)

	res := <-done
	if res.err != nil {
		t.Fatalf("LoadServer (the caller asked while v1 was current): %v", res.err)
	}
	if len(res.specs) != 1 || res.specs[0].RemoteName != "v1tool" {
		t.Fatalf("specs = %+v, want this caller's v1tool specs", res.specs)
	}

	// The N1 state under test: those specs are NOT in the loaded cache.
	mgr.mu.Lock()
	_, cached := mgr.loaded["srv"]
	mgr.mu.Unlock()
	if cached {
		t.Fatal("probe expects the mid-flight specs to be absent from the loaded cache")
	}

	// The factory must still bind the spec to its server (spec.Server).
	tool := mgr.ToolFactory()(res.specs[0])
	out := tool.Execute(ctx, nil)
	if out.IsError {
		t.Fatalf("RemoteTool built from a non-cached spec executed dead: %s", out.ForLLM)
	}
	if out.ForLLM != "pong" {
		t.Errorf("ForLLM = %q, want pong", out.ForLLM)
	}
	// CallRemote re-resolved the current config and dialed it lazily.
	if got := d.dialCount(); got != 2 {
		t.Errorf("dials = %d, want 2 (v1 flight + lazy v2 dial)", got)
	}
	if got := d.cfgAt(1).Command; got != "v2" {
		t.Errorf("lazy dial got command %q, want v2", got)
	}
}

// TestToolFactoryPrefersSpecServerOverLoadedCache pins the unit contract of
// the fix: when spec.Server is set the loaded cache is NEVER consulted —
// here the cache is poisoned with a different owner of the same registered
// name, which the pre-fix reverse lookup (serverOf) would have bound.
func TestToolFactoryPrefersSpecServerOverLoadedCache(t *testing.T) {
	mgr := NewManager(basicFixture(t), &fakeDialer{})
	spec := tools.LoadedMCPTool{
		Name: "mcp_srv_ping", Server: "srv", RemoteName: "ping", Description: "d",
	}
	mgr.loaded["other"] = []tools.LoadedMCPTool{spec} // poisoned cache

	got := mgr.ToolFactory()(spec)
	tool, ok := got.(*RemoteTool)
	if !ok {
		t.Fatalf("factory returned %T, want *RemoteTool", got)
	}
	if tool.server != "srv" {
		t.Errorf("bound server = %q, want srv — spec.Server must win over the loaded cache", tool.server)
	}
}
