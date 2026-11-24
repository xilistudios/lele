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

var (
	usageRepoMu sync.RWMutex
	usageRepo   counterstore.UsageRepo
)

// SetUsageRepo switches MCP usage persistence to the shared SQLite store.
// Passing nil restores the legacy JSON file backend (used on platforms
// without SQLite support and in tests).
func SetUsageRepo(repo counterstore.UsageRepo) {
	usageRepoMu.Lock()
	defer usageRepoMu.Unlock()
	usageRepo = repo
}

func currentUsageRepo() counterstore.UsageRepo {
	usageRepoMu.RLock()
	defer usageRepoMu.RUnlock()
	return usageRepo
}

type usageStore struct {
	mu      sync.Mutex
	path    string
	repo    counterstore.UsageRepo
	counts  map[string]map[string]uint64
	pending map[string]map[string]uint64
}

func loadUsageStore(path string) *usageStore {
	if repo := currentUsageRepo(); repo != nil {
		return &usageStore{
			path:   path,
			repo:   repo,
			counts: counterstore.LoadWithRepo(path, "mcp", "mcp", repo),
		}
	}
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

	if s.path == "" && s.repo == nil {
		return
	}
	if err := s.saveLocked(); err != nil {
		warnUsage("could not persist the MCP usage counters", s.path, err)
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
	if s.repo != nil {
		merged, err := s.repo.MergeUsage("mcp", s.pending)
		if err != nil {
			return err
		}
		s.counts = merged
		s.pending = nil
		return nil
	}
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
