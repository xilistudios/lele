package session

import (
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/providers"
)

// ---- generateSessionName ----

func TestGenerateSessionName_Various(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "New Chat"},
		{"   ", "New Chat"},
		{"hi", "hi"},
		{"  Hello World  ", "Hello World"},
		{"Multi\nline\r\ntext\twith\ttabs", "Multi line text with tabs"},
		{"Remove. punc, tuation!? and:;'bad`char", "Remove punc tuation andbadchar"},
		{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	}
	for _, tt := range tests {
		got := generateSessionName(tt.in)
		if got != tt.want {
			t.Errorf("generateSessionName(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if len(got) > 50 && got != tt.want {
			t.Errorf("generateSessionName(%q) exceeds 50: len=%d", tt.in, len(got))
		}
	}
}

func TestGenerateSessionName_TruncationAtWordBoundary(t *testing.T) {
	// A long content past 50 chars with a trailing word.
	long := "one two three four five six seven eight nine ten eleven twelve thirteen"
	got := generateSessionName(long)
	if len(got) > 50 {
		t.Errorf("name length = %d, want <= 50", len(got))
	}
	if len(got) == 0 {
		t.Error("empty name")
	}
}

func TestGenerateSessionName_UsedOnFirstUserMessage(t *testing.T) {
	sm := NewSessionManager()
	sm.AddMessage("k", "user", "How do I build  a rocket?")
	sess := sm.GetOrCreate("k")
	want := generateSessionName("How do I build  a rocket?")
	if sess.Name != want {
		t.Errorf("session name = %q, want %q", sess.Name, want)
	}
}

// ---- TruncateHistory ----

func TestTruncateHistory_KeepZero(t *testing.T) {
	sm := NewSessionManager()
	key := "t:h0"
	sm.AddMessage(key, "user", "a")
	sm.AddMessage(key, "assistant", "b")
	sm.TruncateHistory(key, 0)
	sess := sm.GetOrCreate(key)
	if len(sess.Messages) != 0 {
		t.Errorf("truncate 0 leaves %d messages, want 0", len(sess.Messages))
	}
}

func TestTruncateHistory_KeepLess(t *testing.T) {
	sm := NewSessionManager()
	key := "t:h1"
	for i := 0; i < 5; i++ {
		sm.AddMessage(key, "user", "u")
		sm.AddMessage(key, "assistant", "a")
	}
	sm.TruncateHistory(key, 3)
	sess := sm.GetOrCreate(key)
	if len(sess.Messages) != 3 {
		t.Errorf("truncate to 3 leaves %d messages", len(sess.Messages))
	}
}

func TestTruncateHistory_KeepMoreThanLen(t *testing.T) {
	sm := NewSessionManager()
	key := "t:hm"
	sm.AddMessage(key, "user", "only")
	sm.TruncateHistory(key, 10) // no-op
	sess := sm.GetOrCreate(key)
	if len(sess.Messages) != 1 {
		t.Errorf("no-op truncate left %d messages", len(sess.Messages))
	}
}

func TestTruncateHistory_MissingSession(t *testing.T) {
	sm := NewSessionManager()
	sm.TruncateHistory("missing", 5) // no panic
}

func TestTruncateHistory_WithStore(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "t:hstore"
	sm.AddMessage(key, "user", "1")
	sm.AddMessage(key, "user", "2")
	sm.AddMessage(key, "user", "3")
	if err := sm.Save(key); err != nil {
		t.Fatalf("save: %v", err)
	}
	sm.TruncateHistory(key, 1)
	sess := sm.GetOrCreate(key)
	if len(sess.Messages) != 1 || sess.Messages[0].Content != "3" {
		t.Errorf("truncate with store leaves %d msgs, first=%+v", len(sess.Messages), sess.Messages)
	}
	// Save the truncation.
	if err := sm.Save(key); err != nil {
		t.Fatalf("save after truncate: %v", err)
	}
}

// ---- ShouldStartFreshSession ----

func TestShouldStartFreshSession_ThresholdZero(t *testing.T) {
	sm := NewSessionManager()
	sm.GetOrCreate("k")
	fresh, idle := sm.ShouldStartFreshSession("k", 0)
	if fresh {
		t.Error("threshold 0 should never trigger fresh session")
	}
	if idle != 0 {
		t.Errorf("idle = %v, want 0", idle)
	}
}

func TestShouldStartFreshSession_EmptySession(t *testing.T) {
	sm := NewSessionManager()
	sm.GetOrCreate("empty")
	fresh, idle := sm.ShouldStartFreshSession("empty", time.Hour)
	if fresh {
		t.Error("empty session with no summary should not be fresh")
	}
	if idle != 0 {
		t.Errorf("idle = %v, want 0", idle)
	}
}

