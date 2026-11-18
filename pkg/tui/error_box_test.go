package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// LLM-failure boxes
// -----------------
// A turn whose LLM request failed after every retry is persisted as an assistant
// message carrying ErrorDetail (json error_detail) with ExcludeFromContext=true.
// It must render as a red box (header + bordered detail) INSTEAD of a normal
// assistant bubble, and must survive the render cache like any other row.

// TestRenderErrorBox_Shape pins the box structure: a bold red header carrying
// the localized title, then the detail inside a left-bordered box. No emoji is
// involved on purpose (VS16 grapheme widths break the row budget).
func TestRenderErrorBox_Shape(t *testing.T) {
	i18n.InitWithLanguage("en")
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	detail := "LLM call failed after retries: 429 Too Many Requests"
	block := renderErrorBox(detail, 80)
	rows := resultRows(block)
	plain := ansi.Strip(block)

	if !strings.Contains(ansi.Strip(rows[0]), i18n.T("tui.llmErrorTitle")) {
		t.Fatalf("header row missing the localized title %q: %q", i18n.T("tui.llmErrorTitle"), ansi.Strip(rows[0]))
	}
	if !strings.Contains(plain, "429 Too Many Requests") {
		t.Fatalf("detail missing from the box:\n%s", plain)
	}
	if len(rows) < 2 {
		t.Fatalf("want a header row plus boxed rows, got %d:\n%s", len(rows), plain)
	}
	for i, row := range rows[1:] {
		if !strings.Contains(ansi.Strip(row), "│") {
			t.Fatalf("body row %d missing box border: %q", i+1, ansi.Strip(row))
		}
	}
	// The header must never be the raw i18n key nor an emoji-broken row.
	if strings.Contains(plain, "tui.llmErrorTitle") {
		t.Fatalf("raw i18n key leaked into the render:\n%s", plain)
	}
}

// TestRenderErrorBox_EmptyDetail pins that a detail-less failure renders the
// header only — no dangling empty box.
func TestRenderErrorBox_EmptyDetail(t *testing.T) {
	i18n.InitWithLanguage("en")
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	for _, detail := range []string{"", "   ", "\n\n"} {
		block := renderErrorBox(detail, 80)
		if rows := resultRows(block); len(rows) != 1 {
			t.Fatalf("detail %q: want header only, got %d rows:\n%s", detail, len(rows), ansi.Strip(block))
		}
		if strings.Contains(ansi.Strip(block), "│") {
			t.Fatalf("detail %q must not render a box: %q", detail, ansi.Strip(block))
		}
	}
}

// TestRenderErrorBox_FitsWidth is the frame-cascade guard: every row must fit
// the pane, or lineViewport's lipgloss Width() pass re-wraps the styled rows and
// drops the indent and the border on the continuation rows.
func TestRenderErrorBox_FitsWidth(t *testing.T) {
	// A long error with spaces and an unbroken URL-like token (no break
	// opportunities) — wrapText must still keep every row inside the budget.
	longDetail := strings.Repeat("provider rejected the request after 3 retries: ", 4) +
		"https://api.example.com/v1/messages?beta=true&model=claude-sonnet-4-5-20250929"

	for _, width := range []int{20, 40, 60, 80, 120} {
		block := renderErrorBox(longDetail, width)
		rows := resultRows(block)
		borderCol := -1
		for i, row := range rows {
			if w := ansi.StringWidth(row); w > width {
				t.Fatalf("width=%d row %d overflows: %d cells: %q", width, i, w, ansi.Strip(row))
			}
			if i == 0 {
				continue
			}
			col := cellIndex(ansi.Strip(row), '│')
			if col < 0 {
				t.Fatalf("width=%d row %d missing box border: %q", width, i, ansi.Strip(row))
			}
			if borderCol == -1 {
				borderCol = col
			} else if col != borderCol {
				t.Fatalf("width=%d row %d border col %d != %d: %q", width, i, col, borderCol, ansi.Strip(row))
			}
			// Rows stay aligned under the header (2-space indent).
			if !strings.HasPrefix(ansi.Strip(row), "  ") {
				t.Fatalf("width=%d row %d lost the indent: %q", width, i, ansi.Strip(row))
			}
		}
	}
}

