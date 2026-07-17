package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/providers"
)

// maxStoredMessages is the maximum number of messages kept in a session file.
// When exceeded, the oldest excluded messages are pruned on save.
const maxStoredMessages = 10000

type Session struct {
	Key                string              `json:"key"`
	Name               string              `json:"name,omitempty"`
	Messages           []providers.Message `json:"messages"`
	Summary            string              `json:"summary,omitempty"`
	VerboseMode        bool                `json:"verbose_mode,omitempty"`   // Deprecated: use VerboseLevel
	VerboseLevel       string              `json:"verbose_level,omitempty"`  // "off", "basic", or "full"
	Model              string              `json:"model,omitempty"`          // Session-specific model override
	ThinkingLevel      string              `json:"thinking_level,omitempty"` // "off", "low", "medium", "high"
	Created            time.Time           `json:"created"`
	Updated            time.Time           `json:"updated"`
	lastStreamFlush    time.Time           // throttle for stream persistence (not persisted)
	hadStreamedContent bool                // tracks if content was delivered via streaming this turn (not persisted)
	// Token tracking
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
	storage  string
	loadOnce sync.Once // ensures loadSessions runs exactly once, on first access
}

func NewSessionManager(storage string) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		storage:  storage,
	}

	if storage != "" {
		os.MkdirAll(storage, 0755)
		// loadSessions is deferred to ensureLoaded() — called on first access
	}

	return sm
}

// ensureLoaded triggers loadSessions exactly once, on the first call.
// Must be called BEFORE acquiring sm.mu to avoid deadlock, since
// loadSessions writes to sm.sessions directly without the mutex.
func (sm *SessionManager) ensureLoaded() {
	sm.loadOnce.Do(func() {
		if sm.storage != "" {
			sm.loadSessions()
		}
	})
}

// MigrateFromWorkspace moves session JSON files from an old per-workspace
// sessions directory into the unified global sessions directory. Existing
// files in the destination are never overwritten (first migration wins).
// Errors are logged but not returned — migration is best-effort.
func MigrateFromWorkspace(oldDir, newDir string) {
	if oldDir == "" || newDir == "" || oldDir == newDir {
		return
	}

	info, err := os.Stat(oldDir)
	if err != nil || !info.IsDir() {
		return
	}

	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return
	}

	if err := os.MkdirAll(newDir, 0755); err != nil {
		logger.WarnCF("session", "Cannot create unified sessions dir",
			map[string]interface{}{"path": newDir, "error": err.Error()})
		return
	}

	migrated := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		srcPath := filepath.Join(oldDir, entry.Name())
		dstPath := filepath.Join(newDir, entry.Name())

		// Never overwrite — the first agent that migrates wins.
		if _, err := os.Stat(dstPath); err == nil {
			continue
		}

		if err := os.Rename(srcPath, dstPath); err != nil {
			// If rename fails (e.g. cross-device), fall back to copy+delete.
			if err := copyFile(srcPath, dstPath); err != nil {
				logger.WarnCF("session", "Failed to migrate session file",
					map[string]interface{}{
						"file":  entry.Name(),
						"src":   oldDir,
						"dst":   newDir,
						"error": err.Error(),
					})
				continue
			}
			// Remove source after successful copy
			_ = os.Remove(srcPath)
		}
		migrated++
	}

	if migrated > 0 {
		logger.InfoCF("session", "Migrated sessions to unified directory",
			map[string]interface{}{
				"count": migrated,
				"from":  oldDir,
				"to":    newDir,
			})
	}
}

// copyFile copies a file from src to dst. Used as fallback when os.Rename fails.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// StoragePath returns the storage directory path.
func (sm *SessionManager) StoragePath() string {
	return sm.storage
}

func (sm *SessionManager) GetOrCreate(key string) *Session {
	sm.ensureLoaded()
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
// If the last message is a streaming assistant message (added by AppendAssistantChunk),
// it updates that message in-place instead of appending a duplicate.
func (sm *SessionManager) AddFullMessage(sessionKey string, msg providers.Message) {
	sm.ensureLoaded()
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

	// New user message starts a new turn — clear the streamed content flag
	// so the deduplication logic is fresh for the next assistant response.
	if msg.Role == "user" {
		session.hadStreamedContent = false
	}

	// If the last message is a streaming assistant and this is an assistant
	// message, update it in-place to avoid duplicates.
	if msg.Role == "assistant" && len(session.Messages) > 0 {
		lastMsg := &session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming {
			// Replace the streaming message with the final version.
			// Keep hadStreamedContent=true so HasStreamedContent still returns
			// true until the next user message arrives.
			msg.Streaming = false
			*lastMsg = msg
			session.Updated = time.Now()
			return
		}
	}

	session.Messages = append(session.Messages, msg)
	session.Updated = time.Now()
}

func (sm *SessionManager) GetHistory(key string) []providers.Message {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		logger.DebugCF("session", "GetHistory: session not found", map[string]interface{}{
			"session_key": key,
		})
		return []providers.Message{}
	}

	history := make([]providers.Message, len(session.Messages))
	copy(history, session.Messages)
	logger.DebugCF("session", "GetHistory: returning history", map[string]interface{}{
		"session_key":    key,
		"messages_count": len(history),
	})
	return history
}

