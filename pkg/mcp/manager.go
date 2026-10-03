package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tools"
)

// Manager owns the per-agent set of MCP servers: lazy connections (dial on
// first use), cached tool specs and reconnect-once semantics. It is safe for
// concurrent use.
//
// Logging policy: only server names, transport kinds, layer names and error
// strings ever reach a log sink — never srv.Env or srv.Headers values, which
// hold API keys (guarded by TestNoSecretsLogged).
type Manager struct {
	paths  Paths
	dialer Dialer

	mu sync.Mutex
	// conns caches open connections, keyed by server name (nil until first use).
	conns map[string]ClientConn
	// loaded caches tool specs per server: after one successful LoadServer,
	// further calls return the slice without dialing.
	loaded map[string][]tools.LoadedMCPTool
	// inflight dedupes concurrent loads of the same server (singleflight).
	inflight map[string]*loadFlight
	// fps fingerprints the winning ServerConfig each server was last resolved
	// with. When it changes (mcp.json edited), the live connection and the
	// cached specs of that server are retired lazily on the next use — see
	// applyConfigChange.
	fps map[string]string
	// lastWarning is the text of the last discovery warning logged. An
	// identical repeat (the same malformed layer, once per remote call) is
	// suppressed so a broken file cannot spam the log.
	lastWarning string
	// closed is set by Close; new dials are rejected afterwards so a
	// shutdown race cannot leak a freshly spawned stdio child.
	closed bool
}

// loadFlight coordinates one in-flight LoadServer: waiters block on done and
// then observe specs/err (published before close).
type loadFlight struct {
	done  chan struct{}
	specs []tools.LoadedMCPTool
	err   error
}

// NewManager builds a manager over the given layer paths. A nil dialer means
// "use the real mcp-go dialer" (tests inject a fake).
func NewManager(paths Paths, dialer Dialer) *Manager {
	if dialer == nil {
		dialer = newMCPDialer()
	}
	return &Manager{
		paths:    paths,
		dialer:   dialer,
		conns:    make(map[string]ClientConn),
		loaded:   make(map[string][]tools.LoadedMCPTool),
		inflight: make(map[string]*loadFlight),
		fps:      make(map[string]string),
	}
}

// discover re-reads the three mcp.json layers (at most three small files) and
// logs non-fatal warnings. Discovery errors are warnings by contract — the
// result is always usable — and their text carries paths and validation
// messages, never expanded secret values.
//
// The same warning is logged only when its text changes: discover runs on
// every LoadServer/CallRemote, so a malformed layer must not emit one line
// per remote call. A later error-free discovery re-arms the dedup.
func (m *Manager) discover() DiscoveryResult {
	res, err := Discover(m.paths)
	if err == nil {
		m.mu.Lock()
		m.lastWarning = ""
		m.mu.Unlock()
		return res
	}
	msg := err.Error()
	m.mu.Lock()
	changed := m.lastWarning != msg
	m.lastWarning = msg
	m.mu.Unlock()
	if changed {
		logger.WarnCF("mcp", "mcp.json discovery reported problems", map[string]interface{}{
			"error": msg,
		})
	}
	return res
}

// Servers returns the configured servers sorted by name with their provenance
// layer, excluding disabled and invalid ones. This feeds the system prompt's
// "## MCP Servers" section (names + descriptions only, no tool schemas).
// The mcp.json layers are re-read on every call, so an edit (a new name, a
// changed description or URL) shows up on the next sync without a restart.
func (m *Manager) Servers() []tools.MCPServerInfo {
	res := m.discover()
	infos := make([]tools.MCPServerInfo, 0, len(res.Names))
	for _, name := range res.Names {
		infos = append(infos, tools.MCPServerInfo{
			Name:        name,
			Description: res.Servers[name].Description,
			Layer:       res.Layers[name],
		})
	}
	return infos
}