// TestMessageFingerprint_IncludesErrorDetail guards the render cache: the same
// Content renders as an error box when ErrorDetail is set and as a normal
// assistant bubble otherwise, so the two must never share a fingerprint.
func TestMessageFingerprint_IncludesErrorDetail(t *testing.T) {
	content := "❌ Error processing message: LLM call failed after retries"
	plain := providers.Message{Role: "assistant", Content: content}
	failed := providers.Message{Role: "assistant", Content: content, ErrorDetail: "LLM call failed after retries"}

	if messageFingerprint(plain, 80) == messageFingerprint(failed, 80) {
		t.Fatal("fingerprint must differ when ErrorDetail changes the rendering")
	}

	// Same ErrorDetail → same fingerprint (cache hits stay possible).
	other := providers.Message{Role: "assistant", Content: content, ErrorDetail: "LLM call failed after retries"}
	if messageFingerprint(failed, 80) != messageFingerprint(other, 80) {
		t.Fatal("fingerprint must be stable for equal messages")
	}

	// Different details → different fingerprints.
	third := providers.Message{Role: "assistant", Content: content, ErrorDetail: "context deadline exceeded"}
	if messageFingerprint(failed, 80) == messageFingerprint(third, 80) {
		t.Fatal("fingerprint must differ when ErrorDetail changes")
	}
}

// TestView_LLMErrorRendersAsBox is the end-to-end guard: a persisted failure
// renders as the red box (title + detail), NOT as a normal assistant bubble
// (no agent-name header, and the legacy "❌ Error …" Content stays hidden).
func TestView_LLMErrorRendersAsBox(t *testing.T) {
	forceTrueColor(t)
	i18n.InitWithLanguage("en")
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)

	key := "tui:chat:llm-error"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "hello there")
	m.sessionMgr.AddFullMessage(key, providers.Message{
		Role:               "assistant",
		Content:            "❌ Error processing message: LLM call failed after retries: 429 Too Many Requests",
		ErrorDetail:        "LLM call failed after retries: 429 Too Many Requests",
		ExcludeFromContext: true,
	})

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
	m.updateViewport()

	frame := m.View()
	plain := ansi.Strip(frame)

	if !strings.Contains(plain, i18n.T("tui.llmErrorTitle")) {
		t.Fatalf("frame missing the error box header %q:\n%s", i18n.T("tui.llmErrorTitle"), plain)
	}
	if !strings.Contains(plain, "429 Too Many Requests") {
		t.Fatalf("frame missing the error detail:\n%s", plain)
	}
	// The error box replaces the assistant bubble: only the real user message
	// keeps a "You" role line, and the box border is present.
	if !strings.Contains(plain, i18n.T("tui.you")) {
		t.Fatalf("frame missing the user message:\n%s", plain)
	}
	if !strings.Contains(plain, "│") {
		t.Fatalf("frame missing the error box border:\n%s", plain)
	}
	// The legacy plain-text error marker must not be rendered as content: the
	// box only shows the detail, and the header carries the semantics.
	if strings.Contains(plain, "❌ Error processing message") {
		t.Fatalf("legacy error text rendered alongside the box:\n%s", plain)
	}
	for i, line := range strings.Split(frame, "\n") {
		if w := ansi.StringWidth(line); w > 100 {
			t.Fatalf("frame row %d exceeds terminal width: %d cells: %q", i, w, ansi.Strip(line))
		}
	}
}

// TestLLMErrorTitlePresentInBuiltinLocales guards the label itself: T() falls
// back to the default locale for a missing key, so only a verbatim lookup in
// each builtin pack proves the key was not forgotten in one of them.
func TestLLMErrorTitlePresentInBuiltinLocales(t *testing.T) {
	defer i18n.InitWithLanguage("en")
	for _, lang := range i18n.BuiltinLanguages() {
		i18n.InitWithLanguage(lang)
		got := i18n.T("tui.llmErrorTitle")
		if got == "" || got == "tui.llmErrorTitle" {
			t.Fatalf("%s: tui.llmErrorTitle missing (T = %q)", lang, got)
		}
	}
}
