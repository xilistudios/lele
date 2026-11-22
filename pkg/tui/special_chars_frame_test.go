package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/xilistudios/lele/pkg/providers"
)

const scMsgA = "✅ Tarea A completada — wrapText y selection.go ahora miden " +
	"por grapheme clusters ( uniseg ), 635 tests en verde, sin tocar el " +
	"working tree fuera de los 3 archivos asignados. Espero a la tarea B:"

const scMsgD = "> Task B is still running. Let me wait again.\n\n" +
	"La tarea B sigue en curso (la corrección del separador propiamente dicha). Sigo esperando:"

const scFinishedSummary = "Subagent task subagent-3 finished."

const scSpecialChars = "keycap 1️⃣ 2️⃣ zwj 👨‍ vs16 ❤️ ☕️ flag 🇪🇸 " +
	"cjk \u65e5\u672c\u8a9e\u30c6\u30b9\u30c8 em—dash → │ tab[\t] cr[mid\rline] " +
	"ansi \x1b[31mred\x1b[0m end"

const scBidiChars = "bidi \u202Ereversed\u202C zwsp [\u200B] " +
	"bom [\uFEFF] soft\u00ADhyphen lrm [\u200E]"

var scToolResultFrags = []string{
	"Timed out",
	"600 seconds",
	"status:",
	"running)",
	"waiting for",
	"after 600",
	"Subagent task",
	"subagent-3 fin",
	"ished.",
}

func seedSpecialCharsSession(t *testing.T, m *Model, specialAsUser bool) {
	t.Helper()
	const key = "tui:chat:special-frame"

	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	if specialAsUser {
		m.sessionMgr.AddMessage(key, "user", scSpecialChars+" "+scBidiChars)
		m.sessionMgr.AddMessage(key, "assistant", scMsgA)
	} else {
		m.sessionMgr.AddMessage(key, "assistant", scMsgA)
	}

	m.sessionMgr.AddFullMessage(key, providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{{
			ID:   "call-wait-1",
			Name: "wait_for_subagent",
			Arguments: map[string]interface{}{
				"task_id":         "subagent-3",
				"timeout_seconds": 600,
			},
		}},
	})

	m.sessionMgr.AddMessage(key, "tool", timedOutSummary)
	m.sessionMgr.AddMessage(key, "assistant", scMsgD)
	m.sessionMgr.AddMessage(key, "tool", scFinishedSummary)
	if !specialAsUser {
		m.sessionMgr.AddMessage(key, "assistant", scSpecialChars)
	}

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
}

func newSpecialCharsModel(t *testing.T, width, height int, specialAsUser bool) *Model {
	t.Helper()

	m := newTestModel(t)
	seedSpecialCharsSession(t, m, specialAsUser)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m2, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update(WindowSizeMsg) returned %T, want *Model", updated)
	}
	*m = *m2

	if m.width != width || m.height != height {
		t.Fatalf("WindowSizeMsg not applied: got %dx%d, want %dx%d", m.width, m.height, width, height)
	}
	m.View()

	if m.showWelcome {
		t.Fatal("expected chat layout after seeding, got welcome screen")
	}
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 {
		t.Fatalf("viewport not sized after render: Width=%d Height=%d", m.viewport.Width, m.viewport.Height)
	}
	return m
}

func dumpRenderedWidths(t *testing.T, tag string, lines []string, budget int) {
	t.Helper()
	var b strings.Builder
	for i, line := range lines {
		w := ansi.StringWidth(line)
		marker := ""
		if w > budget {
			marker = "   <== OVER-WIDE (cellbuf.Wrap will re-wrap this row)"
		}
		fmt.Fprintf(&b, "%s[%03d] width=%3d budget=%3d%s %q\n", tag, i, w, budget, marker, line)
	}
	t.Log(b.String())
}

func assertExactFrameGeometry(t *testing.T, frame string, width, height int) {
	t.Helper()
	lines := strings.Split(frame, "\n")
	if len(lines) != height {
		t.Errorf("frame has %d lines, want exactly %d (terminal height)", len(lines), height)
	}
	bad := false
	for i, line := range lines {
		if w := ansi.StringWidth(line); w != width {
			t.Errorf("frame line %d width = %d, want exactly %d: %q", i, w, width, line)
			bad = true
		}
	}
	if bad || len(lines) != height {
		t.Logf("full frame (%dx%d):\n%q", width, height, frame)
	}
}

