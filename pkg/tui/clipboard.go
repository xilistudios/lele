package tui

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"time"
)

func (m *Model) copyLastAssistantMessage() bool {
	if m.currentKey == "" {
		return false
	}

	history := m.historyView()
	var lastAssistantContent string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "assistant" && history[i].Content != "" {
			lastAssistantContent = history[i].Content
			break
		}
	}

	if lastAssistantContent == "" {
		return false
	}

	copyToClipboard(lastAssistantContent)
	return true
}

const maxOSC52Bytes = 7500

func copyToClipboard(text string) {
	if len(text) <= maxOSC52Bytes {
		osc52 := buildOSC52(text)
		os.Stdout.WriteString(osc52)
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if runClipboardCmd(ctx, text, "xclip", "-selection", "clipboard") {
			return
		}
		if runClipboardCmd(ctx, text, "xsel", "--clipboard", "--input") {
			return
		}
		if runClipboardCmd(ctx, text, "wl-copy") {
			return
		}
		runClipboardCmd(ctx, text, "pbcopy")
	}()
}

func runClipboardCmd(ctx context.Context, text, name string, args ...string) bool {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

func buildOSC52(text string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return "\x1b]52;c;" + encoded + "\x07"
}
