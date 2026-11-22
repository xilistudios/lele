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

func (f *fakeConn) stats() (listCalls, callCalls, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listCalls, f.callCalls, f.closes
}

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

func (d *fakeDialer) cfgAt(n int) ServerConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cfgs[n]
}

func mcpFixture(t *testing.T, serversJSON string) Paths {
	t.Helper()
	dir := t.TempDir()
	writeFileAt(t, dir+"/mcp.json", serversJSON)
	return Paths{LeleDir: dir}
}

func basicFixture(t *testing.T) Paths {
	t.Helper()
	return mcpFixture(t, `{"mcpServers": {"srv": {"command": "srv-bin", "description": "a server"}}}`)
}

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

func transportFail(reason string) error {
	return transport.NewError(errors.New(reason))
}

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

func TestLoadServerConcurrentSingleflight(t *testing.T) {
	d := &fakeDialer{
		delay: 20 * time.Millisecond,
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

func TestNewManagerNilDialerWiresRealDialer(t *testing.T) {
	mgr := NewManager(Paths{}, nil)
	if mgr == nil || mgr.dialer == nil {
		t.Fatal("NewManager must default the dialer")
	}
	if _, ok := mgr.dialer.(*mcpDialer); !ok {
		t.Errorf("default dialer type = %T, want *mcpDialer", mgr.dialer)
	}
}

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

func (d *scriptedDialer) cfgAt(n int) ServerConfig {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n < 0 || n >= len(d.cfgs) {
		return ServerConfig{}
	}
	return d.cfgs[n]
}

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

	waitEnter(t, conn1, 1)
	doneB := make(chan error, 1)
	go func() {
		_, err := mgr.CallRemote(ctx, "srv", "ping", nil)
		doneB <- err
	}()
	waitEnter(t, conn1, 2)

	close(releaseA)
	if err := <-doneA; err != nil {
		t.Fatalf("call A (fresh redial) = %v, want nil", err)
	}

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

	if _, err := mgr.LoadServer(ctx, "srv"); err != nil {
		t.Fatalf("LoadServer after config edit: %v", err)
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2 (specs re-fetched on the live conn)", d.dialCount())
	}

	infos := mgr.Servers()
	if len(infos) != 1 || infos[0].Description != "two" {
		t.Errorf("Servers() = %+v, want the edited description", infos)
	}
}

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

	writeFileAt(t, paths.LeleDir+"/mcp.json", `{"mcpServers": {"ok": {"command": "x"}}}`)
	_ = mgr.Servers()
	writeFileAt(t, paths.LeleDir+"/mcp.json", broken)
	_ = mgr.Servers()
	if n := strings.Count(buf.String(), "mcp.json discovery reported problems"); n != 2 {
		t.Errorf("warning logged %d times after a clean re-read, want 2:\n%s", n, buf.String())
	}
}

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

type listGatedConn struct {
	entered chan struct{}
	release chan struct{}
	tools   []mcp.Tool
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

	select {
	case <-conn1.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for the first ListTools to start")
	}

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
		t.Fatalf("this caller must still get the specs it asked for: %+v", res.specs)
	}

	mgr.mu.Lock()
	_, cached := mgr.loaded["srv"]
	mgr.mu.Unlock()
	if cached {
		t.Fatal("stale v1 specs re-poisoned the loaded cache after a mid-flight config change")
	}

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

