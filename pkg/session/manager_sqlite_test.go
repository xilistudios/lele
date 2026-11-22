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
	sm.SetSessionRepo(s.Sessions())

	key := "test:session-1"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "Hello")
	sm.AddMessage(key, "assistant", "Hi there!")

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

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

	msgs, err := repo.LoadMessages(key)
	if err != nil {
		t.Fatalf("LoadMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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
	sm.SetSessionRepo(s.Sessions())

	key := "test:streaming"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "Tell me a story")

	sm.AppendAssistantChunk(key, "Once ")
	sm.AppendAssistantChunk(key, "upon ")
	sm.AppendAssistantChunk(key, "a time...")

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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
	sm.SetSessionRepo(s.Sessions())

	key := "test:tokens"
	sm.GetOrCreate(key)
	sm.AddTokenCounts(key, 100, 50)
	sm.AddTokenCounts(key, 200, 100)

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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
	sm.SetSessionRepo(s.Sessions())

	key := "test:mode"
	sm.GetOrCreate(key)
	if err := sm.SetMode(key, "chat"); err != nil {
		t.Fatalf("SetMode failed: %v", err)
	}

	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	mode := sm2.GetMode(key)
	if mode != "chat" {
		t.Errorf("expected mode %q, got %q", "chat", mode)
	}

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
	sm.SetSessionRepo(s.Sessions())

	sm.GetOrCreate("session-1")
	sm.AddMessage("session-1", "user", "hello")
	sm.GetOrCreate("session-2")
	sm.AddMessage("session-2", "user", "world")
	sm.GetOrCreate("session-3")
	sm.AddMessage("session-3", "user", "test")

	sm.Save("session-1")
	sm.Save("session-2")
	sm.Save("session-3")

	sessions := sm.ListSessions()
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestSQLite_SessionManager_SessionExists(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:exists"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.Save(key)

	sm.EvictSession(key)

	if !sm.SessionExists(key) {
		t.Error("expected session to exist after eviction")
	}
}

func TestSQLite_SessionManager_StreamingUpdate(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:stream-update"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")

	sm.AppendAssistantChunk(key, "part1 ")
	sm.Save(key)

	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 2 {
		t.Fatalf("expected 2 messages, got %d", count)
	}

	sm.AppendAssistantChunk(key, "part2 ")
	sm.Save(key)

	count, _ = repo.MessageCount(key)
	if count != 2 {
		t.Fatalf("expected 2 messages after streaming update, got %d", count)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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
	sm.SetSessionRepo(s.Sessions())

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
	sm.SetSessionRepo(s.Sessions())

	key := "test:compaction"
	sm.GetOrCreate(key)
	sm.IncrementCompactionCount(key)
	sm.IncrementCompactionCount(key)
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	session := sm2.GetOrCreate(key)
	if session.CompactionCount != 2 {
		t.Errorf("expected compaction count 2, got %d", session.CompactionCount)
	}
}

func TestSQLite_SessionManager_ThinkingLevel(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:thinking"
	sm.GetOrCreate(key)
	sm.SetThinkingLevel(key, "high")
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	level := sm2.GetThinkingLevel(key)
	if level != "high" {
		t.Errorf("expected thinking level %q, got %q", "high", level)
	}
}

func TestSQLite_SessionManager_TruncateHistory(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:truncate"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", "msg")
	}
	sm.Save(key)

	sm.TruncateHistory(key, 3)
	sm.Save(key)

	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 3 {
		t.Fatalf("expected 3 messages after truncate, got %d", count)
	}
}

func TestSQLite_SessionManager_ThinkingLevel_Persistence(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:thinking-persist"
	sm.GetOrCreate(key)
	sm.SetThinkingLevel(key, "medium")
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())

	level := sm2.GetThinkingLevel(key)
	if level != "medium" {
		t.Errorf("expected thinking level %q, got %q", "medium", level)
	}
}

