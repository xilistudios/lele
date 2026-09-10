// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package harness

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// commandsSubdir is the folder inside the global and workspace roots that
// holds command markdown files. The directory level is passed already
// resolved (it is ".lele/commands" relative to the project).
const commandsSubdir = "commands"

// ManagerConfig describes the four discovery levels plus the shell default.
// Any level whose path is "" is disabled, which lets callers omit a level
// (e.g. a headless gateway without a workspace) without special cases.
type ManagerConfig struct {
	LeleDir           string                // global level: <LeleDir>/commands ("" disables)
	Workspace         string                // workspace level: <Workspace>/commands ("" disables)
	Dir               string                // directory level: <Dir> (already includes .lele/commands; "" disables)
	Commands          map[string]CommandDef // config.json level (lowest precedence)
	AllowShellDefault bool                  // default AllowShell for expanded commands
	// AllowAbsoluteFilesDefault is the harness-wide default for @/abs/path
	// inlining; individual commands can override it with the tri-state
	// allow_absolute_files frontmatter flag (unlike AllowShell's OR merge).
	AllowAbsoluteFilesDefault bool
}

// Manager owns the command Registry and keeps it in sync with the four
// discovery levels. It is the only place where precedence is applied:
//
//	config.json < global < workspace < directory
//
// The Registry instance is stable across reloads (contents are swapped with
// Replace), so callers may hold the pointer returned by Registry() forever.
type Manager struct {
	mu              sync.RWMutex
	cfg             ManagerConfig
	reg             *Registry
	lastLoad        time.Time
	shellOverrides  map[string]bool // per-command AllowShell overrides (tests, runtime flags)
	absFileOverride map[string]bool // per-command AllowAbsoluteFiles pins
}

// NewManager builds a Manager and performs the initial load. Loading errors
// are logged per level and never fail construction: a broken command file must
// not take the agent down.
func NewManager(mc ManagerConfig) *Manager {
	m := &Manager{
		cfg:             mc,
		reg:             NewRegistry(),
		shellOverrides:  make(map[string]bool),
		absFileOverride: make(map[string]bool),
	}
	if err := m.Reload(); err != nil {
		slog.Warn("harness: initial command load had errors", "error", err)
	}
	return m
}

// Reload rebuilds the registry from all four levels. Per-level errors are
// logged and collected into the returned error, but the levels that did load
// are still applied, so a transient read failure cannot wipe the commands.
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reloadLocked()
}

// reloadLocked performs the load; m.mu must be held.
func (m *Manager) reloadLocked() error {
	levels, errs := m.loadLevelsLocked()

	// Flatten in precedence order (config -> global -> workspace -> directory);
	// Registry.Replace keeps last-write-wins, so later levels overwrite earlier
	// ones with the same name.
	var cmds []*Command
	for _, src := range levelOrder {
		cmds = append(cmds, levels[src]...)
	}

	m.reg.Replace(cmds)
	m.lastLoad = time.Now()

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// levelOrder lists the discovery sources from lowest to highest precedence.
// It is the single definition of the flattening order used by reloadLocked.
var levelOrder = []Source{SourceConfig, SourceGlobal, SourceWorkspace, SourceDirectory}

// loadLevelsLocked returns the commands of each discovery level WITHOUT
// applying precedence. reloadLocked flattens it (config→global→workspace→
// directory, last-write-wins) exactly as before; Levels exposes it for the UI
// so shadowed files can be surfaced. m.mu must be held (write lock from
// reloadLocked; a read lock is enough for Levels since it only reads m.cfg and
// hits the disk). Per-level errors are returned alongside the levels that did
// load; a disabled level (empty path) maps to an empty slice, never an error.
func (m *Manager) loadLevelsLocked() (map[Source][]*Command, []error) {
	levels := map[Source][]*Command{
		SourceConfig:    {},
		SourceGlobal:    {},
		SourceWorkspace: {},
		SourceDirectory: {},
	}
	var errs []error

	// 1. config.json map (lowest precedence), sorted by key so the level is
	// deterministic for the UI (the merge result never depended on it, since
	// config keys are unique in the map).
	names := make([]string, 0, len(m.cfg.Commands))
	for name := range m.cfg.Commands {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		def := m.cfg.Commands[name]
		stem := strings.ToLower(strings.TrimSpace(name))
		if stem == "" {
			slog.Warn("harness: skipping command with empty name")
			continue
		}
		if strings.TrimSpace(def.Template) == "" {
			slog.Warn("harness: skipping command with empty template", "name", stem)
			continue
		}
		levels[SourceConfig] = append(levels[SourceConfig], def.ToCommand(stem, SourceConfig, ""))
	}

	// 2..4. file levels, ordered so later ones overwrite earlier ones when
	// flattened. The global and workspace roots point at a "commands"
	// subdirectory; the directory level is already the full path.
	loadLevel := func(root string, source Source) {
		if root == "" {
			return
		}
		dir := root
		if source != SourceDirectory {
			dir = filepath.Join(root, commandsSubdir)
		}
		found, err := LoadDir(dir, source)
		if err != nil {
			slog.Warn("harness: command level load failed", "source", source, "dir", dir, "error", err)
			errs = append(errs, err)
			return
		}
		// append (even with a nil found) keeps the level a non-nil empty slice.
		levels[source] = append(levels[source], found...)
	}
	loadLevel(m.cfg.LeleDir, SourceGlobal)
	loadLevel(m.cfg.Workspace, SourceWorkspace)
	loadLevel(m.cfg.Dir, SourceDirectory)

	return levels, errs
}

// Levels returns the commands of each discovery level without merging, so a UI
// can show origins and shadowing. Errors encountered per level are returned;
// levels that loaded are still included. A disabled level (empty path) maps to
// an empty slice, never an error. Results are loaded on demand from disk and
// never cached on the manager, so the registry and lastLoad are untouched.
func (m *Manager) Levels() (map[Source][]*Command, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	levels, errs := m.loadLevelsLocked()
	return levels, errors.Join(errs...)
}

// EnsureFresh reloads when the last load is older than ttl. ttl <= 0 forces a
// reload. Safe for concurrent use; only one goroutine performs the reload.
func (m *Manager) EnsureFresh(ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.lastLoad.IsZero() && time.Since(m.lastLoad) <= ttl {
		return
	}
	if err := m.reloadLocked(); err != nil {
		slog.Warn("harness: refresh had errors", "error", err)
	}
}

// Registry returns the stable registry instance. The pointer never changes
// across reloads, so consumers can cache it.
func (m *Manager) Registry() *Registry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reg
}