func TestCallRemoteRedialReResolvesConfig(t *testing.T) {
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"command": "v1", "description": "one"}}}`)
	release := make(chan struct{})
	conn1 := newGatedConn(
		map[int]chan struct{}{1: release},
		map[int]error{1: transportFail("stdio child died")},
	)
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

	waitEnter(t, conn1, 1)

	writeFileAt(t, paths.LeleDir+"/mcp.json",
		`{"mcpServers": {"srv": {"command": "v2", "description": "two"}}}`)
	if _, err := mgr.resolve("srv"); err != nil {
		t.Fatalf("resolve after the mid-flight edit: %v", err)
	}

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
	if len(res.specs) != 1 || res.specs[0].RemoteName != "v2tool" {
		t.Errorf("specs = %+v, want the v2 tool list", res.specs)
	}
}

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

	mgr.mu.Lock()
	_, cached := mgr.loaded["srv"]
	mgr.mu.Unlock()
	if cached {
		t.Fatal("probe expects the mid-flight specs to be absent from the loaded cache")
	}

	tool := mgr.ToolFactory()(res.specs[0])
	out := tool.Execute(ctx, nil)
	if out.IsError {
		t.Fatalf("RemoteTool built from a non-cached spec executed dead: %s", out.ForLLM)
	}
	if out.ForLLM != "pong" {
		t.Errorf("ForLLM = %q, want pong", out.ForLLM)
	}
	if got := d.dialCount(); got != 2 {
		t.Errorf("dials = %d, want 2 (v1 flight + lazy v2 dial)", got)
	}
	if got := d.cfgAt(1).Command; got != "v2" {
		t.Errorf("lazy dial got command %q, want v2", got)
	}
}

func TestToolFactoryPrefersSpecServerOverLoadedCache(t *testing.T) {
	mgr := NewManager(basicFixture(t), &fakeDialer{})
	spec := tools.LoadedMCPTool{
		Name: "mcp_srv_ping", Server: "srv", RemoteName: "ping", Description: "d",
	}
	mgr.loaded["other"] = []tools.LoadedMCPTool{spec}

	got := mgr.ToolFactory()(spec)
	tool, ok := got.(*RemoteTool)
	if !ok {
		t.Fatalf("factory returned %T, want *RemoteTool", got)
	}
	if tool.server != "srv" {
		t.Errorf("bound server = %q, want srv — spec.Server must win over the loaded cache", tool.server)
	}
}

func TestManagerDropsConnectionWhenAuthRevisionChanges(t *testing.T) {
	const url = "https://srv.example/mcp"
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"url": "`+url+`", "description": "d"}}}`)
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listTools = []mcp.Tool{remoteTool("ping")}
	}}
	mgr := NewManager(paths, d)
	ctx := context.Background()

	if _, err := mgr.LoadServer(ctx, "srv"); err != nil {
		t.Fatalf("first LoadServer: %v", err)
	}
	if d.dialCount() != 1 {
		t.Fatalf("dials = %d, want 1", d.dialCount())
	}

	tok := &transport.Token{AccessToken: "access-token", TokenType: "Bearer"}
	if err := mgr.auth.TokenStore("srv", url).SaveToken(ctx, tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	if _, err := mgr.LoadServer(ctx, "srv"); err != nil {
		t.Fatalf("LoadServer after the token write: %v", err)
	}
	if d.dialCount() != 2 {
		t.Fatalf("dials = %d, want 2 (the token write must retire the cached connection)", d.dialCount())
	}
	if _, _, closes := d.connAt(0).stats(); closes != 1 {
		t.Errorf("first conn closes = %d, want 1 (retired by the revision change)", closes)
	}

	if _, err := mgr.LoadServer(ctx, "srv"); err != nil {
		t.Fatalf("LoadServer with no store change: %v", err)
	}
	if d.dialCount() != 2 {
		t.Errorf("dials = %d, want 2 (a stable revision must reuse the connection)", d.dialCount())
	}
}

