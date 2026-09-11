package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// ---------------------------------------------------------------------------
// A minimal terminal emulator: paints a string into a cell grid honouring SGR
// state. This is the correctness gate for mergeAdjacentSGR — two frames are
// equivalent iff they paint the same rune with the same effective fg/bg/attrs
// in every cell. (Ported from the audit evidence bank zz_exp9_test.go.)
// ---------------------------------------------------------------------------

type gridCell struct {
	r     rune
	fg    string
	bg    string
	attrs string
}

func applySGRState(params string, fg, bg, attrs *string) {
	if params == "" || params == "0" || params == "00" {
		*fg, *bg, *attrs = "", "", ""
		return
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		switch p := parts[i]; {
		case p == "":
			*fg, *bg, *attrs = "", "", ""
		case p == "1" || p == "2" || p == "3" || p == "4" || p == "7" || p == "9":
			if *attrs == "" {
				*attrs = p
			} else if !strings.Contains(*attrs, p) {
				*attrs += "+" + p
			}
		case p == "22" || p == "23" || p == "24" || p == "27" || p == "29":
			*attrs = ""
		case p == "39":
			*fg = ""
		case p == "49":
			*bg = ""
		case p == "38" || p == "48":
			// extended color: 38;2;r;g;b | 38;5;n
			var spec string
			if i+1 < len(parts) {
				switch parts[i+1] {
				case "2":
					if i+4 < len(parts) {
						spec = strings.Join(parts[i:i+5], ";")
						i += 4
					}
				case "5":
					if i+2 < len(parts) {
						spec = strings.Join(parts[i:i+3], ";")
						i += 2
					}
				}
			}
			if p == "38" {
				*fg = spec
			} else {
				*bg = spec
			}
		case len(p) == 2 && p[0] == '3' && p[1] >= '0' && p[1] <= '7':
			*fg = p
		case len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7':
			*bg = p
		}
	}
}

// paintGrid renders s into a w×h cell grid, honouring SGR state and skipping
// non-SGR CSI sequences the way a terminal would.
func paintGrid(s string, w, h int) []gridCell {
	grid := make([]gridCell, w*h)
	for i := range grid {
		grid[i] = gridCell{r: ' '}
	}
	x, y := 0, 0
	var fg, bg, attrs string
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			k := i + 2
			for k < len(s) && (s[k] == ';' || (s[k] >= '0' && s[k] <= '9')) {
				k++
			}
			if k < len(s) && s[k] == 'm' {
				applySGRState(s[i+2:k], &fg, &bg, &attrs)
				i = k + 1
				continue
			}
			// Other CSI (cursor moves etc.) — skip the whole sequence.
			for k < len(s) && s[k] >= '@' && s[k] <= '~' {
				k++
				break
			}
			i = k
			continue
		}
		if s[i] == '\n' {
			y++
			x = 0
			i++
			continue
		}
		if s[i] == '\r' {
			x = 0
			i++
			continue
		}
		r, sz := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && sz == 1 {
			i++
			continue
		}
		rw := 1
		if ws := ansi.StringWidth(string(r)); ws > 0 {
			rw = ws
		}
		if y >= 0 && y < h {
			for d := 0; d < rw; d++ {
				if x+d >= 0 && x+d < w {
					grid[y*w+x+d] = gridCell{r: r, fg: fg, bg: bg, attrs: attrs}
				}
			}
		}
		x += rw
		i += sz
	}
	return grid
}

// countSGRSeqs counts SGR sequences in s (same scanner as the audit banks).
func countSGRSeqs(s string) (n, bytes int) {
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				k := j + 1
				for k < len(s) && (s[k] == ';' || (s[k] >= '0' && s[k] <= '9')) {
					k++
				}
				if k < len(s) && s[k] == 'm' {
					n++
					bytes += k + 1 - i
					i = k + 1
					continue
				}
			}
		}
		i++
	}
	return
}

