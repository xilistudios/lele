package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/xilistudios/lele/pkg/tui/i18n"
)

func TestFrameInvariant_ExactWidthAndHeight(t *testing.T) {
	i18n.InitWithLanguage("en")
	m := newTestModel(t)

	key := "tui:chat:frame-invariant"
	m.sessionMgr.GetOrCreate(key)
	_ = m.sessionMgr.SetMode(key, "agent")
	for i := 0; i < 12; i++ {
		m.sessionMgr.AddMessage(key, "user", fmt.Sprintf("question %d", i))
		m.sessionMgr.AddMessage(key, "assistant",
			fmt.Sprintf("answer %d with a few words to force wrapping at narrow widths", i))
	}
	m.currentKey = key
	m.showWelcome = false
	m.forceGotoBottom = true
	m.updateViewport()

	type state struct {
		name  string
		apply func()
	}

	states := []state{
		{"idle", func() {
			m.processing = false
			m.startTime = time.Time{}
			m.lastDuration = 0
		}},
		{"processing", func() {
			m.processing = true
			m.startTime = time.Now()
			m.streamTokPerSec = 42.5
		}},
		{"done", func() {
			m.processing = false
			m.startTime = time.Time{}
			m.lastDuration = 78 * time.Second
			m.lastTokPerSec = 47.7
		}},
		{"queue", func() {
			m.processing = true
			m.startTime = time.Now()
			m.enqueueMessage("a queued message long enough to be truncated at narrow widths")
		}},
		{"autocomplete", func() {
			m.showAutocomplete = true
			m.autocompleteItems = []commandInfo{
				{name: "/help", description: "show help"},
				{name: "/theme", description: "pick a theme"},
				{name: "/model", description: "switch model"},
			}
		}},
		{"selecting", func() { m.selecting = true }},
		{"copy-feedback", func() { m.selectionFeedback = true }},
		{"long-input", func() {
			m.chatInput.SetValue(strings.Repeat("palabra ", 40))
		}},
		{"unbreakable-input", func() {
			m.chatInput.SetValue("https://example.com/" + strings.Repeat("a", 240))
		}},
		{"multiline-input", func() {
			m.chatInput.SetValue("line one\nline two\nline three\nline four\nline five")
		}},
		{"modal", func() { m.modalMode = ModalSessions }},
		{"welcome", func() { m.showWelcome = true }},
		{"onboarding", func() { m.showWelcome = true; m.onboardingActive = true }},
	}

	sizes := []struct{ w, h int }{
		{40, 10}, {60, 16}, {80, 20}, {80, 24}, {100, 19}, {122, 38}, {160, 45}, {200, 50},
	}

	for _, st := range states {
		for _, size := range sizes {
			t.Run(fmt.Sprintf("%s/%dx%d", st.name, size.w, size.h), func(t *testing.T) {
				m.showWelcome = false
				m.onboardingActive = false
				m.modalMode = ModalNone
				m.selecting = false
				m.selectionFeedback = false
				m.showAutocomplete = false
				m.autocompleteItems = nil
				m.messageQueue = map[string][]queuedMessage{}
				m.chatInput.SetValue("")
				m.processing = false
				m.startTime = time.Time{}
				m.lastDuration = 0
				m.streamTokPerSec = 0
				m.lastTokPerSec = 0

				updated, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
				m = updated.(*Model)
				m.showWelcome = false
				st.apply()
				m.width, m.height = size.w, size.h

				frame := m.View()
				lines := strings.Split(frame, "\n")

				if len(lines) != size.h {
					t.Fatalf("View() returned %d lines, want exactly %d", len(lines), size.h)
				}
				for i, line := range lines {
					if w := ansi.StringWidth(line); w > size.w {
						t.Fatalf("line %d is %d cells wide, want <= %d: %q",
							i, w, size.w, ansi.Strip(line))
					}
				}
			})
		}
	}
}
