package tui

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/agent"
	"github.com/xilistudios/lele/pkg/channels"
	"github.com/xilistudios/lele/pkg/mcp"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// ── helpers ────────────────────────────────────────────────────────────

// mcpWriteDoc writes doc as <dir>/mcp.json with raw bytes (pkg/mcp's
// writer is deliberately NOT used in fixtures).
func mcpWriteDoc(t *testing.T, dir, doc string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(doc), 0o644); err != nil {
		t.Fatalf("write %s/mcp.json: %v", dir, err)
	}
}

// mcpKeyRunes builds a rune key (e.g. "f", "q") like bubbletea does.
func mcpKeyRunes(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// ── 1. formatMCPItem table ─────────────────────────────────────────────

func TestFormatMCPItem(t *testing.T) {
	pad := strings.Repeat(" ", 12) // %-15s of "srv"
	cases := []struct {
		name, layer, effective, want string
	}{
		{"srv", mcp.LayerGlobal, "enabled", "● srv" + pad + " — [global]"},
		{"srv", mcp.LayerAgent, "enabled", "● srv" + pad + " — [agent]"},
		{"srv", mcp.LayerProject, "enabled", "● srv" + pad + " — [project]"},
		{"srv", mcp.LayerGlobal, "disabled", "○ srv" + pad + " — [global]"},
		{"srv", mcp.LayerAgent, "disabled", "○ srv" + pad + " — [agent]"},
		{"srv", mcp.LayerProject, "disabled", "○ srv" + pad + " — [project]"},
		{"srv", mcp.LayerGlobal, "invalid", "! srv" + pad + " — [global]"},
		{"srv", mcp.LayerAgent, "invalid", "! srv" + pad + " — [agent]"},
		{"srv", mcp.LayerProject, "invalid", "! srv" + pad + " — [project]"},
	}
	for _, tc := range cases {
		got := formatMCPItem(tc.name, tc.layer, tc.effective)
		if got != tc.want {
			t.Errorf("formatMCPItem(%q,%q,%q) = %q, want %q",
				tc.name, tc.layer, tc.effective, got, tc.want)
		}
	}
}

// ── 2. loadMCPList on real t.TempDir() fixtures ────────────────────────

// mcpFixturePaths builds three disjoint layer roots:
//
//	global: alpha, beta   agent: gamma   project: delta
func mcpFixturePaths(t *testing.T) mcp.Paths {
	t.Helper()
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir,
		`{"mcpServers":{"beta":{"command":"b"},"alpha":{"command":"a"}}}`)
	mcpWriteDoc(t, paths.AgentWorkspace,
		`{"mcpServers":{"gamma":{"command":"g"}}}`)
	mcpWriteDoc(t, filepath.Join(paths.Cwd, ".lele"),
		`{"mcpServers":{"delta":{"command":"d"}}}`)
	return paths
}

func TestLoadMCPList_FiltersSortKeysAndCursor(t *testing.T) {
	paths := mcpFixturePaths(t)

	cases := []struct {
		filter int
		want   []string // expected keys, sorted
	}{
		{mcpFilterAll, []string{"alpha", "beta", "delta", "gamma"}},
		{mcpFilterGlobal, []string{"alpha", "beta"}},
		{mcpFilterAgent, []string{"gamma"}},
		{mcpFilterProject, []string{"delta"}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("filter=%d", tc.filter), func(t *testing.T) {
			m := &Model{modalMode: ModalMCP, mcpFilter: tc.filter}
			// Cursor far out of range must be clamped into the new list.
			m.modalSelectedIdx = 99
			m.loadMCPListWith(paths)

			if len(m.modalItems) != len(tc.want) {
				t.Fatalf("items = %d (%v), want %d", len(m.modalItems), m.modalItems, len(tc.want))
			}
			if len(m.mcpModalKeys) != len(m.modalItems) {
				t.Fatalf("keys/items desync: %d keys vs %d items",
					len(m.mcpModalKeys), len(m.modalItems))
			}
			for i, want := range tc.want {
				if m.mcpModalKeys[i] != want {
					t.Errorf("keys[%d] = %q, want %q (keys=%v)",
						i, m.mcpModalKeys[i], want, m.mcpModalKeys)
				}
				if m.mcpModalKeys[i] == "" {
					t.Errorf("keys[%d] empty in a non-empty list", i)
				}
			}
			if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.modalItems) {
				t.Errorf("cursor %d not clamped into [0,%d)",
					m.modalSelectedIdx, len(m.modalItems))
			}
		})
	}
}

