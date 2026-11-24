package skills

import (
	"sync"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils/counterstore"
)

var (
	usageRepoMu sync.RWMutex
	usageRepo   counterstore.UsageRepo
)

// SetUsageRepo switches skill usage persistence to the shared SQLite store.
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

type UsageStore struct {
	mu      sync.Mutex
	path    string
	repo    counterstore.UsageRepo
	counts  map[string]map[string]uint64
	pending map[string]map[string]uint64
}

func NewUsageStore(path string) *UsageStore {
	if repo := currentUsageRepo(); repo != nil {
		return &UsageStore{
			path:   path,
			repo:   repo,
			counts: counterstore.LoadWithRepo(path, "skills", "skills", repo),
		}
	}
	return &UsageStore{path: path, counts: counterstore.Load(path, "skills")}
}

func (s *UsageStore) Inc(agentID, skill string) {
	if s == nil || agentID == "" || skill == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.counts[agentID] == nil {
		s.counts[agentID] = make(map[string]uint64)
	}
	s.counts[agentID][skill]++

	if s.pending == nil {
		s.pending = make(map[string]map[string]uint64)
	}
	if s.pending[agentID] == nil {
		s.pending[agentID] = make(map[string]uint64)
	}
	s.pending[agentID][skill]++

	if s.repo != nil {
		merged, err := s.repo.MergeUsage("skills", s.pending)
		if err != nil {
			logger.WarnCF("skills", "could not persist the skills usage counters", map[string]interface{}{
				"path":  s.path,
				"error": err.Error(),
			})
			return
		}
		s.counts = merged
		s.pending = nil
		return
	}

	if s.path == "" {
		return
	}
	merged, err := counterstore.MergeAndSave(s.path, "skills", s.pending)
	if err != nil {
		logger.WarnCF("skills", "could not persist the skills usage file", map[string]interface{}{
			"path":  s.path,
			"error": err.Error(),
		})
		return
	}
	s.counts = merged
	s.pending = nil
}

func (s *UsageStore) CountsFor(agentID string) map[string]uint64 {
	if s == nil {
		return map[string]uint64{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]uint64, len(s.counts[agentID]))
	for skill, count := range s.counts[agentID] {
		out[skill] = count
	}
	return out
}