func TestSQLite_SessionManager_SetHistory(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:set-history"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "old message")
	sm.Save(key)

	newHistory := []providers.Message{
		{Role: "user", Content: "new message 1"},
		{Role: "assistant", Content: "response 1"},
		{Role: "user", Content: "new message 2"},
	}
	sm.SetHistory(key, newHistory)
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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
	sm.SetSessionRepo(s.Sessions())

	key := "test:exclude"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", "msg")
	}
	sm.Save(key)

	sm.ExcludeOldMessagesFromContext(key, 3)
	sm.Save(key)

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
	sm.SetSessionRepo(s.Sessions())

	key := "test:name"
	sm.GetOrCreate(key)
	sm.SetName(key, "My Session")
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	name := sm2.GetName(key)
	if name != "My Session" {
		t.Errorf("expected name %q, got %q", "My Session", name)
	}
}

func TestSQLite_SessionManager_Summary(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:summary"
	sm.GetOrCreate(key)
	sm.SetSummary(key, "This is a summary")
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	summary := sm2.GetSummary(key)
	if summary != "This is a summary" {
		t.Errorf("expected summary %q, got %q", "This is a summary", summary)
	}
}

func TestSQLite_SessionManager_RemoveLastMessage(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:remove-last"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "msg1")
	sm.AddMessage(key, "assistant", "msg2")
	sm.AddMessage(key, "user", "msg3")
	sm.Save(key)

	removed := sm.RemoveLastMessage(key)
	if !removed {
		t.Fatal("expected message to be removed")
	}
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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
	sm.SetSessionRepo(s.Sessions())

	key := "test:fresh"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.Save(key)

	sm.mu.Lock()
	session := sm.sessions[key]
	session.Updated = time.Now().Add(-2 * time.Hour)
	sm.mu.Unlock()
	sm.Save(key)

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
	sm.SetSessionRepo(s.Sessions())

	key := "test:empty"
	session := sm.GetOrCreate(key)
	if session.Key != key {
		t.Errorf("expected key %q, got %q", key, session.Key)
	}

	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	name := sm2.GetName(key)
	if name != "" {
		t.Errorf("expected empty name, got %q", name)
	}
}

func TestSQLite_SessionManager_VerboseLevel(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:verbose"
	sm.GetOrCreate(key)
	sm.SetVerboseLevel(key, "full")
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	level := sm2.GetVerboseLevel(key)
	if level != "full" {
		t.Errorf("expected verbose level %q, got %q", "full", level)
	}
}

func TestSQLite_SessionManager_Model(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:model"
	sm.GetOrCreate(key)
	sm.SetModel(key, "gpt-4")
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	model := sm2.GetModel(key)
	if model != "gpt-4" {
		t.Errorf("expected model %q, got %q", "gpt-4", model)
	}
}

func TestSQLite_SessionManager_CreatedUpdated(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:timestamps"
	before := time.Now()
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.Save(key)
	after := time.Now()

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
	sm.SetSessionRepo(s.Sessions())

	history := sm.GetHistory("nonexistent:session")
	if len(history) != 0 {
		t.Errorf("expected empty history, got %d messages", len(history))
	}
}

func TestSQLite_SessionManager_SetName_CreatesSession(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:setname-create"
	sm.SetName(key, "New Name")

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	name := sm2.GetName(key)
	if name != "New Name" {
		t.Errorf("expected name %q, got %q", "New Name", name)
	}
}

func TestSQLite_SessionManager_MultipleSaves(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:multi-save"
	sm.GetOrCreate(key)

	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", "msg")
		sm.Save(key)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	history := sm2.GetHistory(key)
	if len(history) != 10 {
		t.Fatalf("expected 10 messages, got %d", len(history))
	}
}

func TestSQLite_SessionManager_ConcurrentReadersWriter(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:concurrent-rw"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "initial")
	sm.Save(key)

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

	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", "msg")
		sm.Save(key)
		time.Sleep(time.Millisecond)
	}

	<-done

	if maxWait > 50*time.Millisecond {
		t.Errorf("reader blocked for %v (>50ms threshold)", maxWait)
	}
}

