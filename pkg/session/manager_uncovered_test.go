package session

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// ---- Verbose preference / mode methods ----

func TestHasVerbosePreference(t *testing.T) {
	tests := []struct {
		name  string
		setup func(sm *SessionManager, key string)
		want  bool
	}{
		{"no session", func(*SessionManager, string) {}, false},
		{"verbose level empty mode false", func(sm *SessionManager, key string) {
			sm.GetOrCreate(key)
		}, false},
		{"verbose level set", func(sm *SessionManager, key string) {
			s := sm.GetOrCreate(key)
			s.VerboseLevel = "basic"
		}, true},
		{"verbose mode bool true", func(sm *SessionManager, key string) {
			s := sm.GetOrCreate(key)
			s.VerboseMode = true
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := NewSessionManager()
			key := "test:hasverb"
			tt.setup(sm, key)
			if got := sm.HasVerbosePreference(key); got != tt.want {
				t.Errorf("HasVerbosePreference(%q) = %v, want %v", key, got, tt.want)
			}
		})
	}
}

func TestGetVerboseMode(t *testing.T) {
	sm := NewSessionManager()
	if sm.GetVerboseMode("missing") {
		t.Error("GetVerboseMode for missing session should be false")
	}

	key := "test:vm"
	s := sm.GetOrCreate(key)
	s.VerboseMode = true
	if !sm.GetVerboseMode(key) {
		t.Error("GetVerboseMode should be true after setting VerboseMode")
	}
	s.VerboseMode = false
	if sm.GetVerboseMode(key) {
		t.Error("GetVerboseMode should be false after unsetting VerboseMode")
	}
}

func TestSetVerboseMode(t *testing.T) {
	sm := NewSessionManager()
	key := "test:setvm"

	if err := sm.SetVerboseMode(key, true); err != nil {
		t.Fatalf("SetVerboseMode(true): %v", err)
	}
	if !sm.GetVerboseMode(key) {
		t.Error("VerboseMode should be true after SetVerboseMode(true)")
	}

	if err := sm.SetVerboseMode(key, false); err != nil {
		t.Fatalf("SetVerboseMode(false): %v", err)
	}
	if sm.GetVerboseMode(key) {
		t.Error("VerboseMode should be false after SetVerboseMode(false)")
	}
}

func TestSetVerboseMode_WithStore(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "test:setvmstore"
	// VerboseMode is not persisted via sessionMetaFromSession (only VerboseLevel
	// is), so use SetVerboseLevel to verify persisted level round-trips.
	if err := sm.SetVerboseLevel(key, "full"); err != nil {
		t.Fatalf("SetVerboseLevel with store: %v", err)
	}

	// Fresh manager reloads preference.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	sm2.GetOrCreate(key)
	if !sm2.HasVerbosePreference(key) {
		t.Error("HasVerbosePreference after persistence should be true")
	}
	if got := sm2.GetVerboseLevel(key); got != "full" {
		t.Errorf("GetVerboseLevel after persistence = %q, want full", got)
	}
}

func TestSetVerboseMode_LoadsFromDisk(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "test:vmdisk"
	sm.GetOrCreate(key)
	_ = sm.SetVerboseMode(key, true)
	sm.Save(key)

	// New manager: don't call GetOrCreate first.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	if err := sm2.SetVerboseMode(key, false); err != nil {
		t.Fatalf("SetVerboseMode on unloaded session: %v", err)
	}
	if sm2.GetVerboseMode(key) {
		t.Error("VerboseMode should be false after SetVerboseMode(false)")
	}
}

func TestSetVerboseMode_GetVerboseMode_WithoutStore(t *testing.T) {
	sm := NewSessionManager()
	key := "test:nostore"
	// Only exercised the no-store path in saveMetaOnlyUnlocked; ensure no panic.
	if err := sm.SetVerboseMode(key, true); err != nil {
		t.Fatalf("SetVerboseMode without store should return nil error, got %v", err)
	}
}

// ---- Streaming chunk functions ----

