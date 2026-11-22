package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"sync"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tools"
)

type Manager struct {
	paths  Paths
	dialer Dialer
	auth   *AuthStore
	usage  *usageStore

	mu          sync.Mutex
	conns       map[string]ClientConn
	loaded      map[string][]tools.LoadedMCPTool
	inflight    map[string]*loadFlight
	fps         map[string]string
	lastWarning string
	closed      bool
}

type loadFlight struct {
	done  chan struct{}
	specs []tools.LoadedMCPTool
	err   error
}

func NewManager(paths Paths, dialer Dialer) *Manager {
	if dialer == nil {
		dialer = newMCPDialer()
	}
	return &Manager{
		paths:    paths,
		dialer:   dialer,
		auth:     NewAuthStore(paths.LeleDir),
		usage:    loadUsageStore(usageFilePath(paths.LeleDir)),
		conns:    make(map[string]ClientConn),
		loaded:   make(map[string][]tools.LoadedMCPTool),
		inflight: make(map[string]*loadFlight),
		fps:      make(map[string]string),
	}
}

func usageFilePath(leleDir string) string {
	if leleDir == "" {
		return ""
	}
	return filepath.Join(leleDir, "mcp_usage.json")
}

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

func (m *Manager) resolve(name string) (ServerConfig, error) {
	res := m.discover()
	if cfg, ok := res.Servers[name]; ok {
		cfg = AttachAuthRuntime(name, cfg, m.auth)
		m.applyConfigChange(name, cfg)
		return cfg, nil
	}
	if layer, ok := res.Disabled[name]; ok {
		return ServerConfig{}, fmt.Errorf("mcp server %q is disabled in the %s layer", name, layer)
	}
	return ServerConfig{}, fmt.Errorf("mcp server %q is not configured", name)
}

func (m *Manager) applyConfigChange(name string, cfg ServerConfig) {
	fp := m.fingerprint(name, cfg)
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

func configFingerprint(cfg ServerConfig) string {
	data, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	return fmt.Sprintf("%016x", h.Sum64())
}

func (m *Manager) fingerprint(name string, cfg ServerConfig) string {
	fp := configFingerprint(cfg)
	if m.auth != nil {
		fp += ":" + fmt.Sprintf("%016x", m.auth.Revision(name))
	}
	return fp
}

func (m *Manager) LoadServer(ctx context.Context, name string) ([]tools.LoadedMCPTool, error) {
	cfg, err := m.resolve(name)
	if err != nil {
		return nil, err
	}
	fp := m.fingerprint(name, cfg)

	m.mu.Lock()
	if specs, ok := m.loaded[name]; ok {
		m.mu.Unlock()
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
	if err == nil && !m.closed && m.fps[name] == fp {
		m.loaded[name] = specs
	}
	m.mu.Unlock()

	flight.specs, flight.err = specs, err
	close(flight.done)
	return specs, err
}

func (m *Manager) fetchTools(ctx context.Context, name string, cfg ServerConfig) ([]mcp.Tool, error) {
	conn, err := m.conn(ctx, name, cfg)
	if err != nil {
		return nil, fmt.Errorf("mcp load %q: %w", name, err)
	}
	list, err := conn.ListTools(ctx)
	if err == nil || ctx.Err() != nil {
		return list, wrapLoad(name, err)
	}
	if authRequiredError(err) {
		return nil, fmt.Errorf("mcp load %q: %w", name, ErrAuthRequired)
	}
	if !isTransportFailure(err) {
		return list, wrapLoad(name, err)
	}
	m.dropConn(name, conn)
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

func wrapLoad(name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("mcp load %q: %w", name, err)
}

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
		m.usage.inc(server, remoteTool)
		return res, nil
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("mcp call %s.%s: %w", server, remoteTool, err)
	}
	if authRequiredError(err) {
		return nil, fmt.Errorf("mcp call %s.%s: %w", server, remoteTool, ErrAuthRequired)
	}
	if !isTransportFailure(err) {
		return nil, fmt.Errorf("mcp call %s.%s: %w", server, remoteTool, err)
	}
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
	m.usage.inc(server, remoteTool)
	return res, nil
}

func (m *Manager) ToolUsage(server string) map[string]uint64 {
	return m.usage.countsFor(server)
}

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
		_ = conn.Close()
		return existing, nil
	}
	m.conns[name] = conn
	m.mu.Unlock()
	return conn, nil
}

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

func isTransportFailure(err error) bool {
	var tErr *transport.Error
	return errors.As(err, &tErr)
}
