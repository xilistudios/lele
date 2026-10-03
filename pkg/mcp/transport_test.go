package mcp

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/xilistudios/lele/pkg/logger"
)

// --- test doubles -----------------------------------------------------------

// fakeTransport is a no-op transport.Interface.
type fakeTransport struct{}

func (fakeTransport) Start(context.Context) error { return nil }
func (fakeTransport) SendRequest(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	return nil, nil
}
func (fakeTransport) SendNotification(context.Context, mcp.JSONRPCNotification) error { return nil }
func (fakeTransport) SetNotificationHandler(func(mcp.JSONRPCNotification))            {}
func (fakeTransport) Close() error                                                    { return nil }
func (fakeTransport) GetSessionId() string                                            { return "" }

// recordingClient implements mcpClient and records the handshake calls.
type recordingClient struct {
	starts []context.Context
	inits  []struct {
		ctx context.Context
		req mcp.InitializeRequest
	}
	closes   int
	startErr error
	initErr  error
	// blockInit makes Initialize wait for its context to expire, to prove
	// the Start context is independent of the dial timeout.
	blockInit bool
}

func (c *recordingClient) Start(ctx context.Context) error {
	c.starts = append(c.starts, ctx)
	return c.startErr
}

func (c *recordingClient) Initialize(ctx context.Context, req mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	c.inits = append(c.inits, struct {
		ctx context.Context
		req mcp.InitializeRequest
	}{ctx: ctx, req: req})
	if c.blockInit {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if c.initErr != nil {
		return nil, c.initErr
	}
	return &mcp.InitializeResult{}, nil
}

func (c *recordingClient) ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return &mcp.ListToolsResult{}, nil
}

func (c *recordingClient) CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{}, nil
}

func (c *recordingClient) Close() error {
	c.closes++
	return nil
}

// transportRecord captures how one transport constructor was invoked.
type transportRecord struct {
	kind    string // "stdio" | "http" | "sse"
	command string
	args    []string
	env     []string
	url     string
	headers map[string]string
}

// recordingFactory builds fake transports while remembering their arguments.
func recordingFactory(recs *[]transportRecord) transportFactory {
	return transportFactory{
		stdio: func(command string, env []string, args ...string) transport.Interface {
			*recs = append(*recs, transportRecord{kind: "stdio", command: command, env: env, args: args})
			return fakeTransport{}
		},
		http: func(serverURL string, headers map[string]string) (transport.Interface, error) {
			*recs = append(*recs, transportRecord{kind: "http", url: serverURL, headers: headers})
			return fakeTransport{}, nil
		},
		sse: func(serverURL string, headers map[string]string) (transport.Interface, error) {
			*recs = append(*recs, transportRecord{kind: "sse", url: serverURL, headers: headers})
			return fakeTransport{}, nil
		},
	}
}

// newTestDialer wires a dialer over the recording factory and client.
func newTestDialer(recs *[]transportRecord, cli mcpClient) *mcpDialer {
	return &mcpDialer{
		factory:   recordingFactory(recs),
		newClient: func(transport.Interface) mcpClient { return cli },
	}
}

// --- T4: routing, handshake, failures ---------------------------------------

