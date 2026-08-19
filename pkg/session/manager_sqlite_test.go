package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/store"
)

// newTestStore creates a temporary SQLite store for testing.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSQLite_SessionManager_CreateAndLoad(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	// Create a session
	key := "test:session-1"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "Hello")
	sm.AddMessage(key, "assistant", "Hi there!")

	// Save
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify in SQLite
	repo := s.Sessions()
	meta, err := repo.GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta failed: %v", err)
	}
	if meta == nil {
		t.Fatal("session not found in SQLite")
	}
	if meta.Key != key {
		t.Errorf("expected key %q, got %q", key, meta.Key)
	}

	// Load messages from SQLite
	msgs, err := repo.LoadMessages(key)
	if err != nil {
		t.Fatalf("LoadMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// Create a new manager and load from SQLite
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	history := sm2.GetHistory(key)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages after reload, got %d", len(history))
	}
	if history[0].Content != "Hello" {
		t.Errorf("expected first message %q, got %q", "Hello", history[0].Content)
	}
	if history[1].Content != "Hi there!" {
		t.Errorf("expected second message %q, got %q", "Hi there!", history[1].Content)
	}
}

func TestSQLite_SessionManager_Streaming(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:streaming"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "Tell me a story")

	// Simulate streaming
	sm.AppendAssistantChunk(key, "Once ")
	sm.AppendAssistantChunk(key, "upon ")
	sm.AppendAssistantChunk(key, "a time...")

	// Save
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load and verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	history := sm2.GetHistory(key)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	assistant := history[1]
	if assistant.Content != "Once upon a time..." {
		t.Errorf("expected streaming content %q, got %q", "Once upon a time...", assistant.Content)
	}
}

func TestSQLite_SessionManager_TokenCounts(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:tokens"
	sm.GetOrCreate(key)
	sm.AddTokenCounts(key, 100, 50)
	sm.AddTokenCounts(key, 200, 100)

	// Save
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load and verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	input, output := sm2.GetTokenCounts(key)
	if input != 300 {
		t.Errorf("expected input tokens 300, got %d", input)
	}
	if output != 150 {
		t.Errorf("expected output tokens 150, got %d", output)
	}
}

func TestSQLite_SessionManager_Mode(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:mode"
	sm.GetOrCreate(key)
	if err := sm.SetMode(key, "chat"); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	// Save
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Load and verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	mode := sm2.GetMode(key)
	if mode != "chat" {
		t.Errorf("expected mode %q, got %q", "chat", mode)
	}

	// List by mode
	sessions := sm2.ListSessionsByMode("chat")
	if len(sessions) != 1 {
		t.Fatalf("expected 1 chat session, got %d", len(sessions))
	}
	if sessions[0].Key != key {
		t.Errorf("expected session key %q, got %q", key, sessions[0].Key)
	}
}

func TestSQLite_SessionManager_ListSessions(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	// Create multiple sessions
	sm.GetOrCreate("session-1")
	sm.AddMessage("session-1", "user", "hello")
	sm.GetOrCreate("session-2")
	sm.AddMessage("session-2", "user", "world")
	sm.GetOrCreate("session-3")
	sm.AddMessage("session-3", "user", "test")

	// Save all
	sm.Save("session-1")
	sm.Save("session-2")
	sm.Save("session-3")

	// List sessions
	sessions := sm.ListSessions()
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestSQLite_SessionManager_SessionExists(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:exists"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.Save(key)

	// Evict from memory
	sm.EvictSession(key)

	// Should still exist in SQLite
	if !sm.SessionExists(key) {
		t.Error("expected session to exist after eviction")
	}
}

func TestSQLite_SessionManager_StreamingUpdate(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:stream-update"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")

	// Add streaming message
	sm.AppendAssistantChunk(key, "part1 ")
	sm.Save(key)

	// Verify streaming message is persisted
	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 2 {
		t.Fatalf("expected 2 messages, got %d", count)
	}

	// Add more chunks
	sm.AppendAssistantChunk(key, "part2 ")
	sm.Save(key)

	// Should still be 2 messages (streaming message updated in place)
	count, _ = repo.MessageCount(key)
	if count != 2 {
		t.Fatalf("expected 2 messages after streaming update, got %d", count)
	}

	// Load and verify final content
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	history := sm2.GetHistory(key)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[1].Content != "part1 part2 " {
		t.Errorf("expected streaming content %q, got %q", "part1 part2 ", history[1].Content)
	}
}

func TestSQLite_SessionManager_Concurrent(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := "concurrent:" + string(rune('A'+id))
			sm.GetOrCreate(key)
			for j := 0; j < 100; j++ {
				sm.AddMessage(key, "user", "msg")
			}
			sm.Save(key)
		}(i)
	}
	wg.Wait()

	// Verify all sessions persisted
	repo := s.Sessions()
	metas, err := repo.ListSessionMeta()
	if err != nil {
		t.Fatalf("ListSessionMeta failed: %v", err)
	}
	if len(metas) != 10 {
		t.Fatalf("expected 10 sessions, got %d", len(metas))
	}
}

func TestSQLite_SessionManager_CompactionCount(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:compaction"
	sm.GetOrCreate(key)
	sm.IncrementCompactionCount(key)
	sm.IncrementCompactionCount(key)
	sm.Save(key)

	// Load and verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	session := sm2.GetOrCreate(key)
	if session.CompactionCount != 2 {
		t.Errorf("expected compaction count 2, got %d", session.CompactionCount)
	}
}

func TestSQLite_SessionManager_ThinkingLevel(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:thinking"
	sm.GetOrCreate(key)
	sm.SetThinkingLevel(key, "high")
	sm.Save(key)

	// Load and verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	level := sm2.GetThinkingLevel(key)
	if level != "high" {
		t.Errorf("expected thinking level %q, got %q", "high", level)
	}
}

func TestSQLite_SessionManager_TruncateHistory(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:truncate"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", "msg")
	}
	sm.Save(key)

	// Truncate to keep last 3
	sm.TruncateHistory(key, 3)
	sm.Save(key)

	// Verify
	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 3 {
		t.Fatalf("expected 3 messages after truncate, got %d", count)
	}
}

func TestSQLite_SessionManager_ThinkingLevel_Persistence(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:thinking-persist"
	sm.GetOrCreate(key)
	sm.SetThinkingLevel(key, "medium")
	sm.Save(key)

	// Create new manager to test persistence
	sm2 := NewSessionManager()
	sm2.SetStore(s)

	level := sm2.GetThinkingLevel(key)
	if level != "medium" {
		t.Errorf("expected thinking level %q, got %q", "medium", level)
	}
}

func TestSQLite_SessionManager_SetHistory(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:set-history"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "old message")
	sm.Save(key)

	// Set new history
	newHistory := []providers.Message{
		{Role: "user", Content: "new message 1"},
		{Role: "assistant", Content: "response 1"},
		{Role: "user", Content: "new message 2"},
	}
	sm.SetHistory(key, newHistory)
	sm.Save(key)

	// Verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	history := sm2.GetHistory(key)
	if len(history) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(history))
	}
	if history[0].Content != "new message 1" {
		t.Errorf("expected first message %q, got %q", "new message 1", history[0].Content)
	}
}

func TestSQLite_SessionManager_ExcludeOldMessages(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:exclude"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", "msg")
	}
	sm.Save(key)

	// Exclude first 7 messages (index 0 preserved, indices 1-6 excluded)
	sm.ExcludeOldMessagesFromContext(key, 3)
	sm.Save(key)

	// Verify excluded flag is persisted
	repo := s.Sessions()
	msgJSONs, _ := repo.LoadMessages(key)
	excludedCount := 0
	for _, msgJSON := range msgJSONs {
		var msg providers.Message
		json.Unmarshal([]byte(msgJSON), &msg)
		if msg.ExcludeFromContext {
			excludedCount++
		}
	}
	if excludedCount != 6 {
		t.Errorf("expected 6 excluded messages, got %d", excludedCount)
	}
}

