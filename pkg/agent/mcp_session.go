package agent

import (
	"sync"

	"github.com/xilistudios/lele/pkg/tools"
)

const maxSessionToolLayers = 256

type sessionToolKey struct {
	agentID    string
	sessionKey string
}

type sessionToolLayer struct {
	registry *tools.ToolRegistry
	used     uint64
	gen      uint64
}

type sessionToolSet struct {
	mu      sync.Mutex
	entries map[sessionToolKey]*sessionToolLayer
	clock   uint64
}

func newSessionToolSet() *sessionToolSet {
	return &sessionToolSet{entries: make(map[sessionToolKey]*sessionToolLayer)}
}

func (s *sessionToolSet) getOrCreate(agentID, sessionKey string, gen uint64, build func() *tools.ToolRegistry) *tools.ToolRegistry {
	key := sessionToolKey{agentID: agentID, sessionKey: sessionKey}

	s.mu.Lock()
	defer s.mu.Unlock()
	if layer, ok := s.entries[key]; ok {
		if layer.gen == gen {
			s.clock++
			layer.used = s.clock
			return layer.registry
		}
		delete(s.entries, key)
	}
	registry := build()
	if registry == nil {
		return nil
	}
	s.clock++
	s.entries[key] = &sessionToolLayer{registry: registry, used: s.clock, gen: gen}
	s.pruneLocked()
	return registry
}

func (s *sessionToolSet) pruneLocked() {
	for len(s.entries) > maxSessionToolLayers {
		var (
			oldestKey sessionToolKey
			oldest    uint64
			found     bool
		)
		for key, layer := range s.entries {
			if !found || layer.used < oldest {
				oldestKey, oldest, found = key, layer.used, true
			}
		}
		if !found {
			return
		}
		delete(s.entries, oldestKey)
	}
}

func (s *sessionToolSet) dropAgent(agentID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if key.agentID == agentID {
			delete(s.entries, key)
		}
	}
}

func (s *sessionToolSet) dropSession(sessionKey string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.entries {
		if key.sessionKey == sessionKey {
			delete(s.entries, key)
		}
	}
}

func (s *sessionToolSet) dropAll() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = make(map[sessionToolKey]*sessionToolLayer)
}

func (s *sessionToolSet) len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