// TestDialRoutesServerConfigToTransport proves each ServerConfig shape goes
// to the right constructor and that Dial runs Start + Initialize once.
func TestDialRoutesServerConfigToTransport(t *testing.T) {
	cases := []struct {
		name     string
		srv      ServerConfig
		wantKind string
		wantErr  bool
	}{
		{
			name:     "stdio command args env",
			srv:      ServerConfig{Command: "/bin/lele-mcp", Args: []string{"--stdio", "--port", "1234"}, Env: map[string]string{"KEY": "VAL"}},
			wantKind: "stdio",
		},
		{
			name:     "http default type",
			srv:      ServerConfig{URL: "http://srv.example/mcp"},
			wantKind: "http",
		},
		{
			name:     "http explicit type with headers",
			srv:      ServerConfig{URL: "http://srv.example/mcp", Type: typeHTTP, Headers: map[string]string{"Authorization": "Bearer x"}},
			wantKind: "http",
		},
		{
			name:     "sse",
			srv:      ServerConfig{URL: "http://srv.example/sse", Type: typeSSE, Headers: map[string]string{"X-Key": "y"}},
			wantKind: "sse",
		},
		{
			name:    "neither command nor url",
			srv:     ServerConfig{Type: typeHTTP},
			wantErr: true,
		},
		{
			name:    "unknown remote type",
			srv:     ServerConfig{URL: "http://srv.example", Type: "ws"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var recs []transportRecord
			cli := &recordingClient{}
			d := newTestDialer(&recs, cli)

			conn, err := d.Dial(context.Background(), tc.srv)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if conn != nil {
					t.Fatal("conn must be nil on error")
				}
				if len(recs) != 0 {
					t.Fatalf("no transport must be built, got %+v", recs)
				}
				if cli.closes != 0 {
					t.Fatalf("no client exists, closes = %d", cli.closes)
				}
				return
			}
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			if conn == nil {
				t.Fatal("conn is nil")
			}
			if len(recs) != 1 {
				t.Fatalf("want exactly 1 transport, got %d", len(recs))
			}
			got := recs[0]
			if got.kind != tc.wantKind {
				t.Errorf("transport kind = %q, want %q", got.kind, tc.wantKind)
			}
			switch got.kind {
			case "stdio":
				if got.command != tc.srv.Command {
					t.Errorf("command = %q, want %q", got.command, tc.srv.Command)
				}
				if len(got.args) != len(tc.srv.Args) {
					t.Errorf("args = %v, want %v", got.args, tc.srv.Args)
				}
			default:
				if got.url != tc.srv.URL {
					t.Errorf("url = %q, want %q", got.url, tc.srv.URL)
				}
				if len(got.headers) != len(tc.srv.Headers) {
					t.Errorf("headers = %v, want %v", got.headers, tc.srv.Headers)
				}
			}

			if len(cli.starts) != 1 {
				t.Errorf("Start calls = %d, want 1", len(cli.starts))
			}
			if len(cli.inits) != 1 {
				t.Errorf("Initialize calls = %d, want 1", len(cli.inits))
			}
			if cli.closes != 0 {
				t.Errorf("closes = %d, want 0 after successful dial", cli.closes)
			}
			if err := conn.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if cli.closes != 1 {
				t.Errorf("closes = %d, want 1 after Close", cli.closes)
			}
		})
	}
}

// TestDialInitializeRequestFields pins the initialize handshake content.
func TestDialInitializeRequestFields(t *testing.T) {
	var recs []transportRecord
	cli := &recordingClient{}
	d := newTestDialer(&recs, cli)

	// Parent with a deadline: Start must NOT inherit it (detached), while
	// Initialize must be bounded by the dial timeout.
	parent, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer cancel()
	if _, err := d.Dial(parent, ServerConfig{Command: "/bin/anything"}); err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if len(cli.inits) != 1 {
		t.Fatalf("Initialize calls = %d, want 1", len(cli.inits))
	}
	req := cli.inits[0].req
	if req.Params.ProtocolVersion != mcp.LATEST_PROTOCOL_VERSION {
		t.Errorf("ProtocolVersion = %q, want %q", req.Params.ProtocolVersion, mcp.LATEST_PROTOCOL_VERSION)
	}
	if req.Params.ClientInfo.Name != "lele" {
		t.Errorf("ClientInfo.Name = %q, want lele", req.Params.ClientInfo.Name)
	}
	if req.Params.ClientInfo.Version == "" {
		t.Error("ClientInfo.Version must not be empty")
	}
	if len(cli.starts) != 1 {
		t.Fatalf("Start calls = %d, want 1", len(cli.starts))
	}
	if _, ok := cli.starts[0].Deadline(); ok {
		t.Error("Start context must be detached (no deadline from the caller or the dial timeout)")
	}
}

