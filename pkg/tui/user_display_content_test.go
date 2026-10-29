package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/xilistudios/lele/pkg/providers"
)

// Tests for the user-bubble rule of command-driven turns.
//
// A custom slash command (/pr revisa los tests) is expanded by the backend
// before the turn runs: providers.Message.Content ends up holding the EXPANDED
// prompt — the whole command template — because that is what the model must
// replay, while DisplayContent keeps the "/name args" the human typed. The TUI
// used to render Content unconditionally, so the transcript showed the entire
// command body instead of what was typed (and the pending copy never matched,
// because matching also compared Content).

// seedCommandTurn stores one command-driven user turn plus an assistant reply
// and returns the model pointed at that session.
func seedCommandTurn(t *testing.T, key, expanded, typed string) *Model {
	t.Helper()
	m := newTestModel(t)
	m.sessionMgr.GetOrCreate(key)
	m.sessionMgr.AddFullMessage(key, providers.Message{
		Role:           "user",
		Content:        expanded,
		DisplayContent: typed,
		Command:        &providers.CommandApplied{Name: "pr", Args: "revisa los tests"},
	})
	m.sessionMgr.AddMessage(key, "assistant", "Listo, PR en marcha.")

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	renderLazyModel(t, m)
	return m
}

func TestUserBubble_ShowsDisplayContentNotExpandedPrompt(t *testing.T) {
	const typed = "/pr revisa los tests"
	const expanded = "inicia el pr creando la rama, el commit y el pull request"
	m := seedCommandTurn(t, "tui:chat:cmd-display", expanded, typed)

	out := ansi.Strip(renderedBase(m))
	if !strings.Contains(out, typed) {
		t.Fatalf("rendered transcript is missing what the user typed (%q):\n%s", typed, out)
	}
	if strings.Contains(out, "creando la rama") {
		t.Fatalf("transcript leaked the expanded command prompt:\n%s", out)
	}
}

// TestUserBubble_PlainMessageUnchanged guards the fallback: without
// DisplayContent the bubble still renders Content, so normal messages are
// untouched by the rule.
func TestUserBubble_PlainMessageUnchanged(t *testing.T) {
	m := seedCommandTurn(t, "tui:chat:cmd-plain", "hola mundo", "")

	out := ansi.Strip(renderedBase(m))
	if !strings.Contains(out, "hola mundo") {
		t.Fatalf("plain user message not rendered:\n%s", out)
	}
}

// TestIsPendingUserMessage_MatchesDisplayContent pins why the check exists: the
// pending copy is the typed text, so it can only be found through
// DisplayContent. Matching Content never worked and left the turn either
// duplicated or replaced by the template.
func TestIsPendingUserMessage_MatchesDisplayContent(t *testing.T) {
	const typed = "/pr revisa los tests"
	cmdMsg := providers.Message{Role: "user", Content: "expanded prompt", DisplayContent: typed}
	if !isPendingUserMessage(cmdMsg, typed) {
		t.Error("pending typed text must match a command turn through DisplayContent")
	}
	if isPendingUserMessage(cmdMsg, "expanded prompt") {
		t.Error("the expanded prompt must never confirm the pending bubble")
	}

	// Verbatim storage vs trimmed pending: the comparison is trimmed on both
	// sides (publishUserMessage stores the trimmed input).
	padded := providers.Message{Role: "user", Content: "x", DisplayContent: "  " + typed + "\n"}
	if !isPendingUserMessage(padded, typed) {
		t.Error("trailing whitespace in DisplayContent must not break the match")
	}

	// Non-user roles and empty pending text never match.
	if isPendingUserMessage(providers.Message{Role: "assistant", Content: typed}, typed) {
		t.Error("assistant messages must not confirm the pending user bubble")
	}
	if isPendingUserMessage(cmdMsg, "") {
		t.Error("empty pending text must not match")
	}

	// Plain message: falls back to Content.
	plain := providers.Message{Role: "user", Content: "hola"}
	if !isPendingUserMessage(plain, "hola") {
		t.Error("plain user message must match on Content")
	}
}

// TestMessageFingerprint_DisplayContentChangesKey guards the per-message render
// cache: the bubble is rendered from DisplayContent, so a snapshot that gains
// the display metadata must not reuse the entry rendered from the expanded
// Content.
func TestMessageFingerprint_DisplayContentChangesKey(t *testing.T) {
	base := providers.Message{Role: "user", Content: "expanded prompt"}
	withDisplay := providers.Message{Role: "user", Content: "expanded prompt", DisplayContent: "/pr x"}

	if messageFingerprint(base, 80) == messageFingerprint(withDisplay, 80) {
		t.Fatal("fingerprint ignores DisplayContent: the render cache would keep the expanded prompt")
	}

	otherDisplay := providers.Message{Role: "user", Content: "expanded prompt", DisplayContent: "/pr y"}
	if messageFingerprint(withDisplay, 80) == messageFingerprint(otherDisplay, 80) {
		t.Fatal("fingerprint collides on two different DisplayContent values")
	}
}