func TestSQLite_SessionManager_Name(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:name"
	sm.GetOrCreate(key)
	sm.SetName(key, "My Session")
	sm.Save(key)

	// Verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	name := sm2.GetName(key)
	if name != "My Session" {
		t.Errorf("expected name %q, got %q", "My Session", name)
	}
}

func TestSQLite_SessionManager_Summary(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:summary"
	sm.GetOrCreate(key)
	sm.SetSummary(key, "This is a summary")
	sm.Save(key)

	// Verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	summary := sm2.GetSummary(key)
	if summary != "This is a summary" {
		t.Errorf("expected summary %q, got %q", "This is a summary", summary)
	}
}

func TestSQLite_SessionManager_RemoveLastMessage(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:remove-last"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "msg1")
	sm.AddMessage(key, "assistant", "msg2")
	sm.AddMessage(key, "user", "msg3")
	sm.Save(key)

	// Remove last message
	removed := sm.RemoveLastMessage(key)
	if !removed {
		t.Fatal("expected message to be removed")
	}
	sm.Save(key)

	// Verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	history := sm2.GetHistory(key)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages after removal, got %d", len(history))
	}
	if history[1].Content != "msg2" {
		t.Errorf("expected last message %q, got %q", "msg2", history[1].Content)
	}
}

func TestSQLite_SessionManager_ShouldStartFreshSession(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:fresh"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.Save(key)

	// Simulate old session
	sm.mu.Lock()
	session := sm.sessions[key]
	session.Updated = time.Now().Add(-2 * time.Hour)
	sm.mu.Unlock()
	sm.Save(key)

	// Should start fresh
	shouldReset, idle := sm.ShouldStartFreshSession(key, time.Hour)
	if !shouldReset {
		t.Error("expected session to require fresh start")
	}
	if idle < time.Hour {
		t.Errorf("idle = %v, want >= %v", idle, time.Hour)
	}
}

func TestSQLite_SessionManager_GetOrCreate_NoMessages(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:empty"
	session := sm.GetOrCreate(key)
	if session.Key != key {
		t.Errorf("expected key %q, got %q", key, session.Key)
	}

	// Save (should upsert metadata)
	sm.Save(key)

	// Verify metadata exists
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	name := sm2.GetName(key)
	if name != "" {
		t.Errorf("expected empty name, got %q", name)
	}
}

func TestSQLite_SessionManager_VerboseLevel(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:verbose"
	sm.GetOrCreate(key)
	sm.SetVerboseLevel(key, "full")
	sm.Save(key)

	// Verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	level := sm2.GetVerboseLevel(key)
	if level != "full" {
		t.Errorf("expected verbose level %q, got %q", "full", level)
	}
}

func TestSQLite_SessionManager_Model(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:model"
	sm.GetOrCreate(key)
	sm.SetModel(key, "gpt-4")
	sm.Save(key)

	// Verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	model := sm2.GetModel(key)
	if model != "gpt-4" {
		t.Errorf("expected model %q, got %q", "gpt-4", model)
	}
}

func TestSQLite_SessionManager_CreatedUpdated(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:timestamps"
	before := time.Now()
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.Save(key)
	after := time.Now()

	// Verify
	created := sm.GetCreated(key)
	updated := sm.GetUpdated(key)

	if created.Before(before) || created.After(after) {
		t.Errorf("created time %v not in range [%v, %v]", created, before, after)
	}
	if updated.Before(before) || updated.After(after) {
		t.Errorf("updated time %v not in range [%v, %v]", updated, before, after)
	}
}

func TestSQLite_SessionManager_GetHistory_NonExistent(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	// Get history for non-existent session
	history := sm.GetHistory("nonexistent:session")
	if len(history) != 0 {
		t.Errorf("expected empty history, got %d messages", len(history))
	}
}

func TestSQLite_SessionManager_SetName_CreatesSession(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:setname-create"
	sm.SetName(key, "New Name")

	// Verify
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	name := sm2.GetName(key)
	if name != "New Name" {
		t.Errorf("expected name %q, got %q", "New Name", name)
	}
}

func TestSQLite_SessionManager_MultipleSaves(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:multi-save"
	sm.GetOrCreate(key)

	// Multiple saves
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", "msg")
		sm.Save(key)
	}

	// Verify final state
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	history := sm2.GetHistory(key)
	if len(history) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(history))
	}
}

func TestSQLite_SessionManager_ConcurrentReadersWriter(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:concurrent-rw"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "initial")
	sm.Save(key)

	// Measure reader wait during writes
	var maxWait time.Duration
	var mu sync.Mutex

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			start := time.Now()
			sm.GetHistory(key)
			elapsed := time.Since(start)
			mu.Lock()
			if elapsed > maxWait {
				maxWait = elapsed
			}
			mu.Unlock()
		}
	}()

	// Concurrent writes
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", "msg")
		sm.Save(key)
		time.Sleep(time.Millisecond)
	}

	<-done

	// Readers should never be blocked >50ms (SQLite WAL allows concurrent reads)
	if maxWait > 50*time.Millisecond {
		t.Errorf("reader blocked for %v (>50ms threshold)", maxWait)
	}
}

func TestSQLite_SessionManager_LoadFromDisk(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:lazy-load"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	sm.Save(key)

	// Evict from memory
	sm.EvictSession(key)

	// Access should trigger load from SQLite
	history := sm.GetHistory(key)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages after lazy load, got %d", len(history))
	}
	if history[0].Content != "hello" {
		t.Errorf("expected first message %q, got %q", "hello", history[0].Content)
	}
}

func TestSQLite_SessionManager_AllMessageCounts(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	// Create sessions with messages
	sm.GetOrCreate("sess-a")
	sm.AddMessage("sess-a", "user", "hello")
	sm.AddMessage("sess-a", "assistant", "hi there")
	sm.GetOrCreate("sess-b")
	sm.AddMessage("sess-b", "user", "world")

	// Save to SQLite
	sm.Save("sess-a")
	sm.Save("sess-b")

	// Create a session with no messages (just metadata)
	sm.GetOrCreate("sess-empty")
	sm.Save("sess-empty")

	// Evict all sessions from memory to simulate the real scenario
	// where sessions are only in metadata (the bug condition)
	sm.mu.Lock()
	for k := range sm.sessions {
		delete(sm.sessions, k)
		delete(sm.accessTimes, k)
	}
	sm.mu.Unlock()

	// Now AllMessageCounts should still return correct counts
	// even though sessions are not in memory
	counts := sm.AllMessageCounts()

	if counts["sess-a"] != 2 {
		t.Errorf("sess-a count = %d, want 2", counts["sess-a"])
	}
	if counts["sess-b"] != 1 {
		t.Errorf("sess-b count = %d, want 1", counts["sess-b"])
	}
	if counts["sess-empty"] != 0 {
		t.Errorf("sess-empty count = %d, want 0", counts["sess-empty"])
	}
}

// ============================================================================
// Incremental save tests
// ============================================================================

// TestSQLite_IncrementalSave_AppendOnly verifies that adding new messages
// uses InsertMessage (incremental) instead of ReplaceMessages (full rewrite).
func TestSQLite_IncrementalSave_AppendOnly(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:incr-append"
	sm.GetOrCreate(key)

	// Add 3 messages and save (first save is always full)
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	sm.AddMessage(key, "user", "how are you?")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	// Verify 3 messages in DB
	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 3 {
		t.Fatalf("after initial save: got %d messages, want 3", count)
	}

	// Add 2 more messages — next save should be incremental
	sm.AddMessage(key, "assistant", "I'm good")
	sm.AddMessage(key, "user", "great")
	if err := sm.Save(key); err != nil {
		t.Fatalf("incremental save: %v", err)
	}

	// Verify all 5 messages in DB
	count, _ = repo.MessageCount(key)
	if count != 5 {
		t.Fatalf("after incremental save: got %d messages, want 5", count)
	}

	// Verify content of last message
	msgJSONs, _ := repo.LoadMessages(key)
	var lastMsg providers.Message
	json.Unmarshal([]byte(msgJSONs[4]), &lastMsg)
	if lastMsg.Content != "great" {
		t.Errorf("last message content = %q, want %q", lastMsg.Content, "great")
	}
}