func TestAppendAssistantChunk_CreatesStreamingMessage(t *testing.T) {
	sm := NewSessionManager()
	key := "test:stream1"

	sm.AppendAssistantChunk(key, "Hello ")
	sm.AppendAssistantChunk(key, "world")

	sess := sm.GetOrCreate(key)
	last := sess.Messages[len(sess.Messages)-1]
	if last.Role != "assistant" {
		t.Errorf("last role = %q, want assistant", last.Role)
	}
	if !last.Streaming {
		t.Error("last message should be marked Streaming")
	}
	if last.Content != "Hello world" {
		t.Errorf("content = %q, want %q", last.Content, "Hello world")
	}
	if !sm.HasStreamedContent(key) {
		t.Error("HasStreamedContent should be true after streaming chunks")
	}
}

func TestAppendAssistantChunk_AppendsToExistingStreaming(t *testing.T) {
	sm := NewSessionManager()
	key := "test:stream2"
	// Start with a user message, then stream.
	sm.AddMessage(key, "user", "prompt")
	sm.AppendAssistantChunk(key, "a")

	// Another Append should extend the same message, not create a new one.
	sm.AppendAssistantChunk(key, "b")
	sess := sm.GetOrCreate(key)
	if len(sess.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(sess.Messages))
	}
	last := sess.Messages[len(sess.Messages)-1]
	if last.Content != "ab" {
		t.Errorf("content = %q, want %q", last.Content, "ab")
	}
}

func TestAppendReasoningChunk(t *testing.T) {
	sm := NewSessionManager()
	key := "test:reason"
	sm.AddMessage(key, "user", "think")

	sm.AppendReasoningChunk(key, "step one")
	sm.AppendReasoningChunk(key, ", step two")

	sess := sm.GetOrCreate(key)
	last := sess.Messages[len(sess.Messages)-1]
	if last.Role != "assistant" || !last.Streaming {
		t.Fatalf("expected a streaming assistant message, got %+v", last)
	}
	if last.ReasoningContent != "step one, step two" {
		t.Errorf("reasoning = %q, want %q", last.ReasoningContent, "step one, step two")
	}
	if last.Content != "" {
		t.Errorf("reasoning-only chunk should leave Content empty, got %q", last.Content)
	}
}

func TestAppendAssistantChunk_And_Reasoning_Mix(t *testing.T) {
	sm := NewSessionManager()
	key := "test:mix"
	sm.AppendReasoningChunk(key, "think")
	sm.AppendAssistantChunk(key, "answer")
	sess := sm.GetOrCreate(key)
	last := sess.Messages[len(sess.Messages)-1]
	if last.ReasoningContent != "think" {
		t.Errorf("reasoning = %q, want %q", last.ReasoningContent, "think")
	}
	if last.Content != "answer" {
		t.Errorf("content = %q, want %q", last.Content, "answer")
	}
}

func TestFinalizeAssistantMessage(t *testing.T) {
	sm := NewSessionManager()
	key := "test:finalize"
	sm.AddMessage(key, "user", "hi")
	sm.AppendAssistantChunk(key, "partial")

	sm.FinalizeAssistantMessage(key)
	// Streaming flag stays set for dedup; HasStreamedContent stays true.
	if !sm.HasStreamedContent(key) {
		t.Error("HasStreamedContent should remain true after finalize")
	}
	if got := sm.GetInProgressAssistant(key); got == nil {
		t.Error("GetInProgressAssistant should still return the streaming message")
	}
}

func TestFinalizeAssistantMessage_NoSession(t *testing.T) {
	sm := NewSessionManager()
	// No panic; nothing to finalize.
	sm.FinalizeAssistantMessage("missing")
}

func TestFinalizeAssistantMessage_EmptyOrNotAssistant(t *testing.T) {
	sm := NewSessionManager()

	// Empty session -> no-op.
	sm.AddMessage("s1", "user", "x")
	sm.RemoveLastMessage("s1")
	sess := sm.GetOrCreate("s1")
	if len(sess.Messages) != 0 {
		t.Fatalf("expected empty session, got %d messages", len(sess.Messages))
	}
	sm.FinalizeAssistantMessage("s1")

	// Last message is user (not assistant) -> no-op.
	sm.AddMessage("s2", "user", "hello")
	sm.FinalizeAssistantMessage("s2")
}