func TestLoadMCPList_EmptyInventory(t *testing.T) {
	// Roots that exist but hold no mcp.json at all.
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	m := &Model{modalMode: ModalMCP}
	m.loadMCPListWith(paths)

	if len(m.modalItems) != 1 || m.modalItems[0] != i18n.T("tui.mcpNoServers") {
		t.Fatalf("items = %v, want [%q]", m.modalItems, i18n.T("tui.mcpNoServers"))
	}
	if len(m.mcpModalKeys) != 1 || m.mcpModalKeys[0] != "" {
		t.Fatalf("keys = %v, want [\"\"]", m.mcpModalKeys)
	}
}

// ── 3. registration completeness via Update ────────────────────────────

func TestMCPToggleResult_RoutedByUpdateWithoutLeakingIntoInput(t *testing.T) {
	m := &Model{chatInput: textarea.New()}
	m.chatInput.SetValue("typed")
	if m.modalMode != ModalNone {
		t.Fatalf("precondition: modalMode = %v, want ModalNone", m.modalMode)
	}

	_, _ = m.Update(mcpToggleResultMsg{name: "svc", err: errors.New("boom")})

	// Routed to the handler (feedback line carries the server name)…
	if !strings.Contains(m.mcpFeedback, "svc") {
		t.Errorf("toggle-result not routed to handler: mcpFeedback = %q", m.mcpFeedback)
	}
	// …and it never reached the chat textarea.
	if got := m.chatInput.Value(); got != "typed" {
		t.Errorf("text input mutated by the msg: %q", got)
	}
}

// ── 4. handleMCPKey ────────────────────────────────────────────────────

func TestHandleMCPKey_SpaceTogglesSelectedName(t *testing.T) {
	m := &Model{modalMode: ModalMCP}
	m.mcpModalKeys = []string{"alpha", "beta"}
	m.modalItems = []string{"row0", "row1"}
	m.modalSelectedIdx = 1

	// No agent loop: the cmd reports unavailability but must carry the
	// selected name (and must not panic).
	cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeySpace})
	if !handled {
		t.Fatal("space not handled")
	}
	if cmd == nil {
		t.Fatal("space produced no toggle cmd")
	}
	res, ok := cmd().(mcpToggleResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpToggleResultMsg", cmd())
	}
	if res.name != "beta" {
		t.Errorf("toggled %q, want the selected name %q", res.name, "beta")
	}
	if !errors.Is(res.err, errMCPUnavailable) {
		t.Errorf("err = %v, want errMCPUnavailable", res.err)
	}
}

func TestHandleMCPKey_FCyclesFilter(t *testing.T) {
	m := &Model{modalMode: ModalMCP, mcpFilter: mcpFilterAll}

	want := []int{mcpFilterGlobal, mcpFilterAgent, mcpFilterProject, mcpFilterAll}
	for i, wantFilter := range want {
		cmd, handled := m.handleMCPKey(mcpKeyRunes('f'))
		if !handled {
			t.Fatalf("step %d: f not handled", i)
		}
		if cmd != nil {
			t.Errorf("step %d: f returned a cmd, want none", i)
		}
		if m.mcpFilter != wantFilter {
			t.Errorf("step %d: filter = %d, want %d", i, m.mcpFilter, wantFilter)
		}
	}
}

func TestHandleMCPKey_EscAndQClose(t *testing.T) {
	for _, msg := range []tea.KeyMsg{{Type: tea.KeyEsc}, mcpKeyRunes('q')} {
		m := &Model{modalMode: ModalMCP}
		cmd, handled := m.handleMCPKey(msg)
		if !handled {
			t.Errorf("%q not handled", msg.String())
		}
		if cmd != nil {
			t.Errorf("%q returned a cmd, want none", msg.String())
		}
		if m.modalMode != ModalNone {
			t.Errorf("%q left modalMode = %v, want ModalNone", msg.String(), m.modalMode)
		}
	}
}

func TestHandleMCPKey_EnterKeepsCursor(t *testing.T) {
	m := &Model{modalMode: ModalMCP, modalSelectedIdx: 1}
	m.mcpModalKeys = []string{"a", "b"}
	cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled || cmd != nil {
		t.Fatalf("enter: handled=%v cmd=%v, want handled with no cmd", handled, cmd)
	}
	if m.modalMode != ModalMCP || m.modalSelectedIdx != 1 {
		t.Errorf("enter moved state: mode=%v idx=%d", m.modalMode, m.modalSelectedIdx)
	}
}

// ── 5. the toggle writes the OWNING layer ──────────────────────────────

