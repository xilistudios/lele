package agent

import (
	"strings"
	"sync"
	"testing"
)

func ledgerSectionTestBuilder(t *testing.T) (cb *ContextBuilder, folder string) {
	t.Helper()
	return newFolderTestBuilder(t)
}

func TestSubagentLedgerSection_RenderedWhenResolverSet(t *testing.T) {
	cb, _ := ledgerSectionTestBuilder(t)
	const sessionKey = "native:client-1:conv-1"
	const digest = "## Subagent Tasks\n\n- subagent-1 [running] coder \"Audit auth\""

	cb.SetSubagentLedgerResolver(func(sk string) string {
		if sk == sessionKey {
			return digest
		}
		return ""
	})

	prompt := cb.BuildSystemPromptForSessionWithFolder(sessionKey, "native")
	if !strings.Contains(prompt, digest) {
		t.Fatalf("prompt is missing the ledger digest:\n%s", prompt)
	}
	if !strings.Contains(prompt, "\n\n---\n\n## Subagent Tasks") {
		t.Error("ledger section must be separated from the prompt by the section separator")
	}

	other := cb.BuildSystemPromptForSessionWithFolder("native:client-1:other", "native")
	if strings.Contains(other, "## Subagent Tasks") {
		t.Error("ledger digest leaked into a session without subagent tasks")
	}
}

func TestSubagentLedgerSection_NilResolverPromptByteIdentical(t *testing.T) {
	cb, _ := ledgerSectionTestBuilder(t)

	for _, tc := range []struct{ sessionKey, channel string }{
		{"native:client-1:conv-1", "native"},
		{"telegram:42", "telegram"},
		{"tui:local", "native"},
	} {
		want := cb.BuildSystemPromptForSession(tc.sessionKey, tc.channel)
		got := cb.BuildSystemPromptForSessionWithFolder(tc.sessionKey, tc.channel)
		if got != want {
			t.Errorf("session %q: prompt changed with no ledger resolver wired\n got: %q\nwant: %q",
				tc.sessionKey, got, want)
		}
	}
}

func TestSubagentLedgerSection_EmptyDigestPromptByteIdentical(t *testing.T) {
	cb, _ := ledgerSectionTestBuilder(t)
	cb.SetSubagentLedgerResolver(func(string) string { return "" })

	for _, tc := range []struct{ sessionKey, channel string }{
		{"native:client-1:conv-1", "native"},
		{"telegram:42", "telegram"},
	} {
		want := cb.BuildSystemPromptForSession(tc.sessionKey, tc.channel)
		got := cb.BuildSystemPromptForSessionWithFolder(tc.sessionKey, tc.channel)
		if got != want {
			t.Errorf("session %q: empty digest changed the prompt\n got: %q\nwant: %q",
				tc.sessionKey, got, want)
		}
	}
}

func TestSubagentLedgerSection_AppendedAfterFolder(t *testing.T) {
	cb, folder := ledgerSectionTestBuilder(t)
	const sessionKey = "native:client-1:conv-1"
	const digest = "## Subagent Tasks\n\n- subagent-1 [running] coder \"Audit auth\""

	cb.SetFolderResolver(func(string) string { return folder })
	cb.SetSubagentLedgerResolver(func(string) string { return digest })

	prompt := cb.BuildSystemPromptForSessionWithFolder(sessionKey, "native")

	folderIdx := strings.Index(prompt, "## Selected Folder")
	ledgerIdx := strings.Index(prompt, "## Subagent Tasks")
	if folderIdx < 0 || ledgerIdx < 0 {
		t.Fatalf("both sections must be present (folder=%d ledger=%d):\n%s", folderIdx, ledgerIdx, prompt)
	}
	if ledgerIdx < folderIdx {
		t.Errorf("ledger section must come after the folder section (folder=%d ledger=%d)", folderIdx, ledgerIdx)
	}
	if !strings.HasSuffix(prompt, digest) {
		t.Error("ledger section must be the last thing in the prompt")
	}
}

func TestSubagentLedgerSection_RebuiltEveryTurn(t *testing.T) {
	cb, _ := ledgerSectionTestBuilder(t)
	const sessionKey = "native:client-1:conv-1"

	var mu sync.Mutex
	digest := "## Subagent Tasks\n\n- subagent-1 [running] coder \"Audit auth\""
	cb.SetSubagentLedgerResolver(func(string) string {
		mu.Lock()
		defer mu.Unlock()
		return digest
	})

	first := cb.BuildSystemPromptForSessionWithFolder(sessionKey, "native")
	if !strings.Contains(first, "[running]") {
		t.Fatalf("first render missing the running row:\n%s", first)
	}

	mu.Lock()
	digest = "## Subagent Tasks\n\n- subagent-1 [completed] coder \"Audit auth\": 3 gaps found"
	mu.Unlock()

	second := cb.BuildSystemPromptForSessionWithFolder(sessionKey, "native")
	if !strings.Contains(second, "[completed]") {
		t.Fatalf("second render did not pick up the ledger change:\n%s", second)
	}
	if strings.Contains(second, "[running]") {
		t.Errorf("second render still shows the stale status:\n%s", second)
	}
}

func TestSubagentLedgerSection_NotInGetInitialContextCache(t *testing.T) {
	cb, _ := ledgerSectionTestBuilder(t)
	const digest = "## Subagent Tasks\n\n- subagent-1 [running] coder \"Audit auth\""
	cb.SetSubagentLedgerResolver(func(string) string { return digest })

	_ = cb.BuildSystemPromptForSessionWithFolder("native:client-1:conv", "native")

	if strings.Contains(cb.GetInitialContext(), "## Subagent Tasks") {
		t.Error("ledger digest must not be cached in initialContext")
	}
	if strings.Contains(cb.getHarnessContext(), "## Subagent Tasks") {
		t.Error("ledger digest must not be cached in harnessContext")
	}
}

func TestSubagentLedgerSection_ConcurrentSetAndRead(t *testing.T) {
	cb, _ := ledgerSectionTestBuilder(t)
	const digest = "## Subagent Tasks\n\n- subagent-1 [running] coder \"Audit auth\""

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = cb.BuildSystemPromptForSessionWithFolder("native:client-1:conv", "native")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			cb.SetSubagentLedgerResolver(func(string) string { return digest })
			cb.SetSubagentLedgerResolver(nil)
		}
	}()
	wg.Wait()
}