// TestSQLite_IncrementalSave_StreamingUpdate verifies that streaming updates
// use UpdateMessage for the last message instead of full ReplaceMessages.
func TestSQLite_IncrementalSave_StreamingUpdate(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:incr-stream"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "tell me a story")

	// Initial save — creates the user message
	sm.Save(key)

	// Simulate streaming: create streaming message + append chunk
	// The first chunk creates the streaming message and triggers a flush
	// (first chunk always flushes because lastStreamFlush is zero).
	sm.AppendAssistantChunk(key, "Once upon a time...")

	// The streaming message should be in DB after flush
	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 2 {
		t.Fatalf("after streaming: got %d messages, want 2", count)
	}

	// Verify the streaming content was persisted
	msgJSONs, _ := repo.LoadMessages(key)
	var streamMsg providers.Message
	json.Unmarshal([]byte(msgJSONs[1]), &streamMsg)
	if streamMsg.Content != "Once upon a time..." {
		t.Errorf("streaming content = %q, want %q", streamMsg.Content, "Once upon a time...")
	}
}

// TestSQLite_MetaOnlySave verifies that metadata-only setters (SetName, SetModel)
// don't touch the messages table.
func TestSQLite_MetaOnlySave(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:meta-only"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	sm.Save(key)

	// Get initial message count in DB
	repo := s.Sessions()
	countBefore, _ := repo.MessageCount(key)
	if countBefore != 2 {
		t.Fatalf("before meta save: got %d messages, want 2", countBefore)
	}

	// Change only metadata — should not touch messages
	sm.SetName(key, "My Chat")
	sm.SetModel(key, "gpt-4")

	// Verify metadata was saved
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	name := sm2.GetName(key)
	model := sm2.GetModel(key)
	if name != "My Chat" {
		t.Errorf("name = %q, want %q", name, "My Chat")
	}
	if model != "gpt-4" {
		t.Errorf("model = %q, want %q", model, "gpt-4")
	}

	// Messages should still be intact
	countAfter, _ := repo.MessageCount(key)
	if countAfter != 2 {
		t.Errorf("after meta save: got %d messages, want 2", countAfter)
	}
}

// TestSQLite_FullRewrite_AfterTruncate verifies that TruncateHistory forces
// a full rewrite (ReplaceMessages) on the next save.
func TestSQLite_FullRewrite_AfterTruncate(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:truncate"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("msg-%d", i))
	}
	sm.Save(key)

	// Verify 10 messages in DB
	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 10 {
		t.Fatalf("before truncate: got %d messages, want 10", count)
	}

	// Truncate to last 3
	sm.TruncateHistory(key, 3)
	sm.Save(key)

	// Verify only 3 messages remain
	count, _ = repo.MessageCount(key)
	if count != 3 {
		t.Errorf("after truncate: got %d messages, want 3", count)
	}

	// Verify content
	msgJSONs, _ := repo.LoadMessages(key)
	var firstMsg providers.Message
	json.Unmarshal([]byte(msgJSONs[0]), &firstMsg)
	if firstMsg.Content != "msg-7" {
		t.Errorf("first remaining message = %q, want %q", firstMsg.Content, "msg-7")
	}
}

// TestSQLite_DirtyFlags_Basic verifies that dirty flags are set and cleared
// correctly after saves.
func TestSQLite_DirtyFlags_Basic(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:dirty"
	session := sm.GetOrCreate(key)

	// New session has lastPersistedSeq = -1, which forces full save
	if session.lastPersistedSeq != -1 {
		t.Errorf("new session lastPersistedSeq = %d, want -1", session.lastPersistedSeq)
	}

	// After save, flags should be cleared
	sm.Save(key)
	if session.metaDirty {
		t.Error("after save: metaDirty should be false")
	}
	if session.msgsAppended != 0 {
		t.Errorf("after save: msgsAppended = %d, want 0", session.msgsAppended)
	}
	if session.lastPersistedSeq != -1 {
		t.Errorf("after save (no messages): lastPersistedSeq = %d, want -1", session.lastPersistedSeq)
	}

	// Add a message — should set msgsAppended
	sm.AddMessage(key, "user", "hello")
	if session.msgsAppended != 1 {
		t.Errorf("after add: msgsAppended = %d, want 1", session.msgsAppended)
	}

	// After save, should be cleared
	sm.Save(key)
	if session.msgsAppended != 0 {
		t.Errorf("after save: msgsAppended = %d, want 0", session.msgsAppended)
	}
	if session.lastPersistedSeq != 0 {
		t.Errorf("lastPersistedSeq = %d, want 0", session.lastPersistedSeq)
	}

	// SetName should set metaDirty, then clear it after immediate save
	sm.SetName(key, "test")
	if session.metaDirty {
		t.Error("after SetName+save: metaDirty should be false (already persisted)")
	}
}
func TestSQLite_GapAware_IncrementalSave_SeqOffset(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:gap-incr"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "m0")
	sm.AddMessage(key, "assistant", "m1")
	sm.AddMessage(key, "user", "m2")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Simulate 5 evicted messages: in-memory slice element 0 is SQLite seq 5.
	sess := sm.sessions[key]
	if sess == nil {
		t.Fatal("session not in memory")
	}
	sess.firstInMemorySeq = 5

	// Append at slice index 3; incremental save must write absolute seq 8.
	sm.AddMessage(key, "user", "m3")
	if err := sm.Save(key); err != nil {
		t.Fatalf("incremental Save failed: %v", err)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	// Original rows keep seqs 0,1,2 (full rewrite). Newly appended row is seq 8.
	wantSeqs := []int{0, 1, 2, 8}
	for i, want := range wantSeqs {
		if rows[i].Seq != want {
			t.Errorf("row %d seq = %d, want %d", i, rows[i].Seq, want)
		}
	}
}

func TestSQLite_GapAware_ExcludedRange_SeqOffset(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:gap-excl"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "u0")
	sm.AddMessage(key, "assistant", "a1")
	sm.AddMessage(key, "user", "u2")
	sm.AddMessage(key, "assistant", "a3")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Simulate a gap of 10 evicted messages: 10 messages with seqs 0-9 were
	// evicted from memory (still in SQLite), and the in-memory slice now maps
	// to absolute seqs 10-13. Re-insert the in-memory rows at their absolute
	// seqs (as eviction would leave them), and set firstInMemorySeq = 10 (the
	// seq of in-memory slice element 0).
	sess := sm.sessions[key]
	if sess == nil {
		t.Fatal("session not in memory")
	}
	rows := make([]store.MessageRow, len(sess.Messages))
	for i, m := range sess.Messages {
		msgJSON, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal message: %v", err)
		}
		rows[i] = store.MessageRow{Seq: 10 + i, Role: m.Role, JSON: string(msgJSON), Excluded: m.ExcludeFromContext}
	}
	if err := s.Sessions().ReplaceMessages(key, rows); err != nil {
		t.Fatalf("re-insert at absolute seqs failed: %v", err)
	}
	sess.firstInMemorySeq = 10
	sess.excludedRange = [2]int{1, 3}
	sess.Messages[1].ExcludeFromContext = true
	sess.Messages[2].ExcludeFromContext = true

	if err := sm.Save(key); err != nil {
		t.Fatalf("excluded-range Save failed: %v", err)
	}

	fullRows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(fullRows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(fullRows))
	}
	// Slice index 1 -> seq 11, slice index 2 -> seq 12, both excluded.
	for _, r := range fullRows {
		switch r.Seq {
		case 11:
			if !r.Excluded {
				t.Errorf("seq %d should be excluded", r.Seq)
			}
		case 12:
			if !r.Excluded {
				t.Errorf("seq %d should be excluded", r.Seq)
			}
		case 10, 13:
			if r.Excluded {
				t.Errorf("seq %d should not be excluded", r.Seq)
			}
		default:
			t.Errorf("unexpected seq %d", r.Seq)
		}
	}
}