// TestDialStartContextSurvivesDialTimeout proves the dial timeout only bounds
// Initialize: a timed-out Initialize must not cancel the Start context (that
// would kill a healthy stdio child spawned by exec.CommandContext).
func TestDialStartContextSurvivesDialTimeout(t *testing.T) {
	var recs []transportRecord
	cli := &recordingClient{blockInit: true}
	d := newTestDialer(&recs, cli)
	d.timeout = 20 * time.Millisecond

	conn, err := d.Dial(context.Background(), ServerConfig{Command: "/bin/anything"})
	if err == nil {
		t.Fatal("expected initialize timeout error")
	}
	if conn != nil {
		t.Fatal("conn must be nil on error")
	}
	if !strings.Contains(err.Error(), "initialize") {
		t.Errorf("error should mention initialize, got: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error should wrap DeadlineExceeded, got: %v", err)
	}
	if len(cli.starts) != 1 {
		t.Fatalf("Start calls = %d, want 1", len(cli.starts))
	}
	if cli.starts[0].Err() != nil {
		t.Errorf("Start context must stay alive after dial timeout, err = %v", cli.starts[0].Err())
	}
	if cli.closes != 1 {
		t.Errorf("closes = %d, want 1 (failed dial must clean up)", cli.closes)
	}
}

// TestDialPreInitSkipsInitialize: an already initialized client only gets Start.
func TestDialPreInitSkipsInitialize(t *testing.T) {
	var recs []transportRecord
	cli := &recordingClient{}
	d := newTestDialer(&recs, cli)
	d.preInit = true

	if _, err := d.Dial(context.Background(), ServerConfig{Command: "/bin/anything"}); err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if len(cli.inits) != 0 {
		t.Errorf("Initialize calls = %d, want 0 for a pre-initialized client", len(cli.inits))
	}
	if len(cli.starts) != 1 {
		t.Errorf("Start calls = %d, want 1", len(cli.starts))
	}
}

// TestDialFailuresCloseClient: start and initialize failures both clean up.
func TestDialFailuresCloseClient(t *testing.T) {
	cases := []struct {
		name      string
		cli       *recordingClient
		wantInErr string
	}{
		{name: "start failure", cli: &recordingClient{startErr: errors.New("spawn failed")}, wantInErr: "start"},
		{name: "initialize failure", cli: &recordingClient{initErr: errors.New("handshake failed")}, wantInErr: "initialize"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var recs []transportRecord
			d := newTestDialer(&recs, tc.cli)
			conn, err := d.Dial(context.Background(), ServerConfig{Command: "/bin/anything"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if conn != nil {
				t.Fatal("conn must be nil on error")
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantInErr)
			}
			if tc.cli.closes != 1 {
				t.Errorf("closes = %d, want 1 (failed dial must not leak the client)", tc.cli.closes)
			}
		})
	}
}

// TestDialUnreachableHTTP: a real dialer against a closed port fails with a
// useful error instead of hanging (bounded by the dial timeout).
func TestDialUnreachableHTTP(t *testing.T) {
	d := newMCPDialer()
	d.timeout = 2 * time.Second
	conn, err := d.Dial(context.Background(), ServerConfig{URL: "http://127.0.0.1:1/mcp", Type: typeHTTP})
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("expected a connection error")
	}
	if conn != nil {
		t.Error("conn must be nil on error")
	}
	if !strings.Contains(err.Error(), "mcp dial") {
		t.Errorf("error should carry the mcp dial prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should mention the underlying failure, got: %v", err)
	}
}

// TestDialUnreachableSSE: the legacy SSE transport fails at Start, not hang.
func TestDialUnreachableSSE(t *testing.T) {
	d := newMCPDialer()
	d.timeout = 2 * time.Second
	conn, err := d.Dial(context.Background(), ServerConfig{URL: "http://127.0.0.1:1/sse", Type: typeSSE})
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("expected a connection error")
	}
	if conn != nil {
		t.Error("conn must be nil on error")
	}
	if !strings.Contains(err.Error(), "start sse transport") {
		t.Errorf("error should mention the sse start failure, got: %v", err)
	}
}

// TestDialBadStdioCommand: a launcher that does not exist fails at Start.
func TestDialBadStdioCommand(t *testing.T) {
	d := newMCPDialer()
	d.timeout = 2 * time.Second
	conn, err := d.Dial(context.Background(), ServerConfig{Command: "/nonexistent/lele-mcp-dial-test"})
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("expected a start error")
	}
	if !strings.Contains(err.Error(), "start stdio transport") {
		t.Errorf("error should mention the stdio start failure, got: %v", err)
	}
}

