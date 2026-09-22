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

// Frame-level regression tests for special characters in the transcript.
//
// Bug report (screenshot): a tool-result box ("→ │ Timed out waiting for
// subagent subagent-3 after 600 seconds (current status: running)")
// re-wrapped mid-token at the hyphen, the continuation row lost its "  → "
// label and box border (background starting at column 0), an orphan "│"
// fragment floated alone, and the sidebar left border stopped mid-frame and
// reappeared only at the input row.
//
// Confirmed mechanism: lineViewport.View() (pkg/tui/lineviewport.go) renders
// every base/overlay line through the cached viewStyle
// (lipgloss.NewStyle().Width(w)...), and lipgloss v1.1.1's Width() pass calls
// cellbuf.Wrap(str, w, "") — cellbuf treats '-' as an unconditional
// breakpoint. ANY composed line whose ansi.StringWidth exceeds
// viewport.Width is therefore re-wrapped AT RENDER TIME: the "  → " label and
// box border stay on the first row, the continuation row starts flush-left
// (naked), and the added rows shift everything below inside the fixed-height
// window.
//
// The invariant these tests pin:
//
//	(1) m.View() is exactly height rows of exactly width cells;
//	(2) every line handed to the line viewport — buildRenderedHistoryLines
//	    base lines AND overlay lines — has ansi.StringWidth <= viewport.Width,
//	    so cellbuf.Wrap is a no-op at render time;
//	(3) no rendered tool-result row exists without the 4-cell label prefix
//	    (a naked continuation row means a re-wrap happened somewhere);
//	(4) the sidebar left border '│' sits at its computed column on every
//	    frame row (the layout proxy for the "border stops mid-frame" symptom).
//
// A supplementary control-character scan (5) asserts no raw CR/TAB/non-SGR
// escape survives into the frame: those paint differently than they measure
// (cursor jumps, variable tab gaps, erase sequences) and corrupt the frame
// even when the measured width invariants hold.

// scMsgA mirrors screenshot message (i): assistant status line with emoji,
// em-dash and accented text.
const scMsgA = "✅ Tarea A completada — wrapText y selection.go ahora miden " +
	"por grapheme clusters ( uniseg ), 635 tests en verde, sin tocar el " +
	"working tree fuera de los 3 archivos asignados. Espero a la tarea B:"

// scMsgD mirrors screenshot message (iv): a blockquote followed by prose.
const scMsgD = "> Task B is still running. Let me wait again.\n\n" +
	"La tarea B sigue en curso (la corrección del separador propiamente dicha). Sigo esperando:"

// scFinishedSummary mirrors screenshot message (v) — exact tool result text.
const scFinishedSummary = "Subagent task subagent-3 finished."

// scSpecialChars packs every special-character class from the bug report
// into one assistant/user message: keycap sequences ("1️⃣ 2️⃣"), an
// INCOMPLETE ZWJ sequence ("👨‍"), VS16 emoji ("❤️ ☕️"), a flag ("🇪🇸"),
// CJK, an em-dash, arrows ("→ │"), a tab, a stray CR mid-line, and a raw
// ANSI CSI SGR pair inside the text.
const scSpecialChars = "keycap 1️⃣ 2️⃣ zwj 👨‍ vs16 ❤️ ☕️ flag 🇪🇸 " +
	"cjk \u65e5\u672c\u8a9e\u30c6\u30b9\u30c8 em—dash → │ tab[\t] cr[mid\rline] " + // 日本語テスト (Han/Kana, gosmopolitan)
	"ansi \x1b[31mred\x1b[0m end"

// scBidiChars packs the bidi/zero-width FORMAT (Cf) payloads — the REAL
// width-desync risk on the user path: they measure 0 cells (so every
// width/geometry invariant cannot catch them) yet the terminal reorders or
// ambiguously repaints text around them, drifting painted columns away from
// ansi.StringWidth. RLO/PDF (U+202E/U+202C), ZWSP (U+200B), BOM (U+FEFF),
// soft hyphen (U+00AD), LRM (U+200E). \u escapes keep the invisible
// codepoints visible in source (literal Cf runes would be unreviewable).
const scBidiChars = "bidi \u202Ereversed\u202C zwsp [\u200B] " +
	"bom [\uFEFF] soft\u00ADhyphen lrm [\u200E]"

