package i18n

import (
	"fmt"
	"strings"
	"testing"
)

func initHelpers(lang string) {
	Init()
	SetLanguage(lang)
}

func TestFormatted(t *testing.T) {
	initHelpers("es")

	// Known key with an argument substitution.
	got := Formatted("tui.used", 42.5)
	want := fmt.Sprintf("%.1f%% usado", 42.5)
	if got != want {
		t.Errorf("Formatted(tui.used, 42.5) = %q, want %q", got, want)
	}

	// Multiple args.
	got = Formatted("tui.doneIn", 1.5)
	want = "● Completado en 1.5s"
	if got != want {
		t.Errorf("Formatted(tui.doneIn, 1.5) = %q, want %q", got, want)
	}

	// Missing key -> Formatted uses the key as a literal format string (no
	// verbs), so the key text is retained even when extra args are passed.
	if got := Formatted("no.such.key", "x"); !strings.HasPrefix(got, "no.such.key") {
		t.Errorf("Formatted(missing) = %q, want it to retain the key", got)
	}
}

func TestPlural(t *testing.T) {
	initHelpers("es")

	// We use two arbitrary but valid keys: singular count 1 vs other counts.
	single := T("tui.send")
	plural := T("tui.help")

	if got := Plural("tui.send", "tui.help", 1); got != single {
		t.Errorf("Plural(..., 1) = %q, want singular %q", got, single)
	}
	if got := Plural("tui.send", "tui.help", 0); got != plural {
		t.Errorf("Plural(..., 0) = %q, want plural %q", got, plural)
	}
	if got := Plural("tui.send", "tui.help", 42); got != plural {
		t.Errorf("Plural(..., 42) = %q, want plural %q", got, plural)
	}
}

func TestJoinTranslations(t *testing.T) {
	initHelpers("en")

	got := JoinTranslations(" | ", "tui.send", "tui.help", "tui.quit")
	want := "Send | Help | Quit"
	if got != want {
		t.Errorf("JoinTranslations = %q, want %q", got, want)
	}

	// Single key.
	if got := JoinTranslations(" | ", "tui.help"); got != "Help" {
		t.Errorf("JoinTranslations single = %q, want Help", got)
	}

	// Empty keys.
	if got := JoinTranslations(", "); got != "" {
		t.Errorf("JoinTranslations empty = %q, want empty string", got)
	}
}

func TestStatusWithLabel(t *testing.T) {
	initHelpers("es")

	got := StatusWithLabel("tui.status", "tui.status.ready")
	want := "Estado: Listo"
	if got != want {
		t.Errorf("StatusWithLabel = %q, want %q", got, want)
	}

	// English.
	SetLanguage("en")
	got = StatusWithLabel("tui.status", "tui.status.thinking")
	want = "Status: Thinking..."
	if got != want {
		t.Errorf("StatusWithLabel(en) = %q, want %q", got, want)
	}
}