func TestSQLite_GapAware_FullRewrite_PreservesEvictionGap(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:gap-full"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "m0")
	sm.AddMessage(key, "assistant", "m1")
	sm.AddMessage(key, "user", "m2")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Simulate post-eviction state: persist evicted rows at seqs 0-6 in
	// SQLite (outside the in-memory slice), with the in-memory slice mapping
	// to absolute seqs 7-9. This mirrors what EvictExcludedMessages leaves
	// behind: the evicted (excluded) rows remain persisted in SQLite.
	sess := sm.sessions[key]
	if sess == nil {
		t.Fatal("session not in memory")
	}
	evictedRows := make([]store.MessageRow, 0, 7)
	for i := 0; i < 7; i++ {
		ev := providers.Message{Role: "assistant", Content: fmt.Sprintf("evicted-%d", i), ExcludeFromContext: true}
		evJSON, _ := json.Marshal(ev)
		evictedRows = append(evictedRows, store.MessageRow{Seq: i, Role: ev.Role, JSON: string(evJSON), Excluded: true})
	}
	// Re-insert the actual persisted (in-memory) messages at absolute seqs 7-9.
	for i, m := range sess.Messages {
		mJSON, _ := json.Marshal(m)
		evictedRows = append(evictedRows, store.MessageRow{Seq: 7 + i, Role: m.Role, JSON: string(mJSON)})
	}
	if err := s.Sessions().ReplaceMessages(key, evictedRows); err != nil {
		t.Fatalf("re-insert evicted + inbox rows failed: %v", err)
	}
	sess.firstInMemorySeq = 7
	sess.evictedTotal = 7

	// Force a full rewrite via SetHistory (replaces slice with 2 messages).
	sm.SetHistory(key, []providers.Message{
		{Role: "user", Content: "n0"},
		{Role: "assistant", Content: "n1"},
	})
	if err := sm.Save(key); err != nil {
		t.Fatalf("full-rewrite Save failed: %v", err)
	}

	// A full rewrite re-materializes the evicted rows into SQLite but they are
	// still NOT resident in the in-memory slice. So the eviction gap must be
	// PRESERVED: firstInMemorySeq stays at the number of evicted rows and
	// evictedTotal stays equal to it. Resetting them to 0 here would break the
	// `seq = firstInMemorySeq + sliceIndex` invariant and silently drop the
	// next incremental append.
	if sess.firstInMemorySeq != 7 {
		t.Errorf("after full rewrite firstInMemorySeq = %d, want 7 (gap preserved)", sess.firstInMemorySeq)
	}
	if sess.evictedTotal != 7 {
		t.Errorf("after full rewrite evictedTotal = %d, want 7 (gap preserved)", sess.evictedTotal)
	}
	// lastPersistedSeq is slice-relative: all 2 in-memory messages persisted.
	if sess.lastPersistedSeq != 1 {
		t.Errorf("after full rewrite lastPersistedSeq = %d, want 1", sess.lastPersistedSeq)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	// The full rewrite must KEEP the evicted (excluded) rows: re-materializing
	// them from SQLite before ReplaceMessages means they survive at seqs 0-6,
	// followed by the new in-context messages re-based at 7-8. Total 9 rows.
	if len(rows) != 9 {
		t.Fatalf("expected 9 rows (7 evicted + 2 new), got %d", len(rows))
	}
	// First 7 rows are the evicted excluded messages.
	for i := 0; i < 7; i++ {
		if rows[i].Seq != i {
			t.Errorf("evicted row %d seq = %d, want %d", i, rows[i].Seq, i)
		}
		if !rows[i].Excluded {
			t.Errorf("evicted row %d should be excluded", i)
		}
	}
	// Final 2 rows are the new in-context messages, re-based.
	if rows[7].Seq != 7 || rows[8].Seq != 8 {
		t.Errorf("expected seqs 7,8 after rewrite, got %d,%d", rows[7].Seq, rows[8].Seq)
	}
}
func TestSQLite_EvictExcluded_LoadRoundTrip(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:evict-load"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Exclude the first 7 (keep index 0 + 2 in-context), then save, then
	// evict. EvictExcludedMessages folds index 0 into the summary and evicts
	// the whole contiguous [0..7) prefix so the in-memory slice stays
	// contiguous (index 0 is NOT left dangling in memory).
	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}

	before := sm.GetTotalMessageCount(key)
	if before != 10 {
		t.Fatalf("GetTotalMessageCount before eviction = %d, want 10", before)
	}
	if got := sm.GetEvictedMessageCount(key); got != 0 {
		t.Fatalf("GetEvictedMessageCount before eviction = %d, want 0", got)
	}

	evicted := sm.EvictExcludedMessages(key)
	if evicted != 7 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 7", evicted)
	}

	// After eviction: in-memory slice has 3 (the contiguous kept suffix
	// m7..m9), evicted=7. Index 0 was folded into the summary.
	hist := sm.GetHistoryView(key)
	if len(hist) != 3 {
		t.Fatalf("in-memory len after evict = %d, want 3", len(hist))
	}
	if hist[0].Content != "m7" {
		t.Errorf("first in-memory message after evict = %q, want %q", hist[0].Content, "m7")
	}
	// Index 0's content must have been folded into the summary.
	if got := sm.GetSummary(key); !strings.Contains(got, "m0") {
		t.Errorf("summary after eviction should contain folded index-0 content, got %q", got)
	}
	if got := sm.GetEvictedMessageCount(key); got != 7 {
		t.Fatalf("GetEvictedMessageCount after evict = %d, want 7", got)
	}
	// Total stays stable across eviction.
	if got := sm.GetTotalMessageCount(key); got != 10 {
		t.Fatalf("GetTotalMessageCount after evict = %d, want 10", got)
	}

	// Idempotency: a second eviction is a no-op.
	if again := sm.EvictExcludedMessages(key); again != 0 {
		t.Fatalf("second EvictExcludedMessages = %d, want 0", again)
	}

	// Save after eviction must NOT be a full rewrite that destroys rows; the
	// next save is a no-op. Verify SQLite still has all 10 rows.
	if err := sm.Save(key); err != nil {
		t.Fatalf("save after eviction failed: %v", err)
	}
	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq after evict-save failed: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("SQLite rows after evict-save = %d, want 10 (no data loss)", len(rows))
	}

	// Lazy-load restores exactly the evicted messages before the in-memory ones.
	loaded := sm.LoadEvictedMessages(key)
	if loaded != 7 {
		t.Fatalf("LoadEvictedMessages loaded %d, want 7", loaded)
	}
	restored := sm.GetHistoryView(key)
	if len(restored) != 10 {
		t.Fatalf("restored len = %d, want 10", len(restored))
	}
	for i, msg := range restored {
		if msg.Content != fmt.Sprintf("m%d", i) {
			t.Errorf("restored[%d].Content = %q, want %q", i, msg.Content, fmt.Sprintf("m%d", i))
		}
		// Excluded flag must be preserved on the evicted messages.
		if i >= 1 && i <= 6 && !msg.ExcludeFromContext {
			t.Errorf("restored[%d] should be ExcludeFromContext", i)
		}
	}
	if got := sm.GetEvictedMessageCount(key); got != 0 {
		t.Fatalf("GetEvictedMessageCount after load = %d, want 0", got)
	}
	if got := sm.GetTotalMessageCount(key); got != 10 {
		t.Fatalf("GetTotalMessageCount after load = %d, want 10", got)
	}

	// Load is idempotent when nothing is evicted.
	if again := sm.LoadEvictedMessages(key); again != 0 {
		t.Fatalf("second LoadEvictedMessages = %d, want 0", again)
	}
}

