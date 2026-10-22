package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// ── helpers shared with mcp_test.go ────────────────────────────────────
//
// mcpWriteDoc, mcpKeyRunes and mcpFixturePaths are reused from mcp_test.go
// (same package) — this file only adds what it needs on top.

// mcpAddPaths resolves the three mcp.json roots the running test loop
// reports (the same probe mcpAuthListModel makes), so every caller knows
// the fixture is exercising a loop that can serve a real Update tail. The
// three returned roots are TEMP dirs, disjoint from each other and from the
// loop's real config dir: plant the fixture files under them. Paths
// resolution in Update (m.mcpPaths()) always uses the loop's own roots, so
// Update-driven tests must plant the fixture under config.GetLeleDir()
// (see mcpAddOpenList).
func mcpAddPaths(t *testing.T) mcp.Paths {
	t.Helper()
	m := newTestModel(t)
	if _, ok := m.mcpPaths(); !ok {
		t.Fatal("m.mcpPaths() = false, the fixture must resolve the MCP roots")
	}
	root := t.TempDir()
	return mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
}

// mcpAddEntry returns a minimal valid stdio entry for the tests.
func mcpAddEntry() mcp.ServerConfig {
	return mcp.ServerConfig{Command: "npx", Args: []string{"-y", "pkg"}}
}

// mcpAddOpenList opens the real /mcp list on a loop-backed Model whose
// global layer is the temp LELE_CONFIG_DIR (mcpAuthListModel's pattern): the
// ONLY layout Update-driven tests can use, because m.mcpPaths() always
// resolves the loop's own roots. The other two roots are whatever the loop
// reports (empty/disjoint in the test fixture).
func mcpAddOpenList(t *testing.T) *Model {
	t.Helper()
	m := newTestModel(t)
	m.width, m.height = 140, 40
	m.executeCommand("/mcp") // resetModal(ModalMCP) + loadMCPList
	if m.modalMode != ModalMCP {
		t.Fatalf("mode = %v, want ModalMCP", m.modalMode)
	}
	return m
}

// mcpAddSwapSyncCounter swaps the syncMCPServersFor package var for a
// counting stub and restores it on cleanup (the seam is a var precisely so
// tests can count calls — see the doc at pkg/tui/mcp.go syncMCPServersFor).
func mcpAddSwapSyncCounter(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := syncMCPServersFor
	syncMCPServersFor = func(al *agent.AgentLoop) { calls++ }
	t.Cleanup(func() { syncMCPServersFor = orig })
	return &calls
}

// ── 1. routing: Update dispatches to the handler, never to the input ──

// TestMCPAddResult_RoutedByUpdateWithoutLeakingIntoInput copies the
// mcp_test.go:154 pattern: the result must reach handleMCPAddResult
// (feedback set) AND must not mutate the chat textarea. It fails if the
// dispatch list (handlers.go update()) or the handleAsyncResult case
// (handlers_async.go) drops mcpAddResultMsg.
//
// The third whitelist touch point — the textarea skip list in
// finishUpdate (handlers.go) — is covered separately by
// TestMCPAddResult_TextareaSkipListEntry, because bubbles' textarea.Update
// silently ignores a non-KeyMsg, so an end-to-end Update() call cannot
// distinguish "skipped" from "forwarded and ignored".
func TestMCPAddResult_RoutedByUpdateWithoutLeakingIntoInput(t *testing.T) {
	m := &Model{chatInput: textarea.New()}
	m.chatInput.SetValue("typed")
	if m.modalMode != ModalNone {
		t.Fatalf("precondition: modalMode = %v, want ModalNone", m.modalMode)
	}

	_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", err: errors.New("boom")})

	if !strings.Contains(m.mcpFeedback, "svc") {
		t.Errorf("add-result not routed to handler: mcpFeedback = %q", m.mcpFeedback)
	}
	if got := m.chatInput.Value(); got != "typed" {
		t.Errorf("text input mutated by the msg: %q", got)
	}
}

