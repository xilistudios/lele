package agent

// Tests for the "## Subagent Tasks" section of the system prompt (plan T5).
//
// The section is the half of the subagent ledger that survives context
// compaction: the system prompt is rebuilt on every turn and is never persisted
// nor summarized, so a digest rendered there is the only subagent state a
// summarizer cannot erase. These tests pin the properties that make that safe:
//
//  1. a session with no tasks gets a BYTE-IDENTICAL prompt (acceptance 5), so
//     the feature is inert until a subagent is actually spawned;
//  2. the section is appended AFTER the folder section (D11), keeping the
//     static prefix of the prompt untouched (prompt-cache friendly, C12);
//  3. it is rendered fresh on every call and never cached into
//     initialContext/harnessContext, so a ledger change reaches the prompt;
//  4. the resolver is swapped concurrently with prompt builds under its own
//     mutex, like the folder resolver.

import (
	"strings"
	"sync"
	"testing"
)

// ledgerSectionTestBuilder returns a ContextBuilder over a throwaway workspace
// plus a folder the "user" can select, so the ordering test can wire both
// sections at once.
func ledgerSectionTestBuilder(t *testing.T) (cb *ContextBuilder, folder string) {
	t.Helper()
	return newFolderTestBuilder(t)
}

// TestSubagentLedgerSection_RenderedWhenResolverSet pins that a non-empty
// resolver answer reaches the prompt, separated like every other section.
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

	// A different session must not see this session's ledger.
	other := cb.BuildSystemPromptForSessionWithFolder("native:client-1:other", "native")
	if strings.Contains(other, "## Subagent Tasks") {
		t.Error("ledger digest leaked into a session without subagent tasks")
	}
}

// TestSubagentLedgerSection_NilResolverPromptByteIdentical pins acceptance 5
// for the nil case: with no resolver wired the prompt is exactly what the
// pre-existing entry point produces, so every deployment that never spawns a
// subagent sees no change at all.
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

// TestSubagentLedgerSection_EmptyDigestPromptByteIdentical pins acceptance 5
// for the wired-but-empty case: a resolver that answers "" (the session has no
// subagent tasks) must leave the prompt untouched too.
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

// TestSubagentLedgerSection_AppendedAfterFolder pins D11: the ledger section is
// the LAST thing appended, after the folder section. Appending it earlier would
// move the folder block (a per-session, user-driven section) after a section
// that changes with the ledger, and the ordering is what keeps the static
// prefix stable.
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

// TestSubagentLedgerSection_RebuiltEveryTurn pins C10a: the section is rendered
// on every call, never cached into initialContext/harnessContext. Mutating the
// resolver's backing data between two calls must change the prompt — that is
// exactly what makes the ledger survive compaction.
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

	// The task finishes: the ledger changed, so the next prompt must too.
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

// TestSubagentLedgerSection_NotInGetInitialContextCache pins C10a for the
// cached entry points: the digest must not appear in GetInitialContext nor in
// the harness context, because those are cached for the process lifetime and a
// ledger cached there would freeze on the first turn.
func TestSubagentLedgerSection_NotInGetInitialContextCache(t *testing.T) {
	cb, _ := ledgerSectionTestBuilder(t)
	const digest = "## Subagent Tasks\n\n- subagent-1 [running] coder \"Audit auth\""
	cb.SetSubagentLedgerResolver(func(string) string { return digest })

	// Render once so every cache is warm, then assert the caches are clean.
	_ = cb.BuildSystemPromptForSessionWithFolder("native:client-1:conv", "native")

	if strings.Contains(cb.GetInitialContext(), "## Subagent Tasks") {
		t.Error("ledger digest must not be cached in initialContext")
	}
	if strings.Contains(cb.getHarnessContext(), "## Subagent Tasks") {
		t.Error("ledger digest must not be cached in harnessContext")
	}
}

// TestSubagentLedgerSection_ConcurrentSetAndRead exercises the dedicated mutex:
// prompt builders read the resolver while the loop may swap it (SetSubagentLedgerResolver
// is called from the loop's attach path and from ReloadRegistry).
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