func TestSQLite_SessionManager_LoadFromDisk(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:lazy-load"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	sm.Save(key)

	sm.EvictSession(key)

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
	sm.SetSessionRepo(s.Sessions())

	sm.GetOrCreate("sess-a")
	sm.AddMessage("sess-a", "user", "hello")
	sm.AddMessage("sess-a", "assistant", "hi there")
	sm.GetOrCreate("sess-b")
	sm.AddMessage("sess-b", "user", "world")

	sm.Save("sess-a")
	sm.Save("sess-b")

	sm.GetOrCreate("sess-empty")
	sm.Save("sess-empty")

	sm.mu.Lock()
	for k := range sm.sessions {
		delete(sm.sessions, k)
		delete(sm.accessTimes, k)
	}
	sm.mu.Unlock()

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

func TestSQLite_IncrementalSave_AppendOnly(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:incr-append"
	sm.GetOrCreate(key)

	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	sm.AddMessage(key, "user", "how are you?")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 3 {
		t.Fatalf("after initial save: got %d messages, want 3", count)
	}

	sm.AddMessage(key, "assistant", "I'm good")
	sm.AddMessage(key, "user", "great")
	if err := sm.Save(key); err != nil {
		t.Fatalf("incremental save: %v", err)
	}

	count, _ = repo.MessageCount(key)
	if count != 5 {
		t.Fatalf("after incremental save: got %d messages, want 5", count)
	}

	msgJSONs, _ := repo.LoadMessages(key)
	var lastMsg providers.Message
	json.Unmarshal([]byte(msgJSONs[4]), &lastMsg)
	if lastMsg.Content != "great" {
		t.Errorf("last message content = %q, want %q", lastMsg.Content, "great")
	}
}

func TestSQLite_IncrementalSave_StreamingUpdate(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:incr-stream"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "tell me a story")

	sm.Save(key)

	sm.AppendAssistantChunk(key, "Once upon a time...")

	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 2 {
		t.Fatalf("after streaming: got %d messages, want 2", count)
	}

	msgJSONs, _ := repo.LoadMessages(key)
	var streamMsg providers.Message
	json.Unmarshal([]byte(msgJSONs[1]), &streamMsg)
	if streamMsg.Content != "Once upon a time..." {
		t.Errorf("streaming content = %q, want %q", streamMsg.Content, "Once upon a time...")
	}
}

func TestSQLite_MetaOnlySave(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:meta-only"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")
	sm.AddMessage(key, "assistant", "hi")
	sm.Save(key)

	repo := s.Sessions()
	countBefore, _ := repo.MessageCount(key)
	if countBefore != 2 {
		t.Fatalf("before meta save: got %d messages, want 2", countBefore)
	}

	sm.SetName(key, "My Chat")
	sm.SetModel(key, "gpt-4")

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	name := sm2.GetName(key)
	model := sm2.GetModel(key)
	if name != "My Chat" {
		t.Errorf("name = %q, want %q", name, "My Chat")
	}
	if model != "gpt-4" {
		t.Errorf("model = %q, want %q", model, "gpt-4")
	}

	countAfter, _ := repo.MessageCount(key)
	if countAfter != 2 {
		t.Errorf("after meta save: got %d messages, want 2", countAfter)
	}
}

func TestSQLite_FullRewrite_AfterTruncate(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:truncate"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("msg-%d", i))
	}
	sm.Save(key)

	repo := s.Sessions()
	count, _ := repo.MessageCount(key)
	if count != 10 {
		t.Fatalf("before truncate: got %d messages, want 10", count)
	}

	sm.TruncateHistory(key, 3)
	sm.Save(key)

	count, _ = repo.MessageCount(key)
	if count != 3 {
		t.Errorf("after truncate: got %d messages, want 3", count)
	}

	msgJSONs, _ := repo.LoadMessages(key)
	var firstMsg providers.Message
	json.Unmarshal([]byte(msgJSONs[0]), &firstMsg)
	if firstMsg.Content != "msg-7" {
		t.Errorf("first remaining message = %q, want %q", firstMsg.Content, "msg-7")
	}
}