// scToolResultFrags are distinctive fragments of the two tool-result
// summaries (timedOutSummary + scFinishedSummary). A rendered row that
// contains one of these fragments must be a proper tool-result box row: it
// either starts with the "  → " label (row 0) or with exactly 4 cells of
// padding (continuation rows). None of these fragments appear in any other
// injected message or in the compact tool-call row
// ("wait_for_subagent: task_id=subagent-3, timeout_seconds=600"), so the
// scan has no false positives on this conversation.
var scToolResultFrags = []string{
	"Timed out",      // summary A, survives almost any mid-token split
	"600 seconds",    // summary A tail
	"status:",        // summary A tail
	"running)",       // summary A end
	"waiting for",    // summary A head
	"after 600",      // summary A middle
	"Subagent task",  // summary B head
	"subagent-3 fin", // summary B middle (split of "subagent-3 finished")
	"ished.",         // summary B tail
}

// seedSpecialCharsSession injects the conversation that mirrors the bug
// report screenshot into a fresh session and points the model at it:
//
//	(i)   assistant scMsgA
//	(ii)  assistant with a wait_for_subagent tool call (subagent-3, 600s)
//	(iii) tool result timedOutSummary (exact screenshot text)
//	(iv)  assistant scMsgD
//	(v)   tool result scFinishedSummary
//	(vi)  assistant scSpecialChars  (or leading USER msg when specialAsUser)
func seedSpecialCharsSession(t *testing.T, m *Model, specialAsUser bool) {
	t.Helper()
	const key = "tui:chat:special-frame"

	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")

	if specialAsUser {
		// Append the bidi/zero-width Cf payload: the user-variant test scans
		// the rendered base lines for exactly these codepoints, pinning that
		// the USER ingress path sanitizes them (not that lipgloss drops them).
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

// newSpecialCharsModel builds the model (same helper pattern as
// newTestModel/seedLazySession), injects the screenshot conversation, sends
// tea.WindowSizeMsg, and primes one render so viewport dimensions and the
// base/overlay caches are built at the final width.
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
	m.View() // prime render caches at the final width

	if m.showWelcome {
		t.Fatal("expected chat layout after seeding, got welcome screen")
	}
	if m.viewport.Width <= 0 || m.viewport.Height <= 0 {
		t.Fatalf("viewport not sized after render: Width=%d Height=%d", m.viewport.Width, m.viewport.Height)
	}
	return m
}

// dumpRenderedWidths prints each rendered line with its ansi.StringWidth
// against the viewport budget, tagging any line wider than the budget with a
// marker so the offending producer is easy to spot in -v output.
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

// assertExactFrameGeometry enforces invariant (1): m.View() returns exactly
// height lines, every one exactly width display cells. On violation the full
// frame is dumped with %q.
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

// assertViewportLineWidths enforces invariant (2): every base line produced
// by buildRenderedHistoryLines AND every overlay line has
// ansi.StringWidth <= m.viewport.Width, i.e. cellbuf.Wrap inside
// lineViewport.View() is a no-op. Returns the base lines for reuse by the
// other assertions; on violation both buffers are dumped with widths.
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

// scHasToolResultFrag reports whether s carries a distinctive fragment of a
// tool-result summary (see scToolResultFrags).
func scHasToolResultFrag(s string) bool {
	for _, frag := range scToolResultFrags {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

// assertLabelPrefixIntegrity enforces invariant (3): every rendered row that
// belongs to a ToolResultBox block (identified by a stripped-content fragment
// of the injected tool summaries) must start with either the styled "  → "
// label or exactly 4 cells of padding. A row with tool-result content and no
// prefix is the mid-token re-wrap symptom: lineViewport re-wrapped an
// over-wide composed row at render time.
func assertLabelPrefixIntegrity(t *testing.T, tag string, lines []string) {
	t.Helper()
	for i, line := range lines {
		plain := ansi.Strip(line)
		if plain == "" || !scHasToolResultFrag(plain) {
			continue
		}
		if strings.HasPrefix(plain, "  → ") || strings.HasPrefix(plain, "    ") {
			continue // row 0 with label, or continuation with 4-cell padding
		}
		t.Errorf("%s[%d]: tool-result row lost its '  → ' label / 4-cell padding "+
			"(lineViewport re-wrapped an over-wide composed row): %q", tag, i, plain)
	}
}

// scSGRSeqRe matches a complete SGR escape (CSI with digits/semicolons/colons
// and final 'm') — the only escape sequence lipgloss/glamour legitimately
// emit into rendered lines.
var scSGRSeqRe = regexp.MustCompile(`\x1b\[[0-9;?:]*m`)

// assertNoControlLeaks enforces supplementary invariant (5): no raw CR, TAB
// or non-SGR escape sequence survives into a rendered line. Those characters
// paint differently than ansi.StringWidth measures them (cursor to column 0,
// variable tab gaps, erase/movement directives), corrupting the frame while
// every measured-width assertion still passes.
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

// scBidiCodepoints is the bidi/zero-width FORMAT (Cf) set the ingress
// sanitization must strip from user/LLM text: U+00AD SHY, U+200B ZWSP,
// U+200E/U+200F LRM/RLM, U+202A–U+202E embeddings/overrides (incl. the
// "trojan source" U+202E RLO), U+2060 word joiner, U+2066–U+2069 isolates,
// U+FEFF BOM. All measure 0 cells yet repaint/reorder the terminal row, so
// width invariants cannot catch them — only an ingress scan can.
var scBidiCodepoints = []rune{
	'\u00AD', '\u200B', '\u200E', '\u200F',
	'\u202A', '\u202B', '\u202C', '\u202D', '\u202E',
	'\u2060', '\u2066', '\u2067', '\u2068', '\u2069', '\uFEFF',
}

// assertNoBidiLeaks scans each rendered line for every scBidiCodepoints
// rune with strings.ContainsRune. It pins ingress sanitization (producer
// side): lipgloss' render pass cannot rescue these — Cf survives styling,
// measures 0 cells, and desyncs painted columns from measured widths.
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

// assertViewportWindowIntegrity sweeps the line viewport over every scroll
// offset and checks the rows lineViewport.View() actually paints: exact row
// count (Height), rows <= viewport.Width, label-prefix integrity and no
// control leaks. Sweeping is required because View() only paints one window
// of rows per call — the tool-result rows may sit above or below the initial
// scroll position.
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

// assertSidebarBorderColumn enforces invariant (4): the right sidebar's left
// border '│' is present at its computed column on EVERY frame row — the
// layout proxy for the "sidebar border stops mid-frame" symptom. The column
// mirrors the layout math of renderChatLayout + paintFrame's centered Place:
//
//	leftWidth = width*leftColumnRatio, rightWidth = width-leftWidth-gutter-3,
//	contentW  = leftWidth + gutter + (rightWidth+1 [border]),
//	leftPad   = floor((width-contentW)/2)   (lipgloss.Place Center),
//	borderCol = leftPad + leftWidth + gutter.
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

// TestSpecialChars_FrameWidthInvariant is the primary frame-level regression
// test for the screenshot bug at 140x40: full-frame geometry, over-wide base
// and overlay lines, tool-result label integrity in the base buffer and in
// every viewport window, control-character leaks, and the sidebar border
// column on every row.
func TestSpecialChars_FrameWidthInvariant(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	frame := m.View()

	// (1) exact frame geometry; (4) sidebar border column on every row.
	assertExactFrameGeometry(t, frame, width, height)
	assertSidebarBorderColumn(t, frame, width, height)

	// (2) no over-wide base/overlay line; dump annotated widths for -v.
	base := assertViewportLineWidths(t, m)
	dumpRenderedWidths(t, "base", base, m.viewport.Width)
	dumpRenderedWidths(t, "overlay", m.viewport.overlayLines, m.viewport.Width)

	// (3) tool-result rows keep their 4-cell label prefix — base, overlay,
	// and every scroll window of the rendered viewport.
	assertLabelPrefixIntegrity(t, "base", base)
	assertLabelPrefixIntegrity(t, "overlay", m.viewport.overlayLines)
	assertViewportWindowIntegrity(t, m)

	// (5) the assistant/tool paths sanitize content — no CR/TAB/non-SGR may
	// leak into the frame.
	assertNoControlLeaks(t, "base", base)
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
}

// TestSpecialChars_GeometryTable hunts a reproducing geometry: the same
// conversation at widths 80/100/120/140/200 crossed with heights 24/40/60.
// Narrow widths are the interesting case for the tool-result box: the
// budget math in renderToolResultBlock (width - label 4 - box 3) is what
// keeps the composed row inside the lineViewport wrap limit.
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

// TestSpecialChars_UserMessageVariant injects the same special-char message
// (plus the bidi/zero-width Cf payload) as a USER message. Why this test's
// control scan passed even before the user branch called sanitizeDisplayText:
// the width and control invariants hold on their own — controls measure 0
// cells, so wrapText still keeps every row inside the budget, and lipgloss'
// cellbuf render pass drops CR/TAB/ESC before the lines reach the frame.
// The real desync risk on this path is therefore NOT the controls but the
// bidi/zero-width Cf chars (U+00AD U+200B U+200E U+200F U+202A–U+202E
// U+2060 U+2066–U+2069 U+FEFF): they also measure 0 cells, but they survive
// styling and make the terminal reorder or ambiguously repaint the row, so
// painted columns drift from ansi.StringWidth. Those are pinned at ingress
// by assertNoBidiLeaks below, which scans the produced base lines for the
// exact codepoints instead of trusting the render pass.
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

	// Control invariants hold on their own (see the docstring: controls
	// measure 0 cells and lipgloss' render pass drops CR/TAB/ESC), so this
	// scan cannot distinguish a sanitized from an unsanitized producer.
	assertNoControlLeaks(t, "base", base)
	// The ingress-pinning scan: none of the bidi/zero-width Cf codepoints
	// may reach a rendered base line — only sanitizeDisplayText at the user
	// branch can guarantee that (lipgloss keeps Cf as-is).
	assertNoBidiLeaks(t, "base", base)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)
}

// TestSpecialChars_StreamingOverlayVariant exercises the overlay builder in
// updateViewport (pkg/tui/viewport.go): streaming lines and the executing
// tool-action row bypass the base-line cache and go straight through
// SetOverlayLines into lineViewport. The live stream carries the same
// special-char payload (renderSingleLine must sanitize it per line).
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

	// The streaming path sanitizes per line (renderSingleLine) — no leaks.
	assertNoControlLeaks(t, "base", base)
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
}