func TestSQLite_ReloadAfterEviction_SeqAccounting(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:reload-evict"
	sm.GetOrCreate(key)
	for i := 0; i < 8; i++ {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Evict: exclude first 5 (keep 0 + 3 in-context), save, evict. Index 0 is
	// folded into the summary; the whole contiguous [0..5) prefix is evicted.
	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	evicted := sm.EvictExcludedMessages(key)
	if evicted != 5 {
		t.Fatalf("evicted %d, want 5", evicted)
	}

	// Append a new message after eviction; incremental save must write an
	// absolute seq that fits after the evicted rows (seqs 0-7 exist in
	// SQLite; firstInMemorySeq=5, in-memory index 3 -> abs seq 8).
	sm.AddMessage(key, "user", "m8")
	if err := sm.Save(key); err != nil {
		t.Fatalf("append-after-evict Save failed: %v", err)
	}

	// SQLite must have 9 rows: seqs 0-8 (evicted m0-m4 at 0-4, kept m5-m7 at
	// 5-7, new m8 at 8).
	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 9 {
		t.Fatalf("SQLite rows = %d, want 9", len(rows))
	}
	for i, r := range rows {
		if r.Seq != i {
			t.Errorf("row %d: seq = %d, want %d", i, r.Seq, i)
		}
	}
	if rows[8].JSON == "" {
		t.Errorf("row 8 should contain appended m8")
	}

	// Evict the session from memory entirely.
	sm.EvictSession(key)

	// Reload a fresh session manager over the same store. Per the
	// context-only in-memory design (Phase 7), a cold load must NOT re-inflate
	// evicted rows into RAM: it restores firstInMemorySeq (5) and loads only
	// the non-evicted suffix (m5..m8) plus the appended m8, so SQLite rows
	// (seqs 0-8) map to in-memory slice indices as seq = firstInMemorySeq + i.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	hist := sm2.GetHistoryView(key)
	if len(hist) != 4 {
		t.Fatalf("reloaded in-memory len = %d, want 4 (non-evicted only)", len(hist))
	}
	for i, msg := range hist {
		want := fmt.Sprintf("m%d", i+5)
		if msg.Content != want {
			t.Errorf("reloaded[%d].Content = %q, want %q", i, msg.Content, want)
		}
	}
	// Evicted boundary restored: 5 evicted rows still in SQLite, 9 total.
	if got := sm2.GetEvictedMessageCount(key); got != 5 {
		t.Errorf("GetEvictedMessageCount after reload = %d, want 5", got)
	}
	if got := sm2.GetTotalMessageCount(key); got != 9 {
		t.Errorf("GetTotalMessageCount after reload = %d, want 9", got)
	}

	// A save on the fresh manager is a no-op (no full rewrite) — data intact.
	if err := sm2.Save(key); err != nil {
		t.Fatalf("reload save failed: %v", err)
	}
	rows2, _ := s.Sessions().LoadMessagesWithSeq(key)
	if len(rows2) != 9 {
		t.Fatalf("rows after reload-save = %d, want 9", len(rows2))
	}
}

func TestSQLite_EvictExcluded_WhenDisabled(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:evict-noop"
	sm.GetOrCreate(key)
	for i := 0; i < 6; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	sm.Save(key)

	// Nothing excluded -> eviction is a no-op.
	if got := sm.EvictExcludedMessages(key); got != 0 {
		t.Fatalf("EvictExcludedMessages with no excluded = %d, want 0", got)
	}
	if got := sm.EvictExcludedMessages("missing:key"); got != 0 {
		t.Fatalf("EvictExcludedMessages on missing key = %d, want 0", got)
	}
}

// TestEvictExcluded_NoStore verifies that eviction refuses to run when the
// manager has no SQLite store. Without a store, Save is a no-op and evicting
// would permanently drop messages that were never persisted.
func TestEvictExcluded_NoStore(t *testing.T) {
	sm := NewSessionManager() // no store

	key := "test:evict-nostore"
	sm.GetOrCreate(key)
	for i := 0; i < 8; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	// Exclude a prefix so eviction would have work to do if it were allowed.
	sm.ExcludeOldMessagesFromContext(key, 3)
	_ = sm.Save(key) // no-op without a store

	if got := sm.EvictExcludedMessages(key); got != 0 {
		t.Fatalf("EvictExcludedMessages without store = %d, want 0", got)
	}
	// All messages must still be in memory.
	hist := sm.GetHistory(key)
	if len(hist) != 8 {
		t.Fatalf("history length after refused eviction = %d, want 8", len(hist))
	}
}

// TestSQLite_ColdLoad_RestoresEvictionBoundary verifies that a cold load
// (new SessionManager over the same store) restores the eviction boundary
// (firstInMemorySeq/evictedTotal) from SQLite metadata and does NOT re-inflate
// the evicted rows into RAM: only the non-evicted suffix is loaded.
func TestSQLite_ColdLoad_RestoresEvictionBoundary(t *testing.T) {
	s := newTestStore(t)

	// Manager 1: create + persist the session, exclude a prefix, save, evict.
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:coldload-evicted"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Exclude all but the last 3 in-context (keep m7..m9), persist, then evict.
	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 7 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 7", evicted)
	}

	// Manager 2: cold load from the same store.
	sm2 := NewSessionManager()
	sm2.SetStore(s)

	// Only the non-evicted suffix is resident in RAM (this also triggers the
	// cold load for the count assertions below).
	hist := sm2.GetHistory(key)
	if len(hist) != 3 {
		t.Fatalf("in-memory len after cold load = %d, want 3", len(hist))
	}
	if hist[0].Content != "m7" {
		t.Errorf("first in-memory message after cold load = %q, want %q", hist[0].Content, "m7")
	}
	if hist[1].Content != "m8" || hist[2].Content != "m9" {
		t.Errorf("unexpected kept suffix after cold load: %q, %q", hist[1].Content, hist[2].Content)
	}

	// Boundary restored: 7 evicted, 10 total.
	if got := sm2.GetEvictedMessageCount(key); got != 7 {
		t.Fatalf("GetEvictedMessageCount after cold load = %d, want 7", got)
	}
	if got := sm2.GetTotalMessageCount(key); got != 10 {
		t.Fatalf("GetTotalMessageCount after cold load = %d, want 10", got)
	}

	// The invariant seq = firstInMemorySeq + sliceIndex must hold: the first
	// in-memory row's SQLite seq equals firstInMemorySeq (7). Verify against
	// the persisted rows.
	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("SQLite rows = %d, want 10", len(rows))
	}
	// The first persistent row after the boundary is seq 7.
	found := false
	for _, r := range rows {
		if r.Seq == 7 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a persisted row with seq 7 (firstInMemorySeq), got seqs: %v", seqsOf(rows))
	}
}

