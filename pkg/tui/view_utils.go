package tui

// calculateViewportHeight reserves every line rendered below the viewport.
// Keeping this budget exact prevents the input and bottom bar from spilling
// past the terminal height and being painted twice by the TUI renderer.
func calculateViewportHeight(contentHeight, statusHeight, autocompleteHeight, inputHeight, bottomHeight int) int {
	otherHeight := 1 + statusHeight + 1 + inputHeight + 1 + bottomHeight
	if autocompleteHeight > 0 {
		otherHeight += autocompleteHeight
	}
	viewportHeight := contentHeight - otherHeight
	if viewportHeight < 1 {
		return 1
	}
	return viewportHeight
}

// getBouncingDots renders an animated bouncing dots indicator for processing state.
func (m *Model) getBouncingDots() string {
	width := 12
	pos := m.animationTick % (2 * (width - 3))
	var offset int
	if pos < width-3 {
		offset = pos
	} else {
		offset = 2*(width-3) - pos
	}

	var sb []byte
	sb = append(sb, '[')
	for i := 0; i < width; i++ {
		if i == offset || i == offset+1 || i == offset+2 {
			sb = append(sb, bouncingDotChar...)
		} else {
			sb = append(sb, ' ')
		}
	}
	sb = append(sb, ']')
	return string(sb)
}
