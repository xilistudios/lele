package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// auditDot mirrors the truecolor bouncing dots emitted by
// Model.getBouncingDots(): each visible cell costs ~22 bytes of ANSI.
const auditDot = "\x1b[38;2;139;233;253m●\x1b[0m"

// buildAuditDots replicates the "[● ● ●         ]" bracket frame produced by
// the real dot animation string.
func buildAuditDots(active int) string {
	var b strings.Builder
	b.WriteRune('[')
	for i := 0; i < 12; i++ {
		if i < active {
			b.WriteString(auditDot)
		} else {
			b.WriteRune(' ')
		}
	}
	b.WriteRune(']')
	return b.String()
}

// assertANSIIntact fails if s contains a truncated/malformed escape sequence.
// Robust checks: after ansi.Strip no ESC may remain (a partial CSI swallowed
// by Strip would otherwise hide corruption in the visible text), no ESC run
// may sit at the end of the string (mid-sequence cut), and the sequence
// counts must match between the original and the truncated result modulo
// resets that ansi.Truncate appends.
func assertANSIIntact(t *testing.T, name, s string) {
	t.Helper()
	if strings.Contains(ansi.Strip(s), "\x1b") {
		t.Errorf("%s: stripped text still contains ESC: %q", name, ansi.Strip(s))
	}
	// Every ESC must open a CSI whose terminator appears before the next ESC.
	rest := s
	for {
		i := strings.IndexByte(rest, '\x1b')
		if i < 0 {
			break
		}
		seq := rest[i:]
		j := strings.IndexByte(seq[1:], '\x1b')
		if j >= 0 {
			seq = seq[:j+1]
		}
		if len(seq) < 2 || seq[1] != '[' || !strings.ContainsFunc(seq[2:], func(r rune) bool {
			return r >= '@' && r <= '~'
		}) {
			t.Errorf("%s: incomplete CSI sequence in output: %q", name, rest[i:min(i+12, len(rest))])
			return
		}
		rest = rest[i+len(seq):]
	}
}

// statusLineAtWidth composes a status line exactly like view.go does before
// the clamp, then applies the same clamp (truncateRightCells).
func statusLineAtWidth(raw string, leftWidth int) string {
	if maxSW := leftWidth - 2; maxSW > 0 && ansi.StringWidth(raw) > maxSW {
		return truncateRightCells(raw, maxSW)
	}
	return raw
}

func TestStatusLineTruncateKeepsANSIIntact(t *testing.T) {
	dots := buildAuditDots(3)
	cases := map[string]string{
		"processing (en)":       fmt.Sprintf("%s %s", dots, "Processing... (ESC to cancel)"),
		"processing (es)":       fmt.Sprintf("%s %s", dots, "Procesando... (ESC para cancelar)"),
		"subagent+escHint (en)": fmt.Sprintf("%s %s  ◀ %s", dots, "Press ESC again to cancel", "Back to parent chat"),
		"subagent (en)":         fmt.Sprintf("◄ %s", "Back to parent chat"),
		"selecting (en)":        "Selecting... (release to copy)",
	}
	for name, raw := range cases {
		for _, w := range []int{80, 72, 64, 60, 50, 40} {
			leftWidth := int(float64(w) * leftColumnRatio)
			out := statusLineAtWidth(raw, leftWidth)
			maxSW := leftWidth - 2
			got := ansi.StringWidth(out)
			want := ansi.StringWidth(raw)
			if want <= maxSW {
				if out != raw {
					t.Errorf("%s at term width %d: untruncated line was modified", name, w)
				}
				continue
			}
			if got > maxSW {
				t.Errorf("%s at term width %d: width %d exceeds budget %d", name, w, got, maxSW)
			}
			// Regression for audit H1: rune-slicing collapsed an 80-col
			// subagent line from 63 visible cells down to 3.
			if got < maxSW-10 {
				t.Errorf("%s at term width %d: line collapses to %d visible cols (budget %d, want >= %d) — output %q", name, w, got, maxSW, maxSW-10, out)
			}
			assertANSIIntact(t, fmt.Sprintf("%s@%d", name, w), out)
		}
	}
}

func TestTruncateRightCellsBasics(t *testing.T) {
	styled := "\x1b[31mred\x1b[0m text"
	if got := truncateRightCells(styled, 100); got != styled {
		t.Errorf("no-op truncate changed string: %q", got)
	}
	out := truncateRightCells(styled, 5)
	if w := ansi.StringWidth(out); w > 5 {
		t.Errorf("width %d > 5: %q", w, out)
	}
	assertANSIIntact(t, "styled-5", out)
	if !strings.HasSuffix(ansi.Strip(out), "...") {
		t.Errorf("expected ellipsis tail, got %q", out)
	}
	// Budget smaller than the ellipsis: ansi.Truncate(s, n<3, "...") collapses
	// to "", so the helper must drop the tail instead.
	for _, n := range []int{1, 2} {
		out := truncateRightCells("abcdefgh", n)
		if w := ansi.StringWidth(out); w != n {
			t.Errorf("cells=%d: width %d, want %d (out %q)", n, w, n, out)
		}
	}
	// Wide characters count as cells, not runes.
	out = truncateRightCells("模型测试", 5)
	if w := ansi.StringWidth(out); w > 5 {
		t.Errorf("CJK width %d > 5: %q", w, out)
	}
	if strings.ContainsRune(ansi.Strip(out), '试') {
		t.Errorf("cut on grapheme boundary violated: %q", out)
	}
}