func TestMCPToggleCmdFor_WritesOwningLayerOnly(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	globalDoc := `{"mcpServers":{"svc":{"command":"real"}}}`
	agentDoc := `{"mcpServers":{"svc":{"disabled":true}}}`
	mcpWriteDoc(t, paths.LeleDir, globalDoc)
	mcpWriteDoc(t, paths.AgentWorkspace, agentDoc)

	globalPath := filepath.Join(paths.LeleDir, "mcp.json")
	agentPath := filepath.Join(paths.AgentWorkspace, "mcp.json")
	before, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read global: %v", err)
	}

	// The agent stub shadows the global definition ⇒ the agent layer owns
	// the winning entry, so the write must hit the agent file only.
	got := mcpToggleCmdFor(paths, "svc")()
	res, ok := got.(mcpToggleResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpToggleResultMsg", got)
	}
	if res.err != nil {
		t.Fatalf("toggle failed: %v", res.err)
	}
	if !res.enabled {
		t.Error("result enabled = false, want true (stub was disabling the server)")
	}
	if !res.changed {
		t.Error("result changed = false, want true")
	}

	// GLOBAL: byte-identical (it was shadowed, never the toggle target).
	after, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("re-read global: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("global file modified:\nbefore=%s\nafter=%s", before, after)
	}

	// AGENT: changed, and the stub entry is DELETED (not left as {}).
	agentAfter, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("re-read agent: %v", err)
	}
	if string(agentAfter) == agentDoc {
		t.Fatal("agent file unchanged")
	}
	if strings.Contains(string(agentAfter), `"svc"`) {
		t.Errorf("stub entry not deleted: %s", agentAfter)
	}
}

// ── 6. MCPPathsFor ok=false ⇒ "MCP unavailable", no panic ──────────────

func TestLoadMCPList_PathsUnavailable(t *testing.T) {
	// A bare loop resolves no MCP paths (MCPPathsFor → ok=false).
	m := &Model{agentLoop: &agent.AgentLoop{}}
	m.loadMCPList()

	if len(m.modalItems) != 1 || m.modalItems[0] != i18n.T("tui.mcpUnavailable") {
		t.Fatalf("items = %v, want [%q]", m.modalItems, i18n.T("tui.mcpUnavailable"))
	}
	if len(m.mcpModalKeys) != 1 || m.mcpModalKeys[0] != "" {
		t.Fatalf("keys = %v, want [\"\"]", m.mcpModalKeys)
	}

	// The toggle cmd reports the same condition instead of panicking.
	cmd := m.mcpToggleCmd("svc")
	if cmd == nil {
		t.Fatal("toggle cmd is nil")
	}
	got := cmd()
	res, ok := got.(mcpToggleResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpToggleResultMsg", got)
	}
	if !errors.Is(res.err, errMCPUnavailable) {
		t.Errorf("err = %v, want errMCPUnavailable", res.err)
	}
	if res.name != "svc" {
		t.Errorf("name = %q, want svc", res.name)
	}
}

// ── 7. ModalMCPDetail: enter, rendering, snapshot lifecycle ────────────

// TestMCPDetail_EnterOpensDetailAndKeepsCursor covers requirement: enter on
// a real row opens the detail for THAT name, every other key inside the
// detail is swallowed (no crash, no cursor drift), and esc/q return to the
// list with the cursor and rows untouched.
func TestMCPDetail_EnterOpensDetailAndKeepsCursor(t *testing.T) {
	paths := mcpFixturePaths(t)
	m := &Model{modalMode: ModalMCP, width: 140, height: 40}
	m.loadMCPListWith(paths)
	m.modalSelectedIdx = 1 // "beta" (sorted: alpha, beta, delta, gamma)

	cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !handled || cmd != nil {
		t.Fatalf("enter: handled=%v cmd=%v, want handled with no cmd", handled, cmd)
	}
	if m.modalMode != ModalMCPDetail {
		t.Fatalf("mode = %v, want ModalMCPDetail", m.modalMode)
	}
	if m.mcpDetailName != "beta" {
		t.Errorf("mcpDetailName = %q, want beta", m.mcpDetailName)
	}
	if m.modalSelectedIdx != 1 {
		t.Errorf("enter moved the cursor: idx = %d, want 1", m.modalSelectedIdx)
	}
	out := ansi.Strip(m.renderMCPDetail())
	if !strings.Contains(out, "beta") {
		t.Errorf("detail does not mention the selected name:\n%s", out)
	}
	if !strings.Contains(out, i18n.T("tui.mcpDetail")) {
		t.Errorf("detail does not render its title %q:\n%s", i18n.T("tui.mcpDetail"), out)
	}

	// Any other key inside the detail must be swallowed: no crash, no mode
	// change, no movement of the hidden list cursor.
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyUp}, {Type: tea.KeyDown}, {Type: tea.KeyEnter},
		mcpKeyRunes(' '), mcpKeyRunes('t'), mcpKeyRunes('f'),
		mcpKeyRunes('d'), mcpKeyRunes('x'),
	} {
		cmd, handled := m.handleMCPKey(msg)
		if !handled || cmd != nil {
			t.Fatalf("key %q in detail: handled=%v cmd=%v, want swallowed", msg.String(), handled, cmd)
		}
		if m.modalMode != ModalMCPDetail || m.modalSelectedIdx != 1 {
			t.Fatalf("key %q in detail changed state: mode=%v idx=%d",
				msg.String(), m.modalMode, m.modalSelectedIdx)
		}
	}

	// esc returns to the list with the cursor and the rows untouched.
	cmd, handled = m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled || cmd != nil {
		t.Fatalf("esc: handled=%v cmd=%v, want handled with no cmd", handled, cmd)
	}
	if m.modalMode != ModalMCP {
		t.Errorf("mode after esc = %v, want ModalMCP", m.modalMode)
	}
	if m.modalSelectedIdx != 1 {
		t.Errorf("esc lost the cursor: idx = %d, want 1", m.modalSelectedIdx)
	}
	if len(m.mcpModalKeys) != 4 || m.mcpModalKeys[1] != "beta" {
		t.Errorf("list rows changed while in the detail: keys = %v", m.mcpModalKeys)
	}

	// q behaves exactly like esc.
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled || m.modalMode != ModalMCPDetail {
		t.Fatalf("re-enter: handled=%v mode=%v, want ModalMCPDetail", handled, m.modalMode)
	}
	if _, handled := m.handleMCPKey(mcpKeyRunes('q')); !handled || m.modalMode != ModalMCP {
		t.Errorf("q did not return to the list: handled=%v mode=%v", handled, m.modalMode)
	}
	if m.modalSelectedIdx != 1 {
		t.Errorf("q lost the cursor: idx = %d, want 1", m.modalSelectedIdx)
	}
}