func TestShouldStartFreshSession_Missing(t *testing.T) {
	sm := NewSessionManager()
	fresh, idle := sm.ShouldStartFreshSession("missing", time.Hour)
	if fresh || idle != 0 {
		t.Errorf("missing session fresh=%v idle=%v", fresh, idle)
	}
}

func TestShouldStartFreshSession_Active(t *testing.T) {
	sm := NewSessionManager()
	key := "k:active"
	sm.AddMessage(key, "user", "hi")
	// Recent activity -> not fresh.
	fresh, idle := sm.ShouldStartFreshSession(key, time.Hour)
	if fresh {
		t.Error("recent session should not be fresh")
	}
	if idle <= 0 {
		t.Errorf("idle should be > 0, got %v", idle)
	}
}

func TestShouldStartFreshSession_Idle(t *testing.T) {
	sm := NewSessionManager()
	key := "k:idle"
	sm.AddMessage(key, "user", "hi")
	// Force old Updated time.
	sess := sm.GetOrCreate(key)
	sess.Updated = time.Now().Add(-2 * time.Hour)
	fresh, idle := sm.ShouldStartFreshSession(key, time.Hour)
	if !fresh {
		t.Error("idle session should be fresh")
	}
	if idle < 0 {
		t.Errorf("idle = %v, want positive", idle)
	}
}

func TestShouldStartFreshSession_ZeroTimestamp(t *testing.T) {
	sm := NewSessionManager()
	key := "k:zero"
	sm.AddMessage(key, "user", "hi")
	sess := sm.GetOrCreate(key)
	sess.Updated = time.Time{}
	sess.Created = time.Time{}
	fresh, idle := sm.ShouldStartFreshSession(key, time.Hour)
	if fresh {
		t.Error("zero timestamps should not be fresh")
	}
	if idle != 0 {
		t.Errorf("idle = %v, want 0", idle)
	}
}

// ---- GetSummary / SetSummary ----

func TestGetSummary_Default(t *testing.T) {
	sm := NewSessionManager()
	if got := sm.GetSummary("missing"); got != "" {
		t.Errorf("default summary = %q", got)
	}
	sm.GetOrCreate("k")
	if got := sm.GetSummary("k"); got != "" {
		t.Errorf("new session summary = %q", got)
	}
}

func TestSetGetSummary(t *testing.T) {
	sm := NewSessionManager()
	sm.GetOrCreate("k")
	sm.SetSummary("k", "my summary")
	if got := sm.GetSummary("k"); got != "my summary" {
		t.Errorf("summary = %q", got)
	}
}

func TestSetSummary_MissingSessionNoOp(t *testing.T) {
	sm := NewSessionManager()
	sm.SetSummary("nope", "x") // no panic, no-op
}

// ---- TruncateHistory with eviction ----

// ---- HasMessages / GetTotalMessageCount ----

func TestHasMessages(t *testing.T) {
	sm := NewSessionManager()
	if sm.HasMessages("") {
		t.Error("empty key should be false")
	}
	if sm.HasMessages("missing") {
		t.Error("missing session should be false")
	}
	sm.AddMessage("k", "user", "hi")
	if !sm.HasMessages("k") {
		t.Error("session with messages should be true")
	}
}

func TestHasMessages_WithStore(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "k:hasstore"
	sm.AddMessage(key, "user", "hi")
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetStore(s)
	if !sm2.HasMessages(key) {
		t.Error("cold reload should detect messages via store")
	}
}

func TestGetTotalMessageCount(t *testing.T) {
	sm := NewSessionManager()
	if got := sm.GetTotalMessageCount("missing"); got != 0 {
		t.Errorf("missing count = %d", got)
	}
	for i := 0; i < 3; i++ {
		sm.AddMessage("k", "user", "u")
	}
	if got := sm.GetTotalMessageCount("k"); got != 3 {
		t.Errorf("count = %d, want 3", got)
	}
}

func TestGetTotalMessageCount_WithStore(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "k:total"
	sm.AddMessage(key, "user", "u")
	sm.AddMessage(key, "assistant", "a")
	sm.Save(key)

	sm2 := NewSessionManager()
	sm2.SetStore(s)
	if got := sm2.GetTotalMessageCount(key); got != 2 {
		t.Errorf("cold count = %d, want 2", got)
	}
}

// ---- GetName / GetUpdated / GetCreated / SetName ----

