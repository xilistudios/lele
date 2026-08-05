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

// State represents the persistent state for a workspace.
// It includes information about the last active channel/chat.
type State struct {
	// LastChannel is the last channel used for communication
	LastChannel string `json:"last_channel,omitempty"`

	// LastChatID is the last chat ID used for communication
	LastChatID string `json:"last_chat_id,omitempty"`

	// Timestamp is the last time this state was updated
	Timestamp time.Time `json:"timestamp"`
}

// Manager manages persistent state with atomic saves.
type Manager struct {
	workspace string
	state     *State
	mu        sync.RWMutex
	stateFile string
	kvRepo    *store.KVRepo
}

// NewManager creates a new state manager for the given workspace.
func NewManager(workspace string) *Manager {
	stateDir := filepath.Join(workspace, "state")
	stateFile := filepath.Join(stateDir, "state.json")
	oldStateFile := filepath.Join(workspace, "state.json")

	// Create state directory if it doesn't exist
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		log.Printf("[ERROR] state: failed to create state directory: %v", err)
		return nil // Return manager with empty state
	}

	sm := &Manager{
		workspace: workspace,
		stateFile: stateFile,
		state:     &State{},
	}

	// Try to load from new location first
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		// New file doesn't exist, try migrating from old location
		if data, err := os.ReadFile(oldStateFile); err == nil {
			if err := json.Unmarshal(data, sm.state); err == nil {
				// Migrate to new location
				if err := sm.saveAtomic(); err != nil {
					log.Printf("[ERROR] state: failed to save migrated state: %v", err)
				} else {
					log.Printf("[INFO] state: migrated state from %s to %s", oldStateFile, stateFile)
				}
			}
		}
	} else {
		// Load from new location
		if err := sm.load(); err != nil {
			log.Printf("[ERROR] state: failed to load state: %v", err)
		}
	}

	return sm
}

// SetKVRepo wires the SQLite KV store into the state manager. When set,
// state is persisted to the KV store under the key "state:" + workspace
// instead of a JSON file on disk. Passing nil restores the JSON file
// fallback behavior.
func (sm *Manager) SetKVRepo(repo *store.KVRepo) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.kvRepo = repo
	if repo == nil {
		return
	}

	// Try to load state from KV store. If the key doesn't exist yet, fall
	// back to whatever is currently in memory (e.g., already loaded from
	// the JSON file or freshly initialized).
	if data, ok, err := repo.Get("state:" + sm.workspace); err != nil {
		log.Printf("[ERROR] state: failed to load state from KV store: %v", err)
	} else if ok {
		if err := json.Unmarshal([]byte(data), sm.state); err != nil {
			log.Printf("[ERROR] state: failed to unmarshal state from KV store: %v", err)
		}
	}
}

// SetLastChannel atomically updates the last channel and saves the state.
// This method uses a temp file + rename pattern for atomic writes,
// ensuring that the state file is never corrupted even if the process crashes.
func (sm *Manager) SetLastChannel(channel string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Update state
	sm.state.LastChannel = channel
	sm.state.Timestamp = time.Now()

	// Atomic save using temp file + rename
	if err := sm.saveAtomic(); err != nil {
		return fmt.Errorf("failed to save state atomically: %w", err)
	}

	return nil
}

// SetLastChatID atomically updates the last chat ID and saves the state.
func (sm *Manager) SetLastChatID(chatID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Update state
	sm.state.LastChatID = chatID
	sm.state.Timestamp = time.Now()

	// Atomic save using temp file + rename
	if err := sm.saveAtomic(); err != nil {
		return fmt.Errorf("failed to save state atomically: %w", err)
	}

	return nil
}

// GetLastChannel returns the last channel from the state.
func (sm *Manager) GetLastChannel() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastChannel
}

// GetLastChatID returns the last chat ID from the state.
func (sm *Manager) GetLastChatID() string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.LastChatID
}

// GetTimestamp returns the timestamp of the last state update.
func (sm *Manager) GetTimestamp() time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.state.Timestamp
}

// saveAtomic performs an atomic save using temp file + rename.
// This ensures that the state file is never corrupted:
// 1. Write to a temp file
// 2. Rename temp file to target (atomic on POSIX systems)
// 3. If rename fails, cleanup the temp file
//
// When a KV repo is wired in, the state is persisted to the SQLite KV
// store instead (key: "state:" + workspace), skipping the JSON file write.
//
// Must be called with the lock held.
func (sm *Manager) saveAtomic() error {
	// Marshal state to JSON
	data, err := json.MarshalIndent(sm.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Prefer the KV store when wired in.
	if sm.kvRepo != nil {
		if err := sm.kvRepo.Set("state:"+sm.workspace, string(data)); err != nil {
			return fmt.Errorf("failed to save state to KV store: %w", err)
		}
		return nil
	}

	// Create temp file in the same directory as the target
	tempFile := sm.stateFile + ".tmp"

	// Write to temp file
	if err := os.WriteFile(tempFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename from temp to target
	if err := os.Rename(tempFile, sm.stateFile); err != nil {
		// Cleanup temp file if rename fails
		os.Remove(tempFile)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// load loads the state from disk (or from the KV store when wired in).
func (sm *Manager) load() error {
	// Prefer the KV store when wired in.
	if sm.kvRepo != nil {
		data, ok, err := sm.kvRepo.Get("state:" + sm.workspace)
		if err != nil {
			return fmt.Errorf("failed to read state from KV store: %w", err)
		}
		if !ok {
			// Key doesn't exist yet, that's OK.
			return nil
		}
		if err := json.Unmarshal([]byte(data), sm.state); err != nil {
			return fmt.Errorf("failed to unmarshal state: %w", err)
		}
		return nil
	}

	data, err := os.ReadFile(sm.stateFile)
	if err != nil {
		// File doesn't exist yet, that's OK
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