// AllowShell reports whether shell expansion (!`cmd`) is permitted for cmd:
// an explicit per-command pin wins, otherwise cmd.AllowShell ||
// cfg.AllowShellDefault.
func (m *Manager) AllowShell(cmd *Command) bool {
	if cmd != nil {
		m.mu.RLock()
		ov, ok := m.shellOverrides[cmd.Name]
		m.mu.RUnlock()
		if ok {
			return ov
		}
		return cmd.AllowShell || m.cfg.AllowShellDefault
	}
	return m.cfg.AllowShellDefault
}

// SetAllowShell pins the shell permission for one command name, overriding
// both the command flag and the harness default. Presence in the map is the
// signal, so pinning false really means false. Use ClearAllowShell to remove
// the pin. Intended for tests and explicit runtime overrides.
func (m *Manager) SetAllowShell(name string, allow bool) {
	stem := strings.ToLower(strings.TrimSpace(name))
	if stem == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.shellOverrides == nil {
		m.shellOverrides = make(map[string]bool)
	}
	m.shellOverrides[stem] = allow
}

// ClearAllowShell removes a per-command shell pin.
func (m *Manager) ClearAllowShell(name string) {
	stem := strings.ToLower(strings.TrimSpace(name))
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.shellOverrides, stem)
}

// AllowAbsoluteFiles reports whether @/abs/path inlining is permitted for cmd.
// Resolution order: runtime pin > command tri-state > harness default. The
// command flag is a *bool, so an explicit false genuinely vetoes a global
// true — deliberately different from AllowShell's OR merge, which cannot
// express opt-out (documented wart).
func (m *Manager) AllowAbsoluteFiles(cmd *Command) bool {
	if cmd != nil {
		m.mu.RLock()
		pin, ok := m.absFileOverride[cmd.Name]
		m.mu.RUnlock()
		if ok {
			return pin
		}
		if cmd.AllowAbsoluteFiles != nil {
			return *cmd.AllowAbsoluteFiles
		}
	}
	return m.cfg.AllowAbsoluteFilesDefault
}

// SetAllowAbsoluteFiles pins the absolute-file permission for one command
// name, overriding both the command tri-state and the harness default.
// Presence in the map is the signal, so pinning false really means false.
// Intended for tests and explicit runtime overrides.
func (m *Manager) SetAllowAbsoluteFiles(name string, allow bool) {
	stem := strings.ToLower(strings.TrimSpace(name))
	if stem == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.absFileOverride == nil {
		m.absFileOverride = make(map[string]bool)
	}
	m.absFileOverride[stem] = allow
}

// ClearAllowAbsoluteFiles removes a per-command absolute-file pin.
func (m *Manager) ClearAllowAbsoluteFiles(name string) {
	stem := strings.ToLower(strings.TrimSpace(name))
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.absFileOverride, stem)
}

// Config returns a copy of the manager configuration (for diagnostics).
func (m *Manager) Config() ManagerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := m.cfg
	out.Commands = nil
	return out
}

// String renders a one-line summary, handy in logs.
func (c ManagerConfig) String() string {
	return fmt.Sprintf("harness.ManagerConfig{lele=%q workspace=%q dir=%q defs=%d allow_shell=%v allow_abs_files=%v}",
		c.LeleDir, c.Workspace, c.Dir, len(c.Commands), c.AllowShellDefault, c.AllowAbsoluteFilesDefault)
}