// TestSpecialChars_SubagentProgressOverWide pins the subagent-progress
// overlay row (model.go renderSubagentProgress). The action string is
// agent/LLM-controlled and here carries all three attack classes at once:
// an over-wide run (400+ cells — the old row construction never wrapped it,
// so lineViewport re-wrapped the composed row at render time and cascaded
// the frame: dropped indent, shifted rows), a bidi override (U+202E RLO /
// U+202C PDF), and a raw CSI erase (ESC[2J). The sidebar must be hidden
// for the inline block to render (same pattern as subagent_progress_test.go).
func TestSpecialChars_SubagentProgressOverWide(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	// Hide the sidebar so renderSubagentProgress contributes overlay rows:
	// the inline block is gated off while the sidebar lists subagent
	// statuses itself (isChatSidebarVisible).
	m.showWelcome = true
	m.processing = true
	m.subagentProgress = map[string]string{
		"subagent-1": "exec: " + strings.Repeat("a", 400) + " \u202E bidi \u202C \x1b[2J",
	}
	m.updateViewport()

	if len(m.viewport.overlayLines) == 0 {
		t.Fatal("expected subagent progress rows in the overlay (sidebar hidden)")
	}

	// (2) every overlay row fits the viewport budget — an over-wide row here
	// means lineViewport's cellbuf.Wrap would re-wrap it at paint time.
	for i, line := range m.viewport.overlayLines {
		if w := ansi.StringWidth(line); w > m.viewport.Width {
			t.Errorf("overlay line %d over-wide: ansi.StringWidth=%d > viewport.Width=%d: %q",
				i, w, m.viewport.Width, line)
		}
	}
	// (5) no control/bidi leaks from the unsanitized action text.
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)

	// (1) exact frame geometry of the full view, plus the swept viewport
	// window integrity (row count, widths, labels, control leaks).
	frame := m.View()
	assertExactFrameGeometry(t, frame, width, height)
	assertViewportWindowIntegrity(t, m)
}