func diffGrids(a, b []gridCell) (int, []string) {
	diffs := 0
	var samples []string
	for i := range a {
		if a[i] != b[i] {
			diffs++
			if len(samples) < 4 {
				samples = append(samples,
					fmt.Sprintf("cell %d", i)+
						" orig[r="+string(a[i].r)+" fg="+a[i].fg+" bg="+a[i].bg+" at="+a[i].attrs+"]"+
						" new[r="+string(b[i].r)+" fg="+b[i].fg+" bg="+b[i].bg+" at="+b[i].attrs+"]")
			}
		}
	}
	return diffs, samples
}

// TestMergeAdjacentSGRCellEquivalence is the hard correctness gate: render a
// realistic conversation through glamour WITHOUT the merge (raw lines), merge
// them, paint both FINAL frames into a cell grid and require zero diffs. Also
// asserts per-line ansi.Strip equality and a real byte reduction. The
// production hooks themselves are covered by TestCachedHistoryLinesAreMerged
// and TestRenderedStreamLinesAreMerged.
func TestMergeAdjacentSGRCellEquivalence(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	for _, tc := range []struct{ pairs, w, h int }{
		{100, 120, 30},
		{100, 200, 50},
		{100, 300, 80},
	} {
		m := buildBenchModel(t, tc.pairs)
		m.width, m.height = tc.w, tc.h
		_ = m.View()
		_ = m.View()
		m.updateViewport()

		// raw = same conversation rendered through the glamour path WITHOUT
		// the merge (the pre-PR-1 state); norm = merge of those lines.
		raw := rawRenderedHistoryLines(t, m, tc.pairs)
		if len(raw) == 0 {
			t.Fatal("no raw lines produced")
		}
		norm := make([]string, len(raw))
		for i, l := range raw {
			norm[i] = mergeAdjacentSGR(l)
			if ansi.Strip(raw[i]) != ansi.Strip(norm[i]) {
				t.Fatalf("%dx%d line %d: visible text differs:\n orig  %q\n merged %q",
					tc.w, tc.h, i, ansi.Strip(raw[i]), ansi.Strip(norm[i]))
			}
		}
		rawB, normB := bytesSum(raw), bytesSum(norm)
		rawSGR, normSGR := sgrSum(raw), sgrSum(norm)
		if normB >= rawB {
			t.Errorf("%dx%d: merge did not reduce bytes: %d → %d", tc.w, tc.h, rawB, normB)
		}
		t.Logf("%dx%d lines: %d B/%d SGR → %d B/%d SGR (−%.1f%% bytes, −%.1f%% SGR)",
			tc.w, tc.h, rawB, rawSGR, normB, normSGR,
			100*(1-float64(normB)/float64(rawB)), 100*(1-float64(normSGR)/float64(rawSGR)))

		m.viewport.SetBaseLines(raw)
		m.viewport.GotoBottom()
		frameOrig := m.View()

		m.viewport.SetBaseLines(norm)
		m.viewport.GotoBottom()
		frameNorm := m.View()

		ga, gb := paintGrid(frameOrig, tc.w, tc.h), paintGrid(frameNorm, tc.w, tc.h)
		diffs, samples := diffGrids(ga, gb)
		t.Logf("%dx%d msgs=%d: frame %d→%d bytes (%.1f%% smaller), cell diffs %d/%d",
			tc.w, tc.h, tc.pairs*2, len(frameOrig), len(frameNorm),
			100*(1-float64(len(frameNorm))/float64(len(frameOrig))), diffs, len(ga))
		for _, s := range samples {
			t.Logf("   %s", s)
		}
		if diffs > 0 {
			t.Errorf("%dx%d: %d cell diffs — mergeAdjacentSGR is NOT visually equivalent", tc.w, tc.h, diffs)
		}
	}
}

