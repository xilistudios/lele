package agent

import (
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/session"
	"github.com/xilistudios/lele/pkg/store"
)

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
	sessions.SetSessionRepo(s.Sessions())

	report := newSubagentTokenReporter(sessions)
	report("agent:main:telegram:42", 100, 25)
	report("agent:main:telegram:42", 50, 10)

	in, out := sessions.GetTokenCounts("agent:main:telegram:42")
	if in != 150 || out != 35 {
		t.Fatalf("in-memory counts = (%d, %d), want (150, 35)", in, out)
	}

	reopened := session.NewSessionManager()
	reopened.SetSessionRepo(s.Sessions())
	in, out = reopened.GetTokenCounts("agent:main:telegram:42")
	if in != 150 || out != 35 {
		t.Errorf("persisted counts = (%d, %d), want (150, 35)", in, out)
	}
}

func TestNewSubagentTokenReporter_IgnoresEmptyKeyAndNilSessions(t *testing.T) {
	s := newTokenTestStore(t)
	sessions := session.NewSessionManager()
	sessions.SetSessionRepo(s.Sessions())

	newSubagentTokenReporter(sessions)("", 10, 10)
	if in, out := sessions.GetTokenCounts(""); in != 0 || out != 0 {
		t.Errorf("empty key billed (%d, %d), want (0, 0)", in, out)
	}

	newSubagentTokenReporter(nil)("k", 1, 1)
}
