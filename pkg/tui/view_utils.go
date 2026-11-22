package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func leadingBlankRows(s string) int {
	rows := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(ansi.Strip(line)) != "" {
			return rows
		}
		rows++
	}
	return rows
}

func calculateViewportHeight(contentHeight, statusHeight, queueRowHeight, autocompleteHeight, inputHeight, bottomHeight int) int {
	otherHeight := 1 + statusHeight + 1 + queueRowHeight + inputHeight + 1 + bottomHeight
	if autocompleteHeight > 0 {
		otherHeight += autocompleteHeight
	}
	viewportHeight := contentHeight - otherHeight
	if viewportHeight < 1 {
		return 1
	}
	return viewportHeight
}

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