// TestStdioEnvBaseThenSortedConfigured pins the child environment contract:
// inherited base env first, configured entries after, sorted by key.
func TestStdioEnvBaseThenSortedConfigured(t *testing.T) {
	env := stdioEnv(map[string]string{"ZOO": "1", "ALPHA": "2", "MIDDLE": "3"})
	base := osEnvironLen()
	wantTail := []string{"ALPHA=2", "MIDDLE=3", "ZOO=1"}
	if len(env) != base+len(wantTail) {
		t.Fatalf("env length = %d, want base %d + %d configured", len(env), base, len(wantTail))
	}
	for i, kv := range wantTail {
		if got := env[base+i]; got != kv {
			t.Errorf("env[%d] = %q, want %q", base+i, got, kv)
		}
	}
	// The base must keep the process environment (PATH at minimum) so stdio
	// wrapper scripts keep working.
	foundPath := false
	for _, kv := range env[:base] {
		if strings.HasPrefix(kv, "PATH=") {
			foundPath = true
		}
	}
	if !foundPath {
		t.Error("base environment must contain PATH")
	}
}

// osEnvironLen is a tiny helper kept separate for readability.
func osEnvironLen() int { return len(stdioEnv(nil)) }

// TestNoSecretsLogged: secrets passed as env/headers must reach the transport
// (they are required for auth) but never the log sinks nor the returned
// error strings. The sinks captured are exactly the ones pkg/logger writes to
// (the standard log package) plus the slog default used by mcp-go.
func TestNoSecretsLogged(t *testing.T) {
	const envSecret = "s3cr3t-env-VALUE-9f2a1b"
	const hdrSecret = "s3cr3t-hdr-VALUE-7c1d2e"

	var buf bytes.Buffer
	origLogOut := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(origLogOut) })
	origSlog := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(origSlog) })
	origLevel := logger.GetLevel()
	logger.SetLevel(logger.DEBUG)
	t.Cleanup(func() { logger.SetLevel(origLevel) })

	assertClean := func(stage string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s: expected a dial error", stage)
		}
		if strings.Contains(err.Error(), envSecret) || strings.Contains(err.Error(), hdrSecret) {
			t.Fatalf("%s: error text leaks a secret: %v", stage, err)
		}
		if s := buf.String(); strings.Contains(s, envSecret) || strings.Contains(s, hdrSecret) {
			t.Fatalf("%s: log sink leaks a secret: %s", stage, s)
		}
	}

	d := newMCPDialer()
	d.timeout = 500 * time.Millisecond

	// 1) stdio: secret env handed to the transport, spawn fails.
	_, err := d.Dial(context.Background(), ServerConfig{
		Command: "/nonexistent/lele-mcp-secret-test",
		Env:     map[string]string{"LELE_TEST_SECRET": envSecret},
	})
	assertClean("stdio", err)

	// 2) streamable HTTP: secret header, unreachable endpoint.
	_, err = d.Dial(context.Background(), ServerConfig{
		URL:     "http://127.0.0.1:1/mcp",
		Type:    typeHTTP,
		Headers: map[string]string{"Authorization": "Bearer " + hdrSecret},
	})
	assertClean("http", err)

	// 3) SSE: secret header, unreachable endpoint.
	_, err = d.Dial(context.Background(), ServerConfig{
		URL:     "http://127.0.0.1:1/sse",
		Type:    typeSSE,
		Headers: map[string]string{"X-Api-Key": hdrSecret},
	})
	assertClean("sse", err)

	// 4) The discovery warning path through pkg/logger: a layer with an
	// invalid entry whose env holds the secret must log a warning (proves
	// the sink is actually exercised) without the secret value.
	dir := t.TempDir()
	writeFileAt(t, dir+"/mcp.json", `{
  "mcpServers": {
    "valid": {"command": "x", "env": {"TOKEN": "`+envSecret+`"}},
    "broken": {"command": "x", "url": "http://y", "headers": {"Authorization": "`+hdrSecret+`"}}
  }
}`)
	mgr := NewManager(Paths{LeleDir: dir}, &fakeDialer{})
	_ = mgr.Servers()
	if buf.Len() == 0 {
		t.Fatal("the discovery warning should have reached the pkg/logger console sink")
	}
	if s := buf.String(); strings.Contains(s, envSecret) || strings.Contains(s, hdrSecret) {
		t.Fatalf("discovery warning leaks a secret: %s", s)
	}
}