// TestSQLite_ColdLoad_PrunedPrefixBelowBoundary is a regression test for the
// stale-eviction-boundary guard. saveFullUnlocked physically prunes the OLDEST
// excluded rows from SQLite for oversized sessions, leaving a gap BELOW the
// eviction boundary. The cold-load guard must validate contiguity ABOVE the
// boundary (via MaxSeq), not contiguity from seq 0 (via MessageCount), so a
// pruned prefix must NOT trigger the fallback that would reset
// firstInMemorySeq to 0 and corrupt the `seq = firstInMemorySeq + sliceIndex`
// invariant.
func TestSQLite_ColdLoad_PrunedPrefixBelowBoundary(t *testing.T) {
	s := newTestStore(t)

	// Manager 1: create + persist a session, exclude a prefix, save, evict.
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:pruned-prefix-restore"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Exclude the first 7 (m0..m6), keep m7..m9 in context; persist + evict.
	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 7 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 7", evicted)
	}
	// EvictExcludedMessages persists the boundary (FirstInMemorySeq == 7);
	// confirm it survived in SQLite metadata.
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta failed: %v (err=%v)", meta, err)
	}
	if meta.FirstInMemorySeq != 7 {
		t.Fatalf("FirstInMemorySeq persisted = %d, want 7", meta.FirstInMemorySeq)
	}

	// Simulate the post-full-rewrite pruning of the OLDEST excluded rows
	// (which live below the boundary) by calling the repo directly, the same
	// method saveFullUnlocked uses. keepCount small enough that rows are
	// actually deleted: total=10, keepCount=7 -> 3 oldest excluded rows pruned.
	pruned, pErr := s.Sessions().PruneExcluded(key, 7)
	if pErr != nil {
		t.Fatalf("PruneExcluded failed: %v", pErr)
	}
	if pruned != 3 {
		t.Fatalf("PruneExcluded deleted %d rows, want 3 (oldest excluded prefix)", pruned)
	}

	// rows remaining after pruning are m3..m9 (seq 3..9): m3..m6 still
	// excluded (evicted + persisted), m7..m9 in-context. Boundary is still 7.
	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("SQLite rows after prune = %d, want 7", len(rows))
	}

	// Cold load via a NEW manager over the same store. With the old guard,
	// MessageCount - boundary = 7 - 7 = 0 != 3 would falsely trigger the
	// fallback and reset firstInMemorySeq to 0.
	sm2 := NewSessionManager()
	sm2.SetStore(s)

	hist := sm2.GetHistory(key) // triggers the cold load
	if len(hist) != 3 {
		t.Fatalf("in-memory len after cold load = %d, want 3", len(hist))
	}
	if hist[0].Content != "m7" || hist[1].Content != "m8" || hist[2].Content != "m9" {
		t.Fatalf("unexpected kept suffix after cold load: %q, %q, %q", hist[0].Content, hist[1].Content, hist[2].Content)
	}

	// Boundary was NOT reset to 0: evicted rows still persisted = B - pruned
	// (7 - 3 = 4), and total = 4 + in-memory 3 = 7.
	if got := sm2.GetEvictedMessageCount(key); got != 7-pruned {
		t.Fatalf("GetEvictedMessageCount after cold load = %d, want %d (B-pruned)", got, 7-pruned)
	}
	if inMem := len(sm2.GetHistory(key)); inMem != 3 {
		t.Fatalf("GetHistory len after cold load = %d, want 3", inMem)
	}
	if got := sm2.GetTotalMessageCount(key); got != 7-pruned+len(hist) {
		t.Fatalf("GetTotalMessageCount after cold load = %d, want %d", got, 7-pruned+len(hist))
	}

	// The invariant seq = firstInMemorySeq + sliceIndex must hold after an
	// append: appending one more message via the manager and saving must
	// insert at absolute seq = firstInMemorySeq + sliceIndex (7 + 3 = 10) and
	// must NOT overwrite any existing row.
	sm2.AddMessage(key, "user", "m10")
	if err := sm2.Save(key); err != nil {
		t.Fatalf("save after append failed: %v", err)
	}
	finalRows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq (final) failed: %v", err)
	}
	if len(finalRows) != 8 {
		t.Fatalf("SQLite rows after append = %d, want 8 (grew by exactly 1)", len(finalRows))
	}
	newRow := finalRows[len(finalRows)-1]
	// No seq was overwritten: the new row's seq must equal firstInMemorySeq +
	// sliceIndex (7 + 3 = 10), one beyond the previous max (9). The total row
	// count growing by exactly 1 already proves no existing row was replaced.
	if newRow.Seq != 10 {
		t.Fatalf("appended row seq = %d, want 10 (firstInMemorySeq+sliceIndex), json=%q", newRow.Seq, newRow.JSON)
	}
}

// TestSQLite_ColdLoad_NoEviction_BoundaryZero is a regression test: a session
// with no eviction reloads all messages with firstInMemorySeq == 0, i.e. the
// legacy behavior is unchanged.
func TestSQLite_ColdLoad_NoEviction_BoundaryZero(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:coldload-noevict"
	sm.GetOrCreate(key)
	for i := 0; i < 5; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	sm2 := NewSessionManager()
	sm2.SetStore(s)
	hist := sm2.GetHistory(key)
	if len(hist) != 5 {
		t.Fatalf("in-memory len after cold load = %d, want 5", len(hist))
	}
	if got := sm2.GetEvictedMessageCount(key); got != 0 {
		t.Fatalf("GetEvictedMessageCount = %d, want 0", got)
	}
	if got := sm2.GetTotalMessageCount(key); got != 5 {
		t.Fatalf("GetTotalMessageCount = %d, want 5", got)
	}
	for i, msg := range hist {
		if msg.Content != fmt.Sprintf("m%d", i) {
			t.Errorf("hist[%d].Content = %q, want %q", i, msg.Content, fmt.Sprintf("m%d", i))
		}
	}
}

// TestSQLite_EvictionBoundary_PersistedOnEvict verifies that after
// EvictExcludedMessages + save, a fresh GetSessionMeta via the store sees
// FirstInMemorySeq == number of evicted rows.
func TestSQLite_EvictionBoundary_PersistedOnEvict(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:boundary-persist"
	sm.GetOrCreate(key)
	for i := 0; i < 8; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 6 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 6", evicted)
	}
	// Persist boundary explicitly (EvictExcludedMessages already did, but a
	// save keeps the metadata consistent).
	if err := sm.Save(key); err != nil {
		t.Fatalf("save after evict failed: %v", err)
	}

	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil {
		t.Fatalf("GetSessionMeta failed: %v", err)
	}
	if meta == nil {
		t.Fatal("session not found in SQLite")
	}
	if meta.FirstInMemorySeq != 6 {
		t.Fatalf("FirstInMemorySeq persisted = %d, want 6", meta.FirstInMemorySeq)
	}
}

// TestSQLite_MultipleCompactions_ColdLoadContextOnly verifies that after multiple
// rounds of compactions and new messages, cold-loading the session in a brand new
// SessionManager loads ONLY the in-context messages into RAM (e.g. 2 messages),
// and not the dozens of evicted messages.
func TestSQLite_MultipleCompactions_ColdLoadContextOnly(t *testing.T) {
	s := newTestStore(t)
	sm1 := NewSessionManager()
	sm1.SetStore(s)

	const key = "test:multi-compaction-cold-load"
	sm1.GetOrCreate(key)

	// Round 1: 10 messages, compact keeping 2 -> 8 evicted, 2 kept.
	for i := 0; i < 10; i++ {
		sm1.AddMessage(key, "user", fmt.Sprintf("Question round 1: %d", i))
	}
	if err := sm1.Save(key); err != nil {
		t.Fatalf("round 1 save failed: %v", err)
	}
	sm1.ExcludeOldMessagesFromContext(key, 2)
	if err := sm1.Save(key); err != nil {
		t.Fatalf("round 1 exclude save failed: %v", err)
	}
	sm1.EvictExcludedMessages(key)
	if inMem := len(sm1.GetHistory(key)); inMem != 2 {
		t.Fatalf("round 1 in-mem = %d, want 2", inMem)
	}

	// Round 2: append 10 more messages -> 12 total in memory, compact keeping 2 -> 10 evicted, 2 kept.
	for i := 0; i < 10; i++ {
		sm1.AddMessage(key, "user", fmt.Sprintf("Question round 2: %d", i))
	}
	if err := sm1.Save(key); err != nil {
		t.Fatalf("round 2 save failed: %v", err)
	}
	sm1.ExcludeOldMessagesFromContext(key, 2)
	if err := sm1.Save(key); err != nil {
		t.Fatalf("round 2 exclude save failed: %v", err)
	}
	sm1.EvictExcludedMessages(key)
	if inMem := len(sm1.GetHistory(key)); inMem != 2 {
		t.Fatalf("round 2 in-mem = %d, want 2", inMem)
	}

	// Round 3: append 10 more messages -> 12 total, compact keeping 2 -> 10 evicted, 2 kept.
	for i := 0; i < 10; i++ {
		sm1.AddMessage(key, "user", fmt.Sprintf("Question round 3: %d", i))
	}
	if err := sm1.Save(key); err != nil {
		t.Fatalf("round 3 save failed: %v", err)
	}
	sm1.ExcludeOldMessagesFromContext(key, 2)
	if err := sm1.Save(key); err != nil {
		t.Fatalf("round 3 exclude save failed: %v", err)
	}
	sm1.EvictExcludedMessages(key)
	if inMem := len(sm1.GetHistory(key)); inMem != 2 {
		t.Fatalf("round 3 in-mem = %d, want 2", inMem)
	}

	// Total messages in SQLite is 30, but in RAM only 2 should exist.
	totalCount, _ := s.Sessions().MessageCount(key)
	if totalCount != 30 {
		t.Fatalf("SQLite total messages = %d, want 30", totalCount)
	}

	// Cold load with a new SessionManager (simulating app start).
	sm2 := NewSessionManager()
	sm2.SetStore(s)

	loadedHistory := sm2.GetHistory(key)
	if len(loadedHistory) != 2 {
		t.Fatalf("Cold-loaded history count = %d, want exactly 2 in-context messages", len(loadedHistory))
	}
	if gotEvicted := sm2.GetEvictedMessageCount(key); gotEvicted != 28 {
		t.Fatalf("GetEvictedMessageCount = %d, want 28", gotEvicted)
	}
	if total := sm2.GetTotalMessageCount(key); total != 30 {
		t.Fatalf("GetTotalMessageCount = %d, want 30", total)
	}
}

