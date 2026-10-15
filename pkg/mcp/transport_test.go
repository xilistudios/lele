package mcp

import (
	"bytes"
	"context"
	"errors"
	"log"
	"log/slog"
	"slices"
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

// --- T5: OAuth opt-in at the transport seam --------------------------------

// oauthRecord captures one httpOAuth/sseOAuth constructor call.
type oauthRecord struct {
	kind    string // "httpOAuth" | "sseOAuth"
	url     string
	headers map[string]string
	cfg     transport.OAuthConfig
}

// oauthRecordingFactory records plain and OAuth constructor calls separately,
// so a test can prove WHICH constructor a config routed to.
func oauthRecordingFactory(plain *[]transportRecord, oauth *[]oauthRecord) transportFactory {
	return transportFactory{
		stdio: func(command string, env []string, args ...string) transport.Interface {
			*plain = append(*plain, transportRecord{kind: "stdio", command: command, env: env, args: args})
			return fakeTransport{}
		},
		http: func(serverURL string, headers map[string]string) (transport.Interface, error) {
			*plain = append(*plain, transportRecord{kind: "http", url: serverURL, headers: headers})
			return fakeTransport{}, nil
		},
		sse: func(serverURL string, headers map[string]string) (transport.Interface, error) {
			*plain = append(*plain, transportRecord{kind: "sse", url: serverURL, headers: headers})
			return fakeTransport{}, nil
		},
		httpOAuth: func(serverURL string, headers map[string]string, cfg transport.OAuthConfig) (transport.Interface, error) {
			*oauth = append(*oauth, oauthRecord{kind: "httpOAuth", url: serverURL, headers: headers, cfg: cfg})
			return fakeTransport{}, nil
		},
		sseOAuth: func(serverURL string, headers map[string]string, cfg transport.OAuthConfig) (transport.Interface, error) {
			*oauth = append(*oauth, oauthRecord{kind: "sseOAuth", url: serverURL, headers: headers, cfg: cfg})
			return fakeTransport{}, nil
		},
	}
}

// TestBuildTransportOAuthOptIn is the falsifiable guard of the narrow attach
// rule: OAuth is attached ONLY when the entry opts in explicitly (auth.type ==
// "oauth") or the store records a real 401. Every other shape must take the
// PLAIN constructor (attaching OAuth would break a healthy server, since
// mcp-go's SendRequest returns OAuthAuthorizationRequiredError before sending
// anything when the token store is empty).
func TestBuildTransportOAuthOptIn(t *testing.T) {
	const name = "srv"
	const url = "https://srv.example/mcp"
	const sseURL = "https://srv.example/sse"

	tests := []struct {
		name             string
		cfg              ServerConfig
		seed             func(t *testing.T, store *AuthStore)
		wantKind         string // constructor the dialer must use
		wantClientID     string // OAuth case: ClientID handed to mcp-go
		wantClientSecret string // OAuth case: ClientSecret handed to mcp-go
		wantRedirectURI  string // OAuth case: RedirectURI handed to mcp-go
		wantScopes       []string
		wantPRMURL       string
	}{
		{
			name:     "no auth block and no record => plain http",
			cfg:      ServerConfig{URL: url, Type: typeHTTP},
			wantKind: "http",
		},
		{
			name: "explicit oauth block => httpOAuth with PKCE and scopes",
			cfg: ServerConfig{URL: url, Type: typeHTTP, Auth: &AuthSpec{
				Type: typeAuthOAuth, ClientID: "cid", Scopes: []string{"read", "write"},
			}},
			wantKind:     "httpOAuth",
			wantClientID: "cid",
			wantScopes:   []string{"read", "write"},
		},
		{
			// Falsifiability M-guard: a persisted ClientID alone (residue of a
			// cancelled/failed attempt) must NOT attach OAuth.
			name: "stored client id but not required => plain http",
			cfg:  ServerConfig{URL: url, Type: typeHTTP},
			seed: func(t *testing.T, store *AuthStore) {
				saveRecord(t, store, name, AuthRecord{URL: url, ClientID: "cid"})
			},
			wantKind: "http",
		},
		{
			name: "store records a real 401 => httpOAuth",
			cfg:  ServerConfig{URL: url, Type: typeHTTP},
			seed: func(t *testing.T, store *AuthStore) {
				saveRecord(t, store, name, AuthRecord{URL: url, AuthRequired: true, ClientID: "stored"})
			},
			wantKind:     "httpOAuth",
			wantClientID: "stored",
		},
		{
			name:         "sse with auth => sseOAuth",
			cfg:          ServerConfig{URL: sseURL, Type: typeSSE, Auth: &AuthSpec{Type: typeAuthOAuth, ClientID: "sse-cid"}},
			wantKind:     "sseOAuth",
			wantClientID: "sse-cid",
		},
		{
			name:     "stdio never gets oauth even with an oauth block",
			cfg:      ServerConfig{Command: "/bin/lele-mcp"},
			wantKind: "stdio",
		},
		{
			// The cfg.Command != "" clause of buildAuthRuntime, pinned on its
			// own: a stdio entry carrying an Auth block (constructed directly,
			// bypassing validate) AND an AuthRequired record must still take
			// the stdio path. OAuth is a remote-transport concern — stdio has
			// no HTTP challenge to answer, so attaching it would break a
			// perfectly healthy stdio server.
			name: "stdio with an auth block and an auth-required record stays stdio",
			cfg: ServerConfig{Command: "/bin/lele-mcp", Auth: &AuthSpec{
				Type: typeAuthOAuth, ClientID: "cid",
			}},
			seed: func(t *testing.T, store *AuthStore) {
				saveRecord(t, store, name, AuthRecord{URL: "", AuthRequired: true, ClientID: "stored"})
			},
			wantKind: "stdio",
		},
		{
			// An explicit config value always wins over the stored one.
			name: "explicit client id wins over stored",
			cfg: ServerConfig{URL: url, Type: typeHTTP, Auth: &AuthSpec{
				Type: typeAuthOAuth, ClientID: "explicit",
			}},
			seed: func(t *testing.T, store *AuthStore) {
				saveRecord(t, store, name, AuthRecord{URL: url, ClientID: "stored"})
			},
			wantKind:     "httpOAuth",
			wantClientID: "explicit",
		},
		{
			// An EMPTY field is filled from the persisted record — secret and
			// redirect URI included: they are part of the registration
			// (client_secret / redirect_uris), and a regression that dropped
			// either would have passed while only clientID/PRMURL were pinned.
			name: "empty field filled from stored record",
			cfg: ServerConfig{URL: url, Type: typeHTTP, Auth: &AuthSpec{
				Type: typeAuthOAuth,
			}},
			seed: func(t *testing.T, store *AuthStore) {
				saveRecord(t, store, name, AuthRecord{
					URL:                 url,
					ClientID:            "stored",
					ClientSecret:        "stored-secret",
					RedirectURI:         "http://127.0.0.1:9999/cb",
					ResourceMetadataURL: "https://srv.example/.well-known/oauth-protected-resource",
				})
			},
			wantKind:         "httpOAuth",
			wantClientID:     "stored",
			wantClientSecret: "stored-secret",
			wantRedirectURI:  "http://127.0.0.1:9999/cb",
			wantPRMURL:       "https://srv.example/.well-known/oauth-protected-resource",
		},
		{
			// An explicit config value always wins over the stored one — for
			// the secret and the redirect URI too, not just the client id.
			name: "explicit secret and redirect uri win over the stored record",
			cfg: ServerConfig{URL: url, Type: typeHTTP, Auth: &AuthSpec{
				Type:         typeAuthOAuth,
				ClientSecret: "explicit-secret",
				RedirectURI:  "http://127.0.0.1:7777/cb",
			}},
			seed: func(t *testing.T, store *AuthStore) {
				saveRecord(t, store, name, AuthRecord{
					URL:          url,
					ClientSecret: "stored-secret",
					RedirectURI:  "http://127.0.0.1:9999/cb",
				})
			},
			wantKind:         "httpOAuth",
			wantClientSecret: "explicit-secret",
			wantRedirectURI:  "http://127.0.0.1:7777/cb",
		},
		{
			// A record bound to a different URL must not fill the config.
			name: "record bound to another url is ignored",
			cfg: ServerConfig{URL: url, Type: typeHTTP, Auth: &AuthSpec{
				Type: typeAuthOAuth,
			}},
			seed: func(t *testing.T, store *AuthStore) {
				saveRecord(t, store, name, AuthRecord{URL: "https://other.example/mcp", ClientID: "stale"})
			},
			wantKind:     "httpOAuth",
			wantClientID: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := NewAuthStore(t.TempDir())
			if tc.seed != nil {
				tc.seed(t, store)
			}
			cfg := tc.cfg
			cfg.authRuntime = buildAuthRuntime(store, name, cfg)

			var plain []transportRecord
			var oauth []oauthRecord
			cli := &recordingClient{}
			d := &mcpDialer{
				factory:   oauthRecordingFactory(&plain, &oauth),
				newClient: func(transport.Interface) mcpClient { return cli },
			}

			conn, err := d.Dial(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			if conn == nil {
				t.Fatal("conn is nil")
			}
			t.Cleanup(func() { _ = conn.Close() })

			var gotKind string
			switch {
			case len(plain) == 1 && len(oauth) == 0:
				gotKind = plain[0].kind
			case len(oauth) == 1 && len(plain) == 0:
				gotKind = oauth[0].kind
			default:
				t.Fatalf("want exactly one constructor call, plain=%+v oauth=%+v", plain, oauth)
			}
			if gotKind != tc.wantKind {
				t.Fatalf("transport kind = %q, want %q", gotKind, tc.wantKind)
			}

			if len(oauth) == 0 {
				return // plain path: nothing more to assert
			}
			oc := oauth[0]
			if !oc.cfg.PKCEEnabled {
				t.Error("PKCEEnabled = false, want true")
			}
			if oc.cfg.TokenStore == nil {
				t.Error("TokenStore must be non-nil on an OAuth transport")
			}
			if oc.cfg.ClientID != tc.wantClientID {
				t.Errorf("ClientID = %q, want %q", oc.cfg.ClientID, tc.wantClientID)
			}
			if oc.cfg.ClientSecret != tc.wantClientSecret {
				t.Errorf("ClientSecret = %q, want %q", oc.cfg.ClientSecret, tc.wantClientSecret)
			}
			if oc.cfg.RedirectURI != tc.wantRedirectURI {
				t.Errorf("RedirectURI = %q, want %q", oc.cfg.RedirectURI, tc.wantRedirectURI)
			}
			if len(tc.wantScopes) > 0 {
				if !slices.Equal(oc.cfg.Scopes, tc.wantScopes) {
					t.Errorf("Scopes = %v, want %v", oc.cfg.Scopes, tc.wantScopes)
				}
			}
			if tc.wantPRMURL != "" && oc.cfg.ProtectedResourceMetadataURL != tc.wantPRMURL {
				t.Errorf("ProtectedResourceMetadataURL = %q, want %q", oc.cfg.ProtectedResourceMetadataURL, tc.wantPRMURL)
			}
			if oc.url != cfg.URL {
				t.Errorf("url = %q, want %q", oc.url, cfg.URL)
			}
		})
	}
}

// saveRecord writes a record through the store, failing the test on error.
func saveRecord(t *testing.T, store *AuthStore, name string, rec AuthRecord) {
	t.Helper()
	if err := store.Save(name, rec); err != nil {
		t.Fatalf("Save(%q): %v", name, err)
	}
}

// TestDialMapsAuthorizationChallengeToErrAuthRequired proves an authorization
// challenge from Start or Initialize is reported as ErrAuthRequired (so the
// caller runs the auth flow) and that the message carries no URL or token.
func TestDialMapsAuthorizationChallengeToErrAuthRequired(t *testing.T) {
	cases := []struct {
		name string
		cli  *recordingClient
	}{
		{
			name: "initialize: oauth authorization required",
			cli:  &recordingClient{initErr: &transport.OAuthAuthorizationRequiredError{}},
		},
		{
			name: "initialize: authorization required",
			cli:  &recordingClient{initErr: &transport.AuthorizationRequiredError{}},
		},
		{
			name: "start: oauth authorization required",
			cli:  &recordingClient{startErr: &transport.OAuthAuthorizationRequiredError{}},
		},
		{
			name: "start: authorization required",
			cli:  &recordingClient{startErr: &transport.AuthorizationRequiredError{}},
		},
		{
			// The PRODUCTION wrapping shape: mcp-go's sendRequest wraps every
			// send failure in transport.NewError, so this is what Dial actually
			// sees for a challenge on the wire.
			name: "initialize: transport.NewError(oauth authorization required)",
			cli:  &recordingClient{initErr: transport.NewError(&transport.OAuthAuthorizationRequiredError{})},
		},
		{
			name: "initialize: transport.NewError(authorization required)",
			cli:  &recordingClient{initErr: transport.NewError(&transport.AuthorizationRequiredError{})},
		},
		{
			name: "start: transport.NewError(oauth authorization required)",
			cli:  &recordingClient{startErr: transport.NewError(&transport.OAuthAuthorizationRequiredError{})},
		},
		{
			name: "start: transport.NewError(authorization required)",
			cli:  &recordingClient{startErr: transport.NewError(&transport.AuthorizationRequiredError{})},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var recs []transportRecord
			d := newTestDialer(&recs, tc.cli)

			conn, err := d.Dial(context.Background(), ServerConfig{URL: "https://srv.example/mcp", Type: typeHTTP})
			if err == nil {
				if conn != nil {
					_ = conn.Close()
				}
				t.Fatal("expected an authorization error")
			}
			if conn != nil {
				t.Error("conn must be nil on error")
			}
			if !errors.Is(err, ErrAuthRequired) {
				t.Fatalf("errors.Is(err, ErrAuthRequired) = false; err = %v", err)
			}
			if strings.Contains(err.Error(), "https://") {
				t.Errorf("error must not carry a URL: %v", err)
			}
			if strings.Contains(err.Error(), "Bearer") {
				t.Errorf("error must not carry a token: %v", err)
			}
			if tc.cli.closes != 1 {
				t.Errorf("closes = %d, want 1 (a failed dial must not leak the client)", tc.cli.closes)
			}
		})
	}
}