func TestSQLite_DirtyFlags_Basic(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:dirty"
	session := sm.GetOrCreate(key)

	if session.lastPersistedSeq != -1 {
		t.Errorf("new session lastPersistedSeq = %d, want -1", session.lastPersistedSeq)
	}

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

	sm.AddMessage(key, "user", "hello")
	if session.msgsAppended != 1 {
		t.Errorf("after add: msgsAppended = %d, want 1", session.msgsAppended)
	}

	sm.Save(key)
	if session.msgsAppended != 0 {
		t.Errorf("after save: msgsAppended = %d, want 0", session.msgsAppended)
	}
	if session.lastPersistedSeq != 0 {
		t.Errorf("lastPersistedSeq = %d, want 0", session.lastPersistedSeq)
	}

	sm.SetName(key, "test")
	if session.metaDirty {
		t.Error("after SetName+save: metaDirty should be false (already persisted)")
	}
}
func TestSQLite_GapAware_IncrementalSave_SeqOffset(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:gap-incr"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "m0")
	sm.AddMessage(key, "assistant", "m1")
	sm.AddMessage(key, "user", "m2")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sess := sm.sessions[key]
	if sess == nil {
		t.Fatal("session not in memory")
	}
	sess.firstInMemorySeq = 5

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
	sm.SetSessionRepo(s.Sessions())

	key := "test:gap-excl"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "u0")
	sm.AddMessage(key, "assistant", "a1")
	sm.AddMessage(key, "user", "u2")
	sm.AddMessage(key, "assistant", "a3")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

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
	sm.SetSessionRepo(s.Sessions())

	key := "test:gap-full"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "m0")
	sm.AddMessage(key, "assistant", "m1")
	sm.AddMessage(key, "user", "m2")
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

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
	for i, m := range sess.Messages {
		mJSON, _ := json.Marshal(m)
		evictedRows = append(evictedRows, store.MessageRow{Seq: 7 + i, Role: m.Role, JSON: string(mJSON)})
	}
	if err := s.Sessions().ReplaceMessages(key, evictedRows); err != nil {
		t.Fatalf("re-insert evicted + inbox rows failed: %v", err)
	}
	sess.firstInMemorySeq = 7
	sess.evictedTotal = 7

	sm.SetHistory(key, []providers.Message{
		{Role: "user", Content: "n0"},
		{Role: "assistant", Content: "n1"},
	})
	if err := sm.Save(key); err != nil {
		t.Fatalf("full-rewrite Save failed: %v", err)
	}

	if sess.firstInMemorySeq != 7 {
		t.Errorf("after full rewrite firstInMemorySeq = %d, want 7 (gap preserved)", sess.firstInMemorySeq)
	}
	if sess.evictedTotal != 7 {
		t.Errorf("after full rewrite evictedTotal = %d, want 7 (gap preserved)", sess.evictedTotal)
	}
	if sess.lastPersistedSeq != 1 {
		t.Errorf("after full rewrite lastPersistedSeq = %d, want 1", sess.lastPersistedSeq)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 9 {
		t.Fatalf("expected 9 rows (7 evicted + 2 new), got %d", len(rows))
	}
	for i := 0; i < 7; i++ {
		if rows[i].Seq != i {
			t.Errorf("evicted row %d seq = %d, want %d", i, rows[i].Seq, i)
		}
		if !rows[i].Excluded {
			t.Errorf("evicted row %d should be excluded", i)
		}
	}
	if rows[7].Seq != 7 || rows[8].Seq != 8 {
		t.Errorf("expected seqs 7,8 after rewrite, got %d,%d", rows[7].Seq, rows[8].Seq)
	}
}
func TestSQLite_EvictExcluded_WindowRoundTrip(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:evict-window"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

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

	hist := sm.GetHistoryView(key)
	if len(hist) != 3 {
		t.Fatalf("in-memory len after evict = %d, want 3", len(hist))
	}
	if hist[0].Content != "m7" {
		t.Errorf("first in-memory message after evict = %q, want %q", hist[0].Content, "m7")
	}
	if got := sm.GetSummary(key); !strings.Contains(got, "m0") {
		t.Errorf("summary after eviction should contain folded index-0 content, got %q", got)
	}
	if got := sm.GetEvictedMessageCount(key); got != 7 {
		t.Fatalf("GetEvictedMessageCount after evict = %d, want 7", got)
	}
	if got := sm.GetTotalMessageCount(key); got != 10 {
		t.Fatalf("GetTotalMessageCount after evict = %d, want 10", got)
	}

	if again := sm.EvictExcludedMessages(key); again != 0 {
		t.Fatalf("second EvictExcludedMessages = %d, want 0", again)
	}

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

	page := sm.LoadMessagesWindow(key, -1, 0, 3)
	if page == nil {
		t.Fatalf("LoadMessagesWindow newest page = nil, want a page")
	}
	gotContents := make([]string, 0, len(page.Messages))
	for _, msg := range page.Messages {
		gotContents = append(gotContents, msg.Content)
	}
	want := []string{"m4", "m5", "m6"}
	for i, w := range want {
		if gotContents[i] != w {
			t.Fatalf("newest page[%d] = %q, want %q", i, gotContents[i], w)
		}
	}
	if page.FirstSeq != 4 || page.LastSeq != 6 {
		t.Errorf("newest page seqs = [%d,%d], want [4,6]", page.FirstSeq, page.LastSeq)
	}
	if !page.HasOlder {
		t.Errorf("newest page HasOlder = false, want true (m0..m3 older)")
	}
	if page.EvictedCount != 7 || page.TotalCount != 10 {
		t.Errorf("page counts = evicted %d total %d, want 7/10", page.EvictedCount, page.TotalCount)
	}

	page2 := sm.LoadMessagesWindow(key, 4, 0, 3)
	if page2 == nil || len(page2.Messages) != 3 {
		t.Fatalf("older page = %+v, want 3 messages", page2)
	}
	if page2.Messages[0].Content != "m1" || page2.Messages[2].Content != "m3" {
		t.Errorf("older page = [%q..%q], want [m1..m3]", page2.Messages[0].Content, page2.Messages[2].Content)
	}
	if !page2.HasOlder {
		t.Errorf("older page HasOlder = false, want true (m0 at seq 0)")
	}
	page3 := sm.LoadMessagesWindow(key, 1, 0, 3)
	if page3 == nil || len(page3.Messages) != 1 || page3.Messages[0].Content != "m0" {
		t.Fatalf("oldest page = %+v, want single m0", page3)
	}
	if page3.HasOlder {
		t.Errorf("oldest page HasOlder = true, want false")
	}

	if !page2.Messages[0].ExcludeFromContext {
		t.Errorf("m1 served without ExcludeFromContext flag")
	}

	if got := len(sm.GetHistoryView(key)); got != 3 {
		t.Fatalf("in-memory history after paging = %d, want 3 (window reads must not materialize)", got)
	}
	if got := sm.GetEvictedMessageCount(key); got != 7 {
		t.Fatalf("evicted count after paging = %d, want 7 (must be untouched)", got)
	}

	again := sm.LoadMessagesWindow(key, -1, 0, 3)
	if again == nil || again.FirstSeq != 4 || again.LastSeq != 6 {
		t.Fatalf("repeat newest page = %+v, want seqs [4,6]", again)
	}
}

