package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"telegram:123456", "telegram_123456"},
		{"discord:987654321", "discord_987654321"},
		{"slack:C01234", "slack_C01234"},
		{"no-colons-here", "no-colons-here"},
		{"multiple:colons:here", "multiple_colons_here"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFilename(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSave_WithColonInKey(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	// Create a session with a key containing colon (typical channel session key).
	key := "telegram:123456"
	sm.GetOrCreate(key)
	sm.AddMessage(key, "user", "hello")

	// Save should succeed even though the key contains ':'
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	// The file on disk should use sanitized name.
	expectedFile := filepath.Join(tmpDir, "telegram_123456.json")
	if _, err := os.Stat(expectedFile); os.IsNotExist(err) {
		t.Fatalf("expected session file %s to exist", expectedFile)
	}

	// Load into a fresh manager and verify the session round-trips.
	sm2 := NewSessionManager(tmpDir)
	history := sm2.GetHistory(key)
	if len(history) != 1 {
		t.Fatalf("expected 1 message after reload, got %d", len(history))
	}
	if history[0].Content != "hello" {
		t.Errorf("expected message content %q, got %q", "hello", history[0].Content)
	}
}

func TestSave_RejectsPathTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)

	badKeys := []string{"", ".", "..", "foo/bar", "foo\\bar"}
	for _, key := range badKeys {
		sm.GetOrCreate(key)
		if err := sm.Save(key); err == nil {
			t.Errorf("Save(%q) should have failed but didn't", key)
		}
	}
}

func TestShouldStartFreshSession(t *testing.T) {
	sm := NewSessionManager("")
	key := "telegram:123"
	sm.AddMessage(key, "user", "hello")
	session := sm.GetOrCreate(key)
	session.Updated = time.Now().Add(-2 * time.Minute)

	shouldReset, idle := sm.ShouldStartFreshSession(key, time.Minute)
	if !shouldReset {
		t.Fatal("expected session to require a fresh start after exceeding threshold")
	}
	if idle < time.Minute {
		t.Fatalf("idle = %v, want >= %v", idle, time.Minute)
	}
}

func TestShouldStartFreshSession_IgnoresEmptySession(t *testing.T) {
	sm := NewSessionManager("")
	key := "telegram:empty"
	session := sm.GetOrCreate(key)
	session.Updated = time.Now().Add(-2 * time.Minute)

	shouldReset, _ := sm.ShouldStartFreshSession(key, time.Minute)
	if shouldReset {
		t.Fatal("empty session should not start a fresh session")
	}
}

func TestSessionManager_AddTokenCounts(t *testing.T) {
	sm := NewSessionManager("")
	key := "telegram:123456"

	// Initially should be zero
	input, output := sm.GetTokenCounts(key)
	if input != 0 || output != 0 {
		t.Errorf("GetTokenCounts(%q) = (%d, %d), want (0, 0)", key, input, output)
	}

	// Add some tokens
	sm.AddTokenCounts(key, 100, 50)
	input, output = sm.GetTokenCounts(key)
	if input != 100 || output != 50 {
		t.Errorf("GetTokenCounts(%q) after add = (%d, %d), want (100, 50)", key, input, output)
	}

	// Add more tokens (should accumulate)
	sm.AddTokenCounts(key, 200, 75)
	input, output = sm.GetTokenCounts(key)
	if input != 300 || output != 125 {
		t.Errorf("GetTokenCounts(%q) after second add = (%d, %d), want (300, 125)", key, input, output)
	}
}

func TestSessionManager_AddTokenCounts_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)
	key := "telegram:123456"

	// Add tokens and save
	sm.AddTokenCounts(key, 150, 80)
	sm.AddMessage(key, "user", "test message")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	// Load into fresh manager
	sm2 := NewSessionManager(tmpDir)
	input, output := sm2.GetTokenCounts(key)
	if input != 150 || output != 80 {
		t.Errorf("GetTokenCounts(%q) after reload = (%d, %d), want (150, 80)", key, input, output)
	}
}

func TestSessionManager_GetTokenCounts_NonExistent(t *testing.T) {
	sm := NewSessionManager("")
	key := "non-existent-session"

	input, output := sm.GetTokenCounts(key)
	if input != 0 || output != 0 {
		t.Errorf("GetTokenCounts(%q) for non-existent session = (%d, %d), want (0, 0)", key, input, output)
	}
}

func TestSessionManager_AddTokenCounts_ZeroValues(t *testing.T) {
	sm := NewSessionManager("")
	key := "telegram:test"

	// Adding zero tokens should not change counts
	sm.AddTokenCounts(key, 0, 0)
	input, output := sm.GetTokenCounts(key)
	if input != 0 || output != 0 {
		t.Errorf("GetTokenCounts(%q) after adding zeros = (%d, %d), want (0, 0)", key, input, output)
	}
}

