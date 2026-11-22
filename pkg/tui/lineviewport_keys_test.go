package tui

import (
	"strconv"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func makeScrollLines(n int) []string {
	lines := make([]string, n)
	for i := 0; i < n; i++ {
		lines[i] = "line " + strconv.Itoa(i)
	}
	return lines
}

func TestLineViewport_KeyboardScroll(t *testing.T) {
	v := newLineViewport(80, 10)
	v.SetBaseLines(makeScrollLines(100))
	v.GotoBottom()

	if v.YOffset != v.maxYOffset() {
		t.Fatalf("expected YOffset=%d after GotoBottom, got %d", v.maxYOffset(), v.YOffset)
	}

	_, _ = v.Update(tea.KeyMsg{Type: tea.KeyUp})
	want := v.maxYOffset() - 1
	if v.YOffset != want {
		t.Fatalf("after up: expected YOffset=%d, got %d", want, v.YOffset)
	}

	before := v.YOffset
	_, _ = v.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	want = before - v.pageHeight()
	if v.YOffset != want {
		t.Fatalf("after pgup: expected YOffset=%d, got %d", want, v.YOffset)
	}

	_, _ = v.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if v.YOffset > v.maxYOffset() {
		t.Fatalf("after pgdown: YOffset=%d exceeds maxYOffset=%d", v.YOffset, v.maxYOffset())
	}

	_, _ = v.Update(tea.KeyMsg{Type: tea.KeyDown})
	if v.YOffset != v.maxYOffset() {
		t.Fatalf("after down: expected YOffset=%d (clamped), got %d", v.maxYOffset(), v.YOffset)
	}

	v.GotoTop()
	_, _ = v.Update(tea.KeyMsg{Type: tea.KeyUp})
	if v.YOffset != 0 {
		t.Fatalf("after up at top: expected YOffset=0, got %d", v.YOffset)
	}
}

func TestLineViewport_KeyboardScrollClamping(t *testing.T) {
	v := newLineViewport(80, 10)
	v.SetBaseLines(makeScrollLines(100))

	v.GotoBottom()
	for i := 0; i < 50; i++ {
		_, _ = v.Update(tea.KeyMsg{Type: tea.KeyPgUp})
		if v.YOffset < 0 {
			t.Fatalf("pgup produced negative YOffset=%d", v.YOffset)
		}
	}
	if v.YOffset != 0 {
		t.Fatalf("expected YOffset=0 after many pgup, got %d", v.YOffset)
	}

	for i := 0; i < 50; i++ {
		_, _ = v.Update(tea.KeyMsg{Type: tea.KeyPgDown})
		if v.YOffset > v.maxYOffset() {
			t.Fatalf("pgdown produced YOffset=%d exceeding maxYOffset=%d", v.YOffset, v.maxYOffset())
		}
	}
	if v.YOffset != v.maxYOffset() {
		t.Fatalf("expected YOffset=%d after many pgdown, got %d", v.maxYOffset(), v.YOffset)
	}
}

func TestLineViewport_KeyboardWorksWhenMouseDisabled(t *testing.T) {
	v := newLineViewport(80, 10)
	v.MouseWheelEnabled = false
	v.SetBaseLines(makeScrollLines(100))
	v.GotoBottom()

	start := v.YOffset

	_, _ = v.Update(tea.KeyMsg{Type: tea.KeyUp})
	if v.YOffset != start-1 {
		t.Fatalf("keyboard up with mouse disabled: expected YOffset=%d, got %d", start-1, v.YOffset)
	}

	_, _ = v.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	})
	if v.YOffset != start-1 {
		t.Fatalf("mouse wheel with mouse disabled should not scroll: expected YOffset=%d, got %d", start-1, v.YOffset)
	}
}

func TestLineViewport_UnhandledKeyNoOp(t *testing.T) {
	v := newLineViewport(80, 10)
	v.SetBaseLines(makeScrollLines(100))
	v.GotoTop()

	_, _ = v.Update(tea.KeyMsg{Type: tea.KeyHome})
	if v.YOffset != 0 {
		t.Fatalf("home should be a no-op here, got YOffset=%d", v.YOffset)
	}
	_, _ = v.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if v.YOffset != 0 {
		t.Fatalf("end should be a no-op here, got YOffset=%d", v.YOffset)
	}
}

func TestLineViewport_PageHeight(t *testing.T) {
	v := newLineViewport(80, 10)
	if got := v.pageHeight(); got != 10 {
		t.Fatalf("pageHeight with Height=10 expected 10, got %d", got)
	}

	v.Height = 5
	if got := v.pageHeight(); got != 5 {
		t.Fatalf("pageHeight with Height=5 expected 5, got %d", got)
	}

	v.Height = 0
	if got := v.pageHeight(); got != 1 {
		t.Fatalf("pageHeight with Height=0 expected 1, got %d", got)
	}
}