// TestMergeAdjacentSGRCellEquivalenceStreaming paints frames while a stream
// overlay is on screen, with the overlay lines merged the same way the
// production hooks do (getRenderedStream + overlay), and requires zero diffs.
func TestMergeAdjacentSGRCellEquivalenceStreaming(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(t, 100)
	m.width, m.height = 200, 50
	_ = m.View()
	_ = m.View()
	m.updateViewport()
	raw := rawRenderedHistoryLines(t, m, 100)
	norm := make([]string, len(raw))
	for i, l := range raw {
		norm[i] = mergeAdjacentSGR(l)
	}

	m.processing = true
	m.currentStream = "Streaming a **markdown** response with `code` and a list:\n- one\n- two\n"

	m.viewport.SetBaseLines(raw)
	m.viewport.GotoBottom()
	frameOrig := m.View()
	m.viewport.SetBaseLines(norm)
	m.viewport.GotoBottom()
	frameNorm := m.View()

	ga, gb := paintGrid(frameOrig, m.width, m.height), paintGrid(frameNorm, m.width, m.height)
	diffs, samples := diffGrids(ga, gb)
	t.Logf("streaming: frame %d→%d bytes, cell diffs %d/%d", len(frameOrig), len(frameNorm), diffs, len(ga))
	for _, s := range samples {
		t.Logf("   %s", s)
	}
	if diffs > 0 {
		t.Errorf("streaming: %d cell diffs", diffs)
	}
}

// TestMergeAdjacentSGRStreamingTriplesEquivalence exercises the pathological
// glamour/chroma pattern directly: many repeated OPEN/text/RESET triples per
// line (as produced token-by-token while streaming), merged and painted
// through the grid emulator.
func TestMergeAdjacentSGRStreamingTriplesEquivalence(t *testing.T) {
	triples := []string{
		strings.Repeat(normOpen1+"x"+normReset, 200),
		normOpen1 + "a" + normReset + strings.Repeat(normOpen1+" "+normReset, 100) + normOpen2 + "b" + normReset,
		strings.Repeat(normTrueC+"tok"+normReset+normBold+" "+normReset, 80),
		"no escapes here at all",
		"",
		strings.Repeat(normOpen1+normReset, 50) + "tail",
		normBgOpen + strings.Repeat("█"+normReset+normBgOpen, 60) + "█" + normReset,
	}
	joinedOrig := strings.Join(triples, "\n")
	merged := make([]string, len(triples))
	for i, l := range triples {
		merged[i] = mergeAdjacentSGR(l)
	}
	joinedMerged := strings.Join(merged, "\n")

	const w, h = 400, 16
	ga, gb := paintGrid(joinedOrig, w, h), paintGrid(joinedMerged, w, h)
	diffs, samples := diffGrids(ga, gb)
	if diffs > 0 {
		for _, s := range samples {
			t.Logf("   %s", s)
		}
		t.Errorf("streaming triples: %d cell diffs", diffs)
	}
	for i := range triples {
		if ansi.Strip(triples[i]) != ansi.Strip(merged[i]) {
			t.Errorf("line %d: visible text differs", i)
		}
	}
	origSGR, origB := countSGRSeqs(joinedOrig)
	mrgSGR, mrgB := countSGRSeqs(joinedMerged)
	t.Logf("triples: %d B / %d SGR → %d B / %d SGR (%.1f%% bytes, %.1f%% SGR)",
		origB+len(joinedOrig), origSGR, mrgB+len(joinedMerged), mrgSGR,
		100*(1-float64(mrgB+len(joinedMerged))/float64(origB+len(joinedOrig))),
		100*(1-float64(mrgSGR)/float64(origSGR)))
	if mrgSGR >= origSGR {
		t.Errorf("expected SGR reduction, got %d → %d", origSGR, mrgSGR)
	}
}

// TestRenderedStreamLinesAreMerged checks the production hook itself: lines
// coming out of getRenderedStream must already be merged (no OPEN+RESET empty
// runs, no duplicated adjacent opens) without the test applying the merge.
func TestRenderedStreamLinesAreMerged(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(t, 2)
	m.width, m.height = 200, 50
	m.processing = true
	// A stream with code so renderSingleLine goes through the highlighter.
	m.currentStream = "line one\n```go\nfunc x() error {\n\treturn nil\n}\n```\n"
	out := m.getRenderedStream(120)

	for _, line := range strings.Split(out, "\n") {
		if again := mergeAdjacentSGR(line); again != line {
			t.Errorf("getRenderedStream returned unmerged line:\n got  %q\nwant %q", line, again)
		}
	}

	m.currentThinking = "thinking with **bold** and a\n```go\ncode()\n```\nblock\n"
	outT := m.getRenderedThinking(120)
	for _, line := range strings.Split(outT, "\n") {
		if again := mergeAdjacentSGR(line); again != line {
			t.Errorf("getRenderedThinking returned unmerged line:\n got  %q\nwant %q", line, again)
		}
	}
}

