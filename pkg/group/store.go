package group

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/xilistudios/lele/pkg/store"
)

// Package-level SQLite backend for group state persistence.
// When non-nil, SaveGroup/LoadGroup/ListGroups use the repository
// instead of the legacy per-file JSON backend.
var (
	storeMu   sync.RWMutex
	groupRepo *store.GroupRepo
)

// UseStore configures group persistence to use the given SQLite repository.
// Until called (or with nil), the legacy per-file JSON backend is used.
func UseStore(repo *store.GroupRepo) {
	storeMu.Lock()
	defer storeMu.Unlock()
	groupRepo = repo
}

func getGroupRepo() *store.GroupRepo {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return groupRepo
}

// sortGroupsByUpdatedAt sorts states by UpdatedAt descending with
// tie-break by ID ascending. Reused by both backends.
func sortGroupsByUpdatedAt(states []*GroupState) {
	sort.Slice(states, func(i, j int) bool {
		if states[i].UpdatedAt.Equal(states[j].UpdatedAt) {
			return states[i].ID < states[j].ID
		}
		return states[i].UpdatedAt.After(states[j].UpdatedAt)
	})
}

// SaveGroup persists a GroupState. When a SQLite store is configured
// via UseStore, the state is persisted through the repository and dir
// is ignored. Otherwise, the state is saved as pretty-printed JSON in
// <dir>/<sanitized-id>.json (creating dir if needed).
func SaveGroup(dir string, state *GroupState) error {
	if state == nil {
		return fmt.Errorf("save group: state is nil")
	}

	if repo := getGroupRepo(); repo != nil {
		data, err := json.Marshal(state)
		if err != nil {
			return fmt.Errorf("save group marshal: %w", err)
		}
		if err := repo.SetGroupState(state.ID, string(data)); err != nil {
			return fmt.Errorf("save group set: %w", err)
		}
		return nil
	}

	return saveGroupLegacy(dir, state)
}

// LoadGroup loads a GroupState by groupID. When a SQLite store is
// configured, the state is loaded from the repository first. If not
// found there and dir is non-empty, a legacy JSON lookup is attempted
// and the result is migrated into the repository best-effort.
// Without a SQLite store, the legacy per-file JSON backend is used.
func LoadGroup(dir, groupID string) (*GroupState, error) {
	if repo := getGroupRepo(); repo != nil {
		stateJSON, found, err := repo.GetGroupState(groupID)
		if err != nil {
			return nil, fmt.Errorf("load group %s: %w", groupID, err)
		}
		if found {
			var state GroupState
			if err := json.Unmarshal([]byte(stateJSON), &state); err != nil {
				return nil, fmt.Errorf("load group %s unmarshal: %w", groupID, err)
			}
			return &state, nil
		}

		// Not in repo: try lazy migration from legacy if dir is provided.
		if dir != "" {
			state, legacyErr := loadGroupLegacy(dir, groupID)
			if legacyErr != nil {
				return nil, fmt.Errorf("load group %s: %w", groupID, os.ErrNotExist)
			}
			// Best-effort insert into repo.
			if migrated, mErr := json.Marshal(state); mErr == nil {
				if sErr := repo.SetGroupState(state.ID, string(migrated)); sErr != nil {
					log.Printf("group %s: lazy migration insert failed: %v", state.ID, sErr)
				}
			} else {
				log.Printf("group %s: lazy migration marshal failed: %v", state.ID, mErr)
			}
			return state, nil
		}

		// No dir provided and not in repo.
		return nil, fmt.Errorf("load group %s: %w", groupID, os.ErrNotExist)
	}

	return loadGroupLegacy(dir, groupID)
}

// ListGroups returns every GroupState available. When a SQLite store is
// configured, the repository is the primary source; if it is empty and
// dir is non-empty, legacy JSON files are read and migrated best-effort.
// Without a SQLite store, the legacy per-file JSON backend is used.
func ListGroups(dir string) ([]*GroupState, error) {
	if repo := getGroupRepo(); repo != nil {
		statesMap, err := repo.ListGroupStates()
		if err != nil {
			return nil, fmt.Errorf("list groups: %w", err)
		}

		if len(statesMap) == 0 && dir != "" {
			// Empty repo: try legacy migration.
			legacyStates, legacyErr := listGroupsLegacy(dir)
			if legacyErr == nil && len(legacyStates) > 0 {
				for _, s := range legacyStates {
					if data, mErr := json.Marshal(s); mErr == nil {
						if sErr := repo.SetGroupState(s.ID, string(data)); sErr != nil {
							log.Printf("group %s: list migration insert failed: %v", s.ID, sErr)
						}
					} else {
						log.Printf("group %s: list migration marshal failed: %v", s.ID, mErr)
					}
				}
				return legacyStates, nil
			}
			return nil, nil
		}

		// Build from repo data.
		var states []*GroupState
		for _, stateJSON := range statesMap {
			var s GroupState
			if err := json.Unmarshal([]byte(stateJSON), &s); err != nil {
				continue // skip corrupt entries, consistent with legacy
			}
			states = append(states, &s)
		}
		sortGroupsByUpdatedAt(states)
		return states, nil
	}

	return listGroupsLegacy(dir)
}

// ---------------------------------------------------------------------------
// Legacy per-file JSON backend
// ---------------------------------------------------------------------------

// saveGroupLegacy persists a GroupState as pretty-printed JSON in
// <dir>/<sanitized-id>.json.  It creates dir if it does not exist.
func saveGroupLegacy(dir string, state *GroupState) error {
	if dir == "" {
		return fmt.Errorf("save group: dir is empty")
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

// loadGroupLegacy loads a GroupState from a per-file JSON previously
// persisted by saveGroupLegacy. Returns an error if the file does not
// exist or is corrupt.
func loadGroupLegacy(dir, groupID string) (*GroupState, error) {
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

// listGroupsLegacy returns every GroupState persisted in dir as JSON
// files, sorted by UpdatedAt descending.
func listGroupsLegacy(dir string) ([]*GroupState, error) {
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

	sortGroupsByUpdatedAt(states)

	return states, nil
}

// sanitizeGroupID replaces characters that are unsafe for filenames
// (":", "/", "\") with "_".  The result is always safe to use as a
// single path component on Linux, macOS, and Windows.
func sanitizeGroupID(id string) string {
	r := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	return r.Replace(id)
}
