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

const (
	clientName    = "lele"
	clientVersion = "0.1.0"
)

const defaultDialTimeout = 30 * time.Second

type ClientConn interface {
	ListTools(ctx context.Context) ([]mcp.Tool, error)
	CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error)
	Close() error
}

type Dialer interface {
	Dial(ctx context.Context, srv ServerConfig) (ClientConn, error)
}

type mcpClient interface {
	Start(ctx context.Context) error
	Initialize(ctx context.Context, request mcp.InitializeRequest) (*mcp.InitializeResult, error)
	ListTools(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
	Close() error
}

type transportFactory struct {
	stdio     func(command string, env []string, args ...string) transport.Interface
	http      func(serverURL string, headers map[string]string) (transport.Interface, error)
	sse       func(serverURL string, headers map[string]string) (transport.Interface, error)
	httpOAuth func(serverURL string, headers map[string]string, cfg transport.OAuthConfig) (transport.Interface, error)
	sseOAuth  func(serverURL string, headers map[string]string, cfg transport.OAuthConfig) (transport.Interface, error)
}

type mcpDialer struct {
	timeout   time.Duration
	preInit   bool
	factory   transportFactory
	newClient func(transport.Interface) mcpClient
}

func newMCPDialer() *mcpDialer {
	return &mcpDialer{
		timeout:   defaultDialTimeout,
		factory:   defaultTransportFactory(),
		newClient: func(tr transport.Interface) mcpClient { return client.NewClient(tr) },
	}
}

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
		conn.handshake = serverInfoOf(res)
	}
	return conn, nil
}

func serverInfoOf(res *mcp.InitializeResult) ServerInfo {
	if res == nil {
		return ServerInfo{}
	}
	return ServerInfo{Name: res.ServerInfo.Name, Version: res.ServerInfo.Version}
}

func (d *mcpDialer) dialTimeout() time.Duration {
	if d.timeout > 0 {
		return d.timeout
	}
	return defaultDialTimeout
}

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

func transportKind(srv ServerConfig) string {
	if srv.Command != "" {
		return "stdio"
	}
	if srv.Type == typeSSE {
		return "sse"
	}
	return "http"
}

var ErrAuthRequired = errors.New("authentication required")

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

func initializeRequest() mcp.InitializeRequest {
	var req mcp.InitializeRequest
	req.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	req.Params.ClientInfo = mcp.Implementation{Name: clientName, Version: clientVersion}
	req.Params.Capabilities = mcp.ClientCapabilities{}
	return req
}

type clientConn struct {
	cli       mcpClient
	handshake ServerInfo
}

var _ ClientConn = (*clientConn)(nil)

func (c *clientConn) handshakeInfo() ServerInfo { return c.handshake }

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

func (c *clientConn) Close() error { return c.cli.Close() }
