package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleAutocompleteKey processes a keystroke while the slash-command
// autocomplete dropdown is open. handled reports whether the key was consumed:
// "tab"/"enter" with no items dismiss the dropdown and must fall through to the
// normal key handling below, exactly as in the original inline block.
func (m *Model) handleAutocompleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "up", "ctrl+k":
		if m.autocompleteIdx > 0 {
			m.autocompleteIdx--
		} else {
			m.autocompleteIdx = len(m.autocompleteItems) - 1
		}
		return m, nil, true
	case "down", "ctrl+j":
		if m.autocompleteIdx < len(m.autocompleteItems)-1 {
			m.autocompleteIdx++
		} else {
			m.autocompleteIdx = 0
		}
		return m, nil, true
	case "tab":
		if len(m.autocompleteItems) > 0 {
			completed := m.autocompleteItems[m.autocompleteIdx].name
			m.chatInput.SetValue(completed)
			m.showAutocomplete = false
			// Tab only fills the input — lets the user add arguments
			// before pressing Enter to execute.
			return m, nil, true
		}
		m.showAutocomplete = false
	case "enter":
		if len(m.autocompleteItems) > 0 {
			completed := m.autocompleteItems[m.autocompleteIdx].name
			m.showAutocomplete = false
			// If the user already typed arguments beyond the command
			// name (e.g. "/goal achieve X"), execute the full input
			// directly instead of just the completed command.
			inputVal := m.chatInput.Value()
			if strings.HasPrefix(inputVal, completed) && len(inputVal) > len(completed) && inputVal[len(completed)] == ' ' {
				m.chatInput.SetValue("")
				cmd := m.executeCommand(inputVal)
				if cmd != nil {
					return m, cmd, true
				}
				return m, nil, true
			}
			// /goal needs a text argument — fill but don't execute.
			if completed == "/goal" {
				m.chatInput.SetValue(completed)
				return m, nil, true
			}
			// All other commands execute immediately.
			m.chatInput.SetValue("")
			cmd := m.executeCommand(completed)
			if cmd != nil {
				return m, cmd, true
			}
			return m, nil, true
		}
		// No autocomplete items — dismiss autocomplete and let
		// Enter fall through to send the message.
		m.showAutocomplete = false
	case "esc":
		m.showAutocomplete = false
		return m, nil, true
	}

	return m, nil, false
}