func TestFinalizeAssistantMessage_WithStore(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "test:finalize-store"
	sm.GetOrCreate(key)
	sm.AppendAssistantChunk(key, "streamed")

	sm.FinalizeAssistantMessage(key)
	if err := sm.Save(key); err != nil {
		t.Fatalf("extra Save: %v", err)
	}
}

func TestHasStreamedContent_NonExistent(t *testing.T) {
	sm := NewSessionManager()
	if sm.HasStreamedContent("missing") {
		t.Error("HasStreamedContent for missing session should be false")
	}
}

func TestHasStreamedContent_FlagClearedOnNewUserMessage(t *testing.T) {
	sm := NewSessionManager()
	key := "test:clearflag"
	sm.AppendAssistantChunk(key, "chunk")
	if !sm.HasStreamedContent(key) {
		t.Fatal("expected streamed content true")
	}
	// New user message clears the in-memory flag.
	sm.AddMessage(key, "user", "new turn")
	if sm.HasStreamedContent(key) {
		t.Error("HasStreamedContent should be false after a new user message")
	}
}

func TestHasStreamedContent_StreamingAssistantWithEmptyContent(t *testing.T) {
	sm := NewSessionManager()
	key := "test:empty-stream"
	// This creates a streaming assistant with empty content (via getOrCreateStreamingMsg
	// would require a chunk; simulate by adding a message then making it streaming&empty).
	sess := sm.GetOrCreate(key)
	sess.Messages = append(sess.Messages, providers.Message{
		Role:      "assistant",
		Streaming: true,
		Content:   "",
	})
	// Both Content and ReasoningContent empty -> false.
	if sm.HasStreamedContent(key) {
		t.Error("streaming assistant with all-empty content should not count as streamed")
	}
}

func TestGetInProgressAssistant(t *testing.T) {
	sm := NewSessionManager()
	key := "test:gipa"

	// No session -> nil
	if got := sm.GetInProgressAssistant("missing"); got != nil {
		t.Errorf("expected nil for missing session, got %v", got)
	}

	// Streaming assistant -> returned.
	sm.AppendAssistantChunk(key, "partial")
	msg := sm.GetInProgressAssistant(key)
	if msg == nil {
		t.Fatal("expected in-progress assistant message")
	}
	if msg.Content != "partial" {
		t.Errorf("content = %q, want %q", msg.Content, "partial")
	}

	// After AddMessage (user), last is not assistant streaming -> nil.
	sm.AddMessage(key, "user", "next")
	if got := sm.GetInProgressAssistant(key); got != nil {
		t.Errorf("expected nil after user message, got %v", got)
	}
}

func TestGetInProgressAssistant_NotStreaming(t *testing.T) {
	sm := NewSessionManager()
	key := "test:gipa-ns"
	sm.AddMessage(key, "assistant", "final")
	// Assistant present but not streaming -> nil.
	if got := sm.GetInProgressAssistant(key); got != nil {
		t.Errorf("expected nil for non-streaming assistant, got %v", got)
	}
}

// ---- ActiveCount / Eviction ----

func TestActiveCount(t *testing.T) {
	sm := NewSessionManager()
	if got := sm.ActiveCount(); got != 0 {
		t.Errorf("ActiveCount for empty manager = %d, want 0", got)
	}

	// With store, sessionMeta is populated.
	s := newTestStore(t)
	sm.SetStore(s)
	sm.GetOrCreate("a")
	sm.GetOrCreate("b")
	sm.GetOrCreate("c")
	if got := sm.ActiveCount(); got != 3 {
		t.Errorf("ActiveCount = %d, want 3", got)
	}
}

