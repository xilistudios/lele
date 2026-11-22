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

const commandsSubdir = "commands"

type ManagerConfig struct {
	LeleDir                   string
	Workspace                 string
	Dir                       string
	Commands                  map[string]CommandDef
	AllowShellDefault         bool
	AllowAbsoluteFilesDefault bool
}

type Manager struct {
	mu              sync.RWMutex
	cfg             ManagerConfig
	reg             *Registry
	lastLoad        time.Time
	shellOverrides  map[string]bool
	absFileOverride map[string]bool
}

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

func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reloadLocked()
}

func (m *Manager) reloadLocked() error {
	levels, errs := m.loadLevelsLocked()

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

var levelOrder = []Source{SourceConfig, SourceGlobal, SourceWorkspace, SourceDirectory}

func (m *Manager) loadLevelsLocked() (map[Source][]*Command, []error) {
	levels := map[Source][]*Command{
		SourceConfig:    {},
		SourceGlobal:    {},
		SourceWorkspace: {},
		SourceDirectory: {},
	}
	var errs []error

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
		levels[source] = append(levels[source], found...)
	}
	loadLevel(m.cfg.LeleDir, SourceGlobal)
	loadLevel(m.cfg.Workspace, SourceWorkspace)
	loadLevel(m.cfg.Dir, SourceDirectory)

	return levels, errs
}

func (m *Manager) Levels() (map[Source][]*Command, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	levels, errs := m.loadLevelsLocked()
	return levels, errors.Join(errs...)
}

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

func (m *Manager) Registry() *Registry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reg
}

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

func (m *Manager) ClearAllowShell(name string) {
	stem := strings.ToLower(strings.TrimSpace(name))
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.shellOverrides, stem)
}

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

func (m *Manager) ClearAllowAbsoluteFiles(name string) {
	stem := strings.ToLower(strings.TrimSpace(name))
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.absFileOverride, stem)
}

func (m *Manager) Config() ManagerConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := m.cfg
	out.Commands = nil
	return out
}

func (c ManagerConfig) String() string {
	return fmt.Sprintf("harness.ManagerConfig{lele=%q workspace=%q dir=%q defs=%d allow_shell=%v allow_abs_files=%v}",
		c.LeleDir, c.Workspace, c.Dir, len(c.Commands), c.AllowShellDefault, c.AllowAbsoluteFilesDefault)
}