func TestSessionManager_TokenCounts_WithMultipleSessions(t *testing.T) {
	sm := NewSessionManager("")
	key1 := "telegram:111"
	key2 := "telegram:222"

	// Add tokens to different sessions
	sm.AddTokenCounts(key1, 100, 50)
	sm.AddTokenCounts(key2, 200, 75)

	// Verify they are tracked separately
	input1, output1 := sm.GetTokenCounts(key1)
	input2, output2 := sm.GetTokenCounts(key2)

	if input1 != 100 || output1 != 50 {
		t.Errorf("Session %q: got (%d, %d), want (100, 50)", key1, input1, output1)
	}
	if input2 != 200 || output2 != 75 {
		t.Errorf("Session %q: got (%d, %d), want (200, 75)", key2, input2, output2)
	}
}

func TestSessionManager_ResetTokenCounts(t *testing.T) {
	sm := NewSessionManager("")
	key := "telegram:123456"

	// Add some tokens
	sm.AddTokenCounts(key, 100, 50)
	input, output := sm.GetTokenCounts(key)
	if input != 100 || output != 50 {
		t.Errorf("GetTokenCounts(%q) after add = (%d, %d), want (100, 50)", key, input, output)
	}

	// Reset tokens
	sm.ResetTokenCounts(key)
	input, output = sm.GetTokenCounts(key)
	if input != 0 || output != 0 {
		t.Errorf("GetTokenCounts(%q) after reset = (%d, %d), want (0, 0)", key, input, output)
	}
}

func TestSessionManager_ResetTokenCounts_NonExistent(t *testing.T) {
	sm := NewSessionManager("")
	key := "non-existent-session"

	// Reset on non-existent session should not panic or create session
	sm.ResetTokenCounts(key)
	input, output := sm.GetTokenCounts(key)
	if input != 0 || output != 0 {
		t.Errorf("GetTokenCounts(%q) for non-existent session after reset = (%d, %d), want (0, 0)", key, input, output)
	}
}

func TestSessionManager_ResetTokenCounts_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)
	key := "telegram:123456"

	// Add tokens and save
	sm.AddTokenCounts(key, 150, 80)
	sm.AddMessage(key, "user", "test message")
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) failed: %v", key, err)
	}

	// Reset tokens and save again
	sm.ResetTokenCounts(key)
	if err := sm.Save(key); err != nil {
		t.Fatalf("Save(%q) after reset failed: %v", key, err)
	}

	// Load into fresh manager
	sm2 := NewSessionManager(tmpDir)
	input, output := sm2.GetTokenCounts(key)
	if input != 0 || output != 0 {
		t.Errorf("GetTokenCounts(%q) after reload = (%d, %d), want (0, 0)", key, input, output)
	}
}

func TestSessionManager_ResetTokenCounts_OnlyTargetSession(t *testing.T) {
	sm := NewSessionManager("")
	key1 := "telegram:111"
	key2 := "telegram:222"

	// Add tokens to both sessions
	sm.AddTokenCounts(key1, 100, 50)
	sm.AddTokenCounts(key2, 200, 75)

	// Reset only first session
	sm.ResetTokenCounts(key1)

	// Verify first session is reset
	input1, output1 := sm.GetTokenCounts(key1)
	if input1 != 0 || output1 != 0 {
		t.Errorf("Session %q after reset: got (%d, %d), want (0, 0)", key1, input1, output1)
	}

	// Verify second session is unchanged
	input2, output2 := sm.GetTokenCounts(key2)
	if input2 != 200 || output2 != 75 {
		t.Errorf("Session %q should be unchanged: got (%d, %d), want (200, 75)", key2, input2, output2)
	}
}

func TestSessionManager_ResetTokenCounts_UpdatesTimestamp(t *testing.T) {
	sm := NewSessionManager("")
	key := "telegram:123456"

	// Create session and add tokens
	session := sm.GetOrCreate(key)
	oldUpdated := session.Updated

	// Wait a bit to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Reset tokens
	sm.ResetTokenCounts(key)

	// Verify Updated timestamp changed
	session = sm.GetOrCreate(key)
	if !session.Updated.After(oldUpdated) {
		t.Errorf("Updated timestamp should be after original: got %v, want > %v", session.Updated, oldUpdated)
	}
}