func TestSQLite_ReloadAfterEviction_SeqAccounting(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

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

	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	evicted := sm.EvictExcludedMessages(key)
	if evicted != 4 {
		t.Fatalf("evicted %d, want 4", evicted)
	}

	sm.AddMessage(key, "user", "m8")
	if err := sm.Save(key); err != nil {
		t.Fatalf("append-after-evict Save failed: %v", err)
	}

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

	sm.EvictSession(key)

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
	hist := sm2.GetHistoryView(key)
	if len(hist) != 5 {
		t.Fatalf("reloaded in-memory len = %d, want 5 (non-evicted only)", len(hist))
	}
	for i, msg := range hist {
		want := fmt.Sprintf("m%d", i+4)
		if msg.Content != want {
			t.Errorf("reloaded[%d].Content = %q, want %q", i, msg.Content, want)
		}
	}
	if got := sm2.GetEvictedMessageCount(key); got != 4 {
		t.Errorf("GetEvictedMessageCount after reload = %d, want 4", got)
	}
	if got := sm2.GetTotalMessageCount(key); got != 9 {
		t.Errorf("GetTotalMessageCount after reload = %d, want 9", got)
	}

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
	sm.SetSessionRepo(s.Sessions())

	key := "test:evict-noop"
	sm.GetOrCreate(key)
	for i := 0; i < 6; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	sm.Save(key)

	if got := sm.EvictExcludedMessages(key); got != 0 {
		t.Fatalf("EvictExcludedMessages with no excluded = %d, want 0", got)
	}
	if got := sm.EvictExcludedMessages("missing:key"); got != 0 {
		t.Fatalf("EvictExcludedMessages on missing key = %d, want 0", got)
	}
}

