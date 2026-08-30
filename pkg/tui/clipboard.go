package tui

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"time"
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
		// Each utility gets a short timeout so a hanging tool (e.g. xclip
		// without an X display) can't block this goroutine forever.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// Try xclip (X11)
		if runClipboardCmd(ctx, text, "xclip", "-selection", "clipboard") {
			return
		}
		// Try xsel (X11 alternative)
		if runClipboardCmd(ctx, text, "xsel", "--clipboard", "--input") {
			return
		}
		// Try wl-copy (Wayland)
		if runClipboardCmd(ctx, text, "wl-copy") {
			return
		}
		// Try pbcopy (macOS)
		runClipboardCmd(ctx, text, "pbcopy")
	}()
}

// runClipboardCmd runs a clipboard utility with the given arguments, feeding
// text via stdin. It reports whether the command succeeded (including whether
// it was found on PATH).
func runClipboardCmd(ctx context.Context, text, name string, args ...string) bool {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

// buildOSC52 constructs an OSC 52 escape sequence for copying text to the
// terminal's clipboard. The text is base64-encoded as required by the spec.
func buildOSC52(text string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return "\x1b]52;c;" + encoded + "\x07"
}