func TestManagerFingerprintIncludesAuthRevision(t *testing.T) {
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"url": "https://srv.example/mcp"}}}`)
	mgr := NewManager(paths, &fakeDialer{})
	cfg := ServerConfig{URL: "https://srv.example/mcp"}

	first := mgr.fingerprint("srv", cfg)
	if again := mgr.fingerprint("srv", cfg); again != first {
		t.Errorf("fingerprint changed without a store change: %q vs %q", first, again)
	}

	if err := mgr.auth.Save("srv", AuthRecord{URL: cfg.URL, ClientID: "c1"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	withRecord := mgr.fingerprint("srv", cfg)
	if withRecord == first {
		t.Errorf("fingerprint did not change after a record write: %q", withRecord)
	}
	if again := mgr.fingerprint("srv", cfg); again != withRecord {
		t.Errorf("fingerprint not stable after a record write: %q vs %q", again, withRecord)
	}
	if mgr.auth.Revision("srv") == 0 {
		t.Fatal("probe expects a non-zero revision after the record write")
	}

	mgr.auth = nil
	if got, want := mgr.fingerprint("srv", cfg), configFingerprint(cfg); got != want {
		t.Errorf("nil-store fingerprint = %q, want base %q", got, want)
	}
}

func TestFingerprintNilStore(t *testing.T) {
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listTools = []mcp.Tool{remoteTool("ping")}
	}}
	mgr := NewManager(basicFixture(t), d)
	mgr.auth = nil

	cfg := ServerConfig{URL: "https://srv.example/mcp"}
	if got, want := mgr.fingerprint("srv", cfg), configFingerprint(cfg); got != want {
		t.Errorf("fingerprint = %q, want base %q", got, want)
	}
	if rt := buildAuthRuntime(nil, "srv", cfg); rt != nil {
		t.Errorf("buildAuthRuntime(nil, …) = %+v, want nil", rt)
	}

	if _, err := mgr.LoadServer(context.Background(), "srv"); err != nil {
		t.Fatalf("LoadServer with a nil store: %v", err)
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1", d.dialCount())
	}
}

func TestAuthChallengeOnCachedConnectionIsNotRetried(t *testing.T) {
	const url = "https://srv.example/mcp"
	challenge := transport.NewError(&transport.OAuthAuthorizationRequiredError{})
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"url": "`+url+`"}}}`)
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listErrs = []error{challenge}
		conn.callErrs = []error{challenge}
	}}
	mgr := NewManager(paths, d)

	specs, err := mgr.LoadServer(context.Background(), "srv")
	if err == nil {
		t.Fatalf("LoadServer = %+v, nil; want an authentication error", specs)
	}
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("errors.Is(err, ErrAuthRequired) = false; err = %v", err)
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 (a challenge is not a transport failure)", d.dialCount())
	}
	if _, _, closes := d.connAt(0).stats(); closes != 0 {
		t.Errorf("conn closes = %d, want 0 (the connection must not be dropped)", closes)
	}

	_, err = mgr.CallRemote(context.Background(), "srv", "ping", nil)
	if err == nil {
		t.Fatal("CallRemote succeeded; want an authentication error")
	}
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("errors.Is(CallRemote err, ErrAuthRequired) = false; err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "mcp call srv.ping: ") {
		t.Errorf("error = %q, want the %q prefix", err.Error(), "mcp call srv.ping: ")
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 after the challenge on the tool call", d.dialCount())
	}
	if _, _, closes := d.connAt(0).stats(); closes != 0 {
		t.Errorf("conn closes = %d, want 0 (the connection must not be dropped)", closes)
	}

	if specs, err := mgr.LoadServer(context.Background(), "srv"); err != nil {
		t.Fatalf("second LoadServer on the cached conn: %v", err)
	} else if len(specs) != 0 {
		t.Errorf("specs = %+v, want none (the fake lists no tools)", specs)
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 after a successful list on the cached conn", d.dialCount())
	}
}