func TestFindSubagentSessions_Empty(t *testing.T) {
	sm := NewSessionManager("")
	key := "native:client-1"

	// No sessions at all
	results := sm.FindSubagentSessions(key)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestFindSubagentSessions_FindsMatchingSessions(t *testing.T) {
	sm := NewSessionManager("")
	parent := "native:client-1"

	// Create subagent sessions
	sm.AddMessage(parent+":subagent-1", "user", "do something")
	sm.AddMessage(parent+":subagent-1", "assistant", "done")
	sm.AddMessage(parent+":subagent-2", "user", "analyze this")
	sm.AddMessage(parent+":subagent-2", "assistant", "analyzed")
	sm.AddMessage(parent+":subagent-2", "assistant", "here are results")

	// Create a non-subagent session (should be ignored)
	sm.AddMessage(parent, "user", "hello")
	sm.AddMessage("telegram:999:subagent-1", "user", "different parent")

	results := sm.FindSubagentSessions(parent)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Build a map for easier assertions
	byTaskID := make(map[string]SubagentSessionInfo)
	for _, r := range results {
		byTaskID[r.TaskID] = r
	}

	sa1, ok := byTaskID["subagent-1"]
	if !ok {
		t.Fatal("expected to find subagent-1")
	}
	if sa1.Key != parent+":subagent-1" {
		t.Errorf("key = %q, want %q", sa1.Key, parent+":subagent-1")
	}
	if sa1.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", sa1.Iterations)
	}

	sa2, ok := byTaskID["subagent-2"]
	if !ok {
		t.Fatal("expected to find subagent-2")
	}
	if sa2.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", sa2.Iterations)
	}
}

func TestFindSubagentSessions_SkipsParentAndOtherSessions(t *testing.T) {
	sm := NewSessionManager("")
	parent := "native:client-1"

	sm.AddMessage(parent, "user", "hello")
	sm.AddMessage("telegram:123", "user", "other session")
	sm.AddMessage("other:session:subagent-1", "user", "different parent prefix")

	results := sm.FindSubagentSessions(parent)
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d", len(results))
	}
}

func TestFindSubagentSessions_SummaryFromSession(t *testing.T) {
	sm := NewSessionManager("")
	parent := "native:client-1"

	key := parent + ":subagent-1"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "result")
	sm.SetSummary(key, "Found 3 files")

	results := sm.FindSubagentSessions(parent)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Summary != "Found 3 files" {
		t.Errorf("summary = %q, want %q", results[0].Summary, "Found 3 files")
	}
}

func TestFindSubagentSessions_SummaryFallbackFromLastAssistant(t *testing.T) {
	sm := NewSessionManager("")
	parent := "native:client-1"

	key := parent + ":subagent-1"
	sm.AddMessage(key, "user", "task")
	sm.AddMessage(key, "assistant", "Here is my analysis of the codebase")

	results := sm.FindSubagentSessions(parent)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Summary != "Here is my analysis of the codebase" {
		t.Errorf("summary = %q, want %q", results[0].Summary, "Here is my analysis of the codebase")
	}
}

func TestFindSubagentSessions_SummaryFallbackTruncatesLong(t *testing.T) {
	sm := NewSessionManager("")
	parent := "native:client-1"

	key := parent + ":subagent-1"
	sm.AddMessage(key, "user", "task")
	longContent := ""
	for i := 0; i < 300; i++ {
		longContent += "x"
	}
	sm.AddMessage(key, "assistant", longContent)

	results := sm.FindSubagentSessions(parent)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Summary) > 210 { // 200 + "…" rune
		t.Errorf("summary too long: %d chars", len(results[0].Summary))
	}
}

func TestFindSubagentSessions_Persistence(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewSessionManager(tmpDir)
	parent := "native:client-1"

	// Create and save subagent sessions
	sm.AddMessage(parent+":subagent-1", "user", "task 1")
	sm.AddMessage(parent+":subagent-1", "assistant", "done 1")
	sm.SetSummary(parent+":subagent-1", "Completed task 1")
	sm.Save(parent + ":subagent-1")

	sm.AddMessage(parent+":subagent-2", "user", "task 2")
	sm.AddMessage(parent+":subagent-2", "assistant", "done 2")
	sm.Save(parent + ":subagent-2")

	// Load into a fresh manager — should discover the past subagents
	sm2 := NewSessionManager(tmpDir)
	results := sm2.FindSubagentSessions(parent)
	if len(results) != 2 {
		t.Fatalf("expected 2 results after reload, got %d", len(results))
	}

	byTaskID := make(map[string]SubagentSessionInfo)
	for _, r := range results {
		byTaskID[r.TaskID] = r
	}

	if _, ok := byTaskID["subagent-1"]; !ok {
		t.Error("expected to find subagent-1 after reload")
	}
	if _, ok := byTaskID["subagent-2"]; !ok {
		t.Error("expected to find subagent-2 after reload")
	}

	// Verify summary survived persistence
	if byTaskID["subagent-1"].Summary != "Completed task 1" {
		t.Errorf("summary = %q, want %q", byTaskID["subagent-1"].Summary, "Completed task 1")
	}
}
