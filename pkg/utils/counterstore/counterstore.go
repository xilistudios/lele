package counterstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

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
