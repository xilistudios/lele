package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
const indexFileName = "_index.json"

type Session struct {
	Key                string              `json:"key"`
	Name               string              `json:"name,omitempty"`
	Mode               string              `json:"mode,omitempty"` // "chat", "agent", "group" (default empty = "agent")
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
	InputTokens     int `json:"input_tokens,omitempty"`
	OutputTokens    int `json:"output_tokens,omitempty"`
	CompactionCount int `json:"compaction_count,omitempty"`
}

// sessionMetadata holds lightweight session info for sessions not yet
// fully loaded into memory. This allows listing sessions without
// deserializing their entire message history.
type sessionMetadata struct {
	Key     string    `json:"key"`
	Name    string    `json:"name"`
	Mode    string    `json:"mode,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

type SessionManager struct {
	sessions   map[string]*Session
	mu         sync.RWMutex
	storage    string
	loadOnce   sync.Once // ensures loadSessions runs exactly once, on first access
	indexDirty bool      // true when sessionMeta has been modified since last index save

	// Lazy loading: lightweight metadata for sessions not yet loaded into memory.
	// Populated by loadSessionMetadata() instead of loading full message history.
	sessionMeta map[string]*sessionMetadata // keyed by session key

	// LRU eviction
	maxInMemory int                  // max sessions to keep in memory (0 = unlimited). Default: 50.
	evictionTTL time.Duration        // idle time before a session is eligible for eviction. Default: 30m.
	accessTimes map[string]time.Time // last access time per session key (for LRU)
}

func NewSessionManager(storage string) *SessionManager {
	sm := &SessionManager{
		sessions:    make(map[string]*Session),
		storage:     storage,
		sessionMeta: make(map[string]*sessionMetadata),
		maxInMemory: 50,
		evictionTTL: 30 * time.Minute,
		accessTimes: make(map[string]time.Time),
	}

	if storage != "" {
		os.MkdirAll(storage, 0755)
	}

	return sm
}

// ensureLoaded triggers loadSessionMetadata exactly once, on the first call.
// Must be called BEFORE acquiring sm.mu to avoid deadlock.
func (sm *SessionManager) ensureLoaded() {
	sm.loadOnce.Do(func() {
		if sm.storage != "" {
			sm.loadSessionMetadata()
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

// loadSessionFromDisk loads a full session (including messages) from disk
// into the sessions map. Called when a session is accessed that exists in
// metadata but not in the in-memory map.
// Caller must hold sm.mu (write lock).
func (sm *SessionManager) loadSessionFromDisk(key string) (*Session, bool) {
	// Check if already loaded
	if s, ok := sm.sessions[key]; ok {
		return s, true
	}

	// Check if we have metadata for this session
	_, ok := sm.sessionMeta[key]
	if !ok {
		return nil, false
	}

	// Load from disk
	filename := sanitizeFilename(key)
	sessionPath := filepath.Join(sm.storage, filename+".json")
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		// Session file may have been deleted; clean up metadata
		delete(sm.sessionMeta, key)
		return nil, false
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, false
	}

	// Enforce memory limit before adding a new session
	sm.evictIfNeeded()

	sm.sessions[key] = &session
	sm.accessTimes[key] = time.Now()
	return &session, true
}

// touchSession updates the last access time for a session.
// Caller must hold at least sm.mu (read lock is fine for map write
// since accessTimes is only used under the write path of evictIfNeeded).
func (sm *SessionManager) touchSession(key string) {
	sm.accessTimes[key] = time.Now()
}

// evictIfNeeded evicts idle sessions when the in-memory session count
// exceeds maxInMemory. Caller must hold sm.mu (write lock).
func (sm *SessionManager) evictIfNeeded() {
	if sm.maxInMemory <= 0 {
		return
	}

	// First pass: evict sessions that have been idle longer than evictionTTL
	if sm.evictionTTL > 0 {
		cutoff := time.Now().Add(-sm.evictionTTL)
		for key, lastAccess := range sm.accessTimes {
			if lastAccess.Before(cutoff) {
				if _, ok := sm.sessions[key]; ok {
					_ = sm.saveUnlocked(key)
				}
				delete(sm.sessions, key)
				delete(sm.accessTimes, key)
				// Also clean up sessionMeta for evicted sessions whose
				// underlying file no longer exists or is stale.
				// Keep metadata for sessions that still exist on disk
				// (needed for ListSessions), but only if the session
				// might be reloaded. We keep it — metadata is tiny.
			}
		}
	}

	// Second pass: if still over limit, evict least recently used
	if len(sm.sessions) <= sm.maxInMemory {
		return
	}

	// Find LRU sessions to evict
	type sessionAccess struct {
		key  string
		time time.Time
	}
	accesses := make([]sessionAccess, 0, len(sm.accessTimes))
	for key, t := range sm.accessTimes {
		if _, ok := sm.sessions[key]; ok {
			accesses = append(accesses, sessionAccess{key, t})
		}
	}
	sort.Slice(accesses, func(i, j int) bool {
		return accesses[i].time.Before(accesses[j].time)
	})

	toEvict := len(sm.sessions) - sm.maxInMemory
	for i := 0; i < toEvict && i < len(accesses); i++ {
		key := accesses[i].key
		_ = sm.saveUnlocked(key)
		delete(sm.sessions, key)
		delete(sm.accessTimes, key)
		logger.InfoCF("session", "LRU evicted session", map[string]interface{}{
			"session_key": key,
		})
	}
}

// CleanupIdleSessions evicts sessions that have been idle longer than evictionTTL.
// Unlike evictIfNeeded, this runs unconditionally (not just when adding new sessions).
// Should be called periodically (e.g., via a background goroutine) to ensure
// idle sessions are evicted even when no new sessions are being created.
func (sm *SessionManager) CleanupIdleSessions() int {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	evicted := 0
	if sm.evictionTTL > 0 {
		cutoff := time.Now().Add(-sm.evictionTTL)
		for key, lastAccess := range sm.accessTimes {
			if lastAccess.Before(cutoff) {
				if _, ok := sm.sessions[key]; ok {
					_ = sm.saveUnlocked(key)
				}
				delete(sm.sessions, key)
				delete(sm.accessTimes, key)
				evicted++
			}
		}
	}

	// Also clean up orphaned sessionMeta entries: metadata for sessions
	// whose files no longer exist on disk.
	if sm.storage != "" {
		for key := range sm.sessionMeta {
			filename := sanitizeFilename(key)
			sessionPath := filepath.Join(sm.storage, filename+".json")
			if _, err := os.Stat(sessionPath); err != nil {
				delete(sm.sessionMeta, key)
			}
		}
	}

	if evicted > 0 {
		logger.InfoCF("session", "Idle sessions cleaned up", map[string]interface{}{
			"evicted": evicted,
		})
	}

	if sm.indexDirty {
		sm.saveIndexUnlocked()
		sm.indexDirty = false
	}
	return evicted
}

// StartCleanupGoroutine launches a background goroutine that periodically
// calls CleanupIdleSessions. Returns a stop function that terminates the
// goroutine. The interval should be shorter than evictionTTL for timely cleanup.
func (sm *SessionManager) StartCleanupGoroutine(interval time.Duration) func() {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				sm.CleanupIdleSessions()
			}
		}
	}()
	return func() { close(stop) }
}

// SetMaxInMemory sets the maximum number of sessions to keep in memory.
// 0 means unlimited (no LRU eviction).
func (sm *SessionManager) SetMaxInMemory(max int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.maxInMemory = max
}

// SetEvictionTTL sets how long a session must be idle before it's eligible
// for eviction. 0 means no TTL-based eviction.
func (sm *SessionManager) SetEvictionTTL(ttl time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.evictionTTL = ttl
}

func (sm *SessionManager) GetOrCreate(key string) *Session {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Try in-memory first
	if session, ok := sm.sessions[key]; ok {
		sm.touchSession(key)
		return session
	}

	// Try loading from disk
	if session, ok := sm.loadSessionFromDisk(key); ok {
		sm.touchSession(key)
		return session
	}

	// Create new session
	session := &Session{
		Key:      key,
		Messages: []providers.Message{},
		Created:  time.Now(),
		Updated:  time.Now(),
	}
	sm.evictIfNeeded()
	sm.sessions[key] = session
	sm.accessTimes[key] = time.Now()
	// Register in metadata
	sm.sessionMeta[key] = &sessionMetadata{
		Key:     key,
		Mode:    session.Mode,
		Created: session.Created,
		Updated: session.Updated,
	}
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
		session, ok = sm.loadSessionFromDisk(sessionKey)
		if !ok {
			session = &Session{
				Key:      sessionKey,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[sessionKey] = session
		}
	}
	sm.touchSession(sessionKey)

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
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Try in-memory first
	session, ok := sm.sessions[key]
	if !ok {
		// Try loading from disk (needs write lock, which we already hold)
		session, ok = sm.loadSessionFromDisk(key)
	}
	if !ok {
		logger.DebugCF("session", "GetHistory: session not found", map[string]interface{}{
			"session_key": key,
		})
		return []providers.Message{}
	}

	sm.touchSession(key)
	history := make([]providers.Message, len(session.Messages))
	copy(history, session.Messages)
	logger.DebugCF("session", "GetHistory: returning history", map[string]interface{}{
		"session_key":    key,
		"messages_count": len(history),
	})
	return history
}

// GetHistoryView returns a read-only reference to the session's message slice.
// The caller MUST NOT modify the returned slice or any messages in it.
// This avoids a copy when the caller only needs to read the messages
// (e.g., token estimation, status display).
// For external use where the caller may modify, use GetHistory instead.
func (sm *SessionManager) GetHistoryView(key string) []providers.Message {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Try in-memory first
	if session, ok := sm.sessions[key]; ok {
		sm.touchSession(key)
		return session.Messages
	}
	// Try loading from disk
	if session, ok := sm.loadSessionFromDisk(key); ok {
		sm.touchSession(key)
		return session.Messages
	}
	return []providers.Message{}
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

	// Try in-memory first
	if session, ok := sm.sessions[key]; ok {
		return session.Name
	}
	// Try metadata
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Name
	}
	return ""
}

func (sm *SessionManager) GetUpdated(key string) time.Time {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return session.Updated
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Updated
	}
	return time.Time{}
}

func (sm *SessionManager) GetCreated(key string) time.Time {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if session, ok := sm.sessions[key]; ok {
		return session.Created
	}
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Created
	}
	return time.Time{}
}

func (sm *SessionManager) SetSummary(key string, summary string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return
		}
	}

	session.Summary = summary
	session.Updated = time.Now()
	sm.touchSession(key)
}

func (sm *SessionManager) SetName(key string, name string) error {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:      key,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.Name = strings.TrimSpace(name)
	session.Updated = time.Now()
	sm.touchSession(key)

	return sm.saveUnlocked(key)
}

func (sm *SessionManager) TruncateHistory(key string, keepLast int) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return
		}
	}

	if keepLast <= 0 {
		session.Messages = []providers.Message{}
		session.Updated = time.Now()
		sm.touchSession(key)
		return
	}

	if len(session.Messages) <= keepLast {
		return
	}

	session.Messages = session.Messages[len(session.Messages)-keepLast:]
	session.Updated = time.Now()
	sm.touchSession(key)
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
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return
		}
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
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return false
		}
	}

	if len(session.Messages) == 0 {
		return false
	}

	session.Messages = session.Messages[:len(session.Messages)-1]
	session.Updated = time.Now()
	sm.touchSession(key)
	return true
}

func (sm *SessionManager) ShouldStartFreshSession(key string, threshold time.Duration) (bool, time.Duration) {
	if threshold <= 0 {
		return false, 0
	}

	sm.ensureLoaded()
	sm.mu.Lock() // write lock for loadSessionFromDisk
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
	}
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
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return sm.saveUnlocked(key)
}

// loadSessionMetadata reads all session files but only parses lightweight
// metadata (key, name, created, updated) — NOT the full message history.
// This dramatically reduces startup memory for deployments with many sessions.
// Full sessions are loaded on-demand via loadSessionFromDisk().
//
// On subsequent startups, the metadata is loaded from a cached index file
// (_index.json), reducing startup from ~1.2s to ~3ms. On first startup or
// when the index is stale, parallel loading is used (~120ms).
func (sm *SessionManager) loadSessionMetadata() error {
	files, err := os.ReadDir(sm.storage)
	if err != nil {
		return err
	}

	// Count session files (exclude _index.json)
	fileCount := 0
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		if file.Name() == indexFileName {
			continue
		}
		fileCount++
	}

	// Fast path: try loading from index file
	if cached, err := sm.loadIndex(); err == nil && len(cached) == fileCount {
		// Index is fresh — use it directly
		sm.sessionMeta = cached
		return nil
	}

	// Slow path: build index from files using parallel loading
	sm.loadSessionMetadataParallel(files)

	// Save index for next startup
	sm.saveIndexUnlocked()
	return nil
}

// loadIndex reads the cached metadata index from _index.json.
func (sm *SessionManager) loadIndex() (map[string]*sessionMetadata, error) {
	if sm.storage == "" {
		return nil, os.ErrNotExist
	}
	indexPath := filepath.Join(sm.storage, indexFileName)
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	var cached map[string]*sessionMetadata
	if err := json.Unmarshal(data, &cached); err != nil {
		return nil, err
	}
	return cached, nil
}

// saveIndexUnlocked writes the current sessionMeta to _index.json.
// Caller must hold sm.mu (write lock).
func (sm *SessionManager) saveIndexUnlocked() error {
	if sm.storage == "" {
		return nil
	}
	indexData, err := json.Marshal(sm.sessionMeta)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(sm.storage, indexFileName), indexData, 0644)
}

// loadSessionMetadataParallel loads metadata from all session files in parallel
// using up to min(NumCPU, 16) workers. Caller must NOT hold sm.mu.
func (sm *SessionManager) loadSessionMetadataParallel(files []os.DirEntry) {
	type metaResult struct {
		key  string
		meta *sessionMetadata
	}

	// Collect session file entries (exclude _index.json)
	var sessionFiles []os.DirEntry
	for _, file := range files {
		if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
			continue
		}
		if file.Name() == indexFileName {
			continue
		}
		sessionFiles = append(sessionFiles, file)
	}

	// Determine worker count: min(NumCPU, 16, len(files))
	workers := runtime.NumCPU()
	if workers > 16 {
		workers = 16
	}
	if workers > len(sessionFiles) {
		workers = len(sessionFiles)
	}
	if workers < 1 {
		workers = 1
	}

	jobs := make(chan os.DirEntry, len(sessionFiles))
	results := make(chan metaResult, len(sessionFiles))

	// Launch workers
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				path := filepath.Join(sm.storage, file.Name())
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				var meta struct {
					Key     string    `json:"key"`
					Name    string    `json:"name"`
					Mode    string    `json:"mode,omitempty"`
					Created time.Time `json:"created"`
					Updated time.Time `json:"updated"`
				}
				if err := json.Unmarshal(data, &meta); err != nil {
					continue
				}
				key := meta.Key
				if key == "" {
					key = strings.ReplaceAll(file.Name()[:len(file.Name())-5], "_", ":")
				}
				results <- metaResult{
					key: key,
					meta: &sessionMetadata{
						Key:     key,
						Name:    meta.Name,
						Mode:    meta.Mode,
						Created: meta.Created,
						Updated: meta.Updated,
					},
				}
			}
		}()
	}

	// Feed jobs
	for _, f := range sessionFiles {
		jobs <- f
	}
	close(jobs)

	// Wait for workers to finish, then close results
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	for r := range results {
		sm.sessionMeta[r.key] = r.meta
	}
}

// SetHistory updates the messages of a session.
func (sm *SessionManager) SetHistory(key string, history []providers.Message) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return
		}
	}

	// Create a deep copy to strictly isolate internal state
	// from the caller's slice.
	msgs := make([]providers.Message, len(history))
	copy(msgs, history)
	session.Messages = msgs
	session.Updated = time.Now()
	sm.touchSession(key)
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
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			// Create session if it doesn't exist
			session = &Session{
				Key:      key,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.VerboseMode = enabled
	session.Updated = time.Now()
	sm.touchSession(key)

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
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			// Create session if it doesn't exist
			session = &Session{
				Key:      key,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.VerboseLevel = level
	session.Updated = time.Now()
	sm.touchSession(key)

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
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			// Create session if it doesn't exist
			session = &Session{
				Key:      key,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.Model = model
	session.Updated = time.Now()
	sm.touchSession(key)

	// Persist immediately
	return sm.saveUnlocked(key)
}

// GetMode returns the mode override for a session.
// Returns "" if not set. Callers should normalize "" to "agent" (backward compat).
func (sm *SessionManager) GetMode(key string) string {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[key]
	if ok {
		return session.Mode
	}
	// Try metadata
	if meta, ok := sm.sessionMeta[key]; ok {
		return meta.Mode
	}
	return ""
}

// SetMode sets the mode for a session and persists it.
// Valid values: "", "chat", "agent", "group".
func (sm *SessionManager) SetMode(key string, mode string) error {
	// Validate mode
	validModes := map[string]bool{"": true, "chat": true, "agent": true, "group": true}
	if !validModes[mode] {
		return fmt.Errorf("invalid mode %q: must be one of \"\", \"chat\", \"agent\", \"group\"", mode)
	}

	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			// Create session if it doesn't exist
			session = &Session{
				Key:      key,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.Mode = mode
	session.Updated = time.Now()
	sm.touchSession(key)

	// Update metadata
	sm.sessionMeta[key] = &sessionMetadata{
		Key:     session.Key,
		Name:    session.Name,
		Mode:    session.Mode,
		Created: session.Created,
		Updated: session.Updated,
	}

	return sm.saveUnlocked(key)
}

// ListSessionsByMode returns sessions whose effective mode matches the given mode.
// The parameter mode is normalized: "" is treated as "agent".
// For each session, its effective mode is session.Mode; if "" it is treated as "agent".
func (sm *SessionManager) ListSessionsByMode(mode string) []*Session {
	// Normalize requested mode: "" -> "agent"
	normalizedMode := mode
	if normalizedMode == "" {
		normalizedMode = "agent"
	}

	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	res := make([]*Session, 0)

	seen := make(map[string]bool)
	for key := range sm.sessions {
		seen[key] = true
		session := sm.sessions[key]
		effectiveMode := session.Mode
		if effectiveMode == "" {
			effectiveMode = "agent"
		}
		if effectiveMode == normalizedMode {
			res = append(res, session)
		}
	}
	for key, meta := range sm.sessionMeta {
		if !seen[key] {
			effectiveMode := meta.Mode
			if effectiveMode == "" {
				effectiveMode = "agent"
			}
			if effectiveMode == normalizedMode {
				res = append(res, &Session{
					Key:     meta.Key,
					Name:    meta.Name,
					Mode:    meta.Mode,
					Created: meta.Created,
					Updated: meta.Updated,
				})
			}
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return res[i].Updated.After(res[j].Updated)
	})
	return res
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
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:     key,
				Created: time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.ThinkingLevel = level
	session.Updated = time.Now()
	sm.touchSession(key)

	return sm.saveUnlocked(key)
}

// GetTokenCounts returns the input and output token counts for a session.
func (sm *SessionManager) GetTokenCounts(key string) (inputTokens, outputTokens int) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return 0, 0
		}
		sm.touchSession(key)
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
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:      key,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.InputTokens += inputTokens
	session.OutputTokens += outputTokens
	session.Updated = time.Now()
	sm.touchSession(key)
}

// ResetTokenCounts resets the input and output token counts for a session to zero.
func (sm *SessionManager) ResetTokenCounts(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return
		}
	}

	session.InputTokens = 0
	session.OutputTokens = 0
	session.Updated = time.Now()
	sm.touchSession(key)
}

// IncrementCompactionCount atomically increments the compaction counter for a session.
func (sm *SessionManager) IncrementCompactionCount(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:      key,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
		}
	}

	session.CompactionCount++
	session.Updated = time.Now()
	sm.touchSession(key)
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
		Key:             stored.Key,
		Name:            stored.Name,
		Mode:            stored.Mode,
		Summary:         stored.Summary,
		VerboseMode:     stored.VerboseMode,
		VerboseLevel:    stored.VerboseLevel,
		Model:           stored.Model,
		Created:         stored.Created,
		Updated:         stored.Updated,
		InputTokens:     stored.InputTokens,
		OutputTokens:    stored.OutputTokens,
		CompactionCount: stored.CompactionCount,
	}
	if len(stored.Messages) > 0 {
		snapshot.Messages = make([]providers.Message, len(stored.Messages))
		copy(snapshot.Messages, stored.Messages)
	} else {
		snapshot.Messages = []providers.Message{}
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

	// Stream JSON directly to the temp file instead of buffering in memory.
	// json.Encoder writes to the file as it serializes, avoiding a full
	// in-memory copy of the JSON representation.
	encoder := json.NewEncoder(tmpFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&snapshot); err != nil {
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

	// Update metadata to reflect the saved state
	sm.sessionMeta[key] = &sessionMetadata{
		Key:     stored.Key,
		Name:    stored.Name,
		Mode:    stored.Mode,
		Created: stored.Created,
		Updated: stored.Updated,
	}
	sm.indexDirty = true

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
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok || len(session.Messages) == 0 {
			return
		}
	}
	if len(session.Messages) == 0 {
		return
	}

	lastMsg := &session.Messages[len(session.Messages)-1]
	if lastMsg.Role == "assistant" && lastMsg.Streaming {
		session.Updated = time.Now()
		sm.touchSession(key)
		sm.flushStreamNow(key)
	}
}

// HasStreamedContent returns true if the session already had content delivered
// via streaming chunks this turn. Used to prevent duplicate message.stream
// delivery. It checks the in-memory flag (set by AppendAssistantChunk and
// cleared when a new user message arrives).
// Note: If the session is not in memory (evicted), returns false. The session
// will be loaded on-demand by the streaming methods.
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
// Note: If the session is not in memory (evicted), returns nil. The session
// will be loaded on-demand by the streaming methods.
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
// Uses lazy loading to load sessions from disk on demand.
func (sm *SessionManager) getOrCreateUnlocked(key string) *Session {
	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			session = &Session{
				Key:      key,
				Messages: []providers.Message{},
				Created:  time.Now(),
			}
			sm.evictIfNeeded()
			sm.sessions[key] = session
			sm.sessionMeta[key] = &sessionMetadata{
				Key:     key,
				Mode:    session.Mode,
				Created: session.Created,
				Updated: session.Updated,
			}
		}
	}
	sm.touchSession(key)
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

// PruneExcludedMessages removes all messages marked ExcludeFromContext from
// the in-memory slice. These messages are already persisted to disk (via Save)
// and are stripped before sending to the LLM, so keeping them in RAM is wasteful.
// This should be called after summarization/compaction has completed and the
// session has been saved.
func (sm *SessionManager) PruneExcludedMessages(key string) {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[key]
	if !ok {
		session, ok = sm.loadSessionFromDisk(key)
		if !ok {
			return
		}
	}

	pruned := 0
	kept := make([]providers.Message, 0, len(session.Messages))
	for _, msg := range session.Messages {
		if msg.ExcludeFromContext {
			pruned++
			continue
		}
		kept = append(kept, msg)
	}

	if pruned > 0 {
		session.Messages = kept
		logger.InfoCF("session", "Pruned excluded messages from memory", map[string]interface{}{
			"session_key": key,
			"pruned":      pruned,
			"remaining":   len(kept),
		})
	}
}

// EvictSession removes a session from the in-memory map.
// The session data remains on disk and can be reloaded on demand.
// Returns true if the session was found and evicted.
func (sm *SessionManager) EvictSession(key string) bool {
	sm.ensureLoaded()
	sm.mu.Lock()
	defer sm.mu.Unlock()

	_, ok := sm.sessions[key]
	if ok {
		// Save before evicting to ensure latest state is persisted
		_ = sm.saveUnlocked(key)
		delete(sm.sessions, key)
		delete(sm.accessTimes, key)
		logger.InfoCF("session", "Session evicted from memory", map[string]interface{}{
			"session_key": key,
		})
	}

	// For subagent sessions (key contains ":subagent-"), also remove
	// sessionMeta to prevent unbounded metadata growth. Subagent sessions
	// are transient — once evicted, they should not be reloaded.
	if strings.Contains(key, ":subagent-") {
		delete(sm.sessionMeta, key)
	}

	return ok
}

// ActiveCount returns the number of sessions that exist (have metadata on disk).
// This is useful for detecting agents with active conversations.
func (sm *SessionManager) ActiveCount() int {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	// Count sessions that have metadata (exist on disk)
	return len(sm.sessionMeta)
}

// ListSessions returns a slice of all sessions (including metadata-only sessions
// not yet fully loaded into memory), sorted by updated time descending.
// Sessions only in metadata have nil Messages — they are loaded on-demand
// when accessed via GetOrCreate or GetHistory.
func (sm *SessionManager) ListSessions() []*Session {
	sm.ensureLoaded()
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// For sessions in memory, return the full session
	// For sessions only in metadata, create a lightweight Session with no messages
	res := make([]*Session, 0, len(sm.sessionMeta))

	// Collect all keys from both maps
	seen := make(map[string]bool)
	for key := range sm.sessions {
		seen[key] = true
		res = append(res, sm.sessions[key])
	}
	for key, meta := range sm.sessionMeta {
		if !seen[key] {
			res = append(res, &Session{
				Key:     meta.Key,
				Name:    meta.Name,
				Mode:    meta.Mode,
				Created: meta.Created,
				Updated: meta.Updated,
				// Messages is nil — not loaded
			})
		}
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
	sm.mu.Lock() // need write lock for potential loadSessionFromDisk
	defer sm.mu.Unlock()

	prefix := parentPrefix + ":subagent-"
	var results []SubagentSessionInfo

	// Collect subagent keys from both metadata and in-memory sessions
	subagentKeys := make(map[string]bool)
	for key := range sm.sessionMeta {
		if strings.HasPrefix(key, prefix) {
			subagentKeys[key] = true
		}
	}
	for key := range sm.sessions {
		if strings.HasPrefix(key, prefix) {
			subagentKeys[key] = true
		}
	}

	// Load each matching session from disk on-demand (if not already in memory)
	for key := range subagentKeys {
		session, ok := sm.sessions[key]
		if !ok {
			session, ok = sm.loadSessionFromDisk(key)
			if !ok {
				continue
			}
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