// TestMCPAddResult_TextareaSkipListEntry pins the third whitelist touch
// point (review #1 m1 / review #2 MINOR 2): finishUpdate's ModalNone block
// must list mcpAddResultMsg in the same skip case as its sibling result
// messages, so a future reordering (or a new sibling message) cannot drop
// it while the "text input mutated" assertion above stays green (bubbles'
// textarea.Update ignores a non-KeyMsg, making that assertion blind to a
// skip-list miss).
//
// The check is a source-level scan of the exact case clause, the same
// pattern cmd/lele/gateway_mcp_stop_test.go uses for the gateway
// registrations: the file path is derived from runtime.Caller, not hardcoded,
// so the test keeps working if the file is renamed.
func TestMCPAddResult_TextareaSkipListEntry(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed: cannot locate this test's own file")
	}
	src, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "handlers.go"))
	if err != nil {
		t.Fatalf("read handlers.go: %v", err)
	}
	// Find the case clause inside finishUpdate's ModalNone switch that
	// starts with outboundMsg and holds the skip list.
	text := string(src)
	start := strings.Index(text, "case outboundMsg, completeMsg, tickMsg,")
	if start < 0 {
		t.Fatal("handlers.go: the finishUpdate textarea skip case was not found")
	}
	end := strings.Index(text[start:], "\n")
	if end < 0 {
		t.Fatal("handlers.go: the finishUpdate textarea skip case is not a single line")
	}
	clause := text[start : start+end]
	if !strings.Contains(clause, "mcpAddResultMsg") {
		t.Errorf("the finishUpdate textarea skip list does not include mcpAddResultMsg:\n%s\n"+
			"(a result msg falling through reaches the chat textarea's Update; harmless today "+
			"but the sibling list is the documented contract)", clause)
	}
	// The other two touch points stay asserted by the routing test above
	// (dispatch list in update(), handleAsyncResult case); this file-scan
	// cannot see them, so assert their presence too while we are here —
	// all three clauses are one grep each.
	for _, needle := range []struct{ what, want string }{
		{"the update() dispatch list", "mcpToggleResultMsg, mcpAddResultMsg, mcpAuthStartMsg"},
		{"the handleAsyncResult case", "case mcpAddResultMsg:"},
	} {
		if !strings.Contains(text, needle.want) && needle.what == "the update() dispatch list" {
			t.Errorf("handlers.go: %s does not carry mcpAddResultMsg next to its siblings", needle.what)
		}
		if needle.what == "the handleAsyncResult case" {
			asyncSrc, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "handlers_async.go"))
			if err != nil {
				t.Fatalf("read handlers_async.go: %v", err)
			}
			if !strings.Contains(string(asyncSrc), needle.want) {
				t.Errorf("handlers_async.go: the handleAsyncResult switch has no %s", needle.want)
			}
		}
	}
}

// ── 1b. m4: the feedback line never echoes the raw error ──────────────

// TestMCPAddErrorKind_Table pins the verb-extraction contract: every shaped
// pkg/mcp writer error ("mcp: <verb> <path>[: context]") reduces to its
// verb; an unshaped error degrades to its first token; control/bidi chars
// are stripped (sanitizeDisplayText) before the first token is taken, so a
// crafted error cannot smuggle an escape sequence into the feedback line.
func TestMCPAddErrorKind_Table(t *testing.T) {
	const secret = "s3cr3t-value"
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"shaped write", errors.New("mcp: write /home/u/.lele/mcp.json: bad value \"" + secret + "\""), "write"},
		{"shaped read", errors.New("mcp: read /home/u/.lele/mcp.json: permission denied"), "read"},
		{"shaped edit", errors.New("mcp: edit /home/u/.lele/mcp.json: truncated"), "edit"},
		{"shaped replace", errors.New("mcp: replace /tmp/x: no space left on device"), "replace"},
		// Unshaped: the first token only ("permission" of "permission
		// denied: …"), never the path or the trailing context.
		{"unshaped", errors.New("permission denied: /home/u/.lele/mcp.json"), "permission"},
		// Control chars (CSI SGR + BEL) and an embedded secret are stripped
		// before the token is taken, so the kind cannot smuggle them.
		{"control chars stripped", errors.New("some\x1b[31mstrict\x07thing"), "somestrictthing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mcpAddErrorKind(tc.err)
			if got != tc.want {
				t.Errorf("mcpAddErrorKind(%v) = %q, want %q", tc.err, got, tc.want)
			}
			if strings.Contains(got, secret) {
				t.Errorf("mcpAddErrorKind(%v) = %q leaks the value %q", tc.err, got, secret)
			}
		})
	}
}

