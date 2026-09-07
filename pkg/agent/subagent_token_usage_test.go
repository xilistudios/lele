// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

// ============================================================================
// Subagent token accounting, agent side.
//
// newSubagentTokenReporter is the callback wired into every agent's
// SubagentManager (tool_coordinator.go): the runner bills it with the owner
// session key of a finished/running subagent. The reporter must (1) add the
// delta to the parent's cumulative counters and (2) persist them immediately,
// because an async subagent usually completes after the parent's turn-end Save
// and its spend must survive even if no further turn ever happens.
// ============================================================================

func newTokenTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewSubagentTokenReporter_BillsAndPersistsParentSession(t *testing.T) {
	s := newTokenTestStore(t)
	sessions := session.NewSessionManager()
	sessions.SetStore(s)

	report := newSubagentTokenReporter(sessions)
	report("agent:main:telegram:42", 100, 25)
	report("agent:main:telegram:42", 50, 10)

	in, out := sessions.GetTokenCounts("agent:main:telegram:42")
	if in != 150 || out != 35 {
		t.Fatalf("in-memory counts = (%d, %d), want (150, 35)", in, out)
	}

	// The reporter Saves after each delta, so a fresh manager reading only
	// from SQLite sees the full cumulative total: the subagent's spend
	// survives the process, not just the current turn.
	reopened := session.NewSessionManager()
	reopened.SetStore(s)
	in, out = reopened.GetTokenCounts("agent:main:telegram:42")
	if in != 150 || out != 35 {
		t.Errorf("persisted counts = (%d, %d), want (150, 35)", in, out)
	}
}

func TestNewSubagentTokenReporter_IgnoresEmptyKeyAndNilSessions(t *testing.T) {
	s := newTokenTestStore(t)
	sessions := session.NewSessionManager()
	sessions.SetStore(s)

	// Empty key: nothing billed, no phantom session created.
	newSubagentTokenReporter(sessions)("", 10, 10)
	if in, out := sessions.GetTokenCounts(""); in != 0 || out != 0 {
		t.Errorf("empty key billed (%d, %d), want (0, 0)", in, out)
	}

	// Nil sessions must not panic (standalone/unwired managers).
	newSubagentTokenReporter(nil)("k", 1, 1)
}
