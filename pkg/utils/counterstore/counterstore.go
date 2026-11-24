package counterstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/utils/filelock"
)

func Load(path, label string) map[string]map[string]uint64 {
	counts := make(map[string]map[string]uint64)
	if path == "" {
		return counts
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			warn(label, "could not read the usage file, starting with empty counts", path, err)
		}
		return counts
	}
	var loaded map[string]map[string]uint64
	if err := json.Unmarshal(data, &loaded); err != nil {
		warn(label, "could not parse the usage file, starting with empty counts", path, err)
		return counts
	}
	for group, keys := range loaded {
		if len(keys) > 0 {
			counts[group] = keys
		}
	}
	return counts
}

func MergeAndSave(path, label string, pending map[string]map[string]uint64) (map[string]map[string]uint64, error) {
	release, lockErr := filelock.Acquire(path)
	defer release()
	if lockErr != nil {
		return nil, fmt.Errorf("lock %s: %w", path, lockErr)
	}

	merged := Load(path, label)
	for group, keys := range pending {
		if merged[group] == nil {
			merged[group] = make(map[string]uint64)
		}
		for key, delta := range keys {
			merged[group][key] += delta
		}
	}

	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal usage: %w", err)
	}
	data = append(data, '\n')

	if err := atomicWrite(path, data); err != nil {
		return nil, err
	}
	return merged, nil
}

func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".counterstore-*.tmp")
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
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	committed = true
	return nil
}

func warn(label, message, path string, err error) {
	logger.WarnCF(label, message, map[string]interface{}{
		"path":  path,
		"error": err.Error(),
	})
}

// UsageRepo is the subset of *store.UsageRepo needed to persist counters in
// SQLite. It is declared as an interface so this utility package does not
// depend on pkg/store.
type UsageRepo interface {
	LoadUsage(scope string) (map[string]map[string]uint64, error)
	MergeUsage(scope string, pending map[string]map[string]uint64) (map[string]map[string]uint64, error)
	ReplaceUsage(scope string, counts map[string]map[string]uint64) error
}

// LoadWithRepo loads counters from the SQLite repo, importing the legacy
// JSON file once when the scope is still empty. After a successful import
// the legacy file is renamed to <path>.migrated so it stops being live JSON
// state. The import is guarded by the same file lock used by MergeAndSave,
// so concurrent processes import exactly once. repo must not be nil.
func LoadWithRepo(path, label, scope string, repo UsageRepo) map[string]map[string]uint64 {
	counts, err := repo.LoadUsage(scope)
	if err != nil {
		warn(label, "could not load usage from the store, starting with empty counts", path, err)
		counts = make(map[string]map[string]uint64)
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
		warn(label, "could not lock the legacy usage file for import", path, lockErr)
		return counts
	}
	// Re-check under the lock: another process may have imported already.
	if _, err := os.Stat(path); err != nil {
		return counts
	}

	legacy := Load(path, label)
	if len(counts) == 0 {
		if len(legacy) > 0 {
			merged, mergeErr := repo.MergeUsage(scope, legacy)
			if mergeErr != nil {
				warn(label, "could not import the legacy usage file into the store", path, mergeErr)
				return counts
			}
			counts = merged
		}
	} else if len(legacy) > 0 {
		// The store is authoritative once it holds counters for this scope:
		// retire the stale file instead of leaving a second source of truth
		// that a fallback-mode process would keep writing.
		warn(label, "legacy usage file ignored and retired: the store already holds counters for this scope", path, fmt.Errorf("scope %q populated", scope))
	}
	if err := retireLegacyFile(path); err != nil {
		warn(label, "could not rename the legacy usage file after import", path, err)
	}
	return counts
}

// retireLegacyFile renames the legacy file out of the way without ever
// clobbering an existing .migrated backup (a previous retirement may hold
// the only copy of counts the store never merged).
func retireLegacyFile(path string) error {
	target := path + ".migrated"
	if _, err := os.Stat(target); err == nil {
		target = fmt.Sprintf("%s.migrated.%d", path, time.Now().UnixNano())
	}
	return os.Rename(path, target)
}
