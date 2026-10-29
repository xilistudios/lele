package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// frameLayout is the vertical structure of a rendered frame: everything that
// decides on which row the status line, the queue band, the autocomplete list,
// the input bar and the bottom bar end up.
//
// It exists to detect the moments when a row of the frame changes meaning. The
// renderer keeps a per-row cache (bubbletea's standardRenderer.lastRenderedLines)
// and skips writing rows whose string is unchanged, so a row that used to hold
// the status line but is a blank margin in the new layout is only rewritten if
// the renderer knows it has to. When the layout shifts, that assumption is what
// leaves stale rows behind: the status line or the input placeholder from a
// previous frame keeps showing one or two rows above its current position.
// Comparing this value across frames lets the model ask for a full repaint
// exactly when the structure moved (and never in the steady state).
type frameLayout struct {
	width            int
	height           int
	viewportHeight   int
	queueRows        int
	autocompleteRows int
	welcome          bool
	modal            modalType
}

// noteFrameLayout records the layout of the frame being rendered. It is called
// from View() — the only place where the band heights are known — and the
// message pump (see consumeLayoutRepaint) turns a change into a full repaint.
func (m *Model) noteFrameLayout(width, height, viewportHeight, queueRows, autocompleteRows int) {
	m.frameLayout = frameLayout{
		width:            width,
		height:           height,
		viewportHeight:   viewportHeight,
		queueRows:        queueRows,
		autocompleteRows: autocompleteRows,
		welcome:          m.showWelcome,
		modal:            m.modalMode,
	}
}

// consumeLayoutRepaint returns a command that forces the renderer to repaint
// every row when the frame layout changed since the last repaint, and nil when
// it did not.
//
// The command re-sends the current window size instead of using
// tea.ClearScreen: a WindowSizeMsg makes standardRenderer.repaint() drop its
// per-row cache without erasing the screen, while ClearScreen blanks the screen
// for a tick (a visible flash). Re-sending our own size is idempotent — the
// model re-applies the same geometry, the layout value does not change again
// and no second repaint is requested, so this cannot loop.
func (m *Model) consumeLayoutRepaint() tea.Cmd {
	if m.frameLayout.width == 0 || m.frameLayout.height == 0 {
		return nil
	}
	if m.frameLayoutRepainted && m.frameLayout == m.lastPaintedLayout {
		return nil
	}
	m.lastPaintedLayout = m.frameLayout
	m.frameLayoutRepainted = true

	width, height := m.width, m.height
	return func() tea.Msg {
		return tea.WindowSizeMsg{Width: width, Height: height}
	}
}