// TestMCPDetail_EnterOnEmptyOrUnavailableRowIsNoOp pins that the two
// synthetic rows (empty inventory, "MCP unavailable") both carry the ""
// key and never open a detail.
func TestMCPDetail_EnterOnEmptyOrUnavailableRowIsNoOp(t *testing.T) {
	t.Run("empty inventory row", func(t *testing.T) {
		root := t.TempDir()
		paths := mcp.Paths{
			LeleDir:        filepath.Join(root, "global"),
			AgentWorkspace: filepath.Join(root, "agent"),
			Cwd:            filepath.Join(root, "proj"),
		}
		m := &Model{modalMode: ModalMCP}
		m.loadMCPListWith(paths)
		if len(m.mcpModalKeys) != 1 || m.mcpModalKeys[0] != "" {
			t.Fatalf("keys = %v, want the single empty-state row", m.mcpModalKeys)
		}
		cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
		if !handled || cmd != nil {
			t.Fatalf("enter: handled=%v cmd=%v, want handled with no cmd", handled, cmd)
		}
		if m.modalMode != ModalMCP || m.mcpDetailName != "" {
			t.Errorf("enter on the empty row opened a detail: mode=%v name=%q",
				m.modalMode, m.mcpDetailName)
		}
	})

	t.Run("unavailable row", func(t *testing.T) {
		// A bare loop resolves no MCP paths ⇒ the single "unavailable" row.
		m := &Model{agentLoop: &agent.AgentLoop{}, modalMode: ModalMCP}
		m.loadMCPList()
		if len(m.mcpModalKeys) != 1 || m.mcpModalKeys[0] != "" {
			t.Fatalf("keys = %v, want the single unavailable row", m.mcpModalKeys)
		}
		if m.mcpInventoryValid {
			t.Error("unavailable load left mcpInventoryValid = true")
		}
		cmd, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter})
		if !handled || cmd != nil {
			t.Fatalf("enter: handled=%v cmd=%v, want handled with no cmd", handled, cmd)
		}
		if m.modalMode != ModalMCP || m.mcpDetailName != "" {
			t.Errorf("enter on the unavailable row opened a detail: mode=%v name=%q",
				m.modalMode, m.mcpDetailName)
		}
	})
}

