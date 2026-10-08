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
	"github.com/xilistudios/lele/pkg/agent"
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
