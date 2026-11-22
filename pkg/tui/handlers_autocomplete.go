package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleAutocompleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "up", "ctrl+k":
		if len(m.autocompleteItems) == 0 {
			return m, nil, true
		}
		if m.autocompleteIdx > 0 {
			m.autocompleteIdx--
		} else {
			m.autocompleteIdx = len(m.autocompleteItems) - 1
		}
		return m, nil, true
	case "down", "ctrl+j":
		if len(m.autocompleteItems) == 0 {
			return m, nil, true
		}
		if m.autocompleteIdx < len(m.autocompleteItems)-1 {
			m.autocompleteIdx++
		} else {
			m.autocompleteIdx = 0
		}
		return m, nil, true
	case "tab":
		if m.autocompleteIdx < 0 {
			m.autocompleteIdx = 0
		}
		if m.autocompleteIdx < len(m.autocompleteItems) {
			completed := m.autocompleteItems[m.autocompleteIdx].name
			m.chatInput.SetValue(completed)
			m.showAutocomplete = false
			return m, nil, true
		}
		m.showAutocomplete = false
	case "enter":
		if m.autocompleteIdx < 0 {
			m.autocompleteIdx = 0
		}
		if m.autocompleteIdx < len(m.autocompleteItems) {
			completed := m.autocompleteItems[m.autocompleteIdx].name
			m.showAutocomplete = false
			inputVal := m.chatInput.Value()
			if strings.HasPrefix(inputVal, completed) && len(inputVal) > len(completed) && inputVal[len(completed)] == ' ' {
				m.chatInput.SetValue("")
				cmd := m.executeCommand(inputVal)
				if cmd != nil {
					return m, cmd, true
				}
				return m, nil, true
			}
			if completed == "/goal" {
				m.chatInput.SetValue(completed)
				return m, nil, true
			}
			m.chatInput.SetValue("")
			cmd := m.executeCommand(completed)
			if cmd != nil {
				return m, cmd, true
			}
			return m, nil, true
		}
		m.showAutocomplete = false
	case "esc":
		m.showAutocomplete = false
		return m, nil, true
	}

	return m, nil, false
}
