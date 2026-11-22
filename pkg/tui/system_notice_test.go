package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func TestParseSystemNotice(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantSender string
		wantBody   string
		wantOK     bool
	}{
		{"subagent result", "[System: subagent] the subagent result", "subagent", "the subagent result", true},
		{"no space after colon", "[System:subagent]body", "subagent", "body", true},
		{"sender padded", "[System:   cron  ]  body  ", "cron", "body", true},
		{"multi-line body", "[System: subagent] Subagent status update.\nTask ID: t1", "subagent", "Subagent status update.\nTask ID: t1", true},
		{"empty body", "[System: subagent]", "subagent", "", true},
		{"empty body with spaces", "[System: subagent]   ", "subagent", "", true},
		{"empty sender", "[System: ] body", "", "", false},
		{"lowercase marker", "[system: subagent] body", "", "", false},
		{"marker mid-sentence", "hola [System: subagent] body", "", "", false},
		{"leading space", " [System: subagent] body", "", "", false},
		{"leading newline", "\n[System: subagent] body", "", "", false},
		{"plain user message", "hola mundo", "", "", false},
		{"empty content", "", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender, body, ok := parseSystemNotice(tc.content)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (content %q)", ok, tc.wantOK, tc.content)
			}
			if sender != tc.wantSender || body != tc.wantBody {
				t.Fatalf("got (%q, %q), want (%q, %q)", sender, body, tc.wantSender, tc.wantBody)
			}
		})
	}
}

func TestRenderSystemNotice_CardShape(t *testing.T) {
	i18n.InitWithLanguage("en")
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	label := i18n.T("tui.systemNotice")
	block := renderSystemNotice("subagent", "Subagent status update.\nTask ID: t1", 80)
	rows := resultRows(block)
	plain := ansi.Strip(block)

	if !strings.Contains(ansi.Strip(rows[0]), label) {
		t.Fatalf("header row missing label %q: %q", label, ansi.Strip(rows[0]))
	}
	if !strings.Contains(ansi.Strip(rows[0]), "subagent") {
		t.Fatalf("header row missing sender: %q", ansi.Strip(rows[0]))
	}
	if strings.Contains(plain, "[System:") {
		t.Fatalf("raw marker leaked into the card:\n%s", plain)
	}
	if !strings.Contains(plain, "Task ID: t1") {
		t.Fatalf("body missing from the card:\n%s", plain)
	}
	if len(rows) < 3 {
		t.Fatalf("want >= 3 rows (header + 2 body lines), got %d:\n%s", len(rows), plain)
	}
	for i, row := range rows[1:] {
		if !strings.Contains(ansi.Strip(row), "│") {
			t.Fatalf("body row %d missing box border: %q", i+1, ansi.Strip(row))
		}
	}
}

func TestRenderSystemNotice_EmptyBody(t *testing.T) {
	i18n.InitWithLanguage("en")
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	block := renderSystemNotice("subagent", "", 80)
	if rows := resultRows(block); len(rows) != 1 {
		t.Fatalf("want header only, got %d rows:\n%s", len(rows), ansi.Strip(block))
	}
	if strings.Contains(ansi.Strip(block), "│") {
		t.Fatalf("empty body must not render a box: %q", ansi.Strip(block))
	}
}

func TestRenderSystemNotice_FitsWidth(t *testing.T) {
	longBody := strings.Repeat("the subagent produced a long result that needs wrapping ", 6)
	for _, width := range []int{40, 60, 100, 140} {
		block := renderSystemNotice("subagent-with-a-long-sender-name", longBody, width)
		for i, row := range resultRows(block) {
			if w := ansi.StringWidth(row); w > width {
				t.Fatalf("width=%d row %d overflows: %d cells: %q", width, i, w, ansi.Strip(row))
			}
		}
	}
}

func TestView_SystemNoticeRendersAsCard(t *testing.T) {
	forceTrueColor(t)
	i18n.InitWithLanguage("en")
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	m := newTestModel(t)

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*Model)

	key := "tui:chat:system-notice"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "hola mundo")
	m.sessionMgr.AddMessage(key, "user", "[System: subagent] Subagent status update.\nTask ID: t1")

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
	m.updateViewport()

	frame := m.View()
	plain := ansi.Strip(frame)

	if strings.Contains(plain, "[System:") {
		t.Fatalf("raw marker leaked into the frame:\n%s", plain)
	}
	if !strings.Contains(plain, i18n.T("tui.systemNotice")) {
		t.Fatalf("frame missing the %q card header:\n%s", i18n.T("tui.systemNotice"), plain)
	}
	if !strings.Contains(plain, "Task ID: t1") {
		t.Fatalf("frame missing the notice body:\n%s", plain)
	}
	if !strings.Contains(plain, "hola mundo") {
		t.Fatalf("frame missing the real user message:\n%s", plain)
	}
	if n := strings.Count(plain, i18n.T("tui.you")); n != 1 {
		t.Fatalf("found %d %q role lines, want 1 (the notice must not be a user bubble):\n%s", n, i18n.T("tui.you"), plain)
	}
	for i, line := range strings.Split(frame, "\n") {
		if w := ansi.StringWidth(line); w > 100 {
			t.Fatalf("frame row %d exceeds terminal width: %d cells: %q", i, w, ansi.Strip(line))
		}
	}
}

func TestSystemNoticeKeyPresentInBuiltinLocales(t *testing.T) {
	for _, lang := range i18n.BuiltinLanguages() {
		i18n.InitWithLanguage(lang)
		got := i18n.T("tui.systemNotice")
		if got == "" || got == "tui.systemNotice" {
			t.Fatalf("%s: tui.systemNotice missing (T = %q)", lang, got)
		}
	}
	i18n.InitWithLanguage("en")
}
