package tui

import (
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
)

// copyLastAssistantMessage copies the last assistant message content to the
// system clipboard. It tries OSC 52 first (works in most modern terminals like
// iTerm2, Alacritty, Kitty, WezTerm, Windows Terminal), then falls back to
// xclip, xsel, wl-copy, and pbcopy in that order.
func (m *Model) copyLastAssistantMessage() {
	if m.currentKey == "" {
		return
	}

	history := m.agentLoop.GetProvidable().GetHistoryView(m.currentKey)
	var lastAssistantContent string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" && history[i].Content != "" {
			lastAssistantContent = history[i].Content
			break
		}
	}

	if lastAssistantContent == "" {
		return
	}

	copyToClipboard(lastAssistantContent)
}

// copyToClipboard copies text to the system clipboard using OSC 52 first,
// then falling back to platform clipboard utilities.
func copyToClipboard(text string) {
	// Try OSC 52 escape sequence (terminal-native clipboard)
	// This works in iTerm2, Alacritty, Kitty, WezTerm, Windows Terminal, etc.
	osc52 := buildOSC52(text)
	os.Stdout.WriteString(osc52)

	// Also try platform utilities as a fallback / for terminals that don't
	// support OSC 52. Run in a goroutine so it doesn't block the UI.
	go func() {
		// Try xclip (X11)
		if cmd := exec.Command("xclip", "-selection", "clipboard"); cmd != nil {
			cmd.Stdin = strings.NewReader(text)
			if cmd.Run() == nil {
				return
			}
		}
		// Try xsel (X11 alternative)
		if cmd := exec.Command("xsel", "--clipboard", "--input"); cmd != nil {
			cmd.Stdin = strings.NewReader(text)
			if cmd.Run() == nil {
				return
			}
		}
		// Try wl-copy (Wayland)
		if cmd := exec.Command("wl-copy"); cmd != nil {
			cmd.Stdin = strings.NewReader(text)
			if cmd.Run() == nil {
				return
			}
		}
		// Try pbcopy (macOS)
		if cmd := exec.Command("pbcopy"); cmd != nil {
			cmd.Stdin = strings.NewReader(text)
			if cmd.Run() == nil {
				return
			}
		}
	}()
}

// buildOSC52 constructs an OSC 52 escape sequence for copying text to the
// terminal's clipboard. The text is base64-encoded as required by the spec.
func buildOSC52(text string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return "\x1b]52;c;" + encoded + "\x07"
}
