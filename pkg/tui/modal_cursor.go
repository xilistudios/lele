package tui

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
