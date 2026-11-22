package state

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

type State struct {
	LastChannel string `json:"last_channel,omitempty"`

	LastChatID string `json:"last_chat_id,omitempty"`

	Timestamp time.Time `json:"timestamp"`
}

type Manager struct {
	workspace string
	state     *State
	mu        sync.RWMutex
	stateFile string
	kvRepo    *store.KVRepo
}

func NewManager(workspace string) *Manager {
	stateDir := filepath.Join(workspace, "state")
	stateFile := filepath.Join(stateDir, "state.json")
	oldStateFile := filepath.Join(workspace, "state.json")

	if err := os.MkdirAll(stateDir, 0755); err != nil {
		log.Printf("[ERROR] state: failed to create state directory: %v", err)
		return nil
	}

	sm := &Manager{
		workspace: workspace,
		stateFile: stateFile,
		state:     &State{},
	}

	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		if data, err := os.ReadFile(oldStateFile); err == nil {
			if err := json.Unmarshal(data, sm.state); err == nil {
				if err := sm.saveAtomic(); err != nil {
					log.Printf("[ERROR] state: failed to save migrated state: %v", err)
				} else {
					log.Printf("[INFO] state: migrated state from %s to %s", oldStateFile, stateFile)
				}
			}
		}
	} else {
		if err := sm.load(); err != nil {
			log.Printf("[ERROR] state: failed to load state: %v", err)
		}
	}

	return sm
}

func (sm *Manager) SetKVRepo(repo *store.KVRepo) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.kvRepo = repo
	if repo == nil {
		return
	}

	if data, ok, err := repo.Get("state:" + sm.workspace); err != nil {
		log.Printf("[ERROR] state: failed to load state from KV store: %v", err)
	} else if ok {
		if err := json.Unmarshal([]byte(data), sm.state); err != nil {
			log.Printf("[ERROR] state: failed to unmarshal state from KV store: %v", err)
		}
	}
}

func (sm *Manager) SetLastChannel(channel string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state.LastChannel = channel
	sm.state.Timestamp = time.Now()

	if err := sm.saveAtomic(); err != nil {
		return fmt.Errorf("failed to save state atomically: %w", err)
	}

	return nil
}

func (sm *Manager) SetLastChatID(chatID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state.LastChatID = chatID
	sm.state.Timestamp = time.Now()

	if err := sm.saveAtomic(); err != nil {
		return fmt.Errorf("failed to save state atomically: %w", err)
	}

	return nil
}

func (sm *Manager) GetLastChannel() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastChannel
}

func (sm *Manager) GetLastChatID() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastChatID
}

func (sm *Manager) GetTimestamp() time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.Timestamp
}

func (sm *Manager) saveAtomic() error {
	data, err := json.MarshalIndent(sm.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	if sm.kvRepo != nil {
		if err := sm.kvRepo.Set("state:"+sm.workspace, string(data)); err != nil {
			return fmt.Errorf("failed to save state to KV store: %w", err)
		}
		return nil
	}

	tempFile := sm.stateFile + ".tmp"

	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tempFile, sm.stateFile); err != nil {
		os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (sm *Manager) load() error {
	if sm.kvRepo != nil {
		data, ok, err := sm.kvRepo.Get("state:" + sm.workspace)
		if err != nil {
			return fmt.Errorf("failed to read state from KV store: %w", err)
		}
		if !ok {
			return nil
		}
		if err := json.Unmarshal([]byte(data), sm.state); err != nil {
			return fmt.Errorf("failed to unmarshal state: %w", err)
		}
		return nil
	}

	data, err := os.ReadFile(sm.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	if err := json.Unmarshal(data, sm.state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	return nil
}
