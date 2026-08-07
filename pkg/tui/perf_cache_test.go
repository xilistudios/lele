package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/providers"
)

// TestGetHistoryMessageCount_Cached verifies that getHistoryMessageCount
// caches its O(n) role scan and only recomputes when the history length
// changes. This is a hot path: it is called multiple times per frame.
func TestGetHistoryMessageCount_Cached(t *testing.T) {
	m := newTestModel(t)

	key := "tui:chat:count-cache-test"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddMessage(key, "user", "hello")
	m.sessionMgr.AddMessage(key, "assistant", "hi there")
	m.sessionMgr.AddMessage(key, "tool", "tool output") // not counted
	m.currentKey = key

	if got := m.getHistoryMessageCount(); got != 2 {
		t.Fatalf("getHistoryMessageCount() = %d, want 2", got)
	}
	if m.historyCountKey != key || m.historyCountLen != 3 || m.historyCountValue != 2 {
		t.Fatalf("cache fields not populated: key=%q len=%d value=%d",
			m.historyCountKey, m.historyCountLen, m.historyCountValue)
	}

	// Poison the cached value: if the cache is hit, the poisoned value is
	// returned; if the scan ran, it would return 2.
	m.historyCountValue = 999
	if got := m.getHistoryMessageCount(); got != 999 {
		t.Fatalf("expected cached (poisoned) value 999, got %d — cache miss", got)
	}

	// Adding a message changes len(history) and must invalidate the cache.
	m.sessionMgr.AddMessage(key, "user", "second question")
	if got := m.getHistoryMessageCount(); got != 3 {
		t.Fatalf("after adding message: got %d, want 3", got)
	}
	if m.historyCountLen != 4 || m.historyCountValue != 3 {
		t.Fatalf("cache not refreshed: len=%d value=%d", m.historyCountLen, m.historyCountValue)
	}

	// Switching sessions must invalidate the cache.
	otherKey := "tui:chat:count-cache-other"
	m.sessionMgr.GetOrCreate(otherKey)
	m.sessionMgr.AddMessage(otherKey, "user", "only message")
	m.currentKey = otherKey
	if got := m.getHistoryMessageCount(); got != 1 {
		t.Fatalf("after session switch: got %d, want 1", got)
	}
}

// TestGetSessionSubagentsCached verifies the TTL cache around the expensive
// GetSessionSubagents backend call, including explicit invalidation.
func TestGetSessionSubagentsCached(t *testing.T) {
	m := newTestModel(t)

	queryKey := "native:tui:chat:subagent-cache-test"

	// Seed the cache with a sentinel value. If the cache is hit, the sentinel
	// is returned; a backend call would return an empty list instead.
	sentinel := []channels.SubagentTaskInfo{{TaskID: "subagent-1", Status: "running"}}
	m.subagentsCacheKey = queryKey
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = sentinel

	got := m.getSessionSubagentsCached(queryKey)
	if len(got) != 1 || got[0].TaskID != "subagent-1" {
		t.Fatalf("expected cached sentinel, got %v — cache miss", got)
	}

	// Expired TTL must refresh from the backend (empty list here).
	m.subagentsCacheTime = time.Now().Add(-2 * subagentsCacheTTL)
	got = m.getSessionSubagentsCached(queryKey)
	if len(got) != 0 {
		t.Fatalf("expected refreshed (empty) result after TTL expiry, got %v", got)
	}
	if m.subagentsCacheKey != queryKey {
		t.Fatalf("cache key not refreshed: %q", m.subagentsCacheKey)
	}

	// A different query key must bypass the cache.
	m.subagentsCacheKey = queryKey
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = sentinel
	got = m.getSessionSubagentsCached("native:tui:chat:other")
	if len(got) != 0 {
		t.Fatalf("expected backend result for different key, got cached %v", got)
	}

	// invalidateSubagentsCache must force a backend hit.
	m.subagentsCacheKey = queryKey
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = sentinel
	m.invalidateSubagentsCache()
	if m.subagentsCacheKey != "" {
		t.Fatal("invalidateSubagentsCache did not clear the cache key")
	}
	got = m.getSessionSubagentsCached(queryKey)
	if len(got) != 0 {
		t.Fatalf("expected backend result after invalidation, got cached %v", got)
	}

	// Nil/empty guards.
	if got := m.getSessionSubagentsCached(""); got != nil {
		t.Fatalf("empty query key should return nil, got %v", got)
	}
}

// TestHasRunningSubagents_UsesCache verifies that hasRunningSubagents goes
// through the cached lookup instead of hitting the backend every frame.
func TestHasRunningSubagents_UsesCache(t *testing.T) {
	m := newTestModel(t)
	m.currentKey = "tui:chat:running-subagents-test"

	queryKey := "native:" + m.currentKey

	// Seed cache with a running subagent — hasRunningSubagents must see it
	// without a backend call (the backend has no subagents in this test).
	m.subagentsCacheKey = queryKey
	m.subagentsCacheTime = time.Now()
	m.subagentsCacheValue = []channels.SubagentTaskInfo{{TaskID: "subagent-1", Status: "running"}}

	if !m.hasRunningSubagents() {
		t.Fatal("hasRunningSubagents() = false, want true (cached running subagent)")
	}

	// Completed-only subagents must report false.
	m.subagentsCacheValue = []channels.SubagentTaskInfo{{TaskID: "subagent-1", Status: "completed"}}
	if m.hasRunningSubagents() {
		t.Fatal("hasRunningSubagents() = true, want false (only completed subagents)")
	}
}