// TestMCPDetail_StubOverRealEntryShowsWholeStack: the agent layer's
// {"disabled":true} stub wins over the global real entry. The detail must
// show the winner (agent, a pure disable stub, verdict "disabled") AND the
// shadowed global copy with its real command.
func TestMCPDetail_StubOverRealEntryShowsWholeStack(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir,
		`{"mcpServers":{"svc":{"command":"global-tool","args":["-a","-b"]}}}`)
	mcpWriteDoc(t, paths.AgentWorkspace,
		`{"mcpServers":{"svc":{"disabled":true}}}`)

	m := &Model{modalMode: ModalMCP, width: 140, height: 40}
	m.loadMCPListWith(paths)
	if len(m.mcpModalKeys) != 1 || m.mcpModalKeys[0] != "svc" {
		t.Fatalf("keys = %v, want [svc]", m.mcpModalKeys)
	}
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled || m.modalMode != ModalMCPDetail {
		t.Fatalf("enter did not open the detail: handled=%v mode=%v", handled, m.modalMode)
	}

	out := ansi.Strip(m.renderMCPDetail())

	// Winner = agent layer, and its copy is a pure disable stub (Defines=false).
	if want := i18n.T("tui.mcpDetailLayer") + ": " + mcp.LayerAgent; !strings.Contains(out, want) {
		t.Errorf("detail missing winner layer %q:\n%s", want, out)
	}
	if want := i18n.T("tui.mcpDetailDefinition") + ": " + i18n.T("tui.mcpDetailStub"); !strings.Contains(out, want) {
		t.Errorf("detail missing stub definition %q:\n%s", want, out)
	}
	if bad := i18n.T("tui.mcpDetailDefinition") + ": " + i18n.T("tui.mcpDetailReal"); strings.Contains(out, bad) {
		t.Errorf("winning stub flagged as a real entry (%q):\n%s", bad, out)
	}
	// Effective verdict of the stack: disabled (the stub switches it off).
	if want := "[" + i18n.T("tui.mcpDisabled") + "]"; !strings.Contains(out, want) {
		t.Errorf("detail missing effective verdict %q:\n%s", want, out)
	}
	// The shadowed global copy: its layer, defines=true, and its command.
	shadowedLine := false
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, mcp.LayerGlobal) && strings.Contains(line, "defines=true") {
			shadowedLine = true
		}
	}
	if !shadowedLine {
		t.Errorf("no shadowed global line with defines=true:\n%s", out)
	}
	if !strings.Contains(out, "global-tool") {
		t.Errorf("shadowed global copy does not show its real command:\n%s", out)
	}
}

// TestMCPDetail_InvalidEntryShowsInvalidMessage: an entry with neither
// command nor url renders the validate() message from the stored
// inventory and the localized "invalid" verdict.
func TestMCPDetail_InvalidEntryShowsInvalidMessage(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir,
		`{"mcpServers":{"broken":{"description":"no transport configured"}}}`)

	m := &Model{modalMode: ModalMCP, width: 140, height: 40}
	m.loadMCPListWith(paths)
	if len(m.mcpModalKeys) != 1 || m.mcpModalKeys[0] != "broken" {
		t.Fatalf("keys = %v, want [broken]", m.mcpModalKeys)
	}
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled || m.modalMode != ModalMCPDetail {
		t.Fatalf("enter did not open the detail: handled=%v mode=%v", handled, m.modalMode)
	}

	out := ansi.Strip(m.renderMCPDetail())
	want := i18n.T("tui.mcpDetailInvalid") + ": invalid entry: needs command (stdio) or url (remote)"
	if !strings.Contains(out, want) {
		t.Errorf("detail missing the Invalid message %q:\n%s", want, out)
	}
	if verdict := "[" + i18n.T("tui.mcpInvalid") + "]"; !strings.Contains(out, verdict) {
		t.Errorf("detail missing the invalid verdict %q:\n%s", verdict, out)
	}
}

// TestMCPDetail_ShowsKeyNamesButNeverValues: env/header KEY names (sorted)
// and the raw ${VAR} literal are shown; every VALUE string, and the arg
// VALUES behind the arg count, must appear nowhere in the rendered output.
func TestMCPDetail_ShowsKeyNamesButNeverValues(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir, `{"mcpServers":{"secure":{`+
		`"command":"mytool","args":["--stdio-flag"],`+
		`"description":"proxy for ${UPSTREAM_HOST}",`+
		`"env":{"API_TOKEN":"env-secret-hunter2","ZZ_OPT":"zz-secret-opt"},`+
		`"headers":{"Authorization":"header-secret-bearer","X-Trace":"trace-secret-value"}}}}`)

	m := &Model{modalMode: ModalMCP, width: 140, height: 40}
	m.loadMCPListWith(paths)
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled || m.modalMode != ModalMCPDetail {
		t.Fatalf("enter did not open the detail: handled=%v mode=%v", handled, m.modalMode)
	}

	out := ansi.Strip(m.renderMCPDetail())

	// KEY names, sorted — one line each.
	if want := i18n.T("tui.mcpDetailEnvKeys") + ": API_TOKEN, ZZ_OPT"; !strings.Contains(out, want) {
		t.Errorf("detail missing env key names %q:\n%s", want, out)
	}
	if want := i18n.T("tui.mcpDetailHeaderKeys") + ": Authorization, X-Trace"; !strings.Contains(out, want) {
		t.Errorf("detail missing header key names %q:\n%s", want, out)
	}
	// Raw stored bytes: ${VAR} appears literally, never expanded.
	if want := "${UPSTREAM_HOST}"; !strings.Contains(out, want) {
		t.Errorf("detail missing raw literal %q:\n%s", want, out)
	}
	// Arg COUNT only: the label shows the number, never the arg values.
	if want := i18n.T("tui.mcpDetailArgs") + ": 1"; !strings.Contains(out, want) {
		t.Errorf("detail missing arg count %q:\n%s", want, out)
	}

	// No VALUE may ever reach the screen…
	for _, secret := range []string{
		"env-secret-hunter2", "zz-secret-opt",
		"header-secret-bearer", "trace-secret-value",
	} {
		if strings.Contains(out, secret) {
			t.Errorf("rendered detail leaks the value %q:\n%s", secret, out)
		}
	}
	// …and neither may the arg values behind the count.
	if strings.Contains(out, "--stdio-flag") {
		t.Errorf("rendered detail shows an arg value:\n%s", out)
	}
}

