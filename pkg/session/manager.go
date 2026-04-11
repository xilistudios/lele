package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

type Session struct {
	Key          string              `json:"key"`
	Name         string              `json:"name,omitempty"`
	Messages     []providers.Message `json:"messages"`
	Summary      string              `json:"summary,omitempty"`
	VerboseMode  bool                `json:"verbose_mode,omitempty"`  // Deprecated: use VerboseLevel
	VerboseLevel string              `json:"verbose_level,omitempty"` // "off", "basic", or "full"
	Created      time.Time           `json:"created"`
	Updated      time.Time           `json:"updated"`
	// Token tracking
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	storage  string
}

func NewSessionManager(storage string) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		storage:  storage,
	}

	if storage != "" {
		os.MkdirAll(storage, 0755)
		sm.loadSessions()
	}

	return sm
}

func (sm *SessionManager) GetOrCreate(key string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		return session
	}

	session = &Session{
		Key:      key,
		Messages: []providers.Message{},
		Created:  time.Now(),
		Updated:  time.Now(),
	}
	sm.sessions[key] = session

	return session
}

func generateSessionName(content string) string {
	maxLen := 50
	content = strings.TrimSpace(content)
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.ReplaceAll(content, "\r", " ")
	content = strings.ReplaceAll(content, "\t", " ")

	for _, r := range []string{".", ",", "!", "?", ";", ":", "'", "\"", "`"} {
		content = strings.ReplaceAll(content, r, "")
	}

	words := strings.Fields(content)
	if len(words) == 0 {
		return "New Chat"
	}

	result := strings.Join(words, " ")
	if len(result) <= maxLen {
		return result
	}

	result = result[:maxLen]
	lastSpace := strings.LastIndex(result, " ")
	if lastSpace > 0 && lastSpace > maxLen-20 {
		result = result[:lastSpace]
	}

	return strings.TrimSpace(result)
}

func (sm *SessionManager) AddMessage(sessionKey, role, content string) {
	sm.AddFullMessage(sessionKey, providers.Message{
		Role:    role,
		Content: content,
	})
}

// AddFullMessage adds a complete message with tool calls and tool call ID to the session.
// This is used to save the full conversation flow including tool calls and tool results.
func (sm *SessionManager) AddFullMessage(sessionKey string, msg providers.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionKey]
	if !ok {
		session = &Session{
			Key:      sessionKey,
			Messages: []providers.Message{},
			Created:  time.Now(),
		}
		sm.sessions[sessionKey] = session
	}

	if msg.Role == "user" && len(session.Messages) == 0 && session.Name == "" {
		session.Name = generateSessionName(msg.Content)
	}

	session.Messages = append(session.Messages, msg)
	session.Updated = time.Now()
}

func (sm *SessionManager) GetHistory(key string) []providers.Message {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return []providers.Message{}
	}

	history := make([]providers.Message, len(session.Messages))
	copy(history, session.Messages)
	return history
}

func (sm *SessionManager) GetSummary(key string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return ""
	}
	return session.Summary
}

func (sm *SessionManager) GetName(key string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return ""
	}
	return session.Name
}

func (sm *SessionManager) GetUpdated(key string) time.Time {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return time.Time{}
	}
	return session.Updated
}

func (sm *SessionManager) SetSummary(key string, summary string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		session.Summary = summary
		session.Updated = time.Now()
	}
}

func (sm *SessionManager) SetName(key string, name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session = &Session{
			Key:      key,
			Messages: []providers.Message{},
			Created:  time.Now(),
		}
		sm.sessions[key] = session
	}

	session.Name = strings.TrimSpace(name)
	session.Updated = time.Now()

	return sm.saveUnlocked(key)
}

func (sm *SessionManager) TruncateHistory(key string, keepLast int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	if keepLast <= 0 {
		session.Messages = []providers.Message{}
		session.Updated = time.Now()
		return
	}

	if len(session.Messages) <= keepLast {
		return
	}

	session.Messages = session.Messages[len(session.Messages)-keepLast:]
	session.Updated = time.Now()
}

func (sm *SessionManager) ShouldStartFreshSession(key string, threshold time.Duration) (bool, time.Duration) {
	if threshold <= 0 {
		return false, 0
	}

	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok || session == nil {
		return false, 0
	}

	if len(session.Messages) == 0 && strings.TrimSpace(session.Summary) == "" {
		return false, 0
	}

	lastActivity := session.Updated
	if lastActivity.IsZero() {
		lastActivity = session.Created
	}
	if lastActivity.IsZero() {
		return false, 0
	}

	idle := time.Since(lastActivity)
	if idle <= threshold {
		return false, idle
	}

	return true, idle
}

// sanitizeFilename converts a session key into a cross-platform safe filename.
// Session keys use "channel:chatID" (e.g. "telegram:123456") but ':' is the
// volume separator on Windows, so filepath.Base would misinterpret the key.
// We replace it with '_'. The original key is preserved inside the JSON file,
// so loadSessions still maps back to the right in-memory key.
func sanitizeFilename(key string) string {
	return strings.ReplaceAll(key, ":", "_")
}