// TestCachedHistoryLinesAreMerged checks hook (a): the per-message render
// cache must store already-merged lines.
func TestCachedHistoryLinesAreMerged(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(t, 5)
	m.width, m.height = 200, 50
	_ = m.View() // warms caches through the production path

	if len(m.msgRenderCacheLines) == 0 {
		t.Fatal("expected per-message render cache to be populated")
	}
	checked := 0
	for fp, lines := range m.msgRenderCacheLines {
		for i, l := range lines {
			if again := mergeAdjacentSGR(l); again != l {
				t.Errorf("cache %q line %d not merged at cache-build time:\n got  %q\nwant %q", fp, i, l, again)
			}
		}
		checked += len(lines)
	}
	if checked == 0 {
		t.Fatal("cache had no lines to check")
	}
}

// TestMergedFrameSizeRegression is the size budget for the optimization: a
// warm 200x50 frame (100 message pairs, TrueColor) must stay under 30 KB of
// total View() bytes. Before PR-1 the same frame measured 131,504 bytes and
// 7,461 SGR sequences.
//
// SGR budget note: the spec's ≤500 target was measured against the merged
// BASE LINES (−95% SGR holds there: 7,461 → ~380). The final View() string
// additionally contains chrome (header/statusbar/input styles) plus the SGR
// re-emitted by reapplyBackground and AppContainer — stages PR-1 deliberately
// does not touch — so the final-frame floor with merged content is ~780. We
// assert both: the viewport content (what PR-1 controls) ≤ 300 SGR, and the
// final frame ≤ 1000 SGR (regression guard vs the 7,461 baseline). The
// chat/sidebar gutter adds one background run per row (~50 at 50 lines).
func TestMergedFrameSizeRegression(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	m := buildBenchModel(t, 100)
	m.width, m.height = 200, 50
	_ = m.View()
	_ = m.View()
	inner := m.viewport.View()
	frame := m.View()

	innerSGR, _ := countSGRSeqs(inner)
	sgr, escBytes := countSGRSeqs(frame)
	t.Logf("warm 200x50: viewport content %d B / %d SGR; final frame %d B (%d escape bytes), %d SGR sequences",
		len(inner), innerSGR, len(frame), escBytes, sgr)

	if len(frame) > 30000 {
		t.Errorf("frame size regression: %d bytes > 30,000 budget (baseline was 131,504)", len(frame))
	}
	if innerSGR > 300 {
		t.Errorf("merged content SGR regression: viewport content has %d SGR > 300 budget", innerSGR)
	}
	if sgr > 1000 {
		t.Errorf("SGR regression: final frame has %d SGR > 1000 budget (baseline was 7,461)", sgr)
	}
}

// rawRenderedHistoryLines re-renders the bench conversation through the same
// glamour path buildRenderedHistoryLines uses, but WITHOUT mergeAdjacentSGR —
// i.e. exactly the pre-PR-1 line content. Used as the "original" side of the
// cell-equivalence gate.
func rawRenderedHistoryLines(t *testing.T, m *Model, pairs int) []string {
	t.Helper()
	hist := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	var out []string
	for _, msg := range hist {
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		if msg.Content == "" {
			continue
		}
		w := m.viewport.Width - 6
		if w <= 0 {
			w = 80
		}
		rendered := m.renderMarkdown(msg.Content, w)
		out = append(out, strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")...)
	}
	return out
}

func bytesSum(lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(l)
	}
	return n
}

func sgrSum(lines []string) int {
	n := 0
	for _, l := range lines {
		c, _ := countSGRSeqs(l)
		n += c
	}
	return n
}
