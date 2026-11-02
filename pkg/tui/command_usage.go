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

// commandUsageMaxEntries bounds the persisted history so the file (and the map
// read on every keystroke) cannot grow without limit — a slash-command palette
// has a couple dozen entries, so 100 is already "everything I ever type".
const commandUsageMaxEntries = 100

// commandUsageFile is the on-disk shape of the usage history.
type commandUsageFile struct {
	Counts map[string]int `json:"counts,omitempty"`
}

// commandUsageStore counts how often each slash command was executed. The
// counts are only ever used to sort the autocomplete palette, so every failure
// mode (missing file, unreadable file, malformed JSON, full disk) degrades to
// "no ranking" instead of an error the TUI would have to surface.
type commandUsageStore struct {
	mu     sync.Mutex
	path   string
	counts map[string]int
}

// commandUsagePath is the file the ranking is persisted in. It honours
// LELE_CONFIG_DIR through config.GetLeleDir so tests never touch the real home.
func commandUsagePath() string {
	return filepath.Join(config.GetLeleDir(), "command_usage.json")
}

// newCommandUsageStore loads the history at path, starting empty when the file
// does not exist yet (the normal first run — not worth a warning).
func newCommandUsageStore(path string) *commandUsageStore {
	s := &commandUsageStore{path: path, counts: map[string]int{}}
	// A write that happens between the read and the rename below is lost, so
	// the load is best-effort by design: the worst case is a command ranking
	// one hit lower than reality.
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

// normalizeCommandKey makes "/Models", "models" and " /models " the same key so
// a command typed with different casing still adds up to one ranking entry.
func normalizeCommandKey(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	return "/" + strings.TrimPrefix(key, "/")
}

// Record counts one execution of name and persists the new totals.
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
	// Saving after the unlock keeps keystrokes out of the write path; a lost
	// save only costs ranking accuracy, never the running TUI.
	if err := s.save(); err != nil {
		log.Printf("warning: could not save command usage: %v", err)
	}
}

// Count reports how many times name was executed (0 when unknown).
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

// snapshot returns a copy of the counts so callers can rank commands without
// holding the lock while they sort.
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

// save writes the pruned history atomically, so a crash mid-write cannot leave
// a half-written JSON file behind to be warned about on the next start.
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
		// Leave nothing behind for the next save to trip over.
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// pruneCommandUsage keeps the commandUsageMaxEntries most executed commands.
// Ties break on the key name so the surviving set is deterministic — an
// unordered prune would reshuffle the palette between two identical saves.
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

// sortCommandsByUsage puts the most executed commands first, leaving the order
// untouched when nothing has been recorded yet (a first run looks exactly like
// it always did) and keeping ties stable.
func sortCommandsByUsage(cmds []commandInfo, usage *commandUsageStore) {
	if len(cmds) < 2 {
		return
	}
	counts := usage.snapshot()
	if len(counts) == 0 {
		return
	}
	// The counts are looked up by command name, never through a slice indexed
	// by position: sort.SliceStable swaps elements as it sorts, so a parallel
	// slice would be read at the wrong index from the first swap on.
	sort.SliceStable(cmds, func(i, j int) bool {
		return counts[normalizeCommandKey(cmds[i].name)] > counts[normalizeCommandKey(cmds[j].name)]
	})
}

// isKnownCommand reports whether name is a command the TUI can actually run —
// used to keep typos and plain prose out of the usage history.
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
