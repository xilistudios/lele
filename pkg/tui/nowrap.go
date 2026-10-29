package tui

import (
	"io"
	"sync"
)

// DEC private mode 7 is DECAWM (autowrap). Turning it off makes the terminal
// clip a line that is too wide at the right margin instead of wrapping it onto
// the next row.
const (
	autowrapOffSequence = "\x1b[?7l"
	autowrapOnSequence  = "\x1b[?7h"
)

// DisableAutowrap turns the terminal's autowrap off for the lifetime of the TUI
// and returns the function that turns it back on.
//
// The renderer paints a frame row by row and keeps a per-row cache so it can
// skip rows whose text did not change. That contract assumes one frame line is
// exactly one screen row. When a line ends up one cell wider than the terminal
// measures it — the usual cause is an East Asian "ambiguous" glyph (●, →, ·, …)
// that the terminal draws two cells wide while the width tables used by the TUI
// count it as one — the terminal wraps that line, every row below it shifts
// down by one, and the renderer keeps writing to the rows it believes in. The
// rows it then skips keep the previous frame's text, so a status line or the
// input placeholder stays on screen next to its replacement: exactly the ghost
// rows this exists to prevent.
//
// With autowrap off, an over-wide line loses its last cell instead of pushing
// the whole frame down, so the row grid stays aligned no matter how the
// terminal measures the frame. The extra characters land on the right margin,
// which is cosmetic, while a wrapped row corrupts every row below it.
//
// restore is idempotent and safe to call on any path (normal exit, error exit,
// deferred), so callers can hand it to defer and still call it before os.Exit.
// A SIGKILL cannot be intercepted and leaves the mode off, exactly like the
// alternate screen and the hidden cursor bubbletea itself leaves behind; every
// catchable signal goes through the normal return path and restores it.
func DisableAutowrap(out io.Writer) (restore func()) {
	if out == nil {
		return func() {}
	}
	_, _ = io.WriteString(out, autowrapOffSequence)

	var once sync.Once
	return func() {
		once.Do(func() {
			_, _ = io.WriteString(out, autowrapOnSequence)
		})
	}
}
