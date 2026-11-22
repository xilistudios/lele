package harness

import (
	"slices"
	"strings"
	"sync"
)

type Registry struct {
	mu   sync.RWMutex
	cmds map[string]*Command
}

func NewRegistry() *Registry {
	return &Registry{cmds: make(map[string]*Command)}
}

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

func (r *Registry) Get(name string) (*Command, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.cmds[strings.ToLower(name)]
	return c, ok
}

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

func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.cmds)
}

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