func (sm *SessionManager) GetSummary(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return ""
	}
	return session.Summary
}

func (sm *SessionManager) GetName(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return ""
	}
	return session.Name
}

func (sm *SessionManager) GetUpdated(key string) time.Time {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return time.Time{}
	}
	return session.Updated
}

func (sm *SessionManager) GetCreated(key string) time.Time {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return time.Time{}
	}
	return session.Created
}

func (sm *SessionManager) SetSummary(key string, summary string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if ok {
		session.Summary = summary
		session.Updated = time.Now()
	}
}

func (sm *SessionManager) SetName(key string, name string) error {
	sm.ensureLoaded()
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
	sm.ensureLoaded()
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

// isToolResultMessage returns true if the message is a tool result
// (role "tool" with a non-empty ToolCallID, or role "user" with ToolCallID).
func isToolResultMessage(msg providers.Message) bool {
	return (msg.Role == "tool" || msg.Role == "user") && msg.ToolCallID != ""
}

// ExcludeOldMessagesFromContext marks the first len(messages)-keepCount messages
// as excluded from the LLM context, preserving them in storage for the web UI.
// If keepCount <= 0, all messages are excluded.
func (sm *SessionManager) ExcludeOldMessagesFromContext(key string, keepCount int) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	if len(session.Messages) <= keepCount {
		return
	}

	excludeUpTo := len(session.Messages) - keepCount

	// Adjust boundary to avoid splitting tool_use/tool_result groups.
	// If the first kept message (at excludeUpTo) is a tool result whose
	// corresponding assistant tool_use is in the excluded range, move the
	// boundary forward to exclude the tool_result too. Repeat for any
	// consecutive tool results in the same group.
	for excludeUpTo < len(session.Messages) && isToolResultMessage(session.Messages[excludeUpTo]) {
		excludeUpTo++
	}

	// Also check: if the last excluded message is an assistant with tool_use
	// but the first kept message is NOT a tool_result, the tool_use blocks
	// are orphaned (no results). Move the boundary back to also exclude
	// this assistant message.
	if excludeUpTo > 0 {
		lastExcluded := session.Messages[excludeUpTo-1]
		if lastExcluded.Role == "assistant" && len(lastExcluded.ToolCalls) > 0 {
			// The assistant has tool_use blocks. Check if the next message
			// (first kept) is a tool_result for those.
			if excludeUpTo >= len(session.Messages) || !isToolResultMessage(session.Messages[excludeUpTo]) {
				// No tool_results follow — the tool_use is orphaned.
				// Move boundary back to also exclude this assistant message.
				excludeUpTo--
			}
		}
	}

	// Ensure we don't exclude everything
	if excludeUpTo <= 0 {
		return
	}

	for i := 0; i < excludeUpTo; i++ {
		session.Messages[i].ExcludeFromContext = true
	}
	session.Updated = time.Now()
}
func (sm *SessionManager) RemoveLastMessage(key string) bool {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}

	if len(session.Messages) == 0 {
		return false
	}

	session.Messages = session.Messages[:len(session.Messages)-1]
	session.Updated = time.Now()
	return true
}

func (sm *SessionManager) ShouldStartFreshSession(key string, threshold time.Duration) (bool, time.Duration) {
	if threshold <= 0 {
		return false, 0
	}

	sm.ensureLoaded()
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
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.saveUnlocked(key)
}

func (sm *SessionManager) loadSessions() error {
	files, err := os.ReadDir(sm.storage)
	if err != nil {
		return err
	}

	// Filter to JSON files only
	var jsonFiles []os.DirEntry
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		jsonFiles = append(jsonFiles, file)
	}

	if len(jsonFiles) == 0 {
		return nil
	}

	// Parallel load: read and parse files concurrently
	type loadResult struct {
		session *Session
	}
	results := make([]loadResult, len(jsonFiles))

	// Use a semaphore to limit concurrent file operations
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup

	for i, file := range jsonFiles {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			sessionPath := filepath.Join(sm.storage, name)
			data, err := os.ReadFile(sessionPath)
			if err != nil {
				return
			}

			var session Session
			if err := json.Unmarshal(data, &session); err != nil {
				return
			}
			results[idx] = loadResult{session: &session}
		}(i, file.Name())
	}

	wg.Wait()

	// Collect results into the sessions map
	for _, r := range results {
		if r.session != nil {
			sm.sessions[r.session.Key] = r.session
		}
	}

	return nil
}