func TestEvictExcluded_NoStore(t *testing.T) {
	sm := NewSessionManager()

	key := "test:evict-nostore"
	sm.GetOrCreate(key)
	for i := 0; i < 8; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	sm.ExcludeOldMessagesFromContext(key, 3)
	_ = sm.Save(key)

	if got := sm.EvictExcludedMessages(key); got != 0 {
		t.Fatalf("EvictExcludedMessages without store = %d, want 0", got)
	}
	hist := sm.GetHistory(key)
	if len(hist) != 8 {
		t.Fatalf("history length after refused eviction = %d, want 8", len(hist))
	}
}

func TestSQLite_ColdLoad_RestoresEvictionBoundary(t *testing.T) {
	s := newTestStore(t)

	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:coldload-evicted"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 3)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 7 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 7", evicted)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())

	hist := sm2.GetHistory(key)
	if len(hist) != 3 {
		t.Fatalf("in-memory len after cold load = %d, want 3", len(hist))
	}
	if hist[0].Content != "m7" {
		t.Errorf("first in-memory message after cold load = %q, want %q", hist[0].Content, "m7")
	}
	if hist[1].Content != "m8" || hist[2].Content != "m9" {
		t.Errorf("unexpected kept suffix after cold load: %q, %q, %q", hist[0].Content, hist[1].Content, hist[2].Content)
	}

	if got := sm2.GetEvictedMessageCount(key); got != 7 {
		t.Fatalf("GetEvictedMessageCount after cold load = %d, want 7", got)
	}
	if got := sm2.GetTotalMessageCount(key); got != 10 {
		t.Fatalf("GetTotalMessageCount after cold load = %d, want 10", got)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 10 {
		t.Fatalf("SQLite rows = %d, want 10", len(rows))
	}
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

func TestSQLite_ColdLoad_PrunedPrefixBelowBoundary(t *testing.T) {
	s := newTestStore(t)

	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:pruned-prefix-restore"
	sm.GetOrCreate(key)
	for i := 0; i < 10; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 2)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 8 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 8", evicted)
	}
	meta, err := s.Sessions().GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta failed: %v (err=%v)", meta, err)
	}
	if meta.FirstInMemorySeq != 8 {
		t.Fatalf("FirstInMemorySeq persisted = %d, want 8", meta.FirstInMemorySeq)
	}

	pruned, pErr := s.Sessions().PruneExcluded(key, 7)
	if pErr != nil {
		t.Fatalf("PruneExcluded failed: %v", pErr)
	}
	if pruned != 3 {
		t.Fatalf("PruneExcluded deleted %d rows, want 3 (oldest excluded prefix)", pruned)
	}

	rows, err := s.Sessions().LoadMessagesWithSeq(key)
	if err != nil {
		t.Fatalf("LoadMessagesWithSeq failed: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("SQLite rows after prune = %d, want 7", len(rows))
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())

	hist := sm2.GetHistory(key)
	if len(hist) != 2 {
		t.Fatalf("in-memory len after cold load = %d, want 2", len(hist))
	}
	if hist[0].Content != "m8" || hist[1].Content != "m9" {
		t.Fatalf("unexpected kept suffix after cold load: %q, %q", hist[0].Content, hist[1].Content)
	}

	if got := sm2.GetEvictedMessageCount(key); got != 8-pruned {
		t.Fatalf("GetEvictedMessageCount after cold load = %d, want %d (B-pruned)", got, 8-pruned)
	}
	if inMem := len(sm2.GetHistory(key)); inMem != 2 {
		t.Fatalf("GetHistory len after cold load = %d, want 2", inMem)
	}
	if got := sm2.GetTotalMessageCount(key); got != 8-pruned+len(hist) {
		t.Fatalf("GetTotalMessageCount after cold load = %d, want %d", got, 8-pruned+len(hist))
	}

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
	if newRow.Seq != 10 {
		t.Fatalf("appended row seq = %d, want 10 (firstInMemorySeq+sliceIndex), json=%q", newRow.Seq, newRow.JSON)
	}
}

