package tui

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/xilistudios/lele/pkg/config"
)

const commandUsageMaxEntries = 100

type commandUsageFile struct {
	Counts map[string]int `json:"counts,omitempty"`
}

type commandUsageStore struct {
	mu     sync.Mutex
	path   string
	counts map[string]int
}

func commandUsagePath() string {
	return filepath.Join(config.GetLeleDir(), "command_usage.json")
}

func newCommandUsageStore(path string) *commandUsageStore {
	s := &commandUsageStore{path: path, counts: map[string]int{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("warning: could not read %s: %v", path, err)
		}
		return s
	}
	var file commandUsageFile
	if err := json.Unmarshal(data, &file); err != nil {
		log.Printf("warning: could not parse %s: %v", path, err)
		return s
	}
	for name, count := range file.Counts {
		key := normalizeCommandKey(name)
		if key == "" || count <= 0 {
			continue
		}
		s.counts[key] = count
	}
	return s
}

func normalizeCommandKey(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	return "/" + strings.TrimPrefix(key, "/")
}

func (s *commandUsageStore) Record(name string) {
	if s == nil {
		return
	}
	key := normalizeCommandKey(name)
	if key == "" {
		return
	}
	s.mu.Lock()
	s.counts[key]++
	s.mu.Unlock()
	if err := s.save(); err != nil {
		log.Printf("warning: could not save command usage: %v", err)
	}
}

func (s *commandUsageStore) Count(name string) int {
	if s == nil {
		return 0
	}
	key := normalizeCommandKey(name)
	if key == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[key]
}

func (s *commandUsageStore) snapshot() map[string]int {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.counts))
	for k, v := range s.counts {
		out[k] = v
	}
	return out
}

func (s *commandUsageStore) save() error {
	if s == nil {
		return nil
	}
	pruned := pruneCommandUsage(s.snapshot())
	data, err := json.MarshalIndent(commandUsageFile{Counts: pruned}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func pruneCommandUsage(counts map[string]int) map[string]int {
	if len(counts) <= commandUsageMaxEntries {
		return counts
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	kept := make(map[string]int, commandUsageMaxEntries)
	for _, k := range keys[:commandUsageMaxEntries] {
		kept[k] = counts[k]
	}
	return kept
}

func sortCommandsByUsage(cmds []commandInfo, usage *commandUsageStore) {
	if len(cmds) < 2 {
		return
	}
	counts := usage.snapshot()
	if len(counts) == 0 {
		return
	}
	sort.SliceStable(cmds, func(i, j int) bool {
		return counts[normalizeCommandKey(cmds[i].name)] > counts[normalizeCommandKey(cmds[j].name)]
	})
}

func (m *Model) isKnownCommand(name string) bool {
	key := normalizeCommandKey(name)
	if key == "" {
		return false
	}
	for _, cmd := range allCommands {
		if normalizeCommandKey(cmd.name) == key {
			return true
		}
	}
	return m.isCustomCommand(name)
}
