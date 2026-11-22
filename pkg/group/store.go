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
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

var (
	storeMu   sync.RWMutex
	groupRepo *store.GroupRepo
)

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

func groupSortsBefore(idA string, atA time.Time, idB string, atB time.Time) bool {
	if atA.Equal(atB) {
		return idA < idB
	}
	return atA.After(atB)
}

func sortGroupsByUpdatedAt(states []*GroupState) {
	sort.Slice(states, func(i, j int) bool {
		return groupSortsBefore(states[i].ID, states[i].UpdatedAt, states[j].ID, states[j].UpdatedAt)
	})
}

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

		if dir != "" {
			state, legacyErr := loadGroupLegacy(dir, groupID)
			if legacyErr != nil {
				return nil, fmt.Errorf("load group %s: %w", groupID, os.ErrNotExist)
			}
			if migrated, mErr := json.Marshal(state); mErr == nil {
				if sErr := repo.SetGroupState(state.ID, string(migrated)); sErr != nil {
					log.Printf("group %s: lazy migration insert failed: %v", state.ID, sErr)
				}
			} else {
				log.Printf("group %s: lazy migration marshal failed: %v", state.ID, mErr)
			}
			return state, nil
		}

		return nil, fmt.Errorf("load group %s: %w", groupID, os.ErrNotExist)
	}

	return loadGroupLegacy(dir, groupID)
}

func ListGroups(dir string) ([]*GroupState, error) {
	if repo := getGroupRepo(); repo != nil {
		statesMap, err := repo.ListGroupStates()
		if err != nil {
			return nil, fmt.Errorf("list groups: %w", err)
		}

		if len(statesMap) == 0 && dir != "" {
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

		var states []*GroupState
		for _, stateJSON := range statesMap {
			var s GroupState
			if err := json.Unmarshal([]byte(stateJSON), &s); err != nil {
				continue
			}
			states = append(states, &s)
		}
		sortGroupsByUpdatedAt(states)
		return states, nil
	}

	return listGroupsLegacy(dir)
}

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

func listGroupsLegacy(dir string) ([]*GroupState, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
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
			continue
		}
		var s GroupState
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		states = append(states, &s)
	}

	sortGroupsByUpdatedAt(states)

	return states, nil
}

func sanitizeGroupID(id string) string {
	r := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	return r.Replace(id)
}
