package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	probeFixtureName    = "probe-fixture"
	probeFixtureVersion = "9.9.9"
)

type countingTransport struct {
	transport.Interface
	once    sync.Once
	onClose func()
}

func (c *countingTransport) Close() error {
	c.once.Do(c.onClose)
	return c.Interface.Close()
}

type countingConn struct {
	ClientConn
	mu     sync.Mutex
	closeN int
	err    error
}

func (c *countingConn) Close() error {
	c.mu.Lock()
	c.closeN++
	c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return c.ClientConn.Close()
}

func (c *countingConn) closes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeN
}

type probeFixture struct {
	srv *server.MCPServer

	mu         sync.Mutex
	transports int
	closes     int
}

func newProbeFixture(t *testing.T, tools ...server.ServerTool) *probeFixture {
	t.Helper()
	f := &probeFixture{srv: server.NewMCPServer(
		probeFixtureName, probeFixtureVersion, server.WithToolCapabilities(false))}
	f.srv.AddTools(tools...)
	return f
}

func probeTool(name string) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool(name),
		Handler: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(name), nil
		},
	}
}

func (f *probeFixture) countClose() {
	f.mu.Lock()
	f.closes++
	f.mu.Unlock()
}

func (f *probeFixture) factory() transportFactory {
	build := func() transport.Interface {
		f.mu.Lock()
		f.transports++
		f.mu.Unlock()
		return &countingTransport{
			Interface: transport.NewInProcessTransport(f.srv),
			onClose:   f.countClose,
		}
	}
	return transportFactory{
		stdio: func(string, []string, ...string) transport.Interface { return build() },
		http:  func(string, map[string]string) (transport.Interface, error) { return build(), nil },
		sse:   func(string, map[string]string) (transport.Interface, error) { return build(), nil },
	}
}

func (f *probeFixture) dialer() *mcpDialer {
	d := newMCPDialer()
	d.factory = f.factory()
	return d
}

func (f *probeFixture) counts() (transports, closes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transports, f.closes
}

type cancelDialer struct {
	cancel context.CancelFunc
	conn   ClientConn
	dials  int
}

func (d *cancelDialer) Dial(context.Context, ServerConfig) (ClientConn, error) {
	d.dials++
	d.cancel()
	return d.conn, nil
}

func TestProbeServerRealMCPServer(t *testing.T) {
	srv, _ := startMCPServer(t, "probe")
	conn := &countingConn{ClientConn: connFor(t, srv)}
	dialer := newE2EDialer()
	dialer.script("probe-bin", dialStep{conn: conn})

	res, err := probeServer(context.Background(), ServerConfig{Command: "probe-bin"}, dialer)
	if err != nil {
		t.Fatalf("probeServer: %v", err)
	}
	if want := []string{"echo", "summary"}; !slices.Equal(res.Tools, want) {
		t.Errorf("Tools = %v, want %v", res.Tools, want)
	}
	if n := conn.closes(); n != 1 {
		t.Errorf("Close calls = %d, want exactly 1", n)
	}
	if dialer.dials("probe-bin") != 1 {
		t.Errorf("dials = %d, want 1", dialer.dials("probe-bin"))
	}
}

func TestProbeServerProductionDialAgainstRealServer(t *testing.T) {
	fixture := newProbeFixture(t, probeTool("echo"), probeTool("zeta"))

	res, err := probeServer(context.Background(), ServerConfig{Command: "probe-bin"}, fixture.dialer())
	if err != nil {
		t.Fatalf("probeServer: %v", err)
	}
	if want := []string{"echo", "zeta"}; !slices.Equal(res.Tools, want) {
		t.Errorf("Tools = %v, want %v", res.Tools, want)
	}
	if want := (ServerInfo{Name: probeFixtureName, Version: probeFixtureVersion}); res.Server != want {
		t.Errorf("Server = %+v, want %+v (negotiated in the handshake)", res.Server, want)
	}
	if transports, closes := fixture.counts(); transports != 1 || closes != 1 {
		t.Errorf("transports built = %d, closes = %d, want 1 and 1", transports, closes)
	}
}

