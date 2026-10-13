package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// ServerInfo is the identity an MCP server reported in the initialize
// handshake. Empty fields mean the server (or the connection) surfaced none:
// it is diagnostic information, never trusted input.
type ServerInfo struct {
	// Name is the implementation name the server reported.
	Name string
	// Version is the implementation version the server reported.
	Version string
}

// ProbeResult is the outcome of one successful ProbeServer dial. It stays
// deliberately small: it exists to answer "does this server answer, and with
// which tools?", not to replace the manager's cached tool specs.
type ProbeResult struct {
	// Tools holds the server's tool names exactly as the server reported
	// them: server order preserved, no namespacing, no sorting, no dedup.
	// It is never nil (an empty server yields an empty slice).
	Tools []string
	// Server is the identity the server returned in the initialize
	// handshake. It stays zero when there was nothing to report, either
	// because the server omitted it or because the connection was
	// established without a handshake (a substituted Dialer).
	Server ServerInfo
}

// handshakeInfoer is the optional capability ProbeServer uses to recover the
// server identity from a dial: the production clientConn records it while
// performing the initialize handshake. A connection that does not implement
// it (a test double, or a dialer that skipped the handshake) still probes —
// only ProbeResult.Server is left zero.
type handshakeInfoer interface {
	handshakeInfo() ServerInfo
}

// ProbeServer dials the given server config directly (production dial path:
// transport selection, stdio spawn or HTTP/SSE, initialize handshake), lists
// its tools, and closes the connection in all paths. Unlike Manager.LoadServer
// it takes the config as given: no discovery, no disabled filtering — an
// explicitly disabled config is still probed. It is intended for out-of-band
// diagnostics (the `lele mcp test` CLI); runtime tool loading MUST keep going
// through Manager/LoadServer, which never dials disabled servers.
//
// cfg must already be usable: the ${VAR} references of mcp.json are expanded
// by ParseFile — and therefore by Discover and ReadInventory — which is
// exactly how the config reaches the dialer on the runtime path
// (Manager.resolve hands over a Discover entry). Mirroring that path,
// ProbeServer passes cfg to the dialer verbatim: it never expands. Note that
// a disabled entry is NOT in Discover's Servers map; a caller that wants to
// probe one reads it from ParseFile's File.MCPServers, which keeps disabled
// entries.
//
// A cfg that fails validation is refused with the validation error before any
// dial (validate runs on the local copy, so the normalized Type "http" default
// is what reaches the dialer). The connection is owned by ProbeServer and
// closed before it returns — after a successful listing, after a dial error
// and after a list error alike. The initialize handshake is bounded by
// defaultDialTimeout (30s) and ctx cancellation is honoured: a context that is
// already done returns before dialing, and a cancellation that lands while the
// listing is in flight is reported as the context error (the cause), not as
// the transport error it produces.
func ProbeServer(ctx context.Context, cfg ServerConfig) (ProbeResult, error) {
	return probeServer(ctx, cfg, nil)
}

// probeServer is ProbeServer with an injectable dialer: a nil dialer means the
// production mcp dialer, the same convention NewManager uses. It is the seam
// the tests substitute fakes or an in-process server through.
func probeServer(ctx context.Context, cfg ServerConfig, dialer Dialer) (ProbeResult, error) {
	// Validation first and on the local copy: an invalid entry is a config
	// problem, never a connection problem — nothing may be dialed for it.
	if err := cfg.validate(); err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: %w", err)
	}
	// A dead context must not spend a dial (a stdio spawn is a real child
	// process) to learn what it already knows.
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: %w", err)
	}
	if dialer == nil {
		dialer = newMCPDialer()
	}

	conn, err := dialer.Dial(ctx, cfg)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("mcp probe: %w", err)
	}

	result := ProbeResult{}
	if info, ok := conn.(handshakeInfoer); ok {
		result.Server = info.handshakeInfo()
	}

	listed, listErr := conn.ListTools(ctx)
	// Close on every path, including the failing ones: the connection is
	// owned by this call, never cached (a leaked stdio child would outlive
	// the diagnostic).
	closeErr := conn.Close()

	if listErr != nil {
		// The caller's cancellation is the cause worth reporting, not the
		// transport failure it provokes mid-request.
		if ctxErr := ctx.Err(); ctxErr != nil {
			listErr = ctxErr
		}
		if closeErr != nil {
			return ProbeResult{}, errors.Join(
				fmt.Errorf("mcp probe: %w", listErr),
				fmt.Errorf("mcp probe: close: %w", closeErr),
			)
		}
		return ProbeResult{}, fmt.Errorf("mcp probe: %w", listErr)
	}
	result.Tools = toolNames(listed)
	if closeErr != nil {
		// The listing succeeded, so the result is still worth delivering to a
		// diagnostic caller; the error flags the connection that was not torn
		// down cleanly (typically a stuck stdio child).
		return result, fmt.Errorf("mcp probe: close: %w", closeErr)
	}
	return result, nil
}

// toolNames extracts the reported tool names in server order.
func toolNames(listed []mcp.Tool) []string {
	names := make([]string, 0, len(listed))
	for _, t := range listed {
		names = append(names, t.Name)
	}
	return names
}