// resolve returns the active config for name or a descriptive error (a
// disabled server gets its own message naming the layer that disabled it).
// The error deliberately does NOT list the known servers: the authoritative
// list is appended exactly once by the loader layer (pkg/tools loadError),
// so a manager error flowing through it never yields "known servers: …"
// twice in the final message. On success the winning config is fingerprinted
// so an edit to the entry retires the connection/specs of the previous
// config (applyConfigChange).
func (m *Manager) resolve(name string) (ServerConfig, error) {
	res := m.discover()
	if cfg, ok := res.Servers[name]; ok {
		m.applyConfigChange(name, cfg)
		return cfg, nil
	}
	if layer, ok := res.Disabled[name]; ok {
		return ServerConfig{}, fmt.Errorf("mcp server %q is disabled in the %s layer", name, layer)
	}
	return ServerConfig{}, fmt.Errorf("mcp server %q is not configured", name)
}

// applyConfigChange records the fingerprint of name's winning config and, on
// the first change since the server was last resolved, retires everything
// produced by the previous config: the cached specs and the live connection
// (closed immediately via dropConn, which only unmaps it if it is still the
// cached one). The next LoadServer/CallRemote dials lazily with the new
// config; a call already in flight on the retired connection fails at the
// transport level and is recovered by the reconnect-once retry, which
// RE-RESOLVES the config before redialing (fetchTools/CallRemote) — so the
// retry dials the config that is current at that moment, not the retired one
// it entered with. Because fps already advanced here, that re-resolve never
// retires the freshly redialed connection.
func (m *Manager) applyConfigChange(name string, cfg ServerConfig) {
	fp := configFingerprint(cfg)
	m.mu.Lock()
	prev, seen := m.fps[name]
	m.fps[name] = fp
	if !seen || prev == fp {
		m.mu.Unlock()
		return
	}
	delete(m.loaded, name)
	stale := m.conns[name]
	m.mu.Unlock()
	if stale != nil {
		m.dropConn(name, stale)
	}
}

// configFingerprint hashes the normalized JSON of one server entry. The
// struct's field order is fixed and encoding/json sorts map keys, so equal
// configs always hash equal; FNV-1a keeps the check cheap enough to run on
// every resolve.
func configFingerprint(cfg ServerConfig) string {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "" // unreachable for ServerConfig; never falsely "changed"
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	return fmt.Sprintf("%016x", h.Sum64())
}

// LoadServer returns the tool specs of one server, dialing lazily on the
// first call. It is idempotent (cached specs are returned without dialing)
// and deduped per server: N concurrent calls result in exactly one dial, the
// others wait and observe the same result. Registration into the ToolRegistry
// is the caller's job (see tools.LoadMCPToolsTool).
func (m *Manager) LoadServer(ctx context.Context, name string) ([]tools.LoadedMCPTool, error) {
	cfg, err := m.resolve(name)
	if err != nil {
		return nil, err
	}
	// Fingerprint captured BEFORE the (slow) fetch: a config edit landing
	// mid-flight advances m.fps[name] — and applyConfigChange clears the
	// cached specs — while this load still carries the v1-era cfg. The store
	// below compares against this captured value so a stale flight can never
	// re-poison the cache it is about to overwrite (N1). The specs are still
	// returned to THIS caller: it asked while v1 was current, and the next
	// call will re-dial with v2.
	fp := configFingerprint(cfg)

	m.mu.Lock()
	if specs, ok := m.loaded[name]; ok {
		m.mu.Unlock()
		// A cancelled caller gets its own error back instead of cached specs
		// it cannot use — cheap and less surprising than a silent success.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("mcp load %q: %w", name, err)
		}
		return specs, nil
	}
	if flight, ok := m.inflight[name]; ok {
		m.mu.Unlock()
		select {
		case <-flight.done:
			return flight.specs, flight.err
		case <-ctx.Done():
			return nil, fmt.Errorf("mcp load %q: %w", name, ctx.Err())
		}
	}
	flight := &loadFlight{done: make(chan struct{})}
	m.inflight[name] = flight
	m.mu.Unlock()

	remoteTools, err := m.fetchTools(ctx, name, cfg)
	var specs []tools.LoadedMCPTool
	if err == nil {
		specs = buildSpecs(name, remoteTools)
	}

	m.mu.Lock()
	delete(m.inflight, name)
	// Store ONLY while this flight's config is still the winning one: a
	// mid-flight edit (fps advanced, cache cleared, conn dropped by
	// applyConfigChange) or a Close must not resurrect the stale specs. The
	// flight's result is published to the waiters either way.
	if err == nil && !m.closed && m.fps[name] == fp {
		m.loaded[name] = specs
	}
	m.mu.Unlock()

	flight.specs, flight.err = specs, err
	close(flight.done)
	return specs, err
}