func assertViewportLineWidths(t *testing.T, m *Model) []string {
	t.Helper()

	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	base := m.buildRenderedHistoryLines(history)

	overWide := false
	check := func(tag string, lines []string) {
		for i, line := range lines {
			if w := ansi.StringWidth(line); w > m.viewport.Width {
				t.Errorf("%s line %d over-wide: ansi.StringWidth=%d > viewport.Width=%d: %q",
					tag, i, w, m.viewport.Width, line)
				overWide = true
			}
		}
	}
	check("base", base)
	check("overlay", m.viewport.overlayLines)

	if overWide {
		dumpRenderedWidths(t, "base", base, m.viewport.Width)
		dumpRenderedWidths(t, "overlay", m.viewport.overlayLines, m.viewport.Width)
	}
	return base
}

func scHasToolResultFrag(s string) bool {
	for _, frag := range scToolResultFrags {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

func assertLabelPrefixIntegrity(t *testing.T, tag string, lines []string) {
	t.Helper()
	for i, line := range lines {
		plain := ansi.Strip(line)
		if plain == "" || !scHasToolResultFrag(plain) {
			continue
		}
		if strings.HasPrefix(plain, "  → ") || strings.HasPrefix(plain, "    ") {
			continue
		}
		t.Errorf("%s[%d]: tool-result row lost its '  → ' label / 4-cell padding "+
			"(lineViewport re-wrapped an over-wide composed row): %q", tag, i, plain)
	}
}

var scSGRSeqRe = regexp.MustCompile(`\x1b\[[0-9;?:]*m`)

func assertNoControlLeaks(t *testing.T, tag string, lines []string) {
	t.Helper()
	for i, line := range lines {
		if strings.ContainsRune(line, '\r') {
			t.Errorf("%s[%d]: raw CR survives into the frame (paints from column 0, destroying borders): %q",
				tag, i, line)
		}
		if strings.ContainsRune(line, '\t') {
			t.Errorf("%s[%d]: raw TAB survives into the frame (terminal paints a variable gap, measured 0 cells): %q",
				tag, i, line)
		}
		if rest := scSGRSeqRe.ReplaceAllString(line, ""); strings.ContainsRune(rest, '\x1b') {
			t.Errorf("%s[%d]: non-SGR escape sequence survives into the frame: %q", tag, i, line)
		}
	}
}

var scBidiCodepoints = []rune{
	'\u00AD', '\u200B', '\u200E', '\u200F',
	'\u202A', '\u202B', '\u202C', '\u202D', '\u202E',
	'\u2060', '\u2066', '\u2067', '\u2068', '\u2069', '\uFEFF',
}

func assertNoBidiLeaks(t *testing.T, tag string, lines []string) {
	t.Helper()
	for i, line := range lines {
		for _, r := range scBidiCodepoints {
			if strings.ContainsRune(line, r) {
				t.Errorf("%s[%d]: bidi/zero-width format char U+%04X survives into the frame "+
					"(paints out of sync with measured width): %q", tag, i, r, line)
			}
		}
	}
}

func assertViewportWindowIntegrity(t *testing.T, m *Model) {
	t.Helper()

	orig := m.viewport.YOffset
	defer func() {
		m.viewport.YOffset = orig
		m.viewport.clampOffset()
	}()

	if m.viewport.Height <= 0 {
		t.Fatalf("viewport height not positive: %d", m.viewport.Height)
	}
	maxOff := m.viewport.maxYOffset()
	off := 0
	for {
		m.viewport.YOffset = off
		rows := strings.Split(m.viewport.View(), "\n")
		if len(rows) != m.viewport.Height {
			t.Errorf("viewport.View() @offset %d returned %d rows, want Height=%d",
				off, len(rows), m.viewport.Height)
		}
		for i, row := range rows {
			if w := ansi.StringWidth(row); w > m.viewport.Width {
				t.Errorf("viewport row %d @offset %d over-wide: %d > %d: %q",
					i, off, w, m.viewport.Width, row)
			}
		}
		tag := fmt.Sprintf("viewport@%d", off)
		assertLabelPrefixIntegrity(t, tag, rows)
		assertNoControlLeaks(t, tag, rows)

		if off >= maxOff {
			break
		}
		off += m.viewport.Height
		if off > maxOff {
			off = maxOff
		}
	}
}

func assertSidebarBorderColumn(t *testing.T, frame string, width, height int) {
	t.Helper()

	leftWidth := int(float64(width) * leftColumnRatio)
	rightWidth := width - leftWidth - chatSidebarGutter - 3
	if rightWidth < 1 {
		rightWidth = 1
	}
	contentW := leftWidth + chatSidebarGutter + rightWidth + 1
	leftPad := (width - contentW) / 2
	borderCol := leftPad + leftWidth + chatSidebarGutter
	if borderCol >= width {
		t.Fatalf("layout math broken: borderCol=%d >= width=%d (contentW=%d)",
			borderCol, width, contentW)
	}

	rows := strings.Split(frame, "\n")
	bad := false
	for i, row := range rows {
		head := ansi.Strip(ansi.Truncate(row, borderCol+1, ""))
		if !strings.HasSuffix(head, "│") {
			t.Errorf("frame line %d: sidebar border '│' missing at column %d "+
				"(row content before it: %q)", i, borderCol, head)
			bad = true
		}
	}
	if bad {
		t.Logf("full frame (%dx%d, borderCol=%d):\n%q", width, height, borderCol, frame)
	}
}

func TestSpecialChars_FrameWidthInvariant(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	frame := m.View()

	assertExactFrameGeometry(t, frame, width, height)
	assertSidebarBorderColumn(t, frame, width, height)

	base := assertViewportLineWidths(t, m)
	dumpRenderedWidths(t, "base", base, m.viewport.Width)
	dumpRenderedWidths(t, "overlay", m.viewport.overlayLines, m.viewport.Width)

	assertLabelPrefixIntegrity(t, "base", base)
	assertLabelPrefixIntegrity(t, "overlay", m.viewport.overlayLines)
	assertViewportWindowIntegrity(t, m)

	assertNoControlLeaks(t, "base", base)
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
}

func TestSpecialChars_GeometryTable(t *testing.T) {
	widths := []int{80, 100, 120, 140, 200}
	heights := []int{24, 40, 60}

	for _, width := range widths {
		for _, height := range heights {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				m := newSpecialCharsModel(t, width, height, false)

				frame := m.View()
				assertExactFrameGeometry(t, frame, width, height)
				assertSidebarBorderColumn(t, frame, width, height)

				base := assertViewportLineWidths(t, m)
				assertLabelPrefixIntegrity(t, "base", base)
				assertLabelPrefixIntegrity(t, "overlay", m.viewport.overlayLines)
				assertViewportWindowIntegrity(t, m)

				assertNoControlLeaks(t, "base", base)
				assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
			})
		}
	}
}

