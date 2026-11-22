package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/xilistudios/lele/pkg/logger"
)

type usageStore struct {
	mu      sync.Mutex
	path    string
	counts  map[string]map[string]uint64
	pending map[string]map[string]uint64
}

const usageFileIndent = "  "

func loadUsageStore(path string) *usageStore {
	s := &usageStore{path: path, counts: make(map[string]map[string]uint64)}
	if path == "" {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			warnUsage("could not read the MCP usage file, starting with empty counts", path, err)
		}
		return s
	}
	var loaded map[string]map[string]uint64
	if err := json.Unmarshal(data, &loaded); err != nil {
		warnUsage("could not parse the MCP usage file, starting with empty counts", path, err)
		return s
	}
	for server, tools := range loaded {
		if len(tools) == 0 {
			continue
		}
		s.counts[server] = tools
	}
	return s
}

func warnUsage(message, path string, err error) {
	logger.WarnCF("mcp", message, map[string]interface{}{
		"path":  path,
		"error": err.Error(),
	})
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
	release, lockErr := lockUsageFile(s.path)
	defer release()
	if lockErr != nil {
		warnUsage("could not lock the MCP usage file, saving without the lock", s.path, lockErr)
	}

	merged := s.readSnapshotLocked()
	for server, tools := range s.pending {
		counts := merged[server]
		if counts == nil {
			counts = make(map[string]uint64)
			merged[server] = counts
		}
		for remote, delta := range tools {
			counts[remote] += delta
		}
	}

	data, err := json.MarshalIndent(merged, "", usageFileIndent)
	if err != nil {
		return fmt.Errorf("marshal usage: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".mcp-usage-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace %s: %w", s.path, err)
	}
	committed = true

	s.counts = merged
	s.pending = nil
	return nil
}

func (s *usageStore) readSnapshotLocked() map[string]map[string]uint64 {
	merged := make(map[string]map[string]uint64)
	data, err := os.ReadFile(s.path)
	if err != nil {
		return merged
	}
	var loaded map[string]map[string]uint64
	if err := json.Unmarshal(data, &loaded); err != nil {
		return merged
	}
	for server, tools := range loaded {
		if len(tools) == 0 {
			continue
		}
		merged[server] = tools
	}
	return merged
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
