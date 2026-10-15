package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// clientInfo identifies lele in the MCP initialize handshake.
const (
	clientName    = "lele"
	clientVersion = "0.1.0"
)

// defaultDialTimeout bounds Initialize (and waiting for Start) inside Dial.
// A server that accepts a connection but never answers is cut off here so a
// dead server cannot wedge agent startup.
const defaultDialTimeout = 30 * time.Second

// ClientConn is one open connection to an MCP server. It is the seam between
// the manager (T5) and the mcp-go client: callers see only listing, calling
// and closing, never the underlying transport.
type ClientConn interface {
	// ListTools returns the server's tool specifications.
	ListTools(ctx context.Context) ([]mcp.Tool, error)
	// CallTool invokes one remote tool by its original MCP name.
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)
	// Close releases the connection (terminating a stdio child process).
	// It is idempotent.
	Close() error
}

// Dialer opens a ClientConn for a server config. It is injected into the
// manager so tests can substitute a fake without spawning processes.
type Dialer interface {
	Dial(ctx context.Context, srv ServerConfig) (ClientConn, error)
}

// mcpClient is the subset of *client.Client the dialer drives. It exists as
// a seam so tests can assert Start/Initialize behavior without any real
// transport (the mcp-go client is concrete).
type mcpClient interface {
	Start(ctx context.Context) error
	Initialize(ctx context.Context, request mcp.InitializeRequest) (*mcp.InitializeResult, error)
	ListTools(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
	Close() error
}

// transportFactory constructs the three mcp-go transports. It is a struct of
// functions (not interfaces) so tests can record each constructor's
// arguments and prove which ServerConfig shape routes where.
type transportFactory struct {
	stdio func(command string, env []string, args ...string) transport.Interface
	http  func(serverURL string, headers map[string]string) (transport.Interface, error)
	sse   func(serverURL string, headers map[string]string) (transport.Interface, error)
	// httpOAuth / sseOAuth build the remote transports WITH OAuth attached.
	// They are separate fields rather than an extra parameter on http/sse so
	// every existing recording fake keeps its signature. nil ⇒ default.
	httpOAuth func(serverURL string, headers map[string]string, cfg transport.OAuthConfig) (transport.Interface, error)
	sseOAuth  func(serverURL string, headers map[string]string, cfg transport.OAuthConfig) (transport.Interface, error)
}

// mcpDialer is the production Dialer.
type mcpDialer struct {
	timeout   time.Duration
	preInit   bool             // injected client already ran Initialize: skip ours
	factory   transportFactory // seam: which transport gets built
	newClient func(transport.Interface) mcpClient
}

// newMCPDialer returns the default dialer: real mcp-go transports and client.
func newMCPDialer() *mcpDialer {
	return &mcpDialer{
		timeout:   defaultDialTimeout,
		factory:   defaultTransportFactory(),
		newClient: func(tr transport.Interface) mcpClient { return client.NewClient(tr) },
	}
}

// defaultTransportFactory wires the mcp-go constructors. Note on stdio:
// stderr is drained continuously by the transport into a bounded 64 KiB
// drop-oldest ring buffer (mirrored to a writer that defaults to io.Discard),
// so the OS pipe can never fill up and block the child — no deadlock and no
// unbounded memory. We deliberately never surface that ring into logs: the
// child's stderr may carry server-side secrets.
func defaultTransportFactory() transportFactory {
	return transportFactory{
		stdio: func(command string, env []string, args ...string) transport.Interface {
			return transport.NewStdio(command, env, args...)
		},
		http: func(serverURL string, headers map[string]string) (transport.Interface, error) {
			return transport.NewStreamableHTTP(serverURL, transport.WithHTTPHeaders(headers))
		},
		sse: func(serverURL string, headers map[string]string) (transport.Interface, error) {
			return transport.NewSSE(serverURL, transport.WithHeaders(headers))
		},
		httpOAuth: func(serverURL string, headers map[string]string, c transport.OAuthConfig) (transport.Interface, error) {
			return transport.NewStreamableHTTP(serverURL, transport.WithHTTPHeaders(headers), transport.WithHTTPOAuth(c))
		},
		sseOAuth: func(serverURL string, headers map[string]string, c transport.OAuthConfig) (transport.Interface, error) {
			return transport.NewSSE(serverURL, transport.WithHeaders(headers), transport.WithOAuth(c))
		},
	}
}

// Dial connects, starts and initializes one MCP server, returning a ready
// ClientConn. On any failure the half-open connection is closed before the
// error is returned (no leaked stdio children).
//
// Context handling: Start runs on a detached context because transports bind
// long-lived resources to it — mcp-go's stdio transport spawns the child via
// exec.CommandContext(ctx) and receive loops select on ctx.Done(). Cancelling
// the caller's context (or the dial timeout) after a successful dial would
// kill a healthy connection. Connection lifetime is owned by ClientConn.Close.
// Initialize is bounded by the dial timeout, which still honors caller
// cancellation. The server identity carried by the initialize result is kept
// on the returned connection for out-of-band diagnostics (ProbeServer); the
// manager path never reads it.
//
// Never log srv.Env or srv.Headers values anywhere on this path: they hold
// API keys (TestNoSecretsLogged guards this).
func (d *mcpDialer) Dial(ctx context.Context, srv ServerConfig) (ClientConn, error) {
	tr, err := d.buildTransport(srv)
	if err != nil {
		return nil, fmt.Errorf("mcp dial: %w", err)
	}
	cli := d.newClient(tr)
	if err := cli.Start(context.WithoutCancel(ctx)); err != nil {
		_ = cli.Close()
		if authRequiredError(err) {
			return nil, fmt.Errorf("mcp dial: %s transport: %w", transportKind(srv), ErrAuthRequired)
		}
		return nil, fmt.Errorf("mcp dial: start %s transport: %w", transportKind(srv), err)
	}
	conn := &clientConn{cli: cli}
	if !d.preInit {
		initCtx, cancel := context.WithTimeout(ctx, d.dialTimeout())
		defer cancel()
		res, err := cli.Initialize(initCtx, initializeRequest())
		if err != nil {
			_ = cli.Close()
			if authRequiredError(err) {
				return nil, fmt.Errorf("mcp dial: %s transport: %w", transportKind(srv), ErrAuthRequired)
			}
			return nil, fmt.Errorf("mcp dial: initialize %s transport: %w", transportKind(srv), err)
		}
		// Kept for out-of-band diagnostics only (ProbeServer); the manager
		// never reads it. A server may legitimately omit its identity.
		conn.handshake = serverInfoOf(res)
	}
	return conn, nil
}

// serverInfoOf extracts the identity of an initialize result; a nil result or
// an omitted implementation yields the zero ServerInfo.
func serverInfoOf(res *mcp.InitializeResult) ServerInfo {
	if res == nil {
		return ServerInfo{}
	}
	return ServerInfo{Name: res.ServerInfo.Name, Version: res.ServerInfo.Version}
}

// dialTimeout returns the configured timeout, falling back to the default.
func (d *mcpDialer) dialTimeout() time.Duration {
	if d.timeout > 0 {
		return d.timeout
	}
	return defaultDialTimeout
}

// buildTransport maps a ServerConfig onto one of the three transports:
// command ⇒ stdio, url + type "sse" ⇒ SSE, url + type ""/"http" ⇒ streamable
// HTTP. Header values are passed to the transport (required for auth) and
// never logged.
//
// When srv.authRuntime is set (the manager resolved this dial as opted into
// OAuth), the remote transport is built through httpOAuth/sseOAuth so mcp-go
// attaches its oauthHandler. When it is nil the plain constructors are used,
// byte-for-byte as before: attaching OAuth to a server that does not need it
// would make SendRequest fail with OAuthAuthorizationRequiredError before
// sending anything. stdio never gets OAuth.
func (d *mcpDialer) buildTransport(srv ServerConfig) (transport.Interface, error) {
	f := d.factory
	if f.stdio == nil || f.http == nil || f.sse == nil || f.httpOAuth == nil || f.sseOAuth == nil {
		def := defaultTransportFactory()
		if f.stdio == nil {
			f.stdio = def.stdio
		}
		if f.http == nil {
			f.http = def.http
		}
		if f.sse == nil {
			f.sse = def.sse
		}
		if f.httpOAuth == nil {
			f.httpOAuth = def.httpOAuth
		}
		if f.sseOAuth == nil {
			f.sseOAuth = def.sseOAuth
		}
	}
	switch {
	case srv.Command != "":
		// env is the base process environment plus the configured entries
		// (sorted for determinism; configured wins because os/exec keeps
		// the last duplicate key and mcp-go appends os.Environ() again
		// before ours).
		return f.stdio(srv.Command, stdioEnv(srv.Env), srv.Args...), nil
	case srv.URL != "":
		var oauth *transport.OAuthConfig
		if srv.authRuntime != nil {
			c := srv.authRuntime.oauthConfig()
			oauth = &c
		}
		switch srv.Type {
		case typeSSE:
			if oauth != nil {
				return f.sseOAuth(srv.URL, srv.Headers, *oauth)
			}
			return f.sse(srv.URL, srv.Headers)
		case "", typeHTTP:
			if oauth != nil {
				return f.httpOAuth(srv.URL, srv.Headers, *oauth)
			}
			return f.http(srv.URL, srv.Headers)
		default:
			return nil, fmt.Errorf("unsupported transport type %q", srv.Type)
		}
	default:
		return nil, errors.New("server config has neither command (stdio) nor url (remote)")
	}
}

// stdioEnv builds the child environment: the inherited process environment
// (PATH, HOME, … — stdio servers are usually wrappers that need it) followed
// by the configured KEY=VALUE entries in sorted order for determinism.
// The values themselves must never reach a log sink.
func stdioEnv(env map[string]string) []string {
	base := os.Environ()
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(base)+len(keys))
	out = append(out, base...)
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

// transportKind names the transport for error messages (never the URL or
// headers — those may carry secrets).
func transportKind(srv ServerConfig) string {
	if srv.Command != "" {
		return "stdio"
	}
	if srv.Type == typeSSE {
		return "sse"
	}
	return "http"
}

// ErrAuthRequired marks a dial that reached a server demanding (OAuth)
// authorization. It is a sentinel so callers can answer "run the auth flow"
// instead of "the transport broke".
var ErrAuthRequired = errors.New("authentication required")

// authRequiredError reports whether err is mcp-go's authorization challenge.
//
// Both the pointer types and the two sentinels are checked: mcp-go returns the
// OAuth-flavoured error when an oauthHandler is attached and the bare one when
// it is not, and either can arrive directly or wrapped. Using errors.As/Is (not
// a string match) keeps the test exact and future-proof against mcp-go adding
// context to the message.
func authRequiredError(err error) bool {
	if err == nil {
		return false
	}
	var oauthErr *transport.OAuthAuthorizationRequiredError
	if errors.As(err, &oauthErr) {
		return true
	}
	var authErr *transport.AuthorizationRequiredError
	if errors.As(err, &authErr) {
		return true
	}
	return errors.Is(err, transport.ErrAuthorizationRequired) ||
		errors.Is(err, transport.ErrOAuthAuthorizationRequired)
}

// initializeRequest builds the MCP initialize request: latest protocol
// version and lele's client identity.
func initializeRequest() mcp.InitializeRequest {
	var req mcp.InitializeRequest
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: clientName, Version: clientVersion}
	req.Params.Capabilities = mcp.ClientCapabilities{}
	return req
}

// clientConn adapts *client.Client to ClientConn.
type clientConn struct {
	cli mcpClient
	// handshake is the server identity captured from the initialize result
	// (zero when the handshake was skipped or reported none). It is consumed
	// only by ProbeServer, through handshakeInfo.
	handshake ServerInfo
}

var _ ClientConn = (*clientConn)(nil)

// handshakeInfo reports the identity the server returned in the initialize
// handshake. It implements the optional interface ProbeServer looks for;
// connections built without a handshake simply do not implement it.
func (c *clientConn) handshakeInfo() ServerInfo { return c.handshake }

// ListTools returns the server's tools (uncached here; the manager caches).
func (c *clientConn) ListTools(ctx context.Context) ([]mcp.Tool, error) {
	res, err := c.cli.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	if res == nil {
		return nil, nil
	}
	return res.Tools, nil
}

// CallTool forwards one tool call by its original MCP name.
func (c *clientConn) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	var req mcp.CallToolRequest
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.cli.CallTool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("call tool %q: %w", name, err)
	}
	return res, nil
}

// Close tears the connection down (idempotent: mcp-go cleans up once).
func (c *clientConn) Close() error { return c.cli.Close() }