// --- Round 2: unsanitized/unwrapped ingress surfaces ------------------------
//
// The four tests below pin the remaining user/LLM-controlled ingress surfaces
// reported by the follow-up audit, under the same threat model as
// sanitizeDisplayText: control chars + ANSI escapes (frame corruption, paint
// ≠ measure) and bidi/zero-width Cf (0-cell measure, terminal reorders text),
// plus over-wide composed rows that trigger the cellbuf.Wrap re-wrap cascade
// inside lineViewport.View().
//
// Each test seeds the payload DIRECTLY into the model field (bypassing the
// event handlers) so the assertion pins the render-path invariant itself; the
// production fixes sanitize at both the assignment sites and the single read
// site (defense in depth, the pattern already established by
// recordSubagentProgress/renderSubagentProgress in model.go).

// scToolActionPayload packs the three attack classes into a m.currentToolAction
// value (the tool.executing metadata action/tool, command.applied cards and
// goal/command results are agent-LLM controlled): a CSI erase sequence, a
// trojan-source RLO/PDF pair written as \u escapes so the invisible
// codepoints stay reviewable in source, and an over-wide run. The tool-call
// row wraps (renderToolCallRow) but historically did NOT sanitize, so the ESC
// and Cf chars used to reach the overlay lines verbatim.
var scToolActionPayload = "exec: rm -rf \x1b[2J \u202Ebidi\u202C " + strings.Repeat("x", 300)

