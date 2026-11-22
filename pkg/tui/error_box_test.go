package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/xilistudios/lele/pkg/providers"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

const (
	errorBoxDetail       = "LLM call failed after retries: 429 Too Many Requests"
	errorBoxLegacyText   = "❌ Error processing message: " + errorBoxDetail
	errorBoxLongDetail   = "provider rejected the request after 3 retries: 429 Too Many Requests"
	errorBoxLongerDetail = errorBoxLongDetail + " https://api.example.com/v1/messages?beta=true"
)

func leftColumn(frame string) string {
	rows := strings.Split(frame, "\n")
	if len(rows) == 0 {
		return frame
	}
	if cols := boxRuneColumns(rows[len(rows)-1]); len(cols) != 1 {
		return frame
	} else if sep := cols[0]; sep > 0 {
		out := make([]string, len(rows))
		for i, row := range rows {
			out[i] = ansi.Truncate(row, sep, "")
		}
		return strings.Join(out, "\n")
	}
	return frame
}

func boxRuneColumns(row string) []int {
	var cols []int
	col := 0
	for _, r := range ansi.Strip(row) {
		if r == '│' {
			cols = append(cols, col)
		}
		col += ansi.StringWidth(string(r))
	}
	return cols
}

func squashBox(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '│':
			return -1
		}
		return r
	}, s)
}

func errorBoxFrame(t *testing.T, width int, lang string, msg providers.Message) func() string {
	t.Helper()
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	m := newTestModel(t)
	if lang != "" {
		i18n.InitWithLanguage(lang)
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 40})
	m = updated.(*Model)

	key := "tui:chat:llm-error"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	m.sessionMgr.AddMessage(key, "user", "hello there")
	m.sessionMgr.AddFullMessage(key, msg)

	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.reloadSessions()
	m.updateViewport()

	return m.View
}

func TestView_LLMErrorBox(t *testing.T) {
	forceTrueColor(t)
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	for _, lang := range []string{"en", "es", "pt"} {
		for _, width := range []int{20, 40, 80, 120} {
			t.Run(lang+"/width="+itoa(width), func(t *testing.T) {
				view := errorBoxFrame(t, width, lang, providers.Message{
					Role:               "assistant",
					Content:            errorBoxLegacyText,
					ErrorDetail:        errorBoxDetail,
					ExcludeFromContext: true,
				})
				frame := view()
				plain := ansi.Strip(frame)

				squashed := squashBox(leftColumn(plain))
				if !strings.Contains(squashed, squashBox(i18n.T("tui.llmErrorTitle"))) {
					t.Fatalf("frame missing the localized title %q:\n%s", i18n.T("tui.llmErrorTitle"), plain)
				}
				if !strings.Contains(squashed, squashBox(errorBoxDetail)) {
					t.Fatalf("frame missing the error detail:\n%s", plain)
				}

				if strings.Contains(plain, "❌ Error processing message") {
					t.Fatalf("legacy error text rendered alongside the box:\n%s", plain)
				}
				if !strings.Contains(plain, "│") {
					t.Fatalf("frame missing the error box border:\n%s", plain)
				}
				if !strings.Contains(squashed, squashBox("hello there")) {
					t.Fatalf("frame lost the user message:\n%s", plain)
				}

				for i, line := range strings.Split(frame, "\n") {
					if w := ansi.StringWidth(line); w > width {
						t.Fatalf("frame row %d exceeds the terminal width: %d > %d cells: %q",
							i, w, width, ansi.Strip(line))
					}
				}
			})
		}
	}
}