// TestMCPAddResult_FeedbackNeverEchoesRawError pins the m4 FIX end-to-end
// (review #2 MINOR 3, mutation N7): a non-sentinel error carrying a path, a
// value and control chars must land on the feedback line as its VERB ONLY,
// with no "=", no path and no control characters — the secrecy invariant
// DoD 8 says a failed write never echoes what it was writing.
func TestMCPAddResult_FeedbackNeverEchoesRawError(t *testing.T) {
	// A crafted error carrying everything the feedback line must drop: the
	// file path, a KEY=VALUE pair (the "value" half is the secret), a CSI
	// SGR sequence and a BEL.
	crafty := errors.New("mcp: write /home/u/.lele/mcp.json: bad value \"TOKEN=" + "s3cr3t" + "\"\x1b[31m\x07")

	// Route through Update so the real dispatch + handler chain runs, the
	// same path the write cmd's result takes.
	m := &Model{chatInput: textarea.New()}
	_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", err: crafty})

	fb := m.mcpFeedback
	if fb == "" {
		t.Fatal("feedback line is empty: the error was not surfaced at all")
	}
	if strings.Contains(fb, "s3cr3t") {
		t.Errorf("feedback leaks the value: %q", fb)
	}
	if strings.Contains(fb, "mcp.json") {
		t.Errorf("feedback leaks the file path: %q", fb)
	}
	if strings.Contains(fb, "=") {
		t.Errorf("feedback carries a KEY=VALUE fragment (want verb only): %q", fb)
	}
	if strings.ContainsAny(fb, "\x1b\x07") {
		t.Errorf("feedback carries control characters: %q", fb)
	}
	if want := fmt.Sprintf(i18n.T("tui.mcpAddFailed"), "svc", "write"); fb != want {
		t.Errorf("feedback = %q, want the verb-only rendering %q", fb, want)
	}
}

// ── 2. layer isolation: only the named layer file is written ──────────

func TestMCPAddCmdFor_WritesOnlyTheNamedLayer(t *testing.T) {
	// Disjoint 3-layer fixture (mcpFixturePaths): global=alpha,beta ·
	// agent=gamma · project=delta. The loop-backed model is only needed so
	// the sync seam can be counted (the cmd itself is pure closure logic).
	paths := mcpFixturePaths(t)
	_ = mcpAddOpenList(t)
	globalFile := filepath.Join(paths.LeleDir, "mcp.json")
	agentFile := filepath.Join(paths.AgentWorkspace, "mcp.json")
	projectFile := filepath.Join(paths.Cwd, ".lele", "mcp.json")

	before := map[string][]byte{}
	for _, f := range []string{globalFile, agentFile, projectFile} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		before[f] = b
	}

	got := mcpAddCmdFor(paths, mcp.LayerAgent, "newsvc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if res.err != nil {
		t.Fatalf("add failed: %v", res.err)
	}
	if !res.created || !res.changed {
		t.Errorf("created=%v changed=%v, want both true", res.created, res.changed)
	}
	if res.layer != mcp.LayerAgent {
		t.Errorf("layer = %q, want %q", res.layer, mcp.LayerAgent)
	}
	if res.path != agentFile {
		t.Errorf("path = %q, want %q", res.path, agentFile)
	}

	// AGENT: created, holds the entry.
	agentAfter, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatalf("re-read agent: %v", err)
	}
	if !bytes.Contains(agentAfter, []byte(`"newsvc"`)) {
		t.Errorf("agent file lacks the new entry: %s", agentAfter)
	}

	// GLOBAL and PROJECT: byte-identical.
	for _, f := range []string{globalFile, projectFile} {
		after, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("re-read %s: %v", f, err)
		}
		if !bytes.Equal(before[f], after) {
			t.Errorf("%s modified:\nbefore=%s\nafter=%s", f, before[f], after)
		}
	}
}