// TestSQLite_ColdLoad_UnmigratedExcludedPruned verifies that an older session
// stored with boundary 0 but containing excluded messages is automatically
// pruned to in-context messages on cold load.
func TestSQLite_ColdLoad_UnmigratedExcludedPruned(t *testing.T) {
	s := newTestStore(t)
	repo := s.Sessions()
	const key = "test:unmigrated-cold-load"

	now := time.Now()
	_ = repo.UpsertSession(store.SessionMeta{
		Key:              key,
		Name:             "Unmigrated",
		FirstInMemorySeq: 0, // Legacy boundary 0
		CreatedAt:        now,
		UpdatedAt:        now,
	})

	// Insert 20 messages: first 16 excluded, last 4 in-context.
	var rows []store.MessageRow
	for i := 0; i < 20; i++ {
		isExcluded := i < 16
		rows = append(rows, store.MessageRow{
			Seq:      i,
			Role:     "user",
			JSON:     fmt.Sprintf(`{"role":"user","content":"msg %d","exclude_from_context":%v}`, i, isExcluded),
			Excluded: isExcluded,
		})
	}
	if err := repo.InsertMessages(key, rows); err != nil {
		t.Fatalf("insert messages failed: %v", err)
	}

	// Load with a new SessionManager.
	sm := NewSessionManager()
	sm.SetStore(s)

	history := sm.GetHistory(key)
	if len(history) != 4 {
		t.Fatalf("Cold-loaded unmigrated history count = %d, want 4 in-context messages", len(history))
	}
	if history[0].Content != "msg 16" {
		t.Errorf("history[0].Content = %q, want 'msg 16'", history[0].Content)
	}

	// Boundary must have been persisted to SQLite.
	meta, err := repo.GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta failed: %v", err)
	}
	if meta.FirstInMemorySeq != 16 {
		t.Errorf("persisted FirstInMemorySeq = %d, want 16", meta.FirstInMemorySeq)
	}
}

// seqsOf extracts the ordered seq values from persistence rows for assertions.
func seqsOf(rows []store.MessageRowFull) []int {
	seqs := make([]int, 0, len(rows))
	for _, r := range rows {
		seqs = append(seqs, r.Seq)
	}
	return seqs
}

// TestSQLite_AllTotalMessageCounts_MatchesPerKey verifies that the batched
// AllTotalMessageCounts returns the same per-session totals as calling
// GetTotalMessageCount for each session individually.
func TestSQLite_AllTotalMessageCounts_MatchesPerKey(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	// Create 3 sessions with different message counts.
	sm.GetOrCreate("sess-a")
	sm.AddMessage("sess-a", "user", "hello")
	sm.AddMessage("sess-a", "assistant", "hi there")

	sm.GetOrCreate("sess-b")
	sm.AddMessage("sess-b", "user", "world")

	sm.GetOrCreate("sess-c")
	sm.AddMessage("sess-c", "user", "q1")
	sm.AddMessage("sess-c", "assistant", "a1")
	sm.AddMessage("sess-c", "user", "q2")

	if err := sm.Save("sess-a"); err != nil {
		t.Fatalf("save sess-a: %v", err)
	}
	if err := sm.Save("sess-b"); err != nil {
		t.Fatalf("save sess-b: %v", err)
	}
	if err := sm.Save("sess-c"); err != nil {
		t.Fatalf("save sess-c: %v", err)
	}

	counts := sm.AllTotalMessageCounts()

	for _, key := range []string{"sess-a", "sess-b", "sess-c"} {
		got := counts[key]
		want := sm.GetTotalMessageCount(key)
		if got != want {
			t.Errorf("AllTotalMessageCounts[%q] = %d, want GetTotalMessageCount = %d", key, got, want)
		}
	}
}

// TestSQLite_AllTotalMessageCounts_ColdSessions verifies that sessions evicted
// from memory (metadata only) still get their correct persisted totals via the
// batched store query, matching GetTotalMessageCount.
func TestSQLite_AllTotalMessageCounts_ColdSessions(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	sm.GetOrCreate("sess-a")
	sm.AddMessage("sess-a", "user", "hello")
	sm.AddMessage("sess-a", "assistant", "hi")

	sm.GetOrCreate("sess-b")
	sm.AddMessage("sess-b", "user", "world")

	if err := sm.Save("sess-a"); err != nil {
		t.Fatalf("save sess-a: %v", err)
	}
	if err := sm.Save("sess-b"); err != nil {
		t.Fatalf("save sess-b: %v", err)
	}

	// Evict both sessions from memory. EvictSession persists before removing.
	if !sm.EvictSession("sess-a") {
		t.Fatal("EvictSession(sess-a) returned false")
	}
	if !sm.EvictSession("sess-b") {
		t.Fatal("EvictSession(sess-b) returned false")
	}

	// Confirm they are cold (not in the in-memory map) but still known via metadata.
	sm.mu.RLock()
	if _, ok := sm.sessions["sess-a"]; ok {
		t.Fatal("sess-a should be evicted from memory")
	}
	if _, ok := sm.sessions["sess-b"]; ok {
		t.Fatal("sess-b should be evicted from memory")
	}
	sm.mu.RUnlock()

	counts := sm.AllTotalMessageCounts()

	if counts["sess-a"] != 2 {
		t.Errorf("cold sess-a count = %d, want 2", counts["sess-a"])
	}
	if counts["sess-b"] != 1 {
		t.Errorf("cold sess-b count = %d, want 1", counts["sess-b"])
	}

	// Batched cold-path totals must match the per-key lookup.
	for _, key := range []string{"sess-a", "sess-b"} {
		if got, want := counts[key], sm.GetTotalMessageCount(key); got != want {
			t.Errorf("cold AllTotalMessageCounts[%q] = %d, want GetTotalMessageCount = %d", key, got, want)
		}
	}
}

// TestAllTotalMessageCounts_NoStore verifies that in-memory sessions are
// counted correctly and nothing panics when no store is configured.
func TestAllTotalMessageCounts_NoStore(t *testing.T) {
	sm := NewSessionManager() // store is nil

	sm.GetOrCreate("sess-a")
	sm.AddMessage("sess-a", "user", "hello")
	sm.AddMessage("sess-a", "assistant", "hi")

	sm.GetOrCreate("sess-b")
	sm.AddMessage("sess-b", "user", "world")

	counts := sm.AllTotalMessageCounts()

	if counts["sess-a"] != 2 {
		t.Errorf("sess-a count = %d, want 2", counts["sess-a"])
	}
	if counts["sess-b"] != 1 {
		t.Errorf("sess-b count = %d, want 1", counts["sess-b"])
	}
	if _, ok := counts["nonexistent"]; ok {
		t.Error("nonexistent session unexpectedly appeared in counts")
	}
}