func TestGetName_Fallback(t *testing.T) {
	sm := NewSessionManager()
	if got := sm.GetName("missing"); got != "" {
		t.Errorf("missing name = %q", got)
	}
	if err := sm.SetName("k", "  MyName  "); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if got := sm.GetName("k"); got != "MyName" {
		t.Errorf("name = %q, want MyName", got)
	}
}

func TestGetUpdatedCreated(t *testing.T) {
	sm := NewSessionManager()
	if !sm.GetUpdated("missing").IsZero() {
		t.Error("missing updated should be zero")
	}
	if !sm.GetCreated("missing").IsZero() {
		t.Error("missing created should be zero")
	}
	sm.GetOrCreate("k")
	if sm.GetUpdated("k").IsZero() || sm.GetCreated("k").IsZero() {
		t.Error("created/updated should be set")
	}
}

func TestGetName_MetadataFallback(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "k:meta-name"
	sm.GetOrCreate(key)
	sm.SetName(key, "persisted name")
	sm.Save(key)

	// Fresh manager: sessions map is empty, metadata is loaded from store.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	sm2.SetEvictionTTL(0)
	if got := sm2.GetName(key); got != "persisted name" {
		t.Errorf("metadata name = %q, want persisted name", got)
	}
	if sm2.GetUpdated(key).IsZero() {
		t.Error("updated from metadata should be set")
	}
}

// ---- AllMessageCounts / AllTotalMessageCounts ----

func TestAllMessageCounts(t *testing.T) {
	sm := NewSessionManager()
	sm.AddMessage("a", "user", "u1")
	sm.AddMessage("a", "assistant", "a1")
	sm.AddMessage("b", "user", "u2")

	counts := sm.AllMessageCounts()
	if counts["a"] != 2 {
		t.Errorf("a count = %d, want 2", counts["a"])
	}
	if counts["b"] != 1 {
		t.Errorf("b count = %d, want 1", counts["b"])
	}
	// Injected context messages are skipped: a user message with empty
	// Content but present ContentParts is not counted.
	sess := sm.GetOrCreate("c")
	sess.Messages = append(sess.Messages, providers.Message{Role: "user", Content: ""})
	sess.Messages = append(sess.Messages, providers.Message{Role: "user", Content: "real"})
	if counts := sm.AllMessageCounts(); counts["c"] != 2 {
		t.Errorf("c count = %d, want 2", counts["c"])
	}
}

func TestAllMessageCounts_WithStore(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	sm.AddMessage("m1", "user", "u")
	sm.AddMessage("m2", "user", "u")
	sm.AddMessage("m2", "assistant", "a")
	sm.Save("m1")
	sm.Save("m2")

	// Cold manager, only metadata known.
	sm2 := NewSessionManager()
	sm2.SetStore(s)
	counts := sm2.AllMessageCounts()
	_ = counts
}

// ---- Verbose Level manager ----

func TestVerbose_GetLevel_NoPreferenceFallback(t *testing.T) {
	sm := NewSessionManager()
	vm := NewVerboseManager(sm)
	// No persisted preference -> resolver fallback.
	vm.SetDefaultLevelResolver(func(k string) (VerboseLevel, bool) {
		return VerboseFull, true
	})
	if got := vm.GetLevel("x"); got != VerboseFull {
		t.Errorf("resolver fallback = %v, want full", got)
	}
}

func TestVerbose_OK_PersistErrorLogsButNoPanic(t *testing.T) {
	// Setting a level with a session manager whose persist fails should not panic.
	sm := NewSessionManager()
	vm := NewVerboseManager(sm)
	vm.SetLevel("k", VerboseBasic)
	if got := vm.GetLevel("k"); got != VerboseBasic {
		t.Errorf("level = %v, want basic", got)
	}
}

func TestVerbose_InitializeWithPreference(t *testing.T) {
	s := newTestStore(t)
	sm := NewSessionManager()
	sm.SetStore(s)
	key := "k:init"
	sm.GetOrCreate(key)
	if err := sm.SetVerboseLevel(key, "full"); err != nil {
		t.Fatalf("set level: %v", err)
	}
	vm := NewVerboseManager(sm)
	vm.InitializeFromSession(key)
	if got := vm.GetLevel(key); got != VerboseFull {
		t.Errorf("init level = %v, want full", got)
	}
}

// ---- EvictSession / SessionExists ----

func TestSessionExists_Extra(t *testing.T) {
	sm := NewSessionManager()
	if sm.SessionExists("") {
		t.Error("empty key not exist")
	}
	if sm.SessionExists("missing") {
		t.Error("missing not exist")
	}
	sm.GetOrCreate("k")
	if !sm.SessionExists("k") {
		t.Error("existing should exist")
	}
}