func TestSpecialChars_UserMessageVariant(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, true)

	frame := m.View()
	assertExactFrameGeometry(t, frame, width, height)
	assertSidebarBorderColumn(t, frame, width, height)

	base := assertViewportLineWidths(t, m)
	assertLabelPrefixIntegrity(t, "base", base)
	assertLabelPrefixIntegrity(t, "overlay", m.viewport.overlayLines)
	assertViewportWindowIntegrity(t, m)

	assertNoControlLeaks(t, "base", base)
	assertNoBidiLeaks(t, "base", base)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)
}

func TestSpecialChars_StreamingOverlayVariant(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	m.processing = true
	m.currentStream = scSpecialChars + "\nsegunda línea 👨‍ streaming parcial"
	m.currentToolAction = "wait_for_subagent: task_id=subagent-3, timeout_seconds=600"
	m.forceGotoBottom = true
	m.updateViewport()

	if len(m.viewport.overlayLines) == 0 {
		t.Fatal("expected non-empty overlay while streaming")
	}

	frame := m.View()
	assertExactFrameGeometry(t, frame, width, height)
	assertSidebarBorderColumn(t, frame, width, height)

	base := assertViewportLineWidths(t, m)
	assertLabelPrefixIntegrity(t, "base", base)
	assertLabelPrefixIntegrity(t, "overlay", m.viewport.overlayLines)
	assertViewportWindowIntegrity(t, m)

	assertNoControlLeaks(t, "base", base)
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
}