// ── 3. refusals with zero writes ──────────────────────────────────────

func TestMCPAddCmdFor_RefusesDisabledLayer(t *testing.T) {
	// Empty root ⇒ the global layer's LayerFile resolves to "" (joinRoot's
	// empty-root rule), regardless of whether the loop that serves Update
	// has live roots — the cmd only sees the captured Paths.
	paths := mcpAddPaths(t)
	paths.LeleDir = "" // the global layer is disabled (no root)
	if f := mcp.LayerFile(paths, mcp.LayerGlobal); f != "" {
		t.Fatalf("fixture is wrong: LayerFile(global) = %q, want \"\" (disabled layer)", f)
	}

	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "svc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if !errors.Is(res.err, errMCPOwnerNoPath) {
		t.Errorf("err = %v, want errMCPOwnerNoPath", res.err)
	}
	if res.layer != mcp.LayerGlobal {
		t.Errorf("layer = %q, want %q", res.layer, mcp.LayerGlobal)
	}
	// No file was created anywhere in the fixture.
	for _, dir := range []string{paths.AgentWorkspace, paths.Cwd} {
		f := filepath.Join(dir, "mcp.json")
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("refused add created %s (stat err = %v)", f, err)
		}
	}
}

// TestMCPAddCmdFor_RefusesTargetOutsideAllowedRoots copies the
// mcp_test.go:865 shape INCLUDING the sanity assert that the predicate
// really rejects the probe dir (without it the refusal cannot be exercised
// on this box).
func TestMCPAddCmdFor_RefusesTargetOutsideAllowedRoots(t *testing.T) {
	probe, err := os.MkdirTemp("/var/tmp", "lele-tui-guard-*")
	if err != nil {
		t.Fatalf("cannot create a probe dir outside the allowed roots: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(probe) })

	if channels.IsAllowedWorkspacePath(probe) {
		wd, _ := os.Getwd()
		home, _ := os.UserHomeDir()
		t.Fatalf("probe dir %q is accepted by the predicate (home=%q cwd=%q); the refusal cannot be exercised on this box",
			probe, home, wd)
	}

	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        probe,
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"svc":{"command":"x"}}}`)
	file := filepath.Join(paths.LeleDir, "mcp.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "newsvc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if !errors.Is(res.err, errMCPPathNotAllowed) {
		t.Errorf("err = %v, want errMCPPathNotAllowed", res.err)
	}
	if res.path != file {
		t.Errorf("path = %q, want the refused target %q", res.path, file)
	}

	// Refused BEFORE any write: bytes must be identical.
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("refused target was written:\nbefore=%s\nafter=%s", before, after)
	}

	// The refusal renders through the result handler and names the path.
	m := &Model{chatInput: textarea.New()}
	_, _ = m.Update(res)
	want := fmt.Sprintf(i18n.T("tui.mcpAddPathNotAllowed"), "newsvc", file)
	if m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
}

func TestMCPAddCmdFor_RefusesInvalidEntry(t *testing.T) {
	paths := mcpAddPaths(t)
	// Plant an existing global file so the fixture is realistic (the refusal
	// must not touch it either).
	mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"svc":{"command":"x"}}}`)
	file := filepath.Join(paths.LeleDir, "mcp.json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	// Command AND URL both set — ServerConfig.validate refuses it, and
	// UpsertEntry validates BEFORE writing anything.
	invalid := mcp.ServerConfig{Command: "npx", URL: "https://example.com/mcp"}
	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "newsvc", invalid)()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if res.err == nil {
		t.Fatal("err = nil, want a validation error for the mixed stdio/remote entry")
	}
	if res.created || res.changed {
		t.Errorf("created=%v changed=%v, want both false on refusal", res.created, res.changed)
	}

	// No new file in the (empty) agent layer, and the global file untouched.
	if _, err := os.Stat(filepath.Join(paths.AgentWorkspace, "mcp.json")); !os.IsNotExist(err) {
		t.Errorf("invalid add created an agent file (stat err = %v)", err)
	}
	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("fixture was written on a refused entry:\nbefore=%s\nafter=%s", before, after)
	}
}