// fetchTools lists a server's tools through the (possibly cached) connection
// and retries exactly once after dropping a connection that failed at the
// transport level. Non-transport failures (MCP error responses, caller
// cancellation) are returned as-is.
func (m *Manager) fetchTools(ctx context.Context, name string, cfg ServerConfig) ([]mcp.Tool, error) {
	conn, err := m.conn(ctx, name, cfg)
	if err != nil {
		return nil, fmt.Errorf("mcp load %q: %w", name, err)
	}
	list, err := conn.ListTools(ctx)
	if err == nil || ctx.Err() != nil || !isTransportFailure(err) {
		return list, wrapLoad(name, err)
	}
	// The conn that failed is the one being retired: pass it explicitly so a
	// newer connection installed by a concurrent redial is left alone.
	m.dropConn(name, conn)
	// Re-resolve BEFORE redialing: a mcp.json edit landing mid-call retired
	// the entry-time cfg above (fps advanced, specs cleared, conn dropped),
	// so redialing with it would install a v1 connection that no later
	// resolve ever retires again (fps already equals v2). If the server
	// vanished between the failure and the retry, keep the original cfg —
	// the dial will fail cleanly, as before.
	cfg2, err2 := m.resolve(name)
	if err2 != nil {
		cfg2 = cfg
	}
	conn, err = m.conn(ctx, name, cfg2)
	if err != nil {
		return nil, fmt.Errorf("mcp load %q: reconnect: %w", name, err)
	}
	list, err = conn.ListTools(ctx)
	if err != nil {
		return nil, wrapLoad(name, err)
	}
	return list, nil
}

// wrapLoad contextualizes a list failure; success wraps to nil.
func wrapLoad(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("mcp load %q: %w", name, err)
}

// buildSpecs converts a server's raw tool list into registry-ready specs.
// The registered Name is computed here (pkg/tools cannot call
// NamespacedName); pkg/tools only checks registry.Get(spec.Name). Every spec
// is stamped with its Server so ToolFactory binds the RemoteTool from the
// spec itself — no lookup in the loaded cache (which a mid-flight config
// change deliberately leaves empty, NEW-2).
func buildSpecs(server string, remoteTools []mcp.Tool) []tools.LoadedMCPTool {
	specs := make([]tools.LoadedMCPTool, 0, len(remoteTools))
	for _, t := range remoteTools {
		specs = append(specs, tools.LoadedMCPTool{
			Name:        NamespacedName(server, t.Name),
			Server:      server,
			RemoteName:  t.Name,
			Description: t.Description,
			InputSchema: SchemaFromMCPTool(t),
		})
	}
	return specs
}

