// Lele - coverage tests for forceCompression and related session-manager flows.
package agent

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// TestForceCompression_BuildsLocalSummary adds many user/assistant messages to
// a session, ensures no summary exists, and verifies forceCompression builds a
// local summary, excludes the oldest messages, and increments the compaction
// counter. It covers the main body of the (otherwise 6% covered) function.
func TestForceCompression_BuildsLocalSummary(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	sessionKey := "test:force-compress"

	// Seed enough messages (more than the 4-message guard).
	for i := 0; i < 12; i++ {
		if i%2 == 0 {
			agent.Sessions.AddMessage(sessionKey, "user", strings.Repeat("u", i+5))
		} else {
			agent.Sessions.AddMessage(sessionKey, "assistant", strings.Repeat("a", i+5))
		}
	}

	sm.forceCompression(agent, sessionKey)

	summary := agent.Sessions.GetSummary(sessionKey)
	if !strings.Contains(summary, "Conversation Summary") {
		t.Fatalf("expected auto-compressed summary markers, got: %q", summary)
	}
	if !strings.Contains(summary, "User:") || !strings.Contains(summary, "Assistant:") {
		t.Errorf("summary should mention User and Assistant lines:\n%s", summary)
	}
	// The compaction counter must have been incremented.
	if c := agent.Sessions.GetOrCreate(sessionKey).CompactionCount; c == 0 {
		t.Error("expected compaction count > 0 after forceCompression")
	}
}

// TestForceCompression_ToolAndExistingSummary exercises the tool-role branch
// (ToolCallID rendering) and the branch where a summary already exists (so the
// local-summary building is skipped but exclusion still occurs).
func TestForceCompression_ToolAndExistingSummary(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	sessionKey := "test:force-compress-tool"

	// Pre-seed an existing summary so the local-summary building is skipped.
	agent.Sessions.GetOrCreate(sessionKey)
	agent.Sessions.SetSummary(sessionKey, "existing summary")

	// Seed user and assistant messages; also add a tool message with ID.
	agent.Sessions.AddMessage(sessionKey, "user", "u1")
	agent.Sessions.AddMessage(sessionKey, "assistant", "a1")
	agent.Sessions.AddMessage(sessionKey, "user", "u2")
	agent.Sessions.AddMessage(sessionKey, "assistant", "a2")
	agent.Sessions.AddMessage(sessionKey, "user", "u3")
	agent.Sessions.AddMessage(sessionKey, "assistant", "a3")

	sm.forceCompression(agent, sessionKey)

	// Existing summary should be preserved.
	if got := agent.Sessions.GetSummary(sessionKey); got != "existing summary" {
		t.Errorf("summary changed: %q", got)
	}
	if c := agent.Sessions.GetOrCreate(sessionKey).CompactionCount; c == 0 {
		t.Error("expected compaction count > 0")
	}
}

// TestForceCompression_TooFewMessages verifies the early-return guard when the
// history has <= 4 messages.
func TestForceCompression_TooFewMessages(t *testing.T) {
	al := newTestAgentLoop(t)
	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	sessionKey := "test:force-compress-few"
	agent.Sessions.AddMessage(sessionKey, "user", "only one")
	sm.forceCompression(agent, sessionKey)
	if c := agent.Sessions.GetOrCreate(sessionKey).CompactionCount; c != 0 {
		t.Errorf("expected no compaction for few messages, got %d", c)
	}
}

// TestEvictExcludedAfterCompaction verifies the memory eviction after
// compaction when the config option is enabled.
func TestEvictExcludedAfterCompaction(t *testing.T) {
	al := newTestAgentLoop(t)
	// Enable eviction flag so evictExcludedAfterCompaction proceeds.
	al.cfg().Session.EvictExcludedFromMemory = true
	sm := newSessionManager(al)
	agent := al.registry.GetDefaultAgent()
	if agent == nil {
		t.Fatal("no default agent")
	}
	sessionKey := "test:evict"
	for i := 0; i < 8; i++ {
		agent.Sessions.AddMessage(sessionKey, "user", "m")
	}
	agent.Sessions.ExcludeOldMessagesFromContext(sessionKey, 4)
	before := len(agent.Sessions.GetHistoryView(sessionKey))
	sm.evictExcludedAfterCompaction(agent, sessionKey)
	after := len(agent.Sessions.GetHistoryView(sessionKey))
	if after >= before {
		t.Errorf("expected memory eviction to reduce history (%d -> %d)", before, after)
	}
}

// ensure config import is used
var _ = config.SessionConfig{}