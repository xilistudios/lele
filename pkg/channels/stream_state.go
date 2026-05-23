// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package channels

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

// StreamMessageState holds the accumulated state of a streaming message.
// It persists to disk so that if the client disconnects (page reload, network
// hiccup), the stream content can be recovered when the client reconnects.
type StreamMessageState struct {
	MessageID        string `json:"message_id"`
	SessionKey       string `json:"session_key"`
	Content          string `json:"content"`
	ReasoningContent string `json:"reasoning_content"`
	Done             bool   `json:"done"`
	Error            string `json:"error,omitempty"`
	StartedAt        int64  `json:"started_at"`
	LastChunkAt      int64  `json:"last_chunk_at"`
}

// StreamStateManager manages in-progress stream states with disk persistence.
// Each active stream (identified by sessionKey + messageID) accumulates chunks
// as they arrive from the LLM. The state is flushed to disk on every chunk so
// that it survives server restarts. Completed/expired streams are cleaned up.
type StreamStateManager struct {
	mu       sync.RWMutex
	states   map[string]*StreamMessageState // key: "sessionKey/messageID"
	storeDir string
}

// NewStreamStateManager creates a new manager. storeDir is where stream state
// JSON files are persisted (e.g., $LELEDIR/streams).
func NewStreamStateManager(storeDir string) *StreamStateManager {
	mgr := &StreamStateManager{
		states:   make(map[string]*StreamMessageState),
		storeDir: storeDir,
	}
	// Recover any state files that survived from a previous run
	mgr.recoverFromDisk()
	// Start background cleanup goroutine
	go mgr.cleanupLoop()
	return mgr
}

// stateKey builds the composite key for a stream
func stateKey(sessionKey, messageID string) string {
	return sessionKey + "/" + messageID
}

// StartStream initializes tracking for a new streaming message
func (m *StreamStateManager) StartStream(sessionKey, messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := stateKey(sessionKey, messageID)
	now := time.Now().UnixMilli()
	m.states[key] = &StreamMessageState{
		MessageID:   messageID,
		SessionKey:  sessionKey,
		StartedAt:   now,
		LastChunkAt: now,
	}
	m.flushToDisk(key)
}

// AppendChunk adds a content chunk to the accumulated stream state.
// If the stream doesn't exist yet, it's auto-created (covers WebSocket-initiated
// messages and other non-SSE paths).
func (m *StreamStateManager) AppendChunk(sessionKey, messageID, chunk string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := stateKey(sessionKey, messageID)
	state, ok := m.states[key]
	if !ok {
		// Auto-create: stream was started via non-SSE path (WebSocket, API, etc.)
		now := time.Now().UnixMilli()
		state = &StreamMessageState{
			MessageID:   messageID,
			SessionKey:  sessionKey,
			StartedAt:   now,
			LastChunkAt: now,
		}
		m.states[key] = state
	}
	state.Content += chunk
	state.LastChunkAt = time.Now().UnixMilli()
	m.flushToDisk(key)
}

// AppendReasoning adds a reasoning/thinking chunk.
// Auto-creates the stream state if it doesn't exist yet.
func (m *StreamStateManager) AppendReasoning(sessionKey, messageID, chunk string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := stateKey(sessionKey, messageID)
	state, ok := m.states[key]
	if !ok {
		now := time.Now().UnixMilli()
		state = &StreamMessageState{
			MessageID:   messageID,
			SessionKey:  sessionKey,
			StartedAt:   now,
			LastChunkAt: now,
		}
		m.states[key] = state
	}
	state.ReasoningContent += chunk
	state.LastChunkAt = time.Now().UnixMilli()
	m.flushToDisk(key)
}

// MarkDone marks a stream as complete and cleans up the disk file.
// No-op if the stream was never started (e.g., non-streamed messages).
func (m *StreamStateManager) MarkDone(sessionKey, messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := stateKey(sessionKey, messageID)
	state, ok := m.states[key]
	if !ok {
		return
	}
	state.Done = true
	state.LastChunkAt = time.Now().UnixMilli()
	m.flushToDisk(key)
}

// MarkError records an error on a stream.
// No-op if the stream was never started.
func (m *StreamStateManager) MarkError(sessionKey, messageID, errMsg string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := stateKey(sessionKey, messageID)
	state, ok := m.states[key]
	if !ok {
		return
	}
	state.Error = errMsg
	state.Done = true
	state.LastChunkAt = time.Now().UnixMilli()
	m.flushToDisk(key)
}

