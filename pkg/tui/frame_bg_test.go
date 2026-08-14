package tui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

// forceTrueColor switches lipgloss to TrueColor for the duration of a test so
// background sequences are actually emitted (the default test profile is Ascii).
func forceTrueColor(t *testing.T) {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(old) })
}

// resetFollowers returns, for every full ANSI reset in s, the escape sequence
// (or raw byte description) that immediately follows it. A follower that is
// not a background-setting SGR sequence means unpainted cells.
func resetFollowers(s string) []string {
	var out []string
	idx := 0
	for {
		i := strings.Index(s[idx:], "\x1b[0m")
		if i < 0 {
			return out
		}
		after := idx + i + len("\x1b[0m")
		rest := s[after:]
		switch {
		case rest == "":
			out = append(out, "<eof>")
		case strings.HasPrefix(rest, "\n"):
			out = append(out, "<newline>")
		case strings.HasPrefix(rest, "\x1b["):
			end := strings.IndexByte(rest, 'm')
			if end < 0 {
				out = append(out, "<escape-without-m>")
			} else {
				out = append(out, rest[:end+1])
			}
		default:
			out = append(out, "<raw:"+string(rest[0])+">")
		}
		idx = after
	}
}

// assertNoUnpaintedCells verifies that every full reset is immediately
// followed by a background-setting SGR sequence (or end of line/frame), so no
// cell is ever left unpainted after an inner style reset.
func assertNoUnpaintedCells(t *testing.T, out string) {
	t.Helper()

	if !strings.Contains(out, "\x1b[0m") {
		t.Fatal("frame contains no ANSI resets; test is not exercising styled content")
	}
	for i, f := range resetFollowers(out) {
		if strings.HasPrefix(f, "<raw:") || f == "<eof>" || f == "<escape-without-m>" {
			t.Fatalf("unpainted cells: reset #%d followed by %s", i, f)
		}
		if strings.HasPrefix(f, "\x1b[") && !paramsHasBackground(f[2:len(f)-1]) {
			t.Fatalf("unpainted cells: reset #%d followed by non-background sequence %q", i, f)
		}
	}
}

func TestPaintFrame_ReappliesBackgroundAfterInnerResets(t *testing.T) {
	forceTrueColor(t)

	// Content with inner styled elements (foreground + inner background) that
	// terminate with full ANSI resets, mimicking logo/input/badge rendering.
	content := WelcomeLogo.Render("LELE") + "\n" +
		InputBarContainer.Width(60).Render("hola") + "\n" +
		ModelSelectorStyle.Render("badge")

	m := &Model{width: 100, height: 20}
	out := m.paintFrame(content)

	assertNoUnpaintedCells(t, out)

	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w != m.width {
			t.Fatalf("line %d width = %d, want %d", i, w, m.width)
		}
	}
}

// TestReapplyBackground_ContextAware verifies that raw cells following a
// reset are painted with the innermost enclosing background: the container
// background inside a container that owns its own background, and the app
// background at top level.
func TestReapplyBackground_ContextAware(t *testing.T) {
	forceTrueColor(t)

	const (
		appBgParams   = "48;2;24;24;36" // BgColor #181824
		inputBgParams = "48;2;32;32;48" // InputBgColor #212130
	)

	// Raw text (" raw") after a styled run inside the input container: the
	// cells after the logo reset must be painted with the INPUT background.
	inner := reapplyBackground(
		AppContainer.Width(60).Height(3).Render(
			InputBarContainer.Width(40).Render(WelcomeLogo.Render("hola") + " raw"),
		),
	)
	if !strings.Contains(inner, "hola\x1b[0m\x1b["+inputBgParams+"m") {
		t.Fatalf("raw cells inside input container not painted with container background;\nframe: %q", inner)
	}
	assertNoUnpaintedCells(t, inner)

	// Same at top level: raw cells after the logo reset must be painted with
	// the APP background (the re-emitted sequence may carry extra attributes).
	top := reapplyBackground(
		AppContainer.Width(60).Height(3).Render(WelcomeLogo.Render("hola") + " raw"),
	)
	if !strings.Contains(top, "hola\x1b[0m\x1b[38;2;248;248;242;"+appBgParams+"m") &&
		!strings.Contains(top, "hola\x1b[0m\x1b["+appBgParams+"m") {
		t.Fatalf("raw cells at top level not painted with app background;\nframe: %q", top)
	}
	assertNoUnpaintedCells(t, top)

	// Nested fg-only run inside a bg run: closing the fg run must not pop the
	// enclosing background context.
	nested := reapplyBackground(
		AppContainer.Width(60).Height(3).Render(
			InputBarContainer.Width(40).Render(WelcomeLogo.Render("a") + WelcomeLogo.Render("b") + " tail"),
		),
	)
	assertNoUnpaintedCells(t, nested)
	if !strings.Contains(nested, "b\x1b[0m\x1b["+inputBgParams+"m") {
		t.Fatalf("raw cells after second styled run lost container background;\nframe: %q", nested)
	}
}

