// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package harness

import (
	"slices"
	"strings"
	"sync"
)

// Registry is a thread-safe name -> Command index. Names are normalized to
// lowercase on both write and read, so lookups are case-insensitive. It holds
// no precedence logic: whoever loads commands decides registration order and
// uses Register (last wins) or Replace (atomic swap) accordingly.
type Registry struct {
	mu   sync.RWMutex
	cmds map[string]*Command
}

// NewRegistry returns an empty, ready-to-use registry.
func NewRegistry() *Registry {
	return &Registry{cmds: make(map[string]*Command)}
}

// Register adds or overwrites a command under its lowercased name. A nil
// command or empty name is ignored.
func (r *Registry) Register(c *Command) {
	if c == nil || c.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cmds == nil {
		r.cmds = make(map[string]*Command)
	}
	stored := *c
	stored.Name = strings.ToLower(c.Name)
	r.cmds[stored.Name] = &stored
}

// Get returns the command registered under name (case-insensitive).
func (r *Registry) Get(name string) (*Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.cmds[strings.ToLower(name)]
	return c, ok
}

// All returns a snapshot copy of every command, sorted by name. Safe to mutate
// the slice; the commands themselves are the shared pointers.
func (r *Registry) All() []*Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Command, 0, len(r.cmds))
	for _, c := range r.cmds {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *Command) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Len returns the number of registered commands.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.cmds)
}

// Replace atomically swaps the entire contents for cmds (later duplicates in
// the input win). Used by reload paths so readers never observe a half-built
// registry.
func (r *Registry) Replace(cmds []*Command) {
	next := make(map[string]*Command, len(cmds))
	for _, c := range cmds {
		if c == nil || c.Name == "" {
			continue
		}
		stored := *c
		stored.Name = strings.ToLower(c.Name)
		next[stored.Name] = &stored
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = next
}
