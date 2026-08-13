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

	// Reload a fresh session manager over the same store. Task 2.4: a session
	// saved post-eviction must reload with correct slice (all 9, in order)
	// and no dup/missing rows.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	hist := sm2.GetHistoryView(key)
	if len(hist) != 9 {
		t.Fatalf("reloaded len = %d, want 9", len(hist))
	}
	for i, msg := range hist {
		want := fmt.Sprintf("m%d", i)
		if msg.Content != want {
			t.Errorf("reloaded[%d].Content = %q, want %q", i, msg.Content, want)
		}
	}
	// Evicted messages (1-4) reload as excluded (index 0 keeps excluded=0).
	for i := 1; i <= 4; i++ {
		if !hist[i].ExcludeFromContext {
			t.Errorf("reloaded[%d] should be excluded", i)
		}
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