func TestEvictSession_Subagent(t *testing.T) {
	sm := NewSessionManager()
	key := "parent:subagent-1"
	sm.GetOrCreate(key)
	if !sm.SessionExists(key) {
		t.Fatal("session should exist")
	}
	if !sm.EvictSession(key) {
		t.Error("EvictSession should return true for existing")
	}
	// Subagent metadata removed.
	if sm.SessionExists(key) {
		t.Error("subagent should not exist after evict")
	}
}

func TestEvictSession_NonExistent(t *testing.T) {
	sm := NewSessionManager()
	if sm.EvictSession("nonexist") {
		t.Error("non-existent should return false")
	}
}

// ---- SetMode / GetMode ----

func TestSetGetMode(t *testing.T) {
	sm := NewSessionManager()
	if got := sm.GetMode("missing"); got != "" {
		t.Errorf("missing mode = %q", got)
	}
	if err := sm.SetMode("k", "chat"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if got := sm.GetMode("k"); got != "chat" {
		t.Errorf("mode = %q", got)
	}
	if err := sm.SetMode("k", "bogus"); err == nil {
		t.Error("invalid mode should error")
	}
}

// ---- Model overrides ----

func TestSetGetModel(t *testing.T) {
	sm := NewSessionManager()
	if got := sm.GetModel("missing"); got != "" {
		t.Errorf("missing model = %q", got)
	}
	if err := sm.SetModel("k", "gpt-4"); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if got := sm.GetModel("k"); got != "gpt-4" {
		t.Errorf("model = %q", got)
	}
}

// ---- Thinking level ----

func TestSetGetThinkingLevel(t *testing.T) {
	sm := NewSessionManager()
	if got := sm.GetThinkingLevel("missing"); got != "" {
		t.Errorf("missing thinking = %q", got)
	}
	if err := sm.SetThinkingLevel("k", "medium"); err != nil {
		t.Fatalf("SetThinkingLevel: %v", err)
	}
	if got := sm.GetThinkingLevel("k"); got != "medium" {
		t.Errorf("thinking = %q", got)
	}
}

// ---- Tokens ----

func TestTokenCounts(t *testing.T) {
	sm := NewSessionManager()
	in, out := sm.GetTokenCounts("missing")
	if in != 0 || out != 0 {
		t.Errorf("missing tokens = %d,%d", in, out)
	}
	sm.AddTokenCounts("k", 10, 20)
	sm.AddTokenCounts("k", 5, 5)
	in, out = sm.GetTokenCounts("k")
	if in != 15 || out != 25 {
		t.Errorf("tokens = %d,%d want 15,25", in, out)
	}
	sm.ResetTokenCounts("k")
	in, out = sm.GetTokenCounts("k")
	if in != 0 || out != 0 {
		t.Errorf("after reset tokens = %d,%d", in, out)
	}
}

func TestIncrementCompactionCount(t *testing.T) {
	sm := NewSessionManager()
	sm.IncrementCompactionCount("k")
	sm.IncrementCompactionCount("k")
	sess := sm.GetOrCreate("k")
	if sess.CompactionCount != 2 {
		t.Errorf("compaction count = %d, want 2", sess.CompactionCount)
	}
}

// ---- markModified ----

func TestMarkModified(t *testing.T) {
	s := &Session{}
	s.markModified(-1) // ignored
	if s.modifiedFrom != 0 {
		t.Errorf("negative idx modifiedFrom = %d", s.modifiedFrom)
	}
	s.markModified(2)
	if s.modifiedFrom != 3 {
		t.Errorf("modifiedFrom = %d, want 3", s.modifiedFrom)
	}
	s.markModified(0)
	if s.modifiedFrom != 1 {
		t.Errorf("modifiedFrom after lower idx = %d, want 1", s.modifiedFrom)
	}
	s.markModified(5) // higher idx keeps lowest
	if s.modifiedFrom != 1 {
		t.Errorf("modifiedFrom after higher idx = %d, want 1", s.modifiedFrom)
	}
}

// ---- seqForIndex ----

func TestSeqForIndex(t *testing.T) {
	s := &Session{}
	s.firstInMemorySeq = 5
	if got := s.seqForIndex(0); got != 5 {
		t.Errorf("seqForIndex(0) = %d, want 5", got)
	}
	if got := s.seqForIndex(2); got != 7 {
		t.Errorf("seqForIndex(2) = %d, want 7", got)
	}
}