// GetStream returns the current state of a specific stream
func (m *StreamStateManager) GetStream(sessionKey, messageID string) *StreamMessageState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := stateKey(sessionKey, messageID)
	state, ok := m.states[key]
	if !ok {
		// Try loading from disk
		state = m.loadFromDisk(key)
		if state != nil {
			// Re-populate in-memory (it may still be active)
			return state
		}
		return nil
	}
	return state
}

// ListActiveStreams returns all non-done streams for a given session
func (m *StreamStateManager) ListActiveStreams(sessionKey string) []StreamMessageState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := sessionKey + "/"
	var result []StreamMessageState
	for key, state := range m.states {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			result = append(result, *state)
		}
	}
	return result
}

// RemoveStream removes a stream from memory and disk
func (m *StreamStateManager) RemoveStream(sessionKey, messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := stateKey(sessionKey, messageID)
	delete(m.states, key)
	m.removeDiskFile(key)
}

// diskPath returns the file path for a stream state
func (m *StreamStateManager) diskPath(key string) string {
	return filepath.Join(m.storeDir, key+".json")
}

// flushToDisk writes the stream state to a JSON file. Must be called with m.mu held.
func (m *StreamStateManager) flushToDisk(key string) {
	state, ok := m.states[key]
	if !ok {
		return
	}
	if m.storeDir == "" {
		return
	}

	dir := filepath.Dir(m.diskPath(key))
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.WarnCF("stream_state", "Failed to create stream state directory", map[string]interface{}{
			"dir":   dir,
			"error": err.Error(),
		})
		return
	}

	data, err := json.Marshal(state)
	if err != nil {
		logger.WarnCF("stream_state", "Failed to marshal stream state", map[string]interface{}{
			"key":   key,
			"error": err.Error(),
		})
		return
	}

	tmpPath := m.diskPath(key) + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		logger.WarnCF("stream_state", "Failed to write stream state tmp file", map[string]interface{}{
			"path":  tmpPath,
			"error": err.Error(),
		})
		return
	}
	if err := os.Rename(tmpPath, m.diskPath(key)); err != nil {
		logger.WarnCF("stream_state", "Failed to rename stream state file", map[string]interface{}{
			"path":  m.diskPath(key),
			"error": err.Error(),
		})
	}
}

// loadFromDisk reads a stream state from disk. May be called with m.mu held.
func (m *StreamStateManager) loadFromDisk(key string) *StreamMessageState {
	if m.storeDir == "" {
		return nil
	}
	path := m.diskPath(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state StreamMessageState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	return &state
}

// removeDiskFile deletes the disk file for a stream. Must be called with m.mu held.
func (m *StreamStateManager) removeDiskFile(key string) {
	if m.storeDir == "" {
		return
	}
	path := m.diskPath(key)
	os.Remove(path)
	os.Remove(path + ".tmp")
}

// recoverFromDisk loads any persisted stream states from disk on startup
func (m *StreamStateManager) recoverFromDisk() {
	if m.storeDir == "" {
		return
	}

	entries, err := os.ReadDir(m.storeDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(m.storeDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var state StreamMessageState
		if err := json.Unmarshal(data, &state); err != nil {
			continue
		}

		// Only recover streams that haven't been completed/stale for too long
		if state.Done {
			// Keep completed streams for a short window so reconnecting clients
			// can still retrieve the final state
			age := time.Since(time.UnixMilli(state.LastChunkAt))
			if age > 30*time.Second {
				os.Remove(path)
				continue
			}
		} else {
			// Not done but very old → the server probably restarted while streaming
			// Keep it for 5 minutes in case the client reconnects
			age := time.Since(time.UnixMilli(state.LastChunkAt))
			if age > 5*time.Minute {
				os.Remove(path)
				continue
			}
		}

		key := stateKey(state.SessionKey, state.MessageID)
		m.states[key] = &state
	}
}

// cleanupLoop periodically removes stale stream states
func (m *StreamStateManager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		m.cleanup()
	}
}

// cleanup removes old completed streams
func (m *StreamStateManager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for key, state := range m.states {
		age := now.Sub(time.UnixMilli(state.LastChunkAt))
		if state.Done && age > 5*time.Minute {
			delete(m.states, key)
			m.removeDiskFile(key)
		} else if !state.Done && age > 10*time.Minute {
			// Stream seems abandoned
			delete(m.states, key)
			m.removeDiskFile(key)
		}
	}
}