// SetHistory updates the messages of a session.
func (sm *SessionManager) SetHistory(key string, history []providers.Message) {
	sm.ensureLoaded()
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
	sm.ensureLoaded()
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
	sm.ensureLoaded()
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
	sm.ensureLoaded()
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
	sm.ensureLoaded()
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
	sm.ensureLoaded()
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

// GetModel returns the model override for a session.
func (sm *SessionManager) GetModel(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return ""
	}
	return session.Model
}

// SetModel sets the model override for a session and persists it.
func (sm *SessionManager) SetModel(key string, model string) error {
	sm.ensureLoaded()
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

	session.Model = model
	session.Updated = time.Now()

	// Persist immediately
	return sm.saveUnlocked(key)
}

// GetThinkingLevel returns the thinking level for a session.
func (sm *SessionManager) GetThinkingLevel(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return ""
	}
	return session.ThinkingLevel
}

// SetThinkingLevel sets the thinking level for a session and persists it.
func (sm *SessionManager) SetThinkingLevel(key string, level string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session = &Session{
			Key:     key,
			Created: time.Now(),
		}
		sm.sessions[key] = session
	}

	session.ThinkingLevel = level
	session.Updated = time.Now()

	return sm.saveUnlocked(key)
}

// GetTokenCounts returns the input and output token counts for a session.
func (sm *SessionManager) GetTokenCounts(key string) (inputTokens, outputTokens int) {
	sm.ensureLoaded()
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
	sm.ensureLoaded()
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
	sm.ensureLoaded()
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

	// Prune old excluded messages when over the storage limit
	if len(stored.Messages) > maxStoredMessages {
		toRemove := len(stored.Messages) - maxStoredMessages
		kept := make([]providers.Message, 0, maxStoredMessages)
		removed := 0
		for _, msg := range stored.Messages {
			if removed < toRemove && msg.ExcludeFromContext {
				removed++
				continue
			}
			kept = append(kept, msg)
		}
		stored.Messages = kept
	}

	snapshot := Session{
		Key:          stored.Key,
		Name:         stored.Name,
		Summary:      stored.Summary,
		VerboseMode:  stored.VerboseMode,
		VerboseLevel: stored.VerboseLevel,
		Model:        stored.Model,
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

// ============================================================================
// Streaming support — persists assistant message chunks directly in the session
// file instead of using a separate streams/ directory.
// ============================================================================

// streamFlushInterval is the minimum time between session saves during active streaming.
const streamFlushInterval = 200 * time.Millisecond

// AppendAssistantChunk appends a content chunk to the in-progress assistant message.
// If no in-progress message exists, it creates one with Streaming=true.
// The session is saved to disk periodically (throttled) so the partial content
// survives restarts and allows reconnecting clients to recover the stream.
func (sm *SessionManager) AppendAssistantChunk(key, chunk string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session == nil {
		return
	}

	// Find or create the in-progress assistant message
	msg := sm.getOrCreateStreamingMsg(session)
	msg.Content += chunk
	session.Updated = time.Now()
	session.hadStreamedContent = true

	sm.maybeFlushStream(key)
}

// AppendReasoningChunk appends a reasoning/thinking chunk to the in-progress
// assistant message. Creates the streaming message if it doesn't exist yet.
func (sm *SessionManager) AppendReasoningChunk(key, chunk string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := sm.getOrCreateUnlocked(key)
	if session == nil {
		return
	}

	msg := sm.getOrCreateStreamingMsg(session)
	msg.ReasoningContent += chunk
	session.Updated = time.Now()
	session.hadStreamedContent = true

	sm.maybeFlushStream(key)
}

// FinalizeAssistantMessage marks the in-progress assistant message as complete
// by persisting the session to disk immediately. The Streaming flag is NOT
// cleared here — it stays until AddFullMessage replaces the streaming message
// with the final version. This allows HasStreamedContent to detect that content
// was already delivered via streaming chunks for deduplication.
func (sm *SessionManager) FinalizeAssistantMessage(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok || len(session.Messages) == 0 {
		return
	}

	lastMsg := &session.Messages[len(session.Messages)-1]
	if lastMsg.Role == "assistant" && lastMsg.Streaming {
		session.Updated = time.Now()
		sm.flushStreamNow(key)
	}
}

// HasStreamedContent returns true if the session already had content delivered
// via streaming chunks this turn. Used to prevent duplicate message.stream
// delivery. It checks the in-memory flag (set by AppendAssistantChunk and
// cleared when a new user message arrives).
func (sm *SessionManager) HasStreamedContent(key string) bool {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok {
		return false
	}

	// Check the in-memory flag first (survives Streaming flag being cleared by AddFullMessage)
	if session.hadStreamedContent {
		return true
	}

	// Fallback: check if the last message is still streaming
	if len(session.Messages) > 0 {
		lastMsg := session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming &&
			(lastMsg.Content != "" || lastMsg.ReasoningContent != "") {
			return true
		}
	}

	return false
}

// GetInProgressAssistant returns the in-progress assistant message, if any.
func (sm *SessionManager) GetInProgressAssistant(key string) *providers.Message {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if !ok || len(session.Messages) == 0 {
		return nil
	}

	lastMsg := session.Messages[len(session.Messages)-1]
	if lastMsg.Role == "assistant" && lastMsg.Streaming {
		msg := lastMsg
		return &msg
	}
	return nil
}

// getOrCreateUnlocked returns or creates a session (caller must hold mu).
func (sm *SessionManager) getOrCreateUnlocked(key string) *Session {
	session, ok := sm.sessions[key]
	if !ok {
		session = &Session{
			Key:      key,
			Messages: []providers.Message{},
			Created:  time.Now(),
		}
		sm.sessions[key] = session
	}
	return session
}

// getOrCreateStreamingMsg finds or creates the in-progress assistant message.
// Caller must hold sm.mu.
func (sm *SessionManager) getOrCreateStreamingMsg(session *Session) *providers.Message {
	if len(session.Messages) > 0 {
		lastMsg := &session.Messages[len(session.Messages)-1]
		if lastMsg.Role == "assistant" && lastMsg.Streaming {
			return lastMsg
		}
	}

	// Create a new streaming assistant message
	session.Messages = append(session.Messages, providers.Message{
		Role:      "assistant",
		Streaming: true,
	})
	return &session.Messages[len(session.Messages)-1]
}

// maybeFlushStream saves the session to disk if enough time has passed since
// the last stream flush. Caller must hold sm.mu.
func (sm *SessionManager) maybeFlushStream(key string) {
	if sm.storage == "" {
		return
	}

	session, ok := sm.sessions[key]
	if !ok {
		return
	}

	now := time.Now()
	if now.Sub(session.lastStreamFlush) >= streamFlushInterval {
		session.lastStreamFlush = now
		sm.saveUnlocked(key)
	}
}

// flushStreamNow saves the session to disk immediately. Caller must hold sm.mu.
func (sm *SessionManager) flushStreamNow(key string) {
	if sm.storage == "" {
		return
	}
	session, ok := sm.sessions[key]
	if ok {
		session.lastStreamFlush = time.Now()
	}
	sm.saveUnlocked(key)
}

// ActiveCount returns the number of sessions that have at least one message.
// This is useful for detecting agents with active conversations.
func (sm *SessionManager) ActiveCount() int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	count := 0
	for _, s := range sm.sessions {
		if len(s.Messages) > 0 {
			count++
		}
	}
	return count
}

// ListSessions returns a slice of all sessions loaded in memory, sorted by updated time descending.
func (sm *SessionManager) ListSessions() []*Session {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	res := make([]*Session, 0, len(sm.sessions))
	for _, s := range sm.sessions {
		res = append(res, s)
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Updated.After(res[j].Updated)
	})
	return res
}