// CallRemote invokes a remote tool by its original MCP name. The connection
// is dialed lazily; if it fails at the transport level, the connection is
// dropped, re-dialed and the call retried exactly once (then a clean wrapped
// error is returned). Note: a call that dies mid-flight is retried — the plan
// accepts the (small) double-execution risk in exchange for transparent
// recovery from a crashed stdio child.
func (m *Manager) CallRemote(ctx context.Context, server, remoteTool string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	cfg, err := m.resolve(server)
	if err != nil {
		return nil, err
	}
	conn, err := m.conn(ctx, server, cfg)
	if err != nil {
		return nil, err
	}
	res, err := conn.CallTool(ctx, remoteTool, args)
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil || !isTransportFailure(err) {
		return nil, fmt.Errorf("mcp call %s.%s: %w", server, remoteTool, err)
	}
	// Same rule as fetchTools: retire exactly the conn that failed, then
	// re-resolve so the single redial dials the config that is current now
	// (a mid-flight edit retired the entry-time cfg) — falling back to the
	// original cfg when the server vanished between failure and retry.
	m.dropConn(server, conn)
	cfg2, err2 := m.resolve(server)
	if err2 != nil {
		cfg2 = cfg
	}
	conn, err = m.conn(ctx, server, cfg2)
	if err != nil {
		return nil, fmt.Errorf("mcp call %s.%s: reconnect: %w", server, remoteTool, err)
	}
	res, err = conn.CallTool(ctx, remoteTool, args)
	if err != nil {
		return nil, fmt.Errorf("mcp call %s.%s: %w", server, remoteTool, err)
	}
	return res, nil
}

// serverOf reverse-looks-up which server owns a registered tool name in the
// loaded set. It is only the defensive fallback of ToolFactory, for specs
// that carry no Server of their own (foreign specs built by a non-mcp
// loader): a spec produced by buildSpecs always binds via spec.Server, since
// the loaded cache a mid-flight config change clears cannot be the source of
// truth (NEW-2). Returns "" when the name is not loaded.
func (m *Manager) serverOf(registered string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for server, specs := range m.loaded {
		for _, spec := range specs {
			if spec.Name == registered {
				return server
			}
		}
	}
	return ""
}

// conn returns the cached connection for name, dialing on first use. Two
// concurrent dials cannot install two connections: the loser is closed and
// the winner reused.
func (m *Manager) conn(ctx context.Context, name string, cfg ServerConfig) (ClientConn, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, fmt.Errorf("mcp %q: manager is closed", name)
	}
	if conn, ok := m.conns[name]; ok {
		m.mu.Unlock()
		return conn, nil
	}
	m.mu.Unlock()

	conn, err := m.dialer.Dial(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("mcp dial %q: %w", name, err)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = conn.Close()
		return nil, fmt.Errorf("mcp %q: manager is closed", name)
	}
	if existing, ok := m.conns[name]; ok {
		m.mu.Unlock()
		_ = conn.Close() // lost the dial race: reuse the installed connection
		return existing, nil
	}
	m.conns[name] = conn
	m.mu.Unlock()
	return conn, nil
}

// dropConn retires one specific connection: stale is the conn that failed
// and is always closed (it is the one being retired), but the cache is only
// updated while it still maps to that exact connection. A concurrent redial
// may have installed a newer conn in the meantime — deleting/closing whatever
// is cached would destroy healthy state (the lost-update bug). Closing
// happens outside the lock, since stdio shutdown can block.
func (m *Manager) dropConn(name string, stale ClientConn) {
	if stale == nil {
		return
	}
	m.mu.Lock()
	if cached, ok := m.conns[name]; ok && cached == stale {
		delete(m.conns, name)
	}
	m.mu.Unlock()
	_ = stale.Close()
}

// Close closes every open connection (terminating stdio children) and clears
// the caches. It is idempotent, safe after partial or failed dials, and
// rejects subsequent dials.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	conns := m.conns
	m.conns = make(map[string]ClientConn)
	m.loaded = make(map[string][]tools.LoadedMCPTool)
	m.inflight = make(map[string]*loadFlight)
	m.fps = make(map[string]string)
	m.mu.Unlock()

	var errs []error
	for name, conn := range conns {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mcp close %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// isTransportFailure reports whether err came from the connection itself
// (transport.Error wraps every send failure in mcp-go) rather than from an
// MCP-level error response — only the former is worth a redial.
func isTransportFailure(err error) bool {
	var tErr *transport.Error
	return errors.As(err, &tErr)
}