func TestView_WelcomeFrame_UniformBackground(t *testing.T) {
	forceTrueColor(t)

	m := newTestModel(t)
	m.width = 120
	m.height = 30
	m.showWelcome = true

	out := m.View()
	assertNoUnpaintedCells(t, out)
}

func TestView_ChatFrame_UniformBackground(t *testing.T) {
	forceTrueColor(t)

	m := newTestModel(t)
	key := "tui:chat:frame-bg-test"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddMessage(key, "user", "hello")
	m.sessionMgr.AddMessage(key, "assistant", "hi there")
	m.currentKey = key
	m.showWelcome = false
	m.width = 120
	m.height = 30

	out := m.View()
	assertNoUnpaintedCells(t, out)
}

// TestInput_NoRawBasicANSIColors verifies that the textarea and textinput
// widgets no longer emit raw basic ANSI colors (e.g. \x1b[40m black
// background, \x1b[37m white foreground) from the bubbles stock defaults.
// Those sequences clash with the app theme and, after paintFrame's per-reset
// background re-emission, show up as black patches inside the input box.
func TestInput_NoRawBasicANSIColors(t *testing.T) {
	forceTrueColor(t)

	m := newTestModel(t)
	for _, raw := range []string{m.chatInput.View(), m.textInput.View()} {
		for _, bad := range []string{"\x1b[40m", "\x1b[37m", "\x1b[47m", "\x1b[30m"} {
			if strings.Contains(raw, bad) {
				t.Fatalf("input widget emits raw basic ANSI color %q: %q", bad, raw)
			}
		}
	}
}

// TestView_WelcomeInput_NoBackgroundBleed verifies that the input box
// background (InputBgColor) never extends beyond the welcome box: on every
// frame line, the cells to the right of the centered welcome content must be
// painted with the app background, not the input background. Regression test
// for the textarea being wider than the 60-col welcome box, which made
// InputBgColor bands run to the right edge of the screen.
func TestView_WelcomeInput_NoBackgroundBleed(t *testing.T) {
	forceTrueColor(t)

	m := newTestModel(t)
	m.width = 207
	m.height = 47
	m.showWelcome = true

	const (
		inputBgParams = "48;2;32;32;48" // InputBgColor #212130
		appBgParams   = "48;2;24;24;36" // BgColor #181824
	)

	out := m.View()
	for i, line := range strings.Split(out, "\n") {
		cells, err := ansiCells(line)
		if err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if len(cells) != m.width {
			t.Fatalf("line %d: %d cells, want %d", i, len(cells), m.width)
		}
		// Rightmost 60 columns must be the app background: the welcome box
		// is 60 cols wide and centered, so nothing with its own background
		// may reach the right screen edge.
		for col := m.width - 60; col < m.width; col++ {
			if cells[col].bg != appBgParams {
				t.Fatalf("line %d col %d: background %q, want app background %q (input background bleed)",
					i, col, cells[col].bg, appBgParams)
			}
		}
		// Sanity: the input box itself still paints its own background
		// somewhere (the placeholder lines).
		if strings.Contains(line, inputBgParams) {
			// InputBg cells must be confined to the centered 60-col box.
			for col, c := range cells {
				if c.bg == inputBgParams && (col < (m.width-60)/2 || col >= (m.width+60)/2) {
					t.Fatalf("line %d col %d: input background outside welcome box", i, col)
				}
			}
		}
	}
}

// ansiCell is a single screen cell with the SGR state that paints it.
type ansiCell struct {
	bg string // background parameters in effect (e.g. "48;2;24;24;36"; "" = none)
}

// ansiCells expands a styled line into per-cell background state.
func ansiCells(line string) ([]ansiCell, error) {
	var cells []ansiCell
	bg := ""
	i := 0
	for i < len(line) {
		if strings.HasPrefix(line[i:], "\x1b[") {
			end := strings.IndexByte(line[i:], 'm')
			if end < 0 {
				return nil, fmt.Errorf("unterminated escape at %d", i)
			}
			params := line[i+2 : i+end]
			switch {
			case params == "0" || params == "00" || params == "":
				bg = ""
			default:
				if b := backgroundParams(params); b != "" {
					bg = b
				}
			}
			i += end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		w := runewidth.RuneWidth(r)
		if w == 0 {
			w = 1 // zero-width runes still occupy the cell they combine with
		}
		for k := 0; k < w; k++ {
			cells = append(cells, ansiCell{bg: bg})
		}
		i += size
	}
	return cells, nil
}

// backgroundParams extracts the background portion of an SGR parameter list
// ("48;2;R;G;B" for extended colors, "4x" for basic backgrounds), or "" when
// the sequence does not set a background.
func backgroundParams(params string) string {
	parts := strings.Split(params, ";")
	for i, p := range parts {
		if p == "48" && i+4 < len(parts) {
			return strings.Join(parts[i:i+5], ";")
		}
		if len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7' {
			return p
		}
	}
	return ""
}