func TestSpecialChars_SubagentProgressOverWide(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	m.showWelcome = true
	m.processing = true
	m.subagentProgress = map[string]string{
		"subagent-1": "exec: " + strings.Repeat("a", 400) + " \u202E bidi \u202C \x1b[2J",
	}
	m.updateViewport()

	if len(m.viewport.overlayLines) == 0 {
		t.Fatal("expected subagent progress rows in the overlay (sidebar hidden)")
	}

	for i, line := range m.viewport.overlayLines {
		if w := ansi.StringWidth(line); w > m.viewport.Width {
			t.Errorf("overlay line %d over-wide: ansi.StringWidth=%d > viewport.Width=%d: %q",
				i, w, m.viewport.Width, line)
		}
	}
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)

	frame := m.View()
	assertExactFrameGeometry(t, frame, width, height)
	assertViewportWindowIntegrity(t, m)
}

var scToolActionPayload = "exec: rm -rf \x1b[2J \u202Ebidi\u202C " + strings.Repeat("x", 300)

func TestSpecialChars_CurrentToolAction(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	m.processing = true
	m.currentToolAction = scToolActionPayload
	m.forceGotoBottom = true
	m.updateViewport()

	if len(m.viewport.overlayLines) == 0 {
		t.Fatal("expected tool-action row in the overlay")
	}

	assertViewportLineWidths(t, m)
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)

	frame := m.View()
	assertExactFrameGeometry(t, frame, width, height)
}

func TestSpecialChars_GroupTurnHeader(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	m.groupTranscripts = map[string][]groupTurn{
		"g1": {{
			index:   0,
			layer:   0,
			speaker: "speaker",
			label:   "\u202Ex\u202C \x1b[2J " + strings.Repeat("g", 300),
			role:    "agent",
		}},
	}
	m.activeGroupID = "g1"
	m.forceGotoBottom = true
	m.updateViewport()

	if len(m.viewport.overlayLines) == 0 {
		t.Fatal("expected group-turn rows in the overlay")
	}

	base := assertViewportLineWidths(t, m)
	assertNoControlLeaks(t, "base", base)
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
	assertNoBidiLeaks(t, "base", base)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)

	hdrIdx := -1
	for i, line := range m.viewport.overlayLines {
		if strings.Contains(ansi.Strip(line), "┌") {
			hdrIdx = i
			break
		}
	}
	if hdrIdx < 0 {
		t.Fatalf("no group header row (┌) found in overlay: %q", m.viewport.overlayLines)
	}
	for i := hdrIdx + 1; i < len(m.viewport.overlayLines); i++ {
		plain := ansi.Strip(m.viewport.overlayLines[i])
		if strings.TrimSpace(plain) == "" {
			break
		}
		if !strings.HasPrefix(plain, " ") {
			t.Errorf("overlay[%d]: header continuation row is naked (no indent): %q", i, plain)
		}
	}
}

func TestSpecialChars_CompactFeedback(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	m.compactFeedback = "compacted \x1b[2J \u202Ez\u202C"
	m.forceGotoBottom = true
	m.updateViewport()

	if len(m.viewport.overlayLines) == 0 {
		t.Fatal("expected compactFeedback row in the overlay")
	}
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)
}

func TestSpecialChars_SessionsModalItems(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	m.resetModal(ModalSessions)
	m.modalItems = []string{"> session \x1b[2J \u202Es\u202C " + strings.Repeat("n", 200)}

	frame := m.View()

	assertExactFrameGeometry(t, frame, width, height)
	lines := strings.Split(frame, "\n")
	assertNoControlLeaks(t, "modal frame", lines)
	assertNoBidiLeaks(t, "modal frame", lines)
}

func TestLineViewport_OverWideRowTruncatedNotRewrapped(t *testing.T) {
	v := newLineViewport(40, 5)
	overWide := "  → │ Timed out waiting for subagent subagent-3 after 600 seconds (current status: running)"
	v.SetBaseLines([]string{overWide, "short line"})

	rows := strings.Split(v.View(), "\n")
	if len(rows) != 5 {
		t.Fatalf("View() returned %d rows, want Height=5 (a re-wrap added rows): %q", len(rows), rows)
	}
	for i, row := range rows {
		if w := ansi.StringWidth(row); w > 40 {
			t.Errorf("row %d over-wide: %d > 40: %q", i, w, row)
		}
	}
	if !strings.HasPrefix(ansi.Strip(rows[0]), "  → │ Timed out waiting for") {
		t.Errorf("row 0 lost its prefix (re-wrap happened): %q", rows[0])
	}
	if strings.Contains(ansi.Strip(rows[1]), "after 600") {
		t.Errorf("row 1 holds wrapped tail content (re-wrap happened): %q", rows[1])
	}
}