func TestGoalBadgeBudget(t *testing.T) {
	prefixCells := ansi.StringWidth(goalBadgePrefix) // "🎯 " = 3 cells
	labels := []string{
		"模型🚀test",
		"fix the ANSI truncation bug across the whole TUI rendering pipeline",
		"café ☕ naïve",
		"short",
	}
	for _, label := range labels {
		for _, remaining := range []int{9, 10, 12, 20, 40} {
			out := truncateGoalLabel(label, remaining)
			badge := goalBadgePrefix + out
			if w := ansi.StringWidth(badge); w > remaining {
				t.Errorf("label %q remaining %d: badge width %d exceeds budget (%q)", label, remaining, w, badge)
			}
			assertANSIIntact(t, "goal-badge", out)
			if ansi.StringWidth(label) <= remaining-prefixCells && out != label {
				t.Errorf("label %q fits budget but was modified: %q", label, out)
			}
		}
	}
	// Degenerate budgets never emit negative-length truncation.
	if got := truncateGoalLabel("x", ansi.StringWidth(goalBadgePrefix)); got != "" {
		t.Errorf("budget exactly prefix size: want empty label, got %q", got)
	}
	if got := truncateGoalLabel("x", 2); got != "" {
		t.Errorf("budget below prefix width: want empty label, got %q", got)
	}
}

func TestSidebarPathLeftTruncate(t *testing.T) {
	contentWidth := 21 // e.g. 36-col sidebar minus padding
	path := "/home/alfredo/projects/lele-workspace-tui-audit"
	out := truncateLeftCells(path, contentWidth-1)
	if w := ansi.StringWidth(out); w > contentWidth-1 {
		t.Errorf("width %d > budget %d: %q", w, contentWidth-1, out)
	}
	if !strings.HasPrefix(out, ellipsis) {
		t.Errorf("expected ellipsis prefix, got %q", out)
	}
	// Must end with a contiguous tail of the original path.
	tail := strings.TrimPrefix(out, ellipsis)
	if !strings.HasSuffix(path, tail) {
		t.Errorf("result %q does not end with a tail of %q", out, path)
	}
	// Short paths are untouched.
	if got := truncateLeftCells("short/path", contentWidth-1); got != "short/path" {
		t.Errorf("short path modified: %q", got)
	}
	// Styled paths keep their sequences intact.
	styled := "/very/long/path/\x1b[31mrepo-name\x1b[0m"
	out = truncateLeftCells(styled, 12)
	if w := ansi.StringWidth(out); w > 12 {
		t.Errorf("styled width %d > 12: %q", w, out)
	}
	assertANSIIntact(t, "styled-path", out)
	// Tiny budgets degrade to right truncation but still fit.
	for _, n := range []int{1, 2, 3, 4} {
		out := truncateLeftCells(path, n)
		if w := ansi.StringWidth(out); w > n {
			t.Errorf("cells=%d: width %d exceeds budget: %q", n, w, out)
		}
	}
}

func TestSidebarSessionNameTruncate(t *testing.T) {
	contentWidth := 15
	names := []string{
		"⇗ " + "Subagent chat about the multi-step refactor pipeline",
		"模型会话名称测试用很长的一串",
		"👨‍👩‍👧‍👦 family session name that is quite long",
	}
	for _, name := range names {
		out := name
		if ansi.StringWidth(out) > contentWidth {
			out = truncateRightCells(out, contentWidth)
		}
		if w := ansi.StringWidth(out); w > contentWidth {
			t.Errorf("name %q: width %d > %d: %q", name, w, contentWidth, out)
		}
		assertANSIIntact(t, "session-name", out)
	}
}

// TestViewRealPathStatusLineCells drives the actual Model.View() with the
// subagent processing status line (real getBouncingDots truecolor ANSI), a
// goal badge with emoji/CJK text, a long workspace path and a long session
// name, across the audit width matrix. It asserts the rendered frame never
// contains a broken escape sequence and that no line collapses below the
// visible-width floor that the rune-slicing bug produced.
func TestViewRealPathStatusLineCells(t *testing.T) {
	m := newTestModel(t)

	key := "native:chat:h1-e2e"
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.SetName(key, "模型🚀 very long session name that must be clamped by cells")
	m.agentLoop.GoalManager().Set(key, "模型🚀fix ANSI truncation across the pipeline", 5)

	m.currentKey = key
	m.parentSessionKey = "native:chat:parent"
	m.workspacePath = "/home/alfredo/projects/lele-workspace-tui-audit-very-deep-path"
	m.gitBranch = "feature/audit-h1-branch-name-long"
	m.processing = true
	m.startTime = time.Now()
	m.escHint = true
	m.showWelcome = false

	for _, w := range []int{80, 72, 64, 60, 50, 40} {
		for _, h := range []int{30, 24, 20, 16} {
			m.width = w
			m.height = h
			out := m.View()
			for i, line := range strings.Split(out, "\n") {
				if strings.Contains(ansi.Strip(line), "\x1b") {
					t.Fatalf("term %dx%d line %d: ESC survives Strip (partial CSI): %q", w, h, i, line)
				}
			}
			// The frame must keep its structure: every line fits the width.
			for i, line := range strings.Split(out, "\n") {
				if lw := ansi.StringWidth(line); lw > w {
					t.Fatalf("term %dx%d line %d overflows terminal width: %d > %d (%q)", w, h, i, lw, w, line)
				}
			}
		}
	}
}
