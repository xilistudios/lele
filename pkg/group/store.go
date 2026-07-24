package group

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SaveGroup persists a GroupState as pretty-printed JSON in
// <dir>/<sanitized-id>.json.  It creates dir if it does not exist.
func SaveGroup(dir string, state *GroupState) error {
	if dir == "" {
		return fmt.Errorf("save group: dir is empty")
	}
	if state == nil {
		return fmt.Errorf("save group: state is nil")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("save group mkdir: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("save group marshal: %w", err)
	}

	filename := sanitizeGroupID(state.ID) + ".json"
	path := filepath.Join(dir, filename)

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("save group write: %w", err)
	}
	return nil
}

// LoadGroup loads a GroupState previously persisted by SaveGroup.
// Returns an error if the file does not exist or is corrupt.
func LoadGroup(dir, groupID string) (*GroupState, error) {
	if dir == "" {
		return nil, fmt.Errorf("load group: dir is empty")
	}

	filename := sanitizeGroupID(groupID) + ".json"
	path := filepath.Join(dir, filename)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load group %s: %w", groupID, err)
	}

	var state GroupState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("load group %s unmarshal: %w", groupID, err)
	}
	return &state, nil
}

// ListGroups returns every GroupState persisted in dir, sorted by UpdatedAt
// descending (most recently updated first). Ties are broken by ID ascending.
func ListGroups(dir string) ([]*GroupState, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // empty directory → no groups
		}
		return nil, fmt.Errorf("list groups readdir: %w", err)
	}

	var states []*GroupState
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable files
		}
		var s GroupState
		if err := json.Unmarshal(data, &s); err != nil {
			continue // skip corrupt files
		}
		states = append(states, &s)
	}

	sort.Slice(states, func(i, j int) bool {
		if states[i].UpdatedAt.Equal(states[j].UpdatedAt) {
			return states[i].ID < states[j].ID
		}
		return states[i].UpdatedAt.After(states[j].UpdatedAt)
	})

	return states, nil
}

// sanitizeGroupID replaces characters that are unsafe for filenames
// (":", "/", "\") with "_".  The result is always safe to use as a
// single path component on Linux, macOS, and Windows.
func sanitizeGroupID(id string) string {
	r := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	return r.Replace(id)
}
