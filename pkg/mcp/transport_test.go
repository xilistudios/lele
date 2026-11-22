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

type fakeTransport struct{}

func (fakeTransport) Start(context.Context) error { return nil }
func (fakeTransport) SendRequest(context.Context, transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	return nil, nil
}
func (fakeTransport) SendNotification(context.Context, mcp.JSONRPCNotification) error { return nil }
func (fakeTransport) SetNotificationHandler(func(mcp.JSONRPCNotification))            {}
func (fakeTransport) Close() error                                                    { return nil }
func (fakeTransport) GetSessionId() string                                            { return "" }

type recordingClient struct {
	starts []context.Context
	inits  []struct {
		ctx context.Context
		req mcp.InitializeRequest
	}
	closes    int
	startErr  error
	initErr   error
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

type transportRecord struct {
	kind    string
	command string
	args    []string
	env     []string
	url     string
	headers map[string]string
}

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

func newTestDialer(recs *[]transportRecord, cli mcpClient) *mcpDialer {
	return &mcpDialer{
		factory:   recordingFactory(recs),
		newClient: func(transport.Interface) mcpClient { return cli },
	}
}

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

func TestDialInitializeRequestFields(t *testing.T) {
	var recs []transportRecord
	cli := &recordingClient{}
	d := newTestDialer(&recs, cli)

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

func osEnvironLen() int { return len(stdioEnv(nil)) }

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

	_, err := d.Dial(context.Background(), ServerConfig{
		Command: "/nonexistent/lele-mcp-secret-test",
		Env:     map[string]string{"LELE_TEST_SECRET": envSecret},
	})
	assertClean("stdio", err)

	_, err = d.Dial(context.Background(), ServerConfig{
		URL:     "http://127.0.0.1:1/mcp",
		Type:    typeHTTP,
		Headers: map[string]string{"Authorization": "Bearer " + hdrSecret},
	})
	assertClean("http", err)

	_, err = d.Dial(context.Background(), ServerConfig{
		URL:     "http://127.0.0.1:1/sse",
		Type:    typeSSE,
		Headers: map[string]string{"X-Api-Key": hdrSecret},
	})
	assertClean("sse", err)

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

type oauthRecord struct {
	kind    string
	url     string
	headers map[string]string
	cfg     transport.OAuthConfig
}

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

func TestBuildTransportOAuthOptIn(t *testing.T) {
	const name = "srv"
	const url = "https://srv.example/mcp"
	const sseURL = "https://srv.example/sse"

	tests := []struct {
		name             string
		cfg              ServerConfig
		seed             func(t *testing.T, store *AuthStore)
		wantKind         string
		wantClientID     string
		wantClientSecret string
		wantRedirectURI  string
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
				return
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

func saveRecord(t *testing.T, store *AuthStore, name string, rec AuthRecord) {
	t.Helper()
	if err := store.Save(name, rec); err != nil {
		t.Fatalf("Save(%q): %v", name, err)
	}
}

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
