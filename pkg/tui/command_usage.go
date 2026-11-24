package tui

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/utils/counterstore"
	"github.com/xilistudios/lele/pkg/utils/filelock"
)

const commandUsageMaxEntries = 100

type commandUsageFile struct {
	Counts map[string]int `json:"counts,omitempty"`
}

var (
	commandUsageRepoMu sync.RWMutex
	commandUsageRepo   counterstore.UsageRepo
)

// SetUsageRepo switches command usage persistence to the shared SQLite
// store. Passing nil restores the legacy JSON file backend (used on
// platforms without SQLite support and in tests).
func SetUsageRepo(repo counterstore.UsageRepo) {
	commandUsageRepoMu.Lock()
	defer commandUsageRepoMu.Unlock()
	commandUsageRepo = repo
}

func currentCommandUsageRepo() counterstore.UsageRepo {
	commandUsageRepoMu.RLock()
	defer commandUsageRepoMu.RUnlock()
	return commandUsageRepo
}

type commandUsageStore struct {
	mu     sync.Mutex
	path   string
	repo   counterstore.UsageRepo
	counts map[string]int
}

func commandUsagePath() string {
	return filepath.Join(config.GetLeleDir(), "command_usage.json")
}

func newCommandUsageStore(path string) *commandUsageStore {
	s := &commandUsageStore{path: path, counts: map[string]int{}}
	if repo := currentCommandUsageRepo(); repo != nil {
		s.repo = repo
		s.counts = loadCommandUsageFromRepo(path, repo)
		return s
	}
	s.counts = readCommandUsageFile(path)
	return s
}

func readCommandUsageFile(path string) map[string]int {
	counts := map[string]int{}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("warning: could not read %s: %v", path, err)
		}
		return counts
	}
	var file commandUsageFile
	if err := json.Unmarshal(data, &file); err != nil {
		log.Printf("warning: could not parse %s: %v", path, err)
		return counts
	}
	for name, count := range file.Counts {
		key := normalizeCommandKey(name)
		if key == "" || count <= 0 {
			continue
		}
		counts[key] = count
	}
	return counts
}

// loadCommandUsageFromRepo loads counters from SQLite, importing the legacy
// command_usage.json once when the scope is still empty and renaming it to
// <path>.migrated afterwards. The file lock makes concurrent imports safe.
func loadCommandUsageFromRepo(path string, repo counterstore.UsageRepo) map[string]int {
	counts := map[string]int{}
	stored, err := repo.LoadUsage("command")
	if err != nil {
		log.Printf("warning: could not load command usage from the store: %v", err)
	}
	for key, count := range stored[""] {
		if k := normalizeCommandKey(key); k != "" && count > 0 {
			counts[k] = int(count)
		}
	}
	if path == "" {
		return counts
	}
	if _, err := os.Stat(path); err != nil {
		return counts
	}
	release, lockErr := filelock.Acquire(path)
	defer release()
	if lockErr != nil {
		log.Printf("warning: could not lock %s for import: %v", path, lockErr)
		return counts
	}
	if _, err := os.Stat(path); err != nil {
		return counts
	}
	legacy := readCommandUsageFile(path)
	if len(counts) == 0 {
		if len(legacy) > 0 {
			pending := map[string]map[string]uint64{"": {}}
			for k, v := range legacy {
				pending[""][k] = uint64(v)
			}
			merged, mergeErr := repo.MergeUsage("command", pending)
			if mergeErr != nil {
				log.Printf("warning: could not import %s into the store: %v", path, mergeErr)
				return counts
			}
			for key, count := range merged[""] {
				if k := normalizeCommandKey(key); k != "" && count > 0 {
					counts[k] = int(count)
				}
			}
		}
	} else if len(legacy) > 0 {
		// The store is authoritative once it holds command counters: retire
		// the stale file instead of leaving a second source of truth.
		log.Printf("warning: legacy %s ignored and retired: the store already holds command counters", path)
	}
	if err := retireLegacyCommandUsageFile(path); err != nil {
		log.Printf("warning: could not rename %s after import: %v", path, err)
	}
	return counts
}

// retireLegacyCommandUsageFile renames the legacy file out of the way
// without clobbering an existing .migrated backup.
func retireLegacyCommandUsageFile(path string) error {
	target := path + ".migrated"
	if _, err := os.Stat(target); err == nil {
		target = fmt.Sprintf("%s.migrated.%d", path, time.Now().UnixNano())
	}
	return os.Rename(path, target)
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
	if s.repo != nil {
		counts := map[string]map[string]uint64{"": {}}
		for k, v := range pruned {
			counts[""][k] = uint64(v)
		}
		return s.repo.ReplaceUsage("command", counts)
	}
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
