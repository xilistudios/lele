package mcp

import (
	"sync"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils/counterstore"
)

func warnUsage(message, path string, err error) {
	logger.WarnCF("mcp", message, map[string]interface{}{
		"path":  path,
		"error": err.Error(),
	})
}

type usageStore struct {
	mu      sync.Mutex
	path    string
	counts  map[string]map[string]uint64
	pending map[string]map[string]uint64
}

func loadUsageStore(path string) *usageStore {
	return &usageStore{path: path, counts: counterstore.Load(path, "mcp")}
}

func (s *usageStore) inc(server, remote string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.counts == nil {
		s.counts = make(map[string]map[string]uint64)
	}
	tools := s.counts[server]
	if tools == nil {
		tools = make(map[string]uint64)
		s.counts[server] = tools
	}
	tools[remote]++
	s.addPendingLocked(server, remote)

	if s.path == "" {
		return
	}
	if err := s.saveLocked(); err != nil {
		warnUsage("could not persist the MCP usage file", s.path, err)
	}
}

func (s *usageStore) addPendingLocked(server, remote string) {
	if s.pending == nil {
		s.pending = make(map[string]map[string]uint64)
	}
	tools := s.pending[server]
	if tools == nil {
		tools = make(map[string]uint64)
		s.pending[server] = tools
	}
	tools[remote]++
}

func (s *usageStore) saveLocked() error {
	merged, err := counterstore.MergeAndSave(s.path, "mcp", s.pending)
	if err != nil {
		return err
	}
	s.counts = merged
	s.pending = nil
	return nil
}

func (s *usageStore) countsFor(server string) map[string]uint64 {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]uint64, len(s.counts[server]))
	for remote, count := range s.counts[server] {
		out[remote] = count
	}
	return out
}