// SubagentSessionInfo contains metadata about a persisted subagent session.
type SubagentSessionInfo struct {
	Key        string
	TaskID     string
	Created    time.Time
	Updated    time.Time
	Iterations int    // number of assistant messages
	Summary    string // session summary if available
	Name       string // session name if available
}

// FindSubagentSessions returns persisted subagent sessions whose keys start
// with the given parent prefix followed by ":subagent-". This allows the API
// to surface past subagents even after a server restart when the in-memory
// SubagentManager no longer tracks them.
func (sm *SessionManager) FindSubagentSessions(parentPrefix string) []SubagentSessionInfo {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	prefix := parentPrefix + ":subagent-"
	var results []SubagentSessionInfo

	for key, session := range sm.sessions {
		if !strings.HasPrefix(key, prefix) {
			continue
		}

		taskID := key[len(parentPrefix)+1:] // everything after "{parent}:"

		// Count assistant messages as iteration proxy
		iterations := 0
		for _, msg := range session.Messages {
			if msg.Role == "assistant" {
				iterations++
			}
		}

		summary := session.Summary
		if summary == "" && len(session.Messages) > 0 {
			// Use the last assistant message content as a summary fallback
			for i := len(session.Messages) - 1; i >= 0; i-- {
				if session.Messages[i].Role == "assistant" {
					content := strings.TrimSpace(session.Messages[i].Content)
					if len(content) > 200 {
						content = content[:200] + "…"
					}
					summary = content
					break
				}
			}
		}

		results = append(results, SubagentSessionInfo{
			Key:        key,
			TaskID:     taskID,
			Created:    session.Created,
			Updated:    session.Updated,
			Iterations: iterations,
			Summary:    summary,
			Name:       session.Name,
		})
	}

	return results
}