// TestMCPDetail_ResetModalClearsStoredInventory: reopening /mcp (which
// resets the modal) must drop the snapshot, so a stale detail can never
// render after the reset — it falls back to the list instead.
func TestMCPDetail_ResetModalClearsStoredInventory(t *testing.T) {
	paths := mcpFixturePaths(t)
	m := &Model{modalMode: ModalMCP}
	m.loadMCPListWith(paths)
	if !m.mcpInventoryValid || len(m.mcpInventory.ByName) == 0 {
		t.Fatalf("precondition: valid=%v byName=%d, want a stored inventory",
			m.mcpInventoryValid, len(m.mcpInventory.ByName))
	}
	m.mcpDetailName = "alpha"

	m.resetModal(ModalMCP)

	if m.mcpInventoryValid {
		t.Error("resetModal left mcpInventoryValid = true")
	}
	if len(m.mcpInventory.ByName) != 0 || len(m.mcpInventory.Winner) != 0 {
		t.Errorf("resetModal left inventory data behind: ByName=%d Winner=%d",
			len(m.mcpInventory.ByName), len(m.mcpInventory.Winner))
	}
	if m.mcpDetailName != "" {
		t.Errorf("resetModal left mcpDetailName = %q", m.mcpDetailName)
	}

	// A detail somehow opened after the reset renders the list, not stale data.
	m.modalMode = ModalMCPDetail
	_ = ansi.Strip(m.renderMCPDetail())
	if m.modalMode != ModalMCP {
		t.Errorf("stale detail rendered instead of falling back: mode = %v", m.modalMode)
	}
}

// ── 8. MENOR-1: an invalid row is never toggleable ────────────────────
//
// The API's `enabled` means SET; the TUI computes `enabled := effective ==
// "enabled"` and passes it as SetDisabled's `disabled` flag — a FLIP. For
// an invalid row that flip either LIES (the writer no-ops and the UI would
// report a state change that never happened) or, when the broken entry
// literally carries `"disabled": false`, SPLICES bytes of an entry that is
// already broken. The rule, shared with the WebUI: invalid ⇒ no write,
// point the user at the file (edit the JSON / raw editor).

