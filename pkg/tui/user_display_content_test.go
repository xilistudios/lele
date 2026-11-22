package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/xilistudios/lele/pkg/providers"
)

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

func TestUserBubble_PlainMessageUnchanged(t *testing.T) {
	m := seedCommandTurn(t, "tui:chat:cmd-plain", "hola mundo", "")

	out := ansi.Strip(renderedBase(m))
	if !strings.Contains(out, "hola mundo") {
		t.Fatalf("plain user message not rendered:\n%s", out)
	}
}

func TestIsPendingUserMessage_MatchesDisplayContent(t *testing.T) {
	const typed = "/pr revisa los tests"
	cmdMsg := providers.Message{Role: "user", Content: "expanded prompt", DisplayContent: typed}
	if !isPendingUserMessage(cmdMsg, typed) {
		t.Error("pending typed text must match a command turn through DisplayContent")
	}
	if isPendingUserMessage(cmdMsg, "expanded prompt") {
		t.Error("the expanded prompt must never confirm the pending bubble")
	}

	padded := providers.Message{Role: "user", Content: "x", DisplayContent: "  " + typed + "\n"}
	if !isPendingUserMessage(padded, typed) {
		t.Error("trailing whitespace in DisplayContent must not break the match")
	}

	if isPendingUserMessage(providers.Message{Role: "assistant", Content: typed}, typed) {
		t.Error("assistant messages must not confirm the pending user bubble")
	}
	if isPendingUserMessage(cmdMsg, "") {
		t.Error("empty pending text must not match")
	}

	plain := providers.Message{Role: "user", Content: "hola"}
	if !isPendingUserMessage(plain, "hola") {
		t.Error("plain user message must match on Content")
	}
}

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