// TestBouncingDots_NoPerFrameStyleAllocation verifies the bouncing dots
// animation renders the pre-styled dot character (package-level style) and
// produces a stable-width animation string.
func TestBouncingDots_NoPerFrameStyleAllocation(t *testing.T) {
	m := newTestModel(t)

	first := m.getBouncingDots()
	if first == "" {
		t.Fatal("getBouncingDots() returned empty string")
	}

	// Advance the animation and verify the output stays the same length
	// (the dots bounce within a fixed-width track).
	for i := 0; i < 20; i++ {
		m.animationTick++
		got := m.getBouncingDots()
		if len([]rune(got)) != len([]rune(first)) {
			t.Fatalf("animation width changed at tick %d: %q vs %q", m.animationTick, got, first)
		}
	}

	// The animation must use the pre-rendered dot character (rendered once at
	// package init, not per frame). Note: in test environments lipgloss may
	// strip ANSI styling, so we compare against the pre-rendered value rather
	// than asserting ANSI codes are present.
	if !strings.Contains(first, bouncingDotChar) {
		t.Fatal("getBouncingDots output does not use the pre-rendered bouncingDotChar")
	}
}

// TestMessageFingerprint_Stable verifies that the same message always
// produces the same fingerprint, and that different messages produce
// different fingerprints.
func TestMessageFingerprint_Stable(t *testing.T) {
	msg1 := providers.Message{Role: "user", Content: "hello world"}
	msg2 := providers.Message{Role: "user", Content: "hello world"}
	msg3 := providers.Message{Role: "user", Content: "different content"}

	fp1 := messageFingerprint(msg1, 80)
	fp2 := messageFingerprint(msg2, 80)
	fp3 := messageFingerprint(msg3, 80)

	if fp1 != fp2 {
		t.Fatalf("same message produced different fingerprints: %q vs %q", fp1, fp2)
	}
	if fp1 == fp3 {
		t.Fatalf("different messages produced same fingerprint: %q", fp1)
	}
}

// TestMessageFingerprint_WidthSensitive verifies that the same message
// with different render widths produces different fingerprints.
func TestMessageFingerprint_WidthSensitive(t *testing.T) {
	msg := providers.Message{Role: "assistant", Content: "some response"}

	fp80 := messageFingerprint(msg, 80)
	fp120 := messageFingerprint(msg, 120)

	if fp80 == fp120 {
		t.Fatalf("different widths produced same fingerprint: %q", fp80)
	}
}

// TestMessageFingerprint_RoleSensitive verifies that messages with different
// roles produce different fingerprints.
func TestMessageFingerprint_RoleSensitive(t *testing.T) {
	user := providers.Message{Role: "user", Content: "same content"}
	assistant := providers.Message{Role: "assistant", Content: "same content"}

	fpUser := messageFingerprint(user, 80)
	fpAssistant := messageFingerprint(assistant, 80)

	if fpUser == fpAssistant {
		t.Fatalf("different roles produced same fingerprint: %q", fpUser)
	}
}

// TestMessageFingerprint_ToolCallsSensitive verifies that tool calls are
// included in the fingerprint.
func TestMessageFingerprint_ToolCallsSensitive(t *testing.T) {
	msg1 := providers.Message{
		Role:    "assistant",
		Content: "I'll run that command",
		ToolCalls: []providers.ToolCall{
			{Name: "exec", Function: &providers.FunctionCall{Name: "exec", Arguments: `{"command":"ls"}`}},
		},
	}
	msg2 := providers.Message{
		Role:    "assistant",
		Content: "I'll run that command",
		ToolCalls: []providers.ToolCall{
			{Name: "exec", Function: &providers.FunctionCall{Name: "exec", Arguments: `{"command":"pwd"}`}},
		},
	}

	fp1 := messageFingerprint(msg1, 80)
	fp2 := messageFingerprint(msg2, 80)

	if fp1 == fp2 {
		t.Fatalf("different tool calls produced same fingerprint: %q", fp1)
	}
}

// TestCountHistoryMessages verifies the pure-function message counter.
func TestCountHistoryMessages(t *testing.T) {
	history := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "tool", Content: "tool output"},
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer"},
	}

	if got := countHistoryMessages(history); got != 4 {
		t.Fatalf("countHistoryMessages() = %d, want 4", got)
	}

	// Empty history
	if got := countHistoryMessages(nil); got != 0 {
		t.Fatalf("countHistoryMessages(nil) = %d, want 0", got)
	}
}

// TestLastHistoryRoleFromHistory verifies the pure-function role lookup.
func TestLastHistoryRoleFromHistory(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "system", Content: "compaction summary"},
	}

	if got := lastHistoryRoleFromHistory(history); got != "assistant" {
		t.Fatalf("lastHistoryRoleFromHistory() = %q, want %q", got, "assistant")
	}

	// System-only history
	sysOnly := []providers.Message{{Role: "system", Content: "prompt"}}
	if got := lastHistoryRoleFromHistory(sysOnly); got != "" {
		t.Fatalf("system-only: got %q, want empty", got)
	}
}