// TestMCPToggle_InvalidRowPerformsNoWriteAndShowsHint drives the space/t
// path (handleMCPKey → mcpToggleCmd → mcpToggleCmdFor; the routing is
// pinned in TestHandleMCPKey_SpaceTogglesSelectedName) on an invalid row:
// the cmd must refuse WITHOUT writing, and the refusal must surface as the
// localized "fix it in the file" hint through the EXISTING result path
// (mcpToggleResultMsg → Update → handleMCPToggleResult), not as success.
func TestMCPToggle_InvalidRowPerformsNoWriteAndShowsHint(t *testing.T) {
	cases := []struct{ name, doc string }{
		{"plain invalid entry", `{"mcpServers":{"broken":{"description":"no transport configured"}}}`},
		// Invalid (summary.Disabled == false ⇒ derived verdict "invalid")
		// but the entry LITERALLY holds "disabled": false — planEnable
		// splices that member out, i.e. the flip really writes into an
		// already-invalid entry today.
		{"invalid entry with disabled:false key", `{"mcpServers":{"broken":{"description":"no transport","disabled":false}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			paths := mcp.Paths{
				LeleDir:        filepath.Join(root, "global"),
				AgentWorkspace: filepath.Join(root, "agent"),
				Cwd:            filepath.Join(root, "proj"),
			}
			mcpWriteDoc(t, paths.LeleDir, tc.doc)
			file := filepath.Join(paths.LeleDir, "mcp.json")
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			// The verdict must really be invalid, or the test would prove
			// nothing about the rule it pins.
			if got := mcp.ReadInventory(paths).Effective["broken"]; got != "invalid" {
				t.Fatalf("fixture verdict = %q, want invalid", got)
			}

			got := mcpToggleCmdFor(paths, "broken")()
			res, ok := got.(mcpToggleResultMsg)
			if !ok {
				t.Fatalf("cmd returned %T, want mcpToggleResultMsg", got)
			}
			if !errors.Is(res.err, errMCPInvalid) {
				t.Errorf("err = %v, want errMCPInvalid (refusal, no write)", res.err)
			}

			after, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("re-read fixture: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("invalid row was written:\nbefore=%s\nafter=%s", before, after)
			}

			// The refusal travels the existing result route: Update must
			// hand it to handleMCPToggleResult, which renders the hint.
			m := &Model{chatInput: textarea.New()}
			_, _ = m.Update(res)
			want := fmt.Sprintf(i18n.T("tui.mcpToggleInvalid"), "broken")
			if m.mcpFeedback != want {
				t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
			}
		})
	}
}

// TestMCPToggle_EnabledAndDisabledRowsStillWrite pins that the invalid-row
// refusal changed nothing for the two legitimate toggles: an enabled row
// flips to disabled (writes "disabled": true) and a disabled row flips to
// enabled (the key is deleted). The layer-ownership assertions live in
// TestMCPToggleCmdFor_WritesOwningLayerOnly, which is untouched.
func TestMCPToggle_EnabledAndDisabledRowsStillWrite(t *testing.T) {
	cases := []struct {
		name, target, doc string
		wantEnabled       bool
		wantState         string
	}{
		{
			name:        "enabled row is written to disabled",
			target:      "on",
			doc:         `{"mcpServers":{"on":{"command":"a"}}}`,
			wantEnabled: false,
			wantState:   "disabled",
		},
		{
			name:        "disabled row is written to enabled",
			target:      "off",
			doc:         `{"mcpServers":{"off":{"command":"b","disabled":true}}}`,
			wantEnabled: true,
			wantState:   "enabled",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			paths := mcp.Paths{
				LeleDir:        filepath.Join(root, "global"),
				AgentWorkspace: filepath.Join(root, "agent"),
				Cwd:            filepath.Join(root, "proj"),
			}
			mcpWriteDoc(t, paths.LeleDir, tc.doc)
			file := filepath.Join(paths.LeleDir, "mcp.json")
			before, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			got := mcpToggleCmdFor(paths, tc.target)()
			res, ok := got.(mcpToggleResultMsg)
			if !ok {
				t.Fatalf("cmd returned %T, want mcpToggleResultMsg", got)
			}
			if res.err != nil {
				t.Fatalf("toggle failed: %v", res.err)
			}
			if !res.changed {
				t.Error("changed = false, want true (this toggle must write)")
			}
			if res.enabled != tc.wantEnabled {
				t.Errorf("enabled = %v, want %v", res.enabled, tc.wantEnabled)
			}

			after, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("re-read fixture: %v", err)
			}
			if bytes.Equal(before, after) {
				t.Fatalf("file unchanged: %s", before)
			}

			// The verdict really flipped (the write is the real thing, not
			// a cosmetic byte change).
			if got := mcp.ReadInventory(paths).Effective[tc.target]; got != tc.wantState {
				t.Errorf("verdict after toggle = %q, want %q", got, tc.wantState)
			}
		})
	}
}

// TestMCPToggleResult_NoChangeRendersNoChangeMessage: a result reporting
// changed==false must say "no change" instead of wearing the success line
// for a state it never reached. Driven through the same Update routing as
// TestMCPToggleResult_RoutedByUpdateWithoutLeakingIntoInput (extend, don't
// fork the result path).
func TestMCPToggleResult_NoChangeRendersNoChangeMessage(t *testing.T) {
	m := &Model{chatInput: textarea.New()}
	m.chatInput.SetValue("typed")

	_, _ = m.Update(mcpToggleResultMsg{name: "svc", enabled: true, changed: false})

	want := fmt.Sprintf(i18n.T("tui.mcpToggleNoChange"), "svc")
	if m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}
	// It must not be the success line for the state it did NOT reach.
	lying := fmt.Sprintf(i18n.T("tui.mcpToggleSuccess"), "svc", i18n.T("tui.mcpEnabled"))
	if m.mcpFeedback == lying {
		t.Errorf("no-op rendered as success: %q", m.mcpFeedback)
	}
	// Same routing guarantee as the test it extends: no leak into input.
	if got := m.chatInput.Value(); got != "typed" {
		t.Errorf("text input mutated by the msg: %q", got)
	}
}

// TestMCPDetail_InvalidRowShowsFixHint: the detail of an invalid server
// carries the one-line hint telling the user toggling is off and the file
// must be fixed (edit the JSON / raw editor) — the same rule the WebUI
// shows for the same row.
func TestMCPDetail_InvalidRowShowsFixHint(t *testing.T) {
	root := t.TempDir()
	paths := mcp.Paths{
		LeleDir:        filepath.Join(root, "global"),
		AgentWorkspace: filepath.Join(root, "agent"),
		Cwd:            filepath.Join(root, "proj"),
	}
	mcpWriteDoc(t, paths.LeleDir,
		`{"mcpServers":{"broken":{"description":"no transport configured"}}}`)

	m := &Model{modalMode: ModalMCP, width: 140, height: 40}
	m.loadMCPListWith(paths)
	if _, handled := m.handleMCPKey(tea.KeyMsg{Type: tea.KeyEnter}); !handled || m.modalMode != ModalMCPDetail {
		t.Fatalf("enter did not open the detail: handled=%v mode=%v", handled, m.modalMode)
	}

	out := ansi.Strip(m.renderMCPDetail())
	if !strings.Contains(out, i18n.T("tui.mcpInvalidHint")) {
		t.Errorf("detail of an invalid row lacks the fix hint %q:\n%s",
			i18n.T("tui.mcpInvalidHint"), out)
	}
}

// ── 9. MENOR-3: the toggle applies the REST path guard ────────────────
//
// REST refuses to read/write a layer file outside the allowed workspace
// roots with 403 mcp_path_not_allowed (rest_mcp.go mcpGuardLayerFile,
// backed by channels.isAllowedWorkspacePath). The TUI must refuse the SAME
// path BEFORE any write, with the same rule (one predicate, exported from
// pkg/channels — never a copy).

// TestMCPToggleCmdFor_RefusesTargetOutsideAllowedRoots: a target outside
// home//tmp//var/folders//cwd is refused with no bytes written and a
// refusal message naming the path; an in-root target still toggles.
func TestMCPToggleCmdFor_RefusesTargetOutsideAllowedRoots(t *testing.T) {
	// /var/tmp is world-writable FHS temp but is NOT one of the roots the
	// predicate accepts (it must not match the "/tmp/" prefix — check:
	// "/var/tmp/…" starts with "/var…", not "/tmp/").
	probe, err := os.MkdirTemp("/var/tmp", "lele-tui-guard-*")
	if err != nil {
		// Honest failure, not a skip: without a path the predicate rejects,
		// the refusal cannot be exercised on this box at all.
		t.Fatalf("cannot create a probe dir outside the allowed roots: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(probe) })

	// Sanity: if the predicate accepts the probe, the refusal assertions
	// below would prove nothing.
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

	got := mcpToggleCmdFor(paths, "svc")()
	res, ok := got.(mcpToggleResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want mcpToggleResultMsg", got)
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

	// The refusal renders through the existing result handler and names
	// the path (mirroring REST's mcp_path_not_allowed detail).
	m := &Model{chatInput: textarea.New()}
	_, _ = m.Update(res)
	want := fmt.Sprintf(i18n.T("tui.mcpPathNotAllowed"), "svc", file)
	if m.mcpFeedback != want {
		t.Errorf("feedback = %q, want %q", m.mcpFeedback, want)
	}

	t.Run("in-root target still works", func(t *testing.T) {
		paths2 := mcp.Paths{
			LeleDir:        filepath.Join(root, "inroot"),
			AgentWorkspace: filepath.Join(root, "agent"),
			Cwd:            filepath.Join(root, "proj"),
		}
		mcpWriteDoc(t, paths2.LeleDir, `{"mcpServers":{"svc":{"command":"x"}}}`)
		file2 := filepath.Join(paths2.LeleDir, "mcp.json")
		before2, err := os.ReadFile(file2)
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}

		got := mcpToggleCmdFor(paths2, "svc")()
		res, ok := got.(mcpToggleResultMsg)
		if !ok {
			t.Fatalf("cmd returned %T, want mcpToggleResultMsg", got)
		}
		if res.err != nil {
			t.Fatalf("in-root toggle refused: %v", res.err)
		}
		if !res.changed {
			t.Error("changed = false, want true (in-root toggle writes)")
		}
		after2, err := os.ReadFile(file2)
		if err != nil {
			t.Fatalf("re-read fixture: %v", err)
		}
		if bytes.Equal(before2, after2) {
			t.Errorf("in-root file unchanged: %s", before2)
		}
	})
}
