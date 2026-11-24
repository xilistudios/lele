package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
)

// The /think modal must present every supported level, in cycle order.
func TestThinkModalPresentsAllLevels(t *testing.T) {
	m := newTestModel(t)
	m.executeCommand("/think")
	if m.modalMode != ModalThink {
		t.Fatalf("modalMode = %v, want ModalThink", m.modalMode)
	}
	want := []string{"off", "low", "medium", "high", "xhigh", "max"}
	if len(m.modalItems) != len(want) {
		t.Fatalf("think modal items = %v, want %v", m.modalItems, want)
	}
	for i, w := range want {
		if m.modalItems[i] != w {
			t.Errorf("item %d = %q, want %q", i, m.modalItems[i], w)
		}
	}
}

// Selecting a level on the welcome screen queues it for the first session.
func TestThinkModalSelectionQueuesPendingLevel(t *testing.T) {
	for _, level := range []string{"xhigh", "max"} {
		t.Run(level, func(t *testing.T) {
			m := newTestModel(t)
			m.showWelcome = true
			m.executeCommand("/think")
			idx := -1
			for i, item := range m.modalItems {
				if item == level {
					idx = i
				}
			}
			if idx < 0 {
				t.Fatalf("level %q not offered in think modal: %v", level, m.modalItems)
			}
			m.modalSelectedIdx = idx
			m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})
			if m.pendingThink != level {
				t.Errorf("pendingThink = %q, want %q", m.pendingThink, level)
			}
		})
	}
}

// The settings thinking selector offers inherit plus every supported level.
func TestThinkingLevelSelectorOptionsIncludeNewLevels(t *testing.T) {
	labels, values := thinkingLevelSelectorOptions()
	wantValues := append([]string{""}, config.ThinkingLevels...)
	wantLabels := append([]string{"(default)"}, config.ThinkingLevels...)
	if len(values) != len(wantValues) || len(labels) != len(wantLabels) {
		t.Fatalf("selector options = %v / %v, want %v / %v", labels, values, wantLabels, wantValues)
	}
	for i := range wantValues {
		if values[i] != wantValues[i] || labels[i] != wantLabels[i] {
			t.Errorf("option %d = %q/%q, want %q/%q", i, labels[i], values[i], wantLabels[i], wantValues[i])
		}
	}
}