func TestSQLite_ColdLoad_NoEviction_BoundaryZero(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:coldload-noevict"
	sm.GetOrCreate(key)
	for i := 0; i < 5; i++ {
		sm.AddMessage(key, "user", fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())
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

func TestSQLite_EvictionBoundary_PersistedOnEvict(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

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

func TestSQLite_MultipleCompactions_ColdLoadContextOnly(t *testing.T) {
	s := newTestStore(t)
	sm1 := NewSessionManager()
	sm1.SetSessionRepo(s.Sessions())

	const key = "test:multi-compaction-cold-load"
	sm1.GetOrCreate(key)

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

	totalCount, _ := s.Sessions().MessageCount(key)
	if totalCount != 30 {
		t.Fatalf("SQLite total messages = %d, want 30", totalCount)
	}

	sm2 := NewSessionManager()
	sm2.SetSessionRepo(s.Sessions())

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

func TestSQLite_ColdLoad_UnmigratedExcludedPruned(t *testing.T) {
	s := newTestStore(t)
	repo := s.Sessions()
	const key = "test:unmigrated-cold-load"

	now := time.Now()
	_ = repo.UpsertSession(store.SessionMeta{
		Key:              key,
		Name:             "Unmigrated",
		FirstInMemorySeq: 0,
		CreatedAt:        now,
		UpdatedAt:        now,
	})

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

	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	history := sm.GetHistory(key)
	if len(history) != 4 {
		t.Fatalf("Cold-loaded unmigrated history count = %d, want 4 in-context messages", len(history))
	}
	if history[0].Content != "msg 16" {
		t.Errorf("history[0].Content = %q, want 'msg 16'", history[0].Content)
	}

	meta, err := repo.GetSessionMeta(key)
	if err != nil || meta == nil {
		t.Fatalf("GetSessionMeta failed: %v", err)
	}
	if meta.FirstInMemorySeq != 16 {
		t.Errorf("persisted FirstInMemorySeq = %d, want 16", meta.FirstInMemorySeq)
	}
}

func seqsOf(rows []store.MessageRowFull) []int {
	seqs := make([]int, 0, len(rows))
	for _, r := range rows {
		seqs = append(seqs, r.Seq)
	}
	return seqs
}

func TestSQLite_AllTotalMessageCounts_MatchesPerKey(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

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

func TestSQLite_AllTotalMessageCounts_ColdSessions(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

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

	if !sm.EvictSession("sess-a") {
		t.Fatal("EvictSession(sess-a) returned false")
	}
	if !sm.EvictSession("sess-b") {
		t.Fatal("EvictSession(sess-b) returned false")
	}

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

	for _, key := range []string{"sess-a", "sess-b"} {
		if got, want := counts[key], sm.GetTotalMessageCount(key); got != want {
			t.Errorf("cold AllTotalMessageCounts[%q] = %d, want GetTotalMessageCount = %d", key, got, want)
		}
	}
}

func TestAllTotalMessageCounts_NoStore(t *testing.T) {
	sm := NewSessionManager()

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

func TestSQLite_EvictionGap_IncrementalAndRebase(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetSessionRepo(s.Sessions())

	key := "test:gap-rebase"
	sm.GetOrCreate(key)
	for i := 0; i < 12; i++ {
		role := "user"
		if i%2 != 0 {
			role = "assistant"
		}
		sm.AddMessage(key, role, fmt.Sprintf("m%d", i))
	}
	if err := sm.Save(key); err != nil {
		t.Fatalf("initial Save failed: %v", err)
	}

	sm.ExcludeOldMessagesFromContext(key, 1)
	if err := sm.Save(key); err != nil {
		t.Fatalf("exclude Save failed: %v", err)
	}
	if evicted := sm.EvictExcludedMessages(key); evicted != 10 {
		t.Fatalf("EvictExcludedMessages evicted %d, want 10", evicted)
	}

	pruned, pErr := s.Sessions().PruneExcluded(key, 9)
	if pErr != nil {
		t.Fatalf("PruneExcluded failed: %v", pErr)
	}
	if pruned != 3 {
		t.Fatalf("PruneExcluded deleted %d rows, want 3", pruned)
	}

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
		if rErr := rowsQ.Err(); rErr != nil {
			t.Fatalf("raw rows iteration failed: %v", rErr)
		}
		return out
	}

	rowsBefore := loadRaw()
	if len(rowsBefore) != 9 {
		t.Fatalf("rows after prune = %d, want 9", len(rowsBefore))
	}
	if rowsBefore[1].Seq != 4 {
		t.Fatalf("second row seq = %d, want 4 (gap at seq 1..3 not present?)", rowsBefore[1].Seq)
	}

	sm.AddMessage(key, "user", "m12")
	if err := sm.Save(key); err != nil {
		t.Fatalf("append-with-gap Save failed: %v", err)
	}
	rowsAppend := loadRaw()
	if len(rowsAppend) != 10 {
		t.Fatalf("rows after append = %d, want 10 (grew by exactly 1, no overwrite)", len(rowsAppend))
	}
	last := rowsAppend[len(rowsAppend)-1]
	if last.Seq != 12 {
		t.Fatalf("appended row seq = %d, want 12 (seqForIndex must skip pruned gap rows)", last.Seq)
	}
	var lastMsg providers.Message
	_ = json.Unmarshal([]byte(last.JSON), &lastMsg)
	if lastMsg.Content != "m12" || last.Role != "user" {
		t.Errorf("appended row misaligned: role=%q content=%q, want user/m12", last.Role, lastMsg.Content)
	}

	page := sm.LoadMessagesWindow(key, 9, 0, 10)
	if page == nil {
		t.Fatalf("window page below boundary = nil, want the evicted rows")
	}
	wantSeqs := []int{0, 4, 5, 6, 7, 8}
	if len(page.Seqs) != len(wantSeqs) {
		t.Fatalf("window seqs = %v, want %v", page.Seqs, wantSeqs)
	}
	for i, w := range wantSeqs {
		if page.Seqs[i] != w {
			t.Errorf("window seq[%d] = %d, want %d", i, page.Seqs[i], w)
		}
	}
	if page.Messages[1].Content != "m4" {
		t.Errorf("window message at seq 4 = %q, want m4", page.Messages[1].Content)
	}

	kept := sm.GetHistoryView(key)
	sm.SetHistory(key, kept)
	if err := sm.Save(key); err != nil {
		t.Fatalf("full-rewrite Save failed: %v", err)
	}
	rowsAfter := loadRaw()
	if len(rowsAfter) != 10 {
		t.Fatalf("rows after full rewrite = %d, want 10 (no data loss)", len(rowsAfter))
	}
	for i, row := range rowsAfter {
		if row.Seq != i {
			t.Fatalf("row %d seq = %d, want %d (gap NOT healed by rewrite)", i, row.Seq, i)
		}
		var msg providers.Message
		if uerr := json.Unmarshal([]byte(row.JSON), &msg); uerr != nil {
			t.Fatalf("row %d JSON unmarshal failed: %v", i, uerr)
		}
		if row.Role != msg.Role {
			t.Errorf("row %d role column = %q but JSON role = %q (misaligned write)", i, row.Role, msg.Role)
		}
	}

	wantContent := []string{"m0", "m4", "m5", "m6", "m7", "m8", "m9", "m10", "m11", "m12"}
	for i, row := range rowsAfter {
		var msg providers.Message
		_ = json.Unmarshal([]byte(row.JSON), &msg)
		if msg.Content != wantContent[i] {
			t.Errorf("row %d content = %q, want %q", i, msg.Content, wantContent[i])
		}
	}

	wantExcluded := []bool{false, true, true, true, true, false, true, false, false, false}
	for i, row := range rowsAfter {
		if row.Excluded != wantExcluded[i] {
			t.Errorf("row %d excluded = %v, want %v", i, row.Excluded, wantExcluded[i])
		}
	}

	sm.AddMessage(key, "user", "m13")
	if err := sm.Save(key); err != nil {
		t.Fatalf("post-rewrite append Save failed: %v", err)
	}
	rowsFinal := loadRaw()
	if len(rowsFinal) != 11 {
		t.Fatalf("rows after post-rewrite append = %d, want 11", len(rowsFinal))
	}
	if rowsFinal[10].Seq != 10 {
		t.Fatalf("post-rewrite appended seq = %d, want 10", rowsFinal[10].Seq)
	}
	var fm providers.Message
	_ = json.Unmarshal([]byte(rowsFinal[10].JSON), &fm)
	if fm.Content != "m13" {
		t.Errorf("post-rewrite appended content = %q, want m13", fm.Content)
	}
}
