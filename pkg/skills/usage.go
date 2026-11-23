package skills

import (
	"sync"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils/counterstore"
)

type UsageStore struct {
	mu      sync.Mutex
	path    string
	counts  map[string]map[string]uint64
	pending map[string]map[string]uint64
}

func NewUsageStore(path string) *UsageStore {
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