func TestProbeServerProbesDisabledConfig(t *testing.T) {
	paths := e2ePaths(t)
	entry := `{"mcpServers": {"off": {"command": "probe-bin", "disabled": true, "description": "Off"}}}`
	writeFileAt(t, filepath.Join(paths.LeleDir, "mcp.json"), entry)

	res, err := Discover(paths)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(res.Names) != 0 {
		t.Errorf("Discover.Names = %v, want none (disabled)", res.Names)
	}
	f, err := ParseFile(filepath.Join(paths.LeleDir, "mcp.json"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	cfg, ok := f.MCPServers["off"]
	if !ok || !cfg.Disabled {
		t.Fatalf("ParseFile entry = %+v (present=%v), want the disabled config", cfg, ok)
	}

	mgr := NewManager(paths, &fakeDialer{})
	t.Cleanup(func() { _ = mgr.Close() })
	if _, err := mgr.LoadServer(context.Background(), "off"); err == nil {
		t.Fatal("LoadServer of the disabled server succeeded, want the disabled refusal")
	}

	fixture := newProbeFixture(t, probeTool("echo"))
	got, err := probeServer(context.Background(), cfg, fixture.dialer())
	if err != nil {
		t.Fatalf("probeServer of the disabled config: %v", err)
	}
	if want := []string{"echo"}; !slices.Equal(got.Tools, want) {
		t.Errorf("Tools = %v, want %v", got.Tools, want)
	}
	if servers := mgr.Servers(); len(servers) != 0 {
		t.Errorf("Servers() = %+v, want none (the runtime view is unchanged)", servers)
	}
}

func TestProbeServerPassesConfigVerbatim(t *testing.T) {
	dialer := &fakeDialer{}
	cfg := ServerConfig{
		URL:      "https://${PROBE_HOST}/mcp",
		Headers:  map[string]string{"Authorization": "Bearer ${PROBE_TOKEN}"},
		Env:      map[string]string{"KEY": "${PROBE_KEY}"},
		Disabled: true,
	}
	res, err := probeServer(context.Background(), cfg, dialer)
	if err != nil {
		t.Fatalf("probeServer: %v", err)
	}
	got := dialer.cfgAt(0)
	if got.URL != cfg.URL || got.Headers["Authorization"] != "Bearer ${PROBE_TOKEN}" || got.Env["KEY"] != "${PROBE_KEY}" {
		t.Errorf("dialer config = %+v, want the literal ${VAR} references (no expansion)", got)
	}
	if !got.Disabled {
		t.Error("dialer config lost Disabled, want it passed through")
	}
	if got.Type != typeHTTP {
		t.Errorf("dialer config Type = %q, want %q (the validated default)", got.Type, typeHTTP)
	}
	if res.Tools == nil {
		t.Error("Tools = nil, want an empty non-nil slice for a server without tools")
	}
	if len(res.Tools) != 0 {
		t.Errorf("Tools = %v, want none", res.Tools)
	}
}

func TestProbeServerRefusesInvalidConfigWithoutDialing(t *testing.T) {
	cases := []struct {
		name string
		cfg  ServerConfig
		want string
	}{
		{
			name: "mixes stdio and remote",
			cfg:  ServerConfig{Command: "probe-bin", URL: "https://example.invalid/mcp"},
			want: "mixes stdio and remote",
		},
		{
			name: "neither command nor url",
			cfg:  ServerConfig{},
			want: "needs command (stdio) or url (remote)",
		},
		{
			name: "unknown transport type",
			cfg:  ServerConfig{URL: "https://example.invalid/mcp", Type: "carrier-pigeon"},
			want: "unknown type",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newProbeFixture(t, probeTool("echo"))
			res, err := probeServer(context.Background(), tc.cfg, fixture.dialer())
			if err == nil {
				t.Fatalf("probeServer(%+v) succeeded, want a validation error", tc.cfg)
			}
			if !strings.HasPrefix(err.Error(), "mcp probe: ") {
				t.Errorf("error = %q, want the mcp probe prefix", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
			if len(res.Tools) != 0 {
				t.Errorf("Tools = %v, want none", res.Tools)
			}
			if transports, closes := fixture.counts(); transports != 0 || closes != 0 {
				t.Errorf("transports built = %d, closes = %d, want 0 and 0 (no connection attempt)", transports, closes)
			}
		})
	}
}

func TestProbeServerDialFailure(t *testing.T) {
	res, err := ProbeServer(t.Context(), ServerConfig{Command: "/nonexistent/lele-probe-bin"})
	if err == nil {
		t.Fatal("ProbeServer of a missing binary succeeded")
	}
	if !strings.Contains(err.Error(), "mcp probe: ") || !strings.Contains(err.Error(), "start stdio transport") {
		t.Errorf("error = %q, want the wrapped dial start failure", err)
	}
	if len(res.Tools) != 0 {
		t.Errorf("Tools = %v, want none", res.Tools)
	}
}

func TestProbeServerContextDoneBeforeDial(t *testing.T) {
	fixture := newProbeFixture(t, probeTool("echo"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := probeServer(ctx, ServerConfig{Command: "probe-bin"}, fixture.dialer())
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if transports, _ := fixture.counts(); transports != 0 {
		t.Errorf("transports built = %d, want 0 (no dial with a dead context)", transports)
	}
}

func TestProbeServerContextExpiresDuringDial(t *testing.T) {
	dialer := &fakeDialer{delay: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := probeServer(ctx, ServerConfig{Command: "probe-bin"}, dialer)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("probe took %s, want it to return as soon as the context expired", elapsed)
	}
}

func TestProbeServerListErrorClosesConnection(t *testing.T) {
	conn := &fakeConn{listErrs: []error{errors.New("list exploded")}}
	dialer := &scriptedDialer{conns: []ClientConn{conn}}

	res, err := probeServer(context.Background(), ServerConfig{Command: "probe-bin"}, dialer)
	if err == nil {
		t.Fatal("probeServer succeeded, want the list failure")
	}
	if !strings.Contains(err.Error(), "list exploded") {
		t.Errorf("error = %q, want the list failure", err)
	}
	if len(res.Tools) != 0 {
		t.Errorf("Tools = %v, want none", res.Tools)
	}
	if _, _, closes := conn.stats(); closes != 1 {
		t.Errorf("Close calls = %d, want exactly 1", closes)
	}
}

func TestProbeServerCancellationDuringListWins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := &fakeConn{listErrs: []error{errors.New("broken pipe")}}
	dialer := &cancelDialer{cancel: cancel, conn: conn}

	res, err := probeServer(ctx, ServerConfig{Command: "probe-bin"}, dialer)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if strings.Contains(err.Error(), "broken pipe") {
		t.Errorf("error = %q, want the context error instead of the transport failure", err)
	}
	if len(res.Tools) != 0 {
		t.Errorf("Tools = %v, want none", res.Tools)
	}
	if _, _, closes := conn.stats(); closes != 1 {
		t.Errorf("Close calls = %d, want exactly 1", closes)
	}
}

func TestProbeServerCloseErrorIsReported(t *testing.T) {
	inner := &fakeConn{listTools: []mcp.Tool{remoteTool("echo")}}
	conn := &countingConn{ClientConn: inner, err: errors.New("close exploded")}
	dialer := &scriptedDialer{conns: []ClientConn{conn}}

	res, err := probeServer(context.Background(), ServerConfig{Command: "probe-bin"}, dialer)
	if err == nil {
		t.Fatal("probeServer succeeded, want the close failure")
	}
	if !strings.Contains(err.Error(), "close exploded") {
		t.Errorf("error = %q, want the close failure", err)
	}
	if want := []string{"echo"}; !slices.Equal(res.Tools, want) {
		t.Errorf("Tools = %v, want %v (delivered alongside the close error)", res.Tools, want)
	}
	if n := conn.closes(); n != 1 {
		t.Errorf("Close calls = %d, want exactly 1", n)
	}
}
