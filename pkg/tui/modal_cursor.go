package tui

// clampModalCursor keeps the modal cursor within bounds after the modal list
// has been reloaded/shrunk (e.g. after deleting the last item), and keeps the
// scroll offset consistent with the cursor. Callers must have finished
// rebuilding m.modalItems before invoking this.
//
// It mirrors the clamp idiom already used by reselectCronJob (cron.go) and
// reselectSecret (secrets.go): when the list shrank, the cursor moves to the
// last valid row; when the list is empty, the cursor resets to 0 (the render
// loops and action handlers guard emptiness separately).
func (m *Model) clampModalCursor() {
	if m.modalSelectedIdx < 0 {
		m.modalSelectedIdx = 0
	}
	if n := len(m.modalItems); m.modalSelectedIdx >= n {
		m.modalSelectedIdx = n - 1
		if m.modalSelectedIdx < 0 {
			m.modalSelectedIdx = 0
		}
	}
	if m.modalScrollOffset > m.modalSelectedIdx {
		m.modalScrollOffset = m.modalSelectedIdx
	}
	if m.modalScrollOffset < 0 {
		m.modalScrollOffset = 0
	}
}