// TestSpecialChars_CurrentToolAction pins the currentToolAction overlay row:
// rows fit the viewport budget (wrap-safe), but no CSI erase or bidi Cf may
// survive into the overlay, and the full frame must keep its exact geometry.
// RED before the sanitization fix (control + bidi leaks on the overlay).
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

	// Wrapping keeps every row inside the budget (this held even before the
	// fix — renderToolCallRow wraps); the ingress invariants are the scans:
	assertViewportLineWidths(t, m)
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)

	frame := m.View()
	assertExactFrameGeometry(t, frame, width, height)
}

// TestSpecialChars_GroupTurnHeader pins the group-turn header row built by
// renderGroupTurns from group.turn event metadata: the interpolated
// turn.label/turn.speaker/turn.role must be sanitized AND the composed header
// must be wrapped so an over-wide label cannot cascade inside
// lineViewport.View(). The header block is seeded by direct Model-field
// assignment — the pattern group_status_test.go uses for group state (that
// file has no turn-seeding helper) — and rendered through the real
// updateViewport path. Empty turn.content delimits the header block: the
// next overlay row after the header is the group block's blank spacer, so
// the continuation scan cannot run past the header.
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

	// (2) no over-wide base/overlay row: an over-wide header here means
	// cellbuf.Wrap would re-wrap it at paint time (dropping the ┌ prefix).
	base := assertViewportLineWidths(t, m)
	// (5) no control/bidi leaks from the unsanitized metadata fields.
	assertNoControlLeaks(t, "base", base)
	assertNoControlLeaks(t, "overlay", m.viewport.overlayLines)
	assertNoBidiLeaks(t, "base", base)
	assertNoBidiLeaks(t, "overlay", m.viewport.overlayLines)

	// The header row 0 keeps the "┌" prefix; every continuation row of the
	// header block is indented (starts with a space), never naked at column 0.
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
			break // blank spacer = end of the group block
		}
		if !strings.HasPrefix(plain, " ") {
			t.Errorf("overlay[%d]: header continuation row is naked (no indent): %q", i, plain)
		}
	}
}

// TestSpecialChars_CompactFeedback pins the /compact result overlay row: the
// result string assigned to m.compactFeedback is backend/LLM-controlled and
// must not leak CSI erase sequences or bidi Cf into the overlay. RED before
// the sanitization fix.
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

// TestSpecialChars_SessionsModalItems pins the /sessions modal item render
// (renderModal): session names are user-controlled, so the item text must be
// sanitized and truncated to the modal's usable width before rendering — an
// over-wide item makes the modal content wider than the frame, lipgloss.Place
// then no-ops (gap <= 0) and stops padding every frame line to exactly
// m.width, breaking the frame geometry, while the raw ESC/Cf chars ride into
// the painted frame. Item ordering/selection logic is untouched: the item is
// populated directly (resetModal pattern from the other modal tests).
func TestSpecialChars_SessionsModalItems(t *testing.T) {
	const width, height = 140, 40
	m := newSpecialCharsModel(t, width, height, false)

	m.resetModal(ModalSessions)
	m.modalItems = []string{"> session \x1b[2J \u202Es\u202C " + strings.Repeat("n", 200)}

	frame := m.View()

	// Every frame line is exactly m.width (modal fits, Place pads).
	assertExactFrameGeometry(t, frame, width, height)
	// No raw CSI erase or bidi Cf survives into the painted frame.
	lines := strings.Split(frame, "\n")
	assertNoControlLeaks(t, "modal frame", lines)
	assertNoBidiLeaks(t, "modal frame", lines)
}

// TestLineViewport_OverWideRowTruncatedNotRewrapped pins the structural
// hardening in lineViewport.View(): an over-wide row is TRUNCATED to the
// render width, never word-wrapped. lipgloss Width() (cellbuf.Wrap) breaks
// rows at '-' and drops row prefixes ("  → │ …"), adding extra rows inside
// the fixed-height window — the exact full-frame corruption cascade from the
// bug-report screenshot. Producers wrap to the budget, so this path only
// fires on a width-math bug or an unsanitized ingress; it must degrade to a
// clean truncation instead of corrupting the frame.
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
	// Truncation keeps row 0's prefix; a re-wrap would have moved the tail
	// after the hyphen breakpoint onto its own naked row.
	if !strings.HasPrefix(ansi.Strip(rows[0]), "  → │ Timed out waiting for") {
		t.Errorf("row 0 lost its prefix (re-wrap happened): %q", rows[0])
	}
	if strings.Contains(ansi.Strip(rows[1]), "after 600") {
		t.Errorf("row 1 holds wrapped tail content (re-wrap happened): %q", rows[1])
	}
}