func TestAuthChallengeOnToolCallIsNotRetried(t *testing.T) {
	const url = "https://srv.example/mcp"
	challenge := transport.NewError(&transport.AuthorizationRequiredError{})
	paths := mcpFixture(t, `{"mcpServers": {"srv": {"url": "`+url+`"}}}`)
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.callErrs = []error{challenge}
	}}
	mgr := NewManager(paths, d)

	_, err := mgr.CallRemote(context.Background(), "srv", "ping", nil)
	if err == nil {
		t.Fatal("CallRemote succeeded; want an authentication error")
	}
	if !errors.Is(err, ErrAuthRequired) {
		t.Fatalf("errors.Is(err, ErrAuthRequired) = false; err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "mcp call srv.ping: ") {
		t.Errorf("error = %q, want the %q prefix", err.Error(), "mcp call srv.ping: ")
	}
	if d.dialCount() != 1 {
		t.Errorf("dials = %d, want 1 (a challenge is not a transport failure)", d.dialCount())
	}
	if _, _, closes := d.connAt(0).stats(); closes != 0 {
		t.Errorf("conn closes = %d, want 0 (the connection must not be dropped)", closes)
	}
}

func TestResolveUsesAttachAuthRuntime(t *testing.T) {
	const url = "https://srv.example/mcp"
	paths := mcpFixture(t, `{
  "mcpServers": {
    "authed": {"url": "`+url+`"},
    "open":   {"url": "https://open.example/mcp"}
  }
}`)
	d := &fakeDialer{setup: func(_ int, conn *fakeConn) {
		conn.listTools = []mcp.Tool{remoteTool("ping")}
	}}
	mgr := NewManager(paths, d)
	if err := mgr.auth.Save("authed", AuthRecord{URL: url, AuthRequired: true, ClientID: "stored"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg, err := mgr.resolve("authed")
	if err != nil {
		t.Fatalf("resolve(authed): %v", err)
	}
	if cfg.authRuntime == nil {
		t.Fatal("resolve must attach an OAuth runtime for a store-opted-in server")
	}
	want := AttachAuthRuntime("authed", ServerConfig{URL: url}, mgr.auth)
	if want.authRuntime == nil {
		t.Fatal("AttachAuthRuntime returned no runtime for a store-opted-in server")
	}
	if cfg.authRuntime.clientID != want.authRuntime.clientID || cfg.authRuntime.clientID != "stored" {
		t.Errorf("clientID = %q, want %q (resolve must use AttachAuthRuntime)",
			cfg.authRuntime.clientID, want.authRuntime.clientID)
	}

	if open, err := mgr.resolve("open"); err != nil {
		t.Fatalf("resolve(open): %v", err)
	} else if open.authRuntime != nil {
		t.Errorf("resolve(open).authRuntime = %+v, want nil", open.authRuntime)
	}

	if _, err := mgr.LoadServer(context.Background(), "authed"); err != nil {
		t.Fatalf("LoadServer(authed): %v", err)
	}
	if d.cfgAt(0).authRuntime == nil {
		t.Error("the dial path must receive the resolved OAuth runtime")
	}

	var plain []transportRecord
	var oauth []oauthRecord
	probe := NewManager(paths, &mcpDialer{
		factory:   oauthRecordingFactory(&plain, &oauth),
		newClient: func(transport.Interface) mcpClient { return &recordingClient{} },
	})
	defer func() { _ = probe.Close() }()

	if _, err := probe.LoadServer(context.Background(), "open"); err != nil {
		t.Fatalf("LoadServer(open): %v", err)
	}
	if _, err := probe.LoadServer(context.Background(), "authed"); err != nil {
		t.Fatalf("LoadServer(authed): %v", err)
	}
	if len(plain) != 1 || plain[0].kind != "http" {
		t.Errorf("plain constructors = %+v, want exactly one http for the record-less server", plain)
	}
	if len(oauth) != 1 || oauth[0].kind != "httpOAuth" {
		t.Fatalf("oauth constructors = %+v, want exactly one httpOAuth for the stored-401 server", oauth)
	}
	if oauth[0].cfg.ClientID != "stored" {
		t.Errorf("OAuth ClientID = %q, want the stored record's client id", oauth[0].cfg.ClientID)
	}
	if oauth[0].cfg.TokenStore == nil {
		t.Error("OAuth TokenStore must be non-nil")
	}
}