func (sm *SessionManager) Save(key string) error {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.saveUnlocked(key)
}

func (sm *SessionManager) loadSessions() error {
	files, err := os.ReadDir(sm.storage)
	if err != nil {
		return err
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		if filepath.Ext(file.Name()) != ".json" {
			continue
		}

		sessionPath := filepath.Join(sm.storage, file.Name())
		data, err := os.ReadFile(sessionPath)
		if err != nil {
			continue
		}

		var session Session
		if err := json.Unmarshal(data, &session); err != nil {
			continue
		}

		sm.sessions[session.Key] = &session
	}

	return nil
}

// SetHistory updates the messages of a session.
func (sm *SessionManager) SetHistory(key string, history []providers.Message) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		// Create a deep copy to strictly isolate internal state
		// from the caller's slice.
		msgs := make([]providers.Message, len(history))
		copy(msgs, history)
		session.Messages = msgs
		session.Updated = time.Now()
	}
}

func (sm *SessionManager) HasVerbosePreference(key string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}

	return session.VerboseLevel != "" || session.VerboseMode
}

// GetVerboseMode returns the verbose mode setting for a session (legacy compatibility).
func (sm *SessionManager) GetVerboseMode(key string) bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}
	return session.VerboseMode
}

// SetVerboseMode sets the verbose mode for a session and persists it (legacy compatibility).
func (sm *SessionManager) SetVerboseMode(key string, enabled bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		// Create session if it doesn't exist
		session = &Session{
			Key:      key,
			Messages: []providers.Message{},
			Created:  time.Now(),
		}
		sm.sessions[key] = session
	}

	session.VerboseMode = enabled
	session.Updated = time.Now()

	// Persist immediately
	return sm.saveUnlocked(key)
}

// GetVerboseLevel returns the verbose level for a session ("off", "basic", or "full").
// Migration: if VerboseMode is true but VerboseLevel is empty, returns "full".
func (sm *SessionManager) GetVerboseLevel(key string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return "off"
	}

	// Migration: handle legacy VerboseMode field
	if session.VerboseLevel == "" && session.VerboseMode {
		return "full"
	}
	if session.VerboseLevel == "" {
		return "off"
	}
	return session.VerboseLevel
}

// SetVerboseLevel sets the verbose level for a session and persists it.
func (sm *SessionManager) SetVerboseLevel(key string, level string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		// Create session if it doesn't exist
		session = &Session{
			Key:      key,
			Messages: []providers.Message{},
			Created:  time.Now(),
		}
		sm.sessions[key] = session
	}

	session.VerboseLevel = level
	session.Updated = time.Now()

	// Persist immediately
	return sm.saveUnlocked(key)
}

// GetTokenCounts returns the input and output token counts for a session.
func (sm *SessionManager) GetTokenCounts(key string) (inputTokens, outputTokens int) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return 0, 0
	}
	return session.InputTokens, session.OutputTokens
}

// AddTokenCounts adds token counts to a session.
func (sm *SessionManager) AddTokenCounts(key string, inputTokens, outputTokens int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session = &Session{
			Key:      key,
			Messages: []providers.Message{},
			Created:  time.Now(),
		}
		sm.sessions[key] = session
	}

	session.InputTokens += inputTokens
	session.OutputTokens += outputTokens
	session.Updated = time.Now()
}

// ResetTokenCounts resets the input and output token counts for a session to zero.
func (sm *SessionManager) ResetTokenCounts(key string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	session.InputTokens = 0
	session.OutputTokens = 0
	session.Updated = time.Now()
}

// saveUnlocked saves a session without acquiring the lock (caller must hold lock).
func (sm *SessionManager) saveUnlocked(key string) error {
	if sm.storage == "" {
		return nil
	}

	filename := sanitizeFilename(key)

	if filename == "." || !filepath.IsLocal(filename) || strings.ContainsAny(filename, `/\`) {
		return os.ErrInvalid
	}

	stored, ok := sm.sessions[key]
	if !ok {
		return nil
	}

	snapshot := Session{
		Key:          stored.Key,
		Name:         stored.Name,
		Summary:      stored.Summary,
		VerboseMode:  stored.VerboseMode,
		Created:      stored.Created,
		Updated:      stored.Updated,
		InputTokens:  stored.InputTokens,
		OutputTokens: stored.OutputTokens,
	}
	if len(stored.Messages) > 0 {
		snapshot.Messages = make([]providers.Message, len(stored.Messages))
		copy(snapshot.Messages, stored.Messages)
	} else {
		snapshot.Messages = []providers.Message{}
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	sessionPath := filepath.Join(sm.storage, filename+".json")
	tmpFile, err := os.CreateTemp(sm.storage, "session-*.tmp")
	if err != nil {
		return err
	}

	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(0644); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, sessionPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}