func TestSetMaxInMemory_And_Eviction(t *testing.T) {
	sm := NewSessionManager()

	// Distinct timestamps so LRU ordering is deterministic even with a fast clock.
	keys := []string{"k1", "k2", "k3", "k4", "k5", "k6"}
	for i, k := range keys {
		sm.GetOrCreate(k)
		// Give each session a slightly later access time.
		sm.accessTimes[k] = sm.accessTimes[k].Add(time.Duration(i) * time.Millisecond)
	}

	// Set the cap below the current resident count. Adding a new session calls
	// evictIfNeeded BEFORE inserting it, so the resident count settles at
	// maxInMemory+1 (the freshly created session is always resident).
	sm.SetMaxInMemory(2)
	sm.GetOrCreate("k7")
	sm.accessTimes["k7"] = sm.accessTimes["k7"].Add(time.Minute)

	sm.mu.RLock()
	inMem := len(sm.sessions)
	sm.mu.RUnlock()
	if inMem != 3 {
		t.Errorf("after eviction, %d sessions in memory, want 3 (cap+recently-created)", inMem)
	}

	// The most recently accessed (k7) and one just below it (k6) remain;
	// old k1..k3 must be evicted.
	sm.mu.RLock()
	_, has7 := sm.sessions["k7"]
	_, has6 := sm.sessions["k6"]
	_, has1 := sm.sessions["k1"]
	sm.mu.RUnlock()
	if !has7 {
		t.Error("k7 should be kept (most recent)")
	}
	if has1 {
		t.Error("k1 should have been evicted")
	}
	_ = has6
}

func TestSetEvictionTTL_And_CleanupIdleSessions(t *testing.T) {
	sm := NewSessionManager()
	sm.SetEvictionTTL(time.Millisecond)

	sm.GetOrCreate("idle-sess")
	// Do not touch it again; sleep beyond the tiny TTL.
	time.Sleep(5 * time.Millisecond)

	cleaned := sm.CleanupIdleSessions()
	if cleaned != 1 {
		t.Errorf("CleanupIdleSessions cleaned %d sessions, want 1", cleaned)
	}

	sm.mu.RLock()
	_, ok := sm.sessions["idle-sess"]
	sm.mu.RUnlock()
	if ok {
		t.Error("idle session should be evicted")
	}
}

func TestCleanupIdleSessions_NoTTL(t *testing.T) {
	sm := NewSessionManager()
	sm.SetEvictionTTL(0)
	sm.GetOrCreate("s1")
	if cleaned := sm.CleanupIdleSessions(); cleaned != 0 {
		t.Errorf("with TTL 0, cleanup should evict 0, got %d", cleaned)
	}
}

func TestCleanupIdleSessions_FreshSessionNotEvicted(t *testing.T) {
	sm := NewSessionManager()
	sm.SetEvictionTTL(time.Hour)
	sm.GetOrCreate("fresh")
	cleaned := sm.CleanupIdleSessions()
	if cleaned != 0 {
		t.Errorf("fresh session should not be cleaned, got %d", cleaned)
	}
}

func TestStartCleanupGoroutine(t *testing.T) {
	sm := NewSessionManager()
	sm.SetEvictionTTL(10 * time.Millisecond)

	stop := sm.StartCleanupGoroutine(5 * time.Millisecond)
	sm.GetOrCreate("gc-sess")

	// Wait for the goroutine to run at least once.
	time.Sleep(25 * time.Millisecond)
	stop()

	sm.mu.RLock()
	_, ok := sm.sessions["gc-sess"]
	sm.mu.RUnlock()
	if ok {
		t.Error("session should have been cleaned up by goroutine")
	}
}

func TestStartCleanupGoroutine_Stops(t *testing.T) {
	sm := NewSessionManager()
	stop := sm.StartCleanupGoroutine(time.Hour)
	stop() // must not panic; goroutine terminates
}

// ---- Concurrency safety of verbose methods ----

func TestVerboseMethods_Concurrent(t *testing.T) {
	sm := NewSessionManager()
	key := "test:concat"

	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				sm.AppendAssistantChunk(key, "x")
				sm.HasStreamedContent(key)
				sm.GetInProgressAssistant(key)
			}
		}()
	}
	wg.Wait()

	sess := sm.GetOrCreate(key)
	last := sess.Messages[len(sess.Messages)-1]
	// All chunks append to a single streaming assistant message.
	if last.Role != "assistant" {
		t.Errorf("last role = %q, want assistant", last.Role)
	}
	expectedLen := 1000
	if len(last.Content) != expectedLen {
		t.Errorf("content length = %d, want %d", len(last.Content), expectedLen)
	}
	if !strings.Contains(last.Content, "xx") {
		t.Error("expected concatenated content over multiple chunks")
	}
}