// ── 4. syncMCPServersFor is called exactly once on success, 0 on refuse ─

func TestMCPAddResult_SyncsOnceOnSuccessAndZeroOnRefusal(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		calls := mcpAddSwapSyncCounter(t)
		m := &Model{chatInput: textarea.New()}
		_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", created: true, changed: true})
		if *calls != 1 {
			t.Errorf("sync calls = %d, want exactly 1", *calls)
		}
	})
	t.Run("refusal", func(t *testing.T) {
		calls := mcpAddSwapSyncCounter(t)
		m := &Model{chatInput: textarea.New()}
		_, _ = m.Update(mcpAddResultMsg{name: "svc", err: errMCPUnavailable})
		if *calls != 0 {
			t.Errorf("sync calls = %d, want 0 on refusal", *calls)
		}
	})
	t.Run("no-change success still syncs", func(t *testing.T) {
		calls := mcpAddSwapSyncCounter(t)
		m := &Model{chatInput: textarea.New()}
		_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", changed: false})
		if *calls != 1 {
			t.Errorf("sync calls = %d, want 1 (idempotent pass, like the toggle)", *calls)
		}
	})
}

// ── 5. exact bytes + ${TOKEN} literal on disk ─────────────────────────

func TestMCPAddCmdFor_WritesExactBytesAndLiteralToken(t *testing.T) {
	t.Run("exact bytes", func(t *testing.T) {
		paths := mcpAddPaths(t)
		got := mcpAddCmdFor(paths, mcp.LayerGlobal, "x", mcpAddEntry())()
		res, ok := got.(mcpAddResultMsg)
		if !ok {
			t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
		}
		if res.err != nil {
			t.Fatalf("add failed: %v", res.err)
		}
		// docs/mcp.md:488 — the canonical compact single-line shape
		// UpsertEntry produces for a minimal stdio entry.
		want := `{"mcpServers":{"x":{"command":"npx","args":["-y","pkg"]}}}`
		after, err := os.ReadFile(filepath.Join(paths.LeleDir, "mcp.json"))
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if string(after) != want {
			t.Errorf("file bytes = %s, want %s", after, want)
		}
	})

	t.Run("literal ${TOKEN} is never expanded", func(t *testing.T) {
		paths := mcpAddPaths(t)
		entry := mcp.ServerConfig{
			URL:     "https://example.com/mcp",
			Headers: map[string]string{"Authorization": "Bearer ${TOKEN}"},
			Env:     map[string]string{"SECRET": "${TOKEN}"},
		}
		got := mcpAddCmdFor(paths, mcp.LayerGlobal, "tok", entry)()
		res, ok := got.(mcpAddResultMsg)
		if !ok {
			t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
		}
		if res.err != nil {
			t.Fatalf("add failed: %v", res.err)
		}
		after, err := os.ReadFile(filepath.Join(paths.LeleDir, "mcp.json"))
		if err != nil {
			t.Fatalf("re-read: %v", err)
		}
		if !bytes.Contains(after, []byte("${TOKEN}")) {
			t.Errorf("${TOKEN} was expanded (or lost) on disk: %s", after)
		}
		if bytes.Contains(after, []byte("Bearer ey")) {
			t.Errorf("a secret leaked into the file: %s", after)
		}
	})
}