func TestView_LLMErrorBoxReplacesNormalBubble(t *testing.T) {
	forceTrueColor(t)

	const width = 100
	title := i18n.T("tui.llmErrorTitle")

	failedFrame := leftColumn(ansi.Strip(errorBoxFrame(t, width, "en", providers.Message{
		Role:               "assistant",
		Content:            errorBoxLegacyText,
		ErrorDetail:        errorBoxDetail,
		ExcludeFromContext: true,
	})()))

	plainFrame := leftColumn(ansi.Strip(errorBoxFrame(t, width, "en", providers.Message{
		Role:               "assistant",
		Content:            errorBoxLegacyText,
		ExcludeFromContext: true,
	})()))

	if !strings.Contains(failedFrame, title) {
		t.Fatalf("the failure did not render as an error box:\n%s", failedFrame)
	}
	if strings.Contains(failedFrame, "❌ Error processing message") {
		t.Fatalf("the failure rendered its legacy text too:\n%s", failedFrame)
	}
	if strings.Contains(plainFrame, title) {
		t.Fatalf("a plain assistant message was rendered as an error box:\n%s", plainFrame)
	}
	if !strings.Contains(plainFrame, "❌ Error processing message") {
		t.Fatalf("a plain assistant message lost its content:\n%s", plainFrame)
	}
}

func TestView_LLMErrorBoxRepeatedRenders(t *testing.T) {
	forceTrueColor(t)
	i18n.InitWithLanguage("en")
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	view := errorBoxFrame(t, 100, "en", providers.Message{
		Role:               "assistant",
		Content:            errorBoxLegacyText,
		ErrorDetail:        errorBoxDetail,
		ExcludeFromContext: true,
	})

	first := leftColumn(ansi.Strip(view()))
	second := leftColumn(ansi.Strip(view()))
	if first != second {
		t.Fatalf("the same message rendered differently on a second pass:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}

	other := leftColumn(ansi.Strip(errorBoxFrame(t, 100, "en", providers.Message{
		Role:               "assistant",
		Content:            errorBoxLegacyText,
		ErrorDetail:        "context deadline exceeded",
		ExcludeFromContext: true,
	})()))
	if other == first {
		t.Fatal("two different failure details produced the same frame")
	}
	if !strings.Contains(squashBox(other), squashBox("context deadline exceeded")) {
		t.Fatalf("the second failure's detail was not displayed:\n%s", other)
	}
	if strings.Contains(squashBox(other), squashBox(errorBoxDetail)) {
		t.Fatalf("the first failure's detail leaked into the second render:\n%s", other)
	}
}

func TestView_LLMErrorBoxWrapsLongDetail(t *testing.T) {
	forceTrueColor(t)
	i18n.InitWithLanguage("en")
	t.Cleanup(func() { i18n.InitWithLanguage("en") })

	for _, width := range []int{40, 80, 120} {
		t.Run("width="+itoa(width), func(t *testing.T) {
			frame := errorBoxFrame(t, width, "en", providers.Message{
				Role:               "assistant",
				Content:            "❌ Error processing message: " + errorBoxLongerDetail,
				ErrorDetail:        errorBoxLongerDetail,
				ExcludeFromContext: true,
			})()

			if !strings.Contains(squashBox(leftColumn(ansi.Strip(frame))), squashBox(errorBoxLongerDetail)) {
				t.Fatalf("width=%d: the whole detail must stay visible:\n%s", width, ansi.Strip(frame))
			}
			for i, line := range strings.Split(frame, "\n") {
				if w := ansi.StringWidth(line); w > width {
					t.Fatalf("width=%d row %d overflows: %d cells: %q", width, i, w, ansi.Strip(line))
				}
			}
		})
	}
}

func TestLLMErrorTitlePresentInBuiltinLocales(t *testing.T) {
	const key = "tui.llmErrorTitle"

	for _, lang := range i18n.BuiltinLanguages() {
		t.Run(lang, func(t *testing.T) {
			path := filepath.Join("i18n", "locales", lang+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			var pack map[string]string
			if err := json.Unmarshal(data, &pack); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			got, ok := pack[key]
			if !ok {
				t.Fatalf("%s does not carry %q (no fallback would help here)", path, key)
			}
			if strings.TrimSpace(got) == "" {
				t.Fatalf("%s carries an empty %q", path, key)
			}
		})
	}

	previous := i18n.GetLanguage()
	defer i18n.SetLanguage(previous)
	for _, lang := range i18n.BuiltinLanguages() {
		i18n.SetLanguage(lang)
		if got := i18n.T(key); got == "" || got == key {
			t.Fatalf("%s: i18n.T(%q) = %q, want a translation", lang, key, got)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