// TestSQLite_LoadEvictedMessages_GapRebase is a regression test for the
// seq-gap corruption bug. Reproduction steps (mirrors the live incident):
//
//  1. Build a session, exclude a prefix, save, evict → boundary = B, the
//     evicted rows live in SQLite below the boundary.
//  2. A full rewrite later calls PruneExcluded, physically deleting the
//     OLDEST excluded rows. ExcludeOldMessagesFromContext never excludes
//     index 0 (the original user request), so the deleted rows are seqs
//     1..P, leaving a gap in the middle of the persisted range.
//  3. LoadEvictedMessages (WebUI history pagination) loads the gapped evicted
//     rows and prepends them, resetting firstInMemorySeq = 0. WITHOUT a rebase,
//     slice index i no longer maps to SQLite seq i: the in-memory slice is
//     contiguous but the persisted rows still carry their gapped seqs.
//  4. The next incremental/streaming save computes seqs via seqForIndex
//     (= firstInMemorySeq + i = i) and writes content onto the WRONG rows —
//     shifting JSON blobs relative to the role column (the observed corruption).
//
// The fix: LoadEvictedMessages performs a full rewrite (saveFullUnlocked) that
// re-numbers all rows contiguously from 0, healing the gap. This test asserts
// that after LoadEvictedMessages the persisted rows are contiguous from seq 0
// and that role/JSON stay aligned on every row.
func TestSQLite_LoadEvictedMessages_GapRebase(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)

	key := "test:gap-rebase"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	// Exclude the first 7 (m0..m6), keep m7..m9 in context; persist + evict.
	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 7 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 7", evicted)
	}

	// Simulate the post-full-rewrite pruning that physically deletes the
	// OLDEST excluded rows, creating a gap. ExcludeOldMessagesFromContext
	// never excludes index 0 (the original user request), so the excluded rows
	// are m1..m6 (seq 1..6). keepCount=7 → total 10, so the 3 oldest excluded
	// rows (seq 1,2,3 = m1,m2,m3) are deleted, leaving a gap at seq 1..3.
	pruned, pErr := s.Sessions().PruneExcluded(key, 7)
	if pErr != nil {
		t.Fatalf("PruneExcluded failed: %v", pErr)
	}
	if pruned != 3 {
		t.Fatalf("PruneExcluded deleted %d rows, want 3", pruned)
	}

	// loadRaw reads seq + the raw role column + JSON + excluded directly, since
	// the observed corruption was a role-column vs JSON-role mismatch that the
	// higher-level MessageRowFull (no Role field) cannot surface.
	type rawRow struct {
		Seq      int
		Role     string
		JSON     string
		Excluded bool
	}
	loadRaw := func() []rawRow {
		t.Helper()
		rowsQ, qErr := s.DB().Query(
			`SELECT seq, role, message, excluded FROM session_messages WHERE session_key = ? ORDER BY seq ASC`, key)
		if qErr != nil {
			t.Fatalf("raw query failed: %v", qErr)
		}
		defer rowsQ.Close()
		var out []rawRow
		for rowsQ.Next() {
			var r rawRow
			if sErr := rowsQ.Scan(&r.Seq, &r.Role, &r.JSON, &r.Excluded); sErr != nil {
				t.Fatalf("raw scan failed: %v", sErr)
			}
			out = append(out, r)
		}
		return out
	}

	// Sanity: rows now occupy seq 0,4,5,6,7,8,9 — m0 (seq 0, never excluded)
	// survives, then a gap at seq 1..3, then m4..m9. This gap below/around the
	// eviction boundary (7) is what breaks the seq↔sliceIndex invariant on load.
	rowsBefore := loadRaw()
	if len(rowsBefore) != 7 {
		t.Fatalf("rows before LoadEvictedMessages = %d, want 7", len(rowsBefore))
	}
	if rowsBefore[0].Seq != 0 {
		t.Fatalf("first row seq before load = %d, want 0 (m0 survives)", rowsBefore[0].Seq)
	}
	if rowsBefore[1].Seq != 4 {
		t.Fatalf("second row seq before load = %d, want 4 (gap at seq 1..3 NOT present?)", rowsBefore[1].Seq)
	}

	// Lazy-load the evicted rows back (the WebUI pagination path). This loads
	// the 4 rows below the boundary (seq 0,4,5,6 = m0,m4,m5,m6) and prepends
	// them. With the fix it then triggers a full rewrite (saveFullUnlocked)
	// that re-bases all rows contiguously to seq 0..6, healing the gap.
	loaded := sm.LoadEvictedMessages(key)
	if loaded != 4 {
		t.Fatalf("LoadEvictedMessages loaded %d, want 4 (rows below boundary: m0,m4,m5,m6)", loaded)
	}

	// CRITICAL ASSERTION: rows must now be contiguous from seq 0. Without the
	// rebase they would still sit at seq 0,4,5,6,7,8,9, and the next
	// incremental/streaming save (seqForIndex = sliceIndex) would write content
	// onto the WRONG rows — the observed corruption.
	rowsAfter := loadRaw()
	if len(rowsAfter) != 7 {
		t.Fatalf("rows after LoadEvictedMessages = %d, want 7 (no data loss)", len(rowsAfter))
	}
	for i, row := range rowsAfter {
		if row.Seq != i {
			t.Fatalf("row %d seq = %d, want %d (gap NOT healed — rebase missing)", i, row.Seq, i)
		}
		// Role/JSON alignment: the role column must match the role inside the
		// JSON blob. A shifted write would make these diverge.
		var msg providers.Message
		if uerr := json.Unmarshal([]byte(row.JSON), &msg); uerr != nil {
			t.Fatalf("row %d JSON unmarshal failed: %v", i, uerr)
		}
		if row.Role != msg.Role {
			t.Errorf("row %d role column = %q but JSON role = %q (misaligned write)", i, row.Role, msg.Role)
		}
	}

	// Content order must be m0,m4,m5,m6,m7,m8,m9 (the pruned m1,m2,m3 are gone
	// for good; m0 was folded into the summary but stays as a persisted row).
	wantContent := []string{"m0", "m4", "m5", "m6", "m7", "m8", "m9"}
	for i, row := range rowsAfter {
		var msg providers.Message
		_ = json.Unmarshal([]byte(row.JSON), &msg)
		if msg.Content != wantContent[i] {
			t.Errorf("row %d content = %q, want %q", i, msg.Content, wantContent[i])
		}
	}

	// Excluded flags must be preserved by the rebase: m0 (seq 0) was never
	// excluded; m4,m5,m6 (seq 1,2,3) stay excluded; m7,m8,m9 (seq 4,5,6) stay
	// in-context. The rebase must NOT resurrect the pruned rows.
	wantExcluded := []bool{false, true, true, true, false, false, false}
	for i, row := range rowsAfter {
		if row.Excluded != wantExcluded[i] {
			t.Errorf("row %d excluded = %v, want %v", i, row.Excluded, wantExcluded[i])
		}
	}

	// Final proof the invariant holds: appending a new message must insert at
	// seq 7 (firstInMemorySeq 0 + sliceIndex 7), NOT overwrite an existing row.
	sm.AddMessage(key, "user", "m10")
	if err := sm.Save(key); err != nil {
		t.Fatalf("save after append failed: %v", err)
	}
	finalRows := loadRaw()
	if len(finalRows) != 8 {
		t.Fatalf("rows after append = %d, want 8 (grew by exactly 1, no overwrite)", len(finalRows))
	}
	last := finalRows[len(finalRows)-1]
	if last.Seq != 7 {
		t.Fatalf("appended row seq = %d, want 7", last.Seq)
	}
	var lastMsg providers.Message
	_ = json.Unmarshal([]byte(last.JSON), &lastMsg)
	if lastMsg.Content != "m10" || last.Role != "user" {
		t.Errorf("appended row misaligned: role=%q content=%q, want user/m10", last.Role, lastMsg.Content)
	}
}