// ── 6. filter reset + cursor lands on the new row ─────────────────────

func TestMCPAddResult_ResetsFilterThatWouldHideTheNewRow(t *testing.T) {
	// Update reloads the list through m.mcpPaths(), which always resolves
	// the LOOP's own roots — so the fixture must live under the temp
	// LELE_CONFIG_DIR (mcpAddOpenList's layout). "alpha" and "delta" are
	// both owned by the global layer (the loop's project root points into
	// the real repo tree, so no fixture may be planted there); the filters
	// exercised are the ones that would hide a global row.
	m := mcpAddOpenList(t)
	paths, ok := m.mcpPaths()
	if !ok {
		t.Fatal("fixture lost MCP paths")
	}
	mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"alpha":{"command":"a"}}}`)

	t.Run("mismatched filter resets and reselects", func(t *testing.T) {
		m.mcpFilter = mcpFilterAll
		m.loadMCPList()                // alpha only (delta does not exist yet)
		m.mcpFilter = mcpFilterProject // would hide a global row
		m.modalSelectedIdx = 0

		// Mirror on disk what the write cmd would have left behind for the
		// synthetic result msg fed below: "delta" created in the global layer.
		mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"alpha":{"command":"a"},"delta":{"command":"d"}}}`)
		_, _ = m.Update(mcpAddResultMsg{name: "delta", layer: mcp.LayerGlobal, created: true, changed: true})

		if m.mcpFilter != mcpFilterAll {
			t.Errorf("filter = %d, want mcpFilterAll (%d)", m.mcpFilter, mcpFilterAll)
		}
		idx := -1
		for i, k := range m.mcpModalKeys {
			if k == "delta" {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("keys = %v, want delta in the reloaded list", m.mcpModalKeys)
		}
		if m.modalSelectedIdx != idx {
			t.Errorf("cursor = %d, want %d (the new row)", m.modalSelectedIdx, idx)
		}
	})

	t.Run("matching filter is left alone", func(t *testing.T) {
		m.mcpFilter = mcpFilterGlobal
		m.loadMCPList() // alpha, owned by global
		m.modalSelectedIdx = 0

		_, _ = m.Update(mcpAddResultMsg{name: "alpha", layer: mcp.LayerGlobal, created: true, changed: true})

		if m.mcpFilter != mcpFilterGlobal {
			t.Errorf("filter = %d, want mcpFilterGlobal (%d) untouched", m.mcpFilter, mcpFilterGlobal)
		}
	})
}

// ── 7. wizard state is never touched ──────────────────────────────────

func TestMCPAddResult_NeverTouchesWizardState(t *testing.T) {
	m := &Model{chatInput: textarea.New()}
	m.formValues = []string{"sentinel-name", "sentinel-kind"}
	m.formStepIndex = 3
	m.formError = "sentinel-error"

	_, _ = m.Update(mcpAddResultMsg{name: "svc", layer: "global", created: true, changed: true})

	if len(m.formValues) != 2 || m.formValues[0] != "sentinel-name" || m.formValues[1] != "sentinel-kind" {
		t.Errorf("formValues mutated: %v", m.formValues)
	}
	if m.formStepIndex != 3 {
		t.Errorf("formStepIndex = %d, want 3 (unchanged)", m.formStepIndex)
	}
	if m.formError != "sentinel-error" {
		t.Errorf("formError = %q, want %q (unchanged)", m.formError, "sentinel-error")
	}
}

// TestMCPAddCmdFor_ReportsHighestShadowingLayer pins the read-only pre-write
// inventory pass (plan §4.4): a copy ABOVE the target layer is reported, a
// copy below it is ignored, and only the highest shadowing layer survives.
func TestMCPAddCmdFor_ReportsHighestShadowingLayer(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	// The agent copy is lower-ranked (it cannot shadow a global write); the
	// project copy is the only one allowed to shadow the global target.
	mcpWriteDoc(t, paths.AgentWorkspace,
		`{"mcpServers":{"svc":{"command":"agent-tool"}}}`)
	mcpWriteDoc(t, filepath.Join(paths.Cwd, ".lele"),
		`{"mcpServers":{"svc":{"command":"project-tool"}}}`)
	projectFile := mcp.LayerFile(paths, mcp.LayerProject)
	beforeProject, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatalf("read project fixture: %v", err)
	}
	agentFile := mcp.LayerFile(paths, mcp.LayerAgent)
	beforeAgent, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatalf("read agent fixture: %v", err)
	}

	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "svc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if res.err != nil {
		t.Fatalf("add failed: %v", res.err)
	}
	if res.shadow != mcp.LayerProject {
		t.Errorf("shadow = %q, want %q (highest layer above the target)", res.shadow, mcp.LayerProject)
	}

	// The pre-write inventory read must remain read-only for the upper layers.
	afterAgent, err := os.ReadFile(agentFile)
	if err != nil {
		t.Fatalf("re-read agent: %v", err)
	}
	afterProject, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatalf("re-read project: %v", err)
	}
	if !bytes.Equal(beforeAgent, afterAgent) {
		t.Errorf("agent file modified:\nbefore=%s\nafter=%s", beforeAgent, afterAgent)
	}
	if !bytes.Equal(beforeProject, afterProject) {
		t.Errorf("project file modified:\nbefore=%s\nafter=%s", beforeProject, afterProject)
	}

	// The routed result appends the localized shadow note exactly once.
	m := &Model{chatInput: textarea.New()}
	_, _ = m.Update(res)
	base := fmt.Sprintf(i18n.T("tui.mcpAddSuccess"), "svc", mcp.LayerGlobal,
		mcp.LayerFile(paths, mcp.LayerGlobal))
	want := base + " " + fmt.Sprintf(i18n.T("tui.mcpAddShadowSuffix"), mcp.LayerProject)
	if m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
	if strings.Count(m.mcpFeedback, fmt.Sprintf(i18n.T("tui.mcpAddShadowSuffix"), mcp.LayerProject)) != 1 {
		t.Errorf("shadow note is not appended exactly once: %q", m.mcpFeedback)
	}
}

// TestMCPAddCmdFor_NeverReportsTheTargetLayerAsShadowing pins the self-shadow
// guard of mcpAddShadowLayer (the `r <= rank` term): a copy in the TARGET
// layer is the replace case, never a shadow — reporting the layer just
// written as "shadowing" would make the feedback contradict itself. The
// mutation `r <= rank` → `r < rank` promotes the same-layer copy to a shadow
// and must fail here.
func TestMCPAddCmdFor_NeverReportsTheTargetLayerAsShadowing(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	// A copy of svc already lives in the TARGET layer itself — the replace
	// case of the review step, not a shadowing one.
	mcpWriteDoc(t, paths.LeleDir,
		`{"mcpServers":{"svc":{"command":"old-global"}}}`)

	got := mcpAddCmdFor(paths, mcp.LayerGlobal, "svc", mcpAddEntry())()
	res, ok := got.(mcpAddResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpAddResultMsg", got)
	}
	if res.err != nil {
		t.Fatalf("add failed: %v", res.err)
	}
	if res.shadow != "" {
		t.Errorf("shadow = %q, want \"\" (the target layer's own copy is a replace, never a shadow)", res.shadow)
	}
	if !res.replaced {
		t.Errorf("replaced = false, want true (the write overwrites the target layer's copy)")
	}
}
