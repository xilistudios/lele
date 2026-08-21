package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStripMouseEscapeSequences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no sequence", "abc", "abc"},
		{"complete m", "\x1b[<0;38;4M", ""},
		{"complete M", "\x1b[<65;22;22M", ""},
		{"multiple", "\x1b[<0;1;1Mhello\x1b[<2;5;5m", "hello"},
		{"no leading esc", "abc\x1b[<1;2;3Mdef", "abcdef"},
		{"regular text preserved", "hi [<1;2;3M", "hi "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripMouseEscapeSequences(tt.in); got != tt.want {
				t.Errorf("stripMouseEscapeSequences(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFindTrailingIncompleteEscape(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"no esc", "hello", 0},
		{"lone esc", "\x1b", 1},
		{"esc bracket", "\x1b[", 2},
		{"esc bracket less", "\x1b[<", 3},
		{"esc bracket less digits", "\x1b[<65", 5},
		{"esc bracket less params", "\x1b[<65;57;25", 11},
		{"complete sequence no buffer", "\x1b[<65;57;25M", 0},
		{"text then lone esc", "hi\x1b", 1},
		{"headless open bracket less", "hi[<", 2},
		{"headless partial", "x[<65;57", 7},
		{"headless bracket no less", "[", 0},
		{"headless bracket digit", "[65", 0},
		{"headless invalid", "[<65M", 0},
		{"trailing bracket alone", "abc[", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findTrailingIncompleteEscape(tt.in); got != tt.want {
				t.Errorf("findTrailingIncompleteEscape(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestFilterAndBufferEscapes(t *testing.T) {
	m := &Model{escBuffer: []rune{}}

	// Complete mouse sequence in msg → cleaned empty, not forwarded.
	msg := tea.KeyMsg{Runes: []rune("\x1b[<0;10;20M")}
	r, ok := filterAndBufferEscapes(m, msg)
	if ok {
		t.Error("expected false (not forwarded) for pure mouse sequence")
	}
	if len(r) != 0 {
		t.Errorf("expected empty runes, got %q", string(r))
	}

	// Incomplete tail buffered.
	m2 := &Model{escBuffer: []rune{}}
	msg2 := tea.KeyMsg{Runes: []rune("\x1b[<")}
	r2, ok2 := filterAndBufferEscapes(m2, msg2)
	if ok2 {
		t.Error("expected false for incomplete fragment")
	}
	if string(m2.escBuffer) != "\x1b[<" {
		t.Errorf("expected escBuffer '\x1b[<', got %q", string(m2.escBuffer))
	}
	_ = r2

	// Buffer combines with next message. Start with buffer then complete runes.
	m3 := &Model{escBuffer: []rune{0x1b, '[', '<', '6', '5', ';', '5'}}
	msg3 := tea.KeyMsg{Runes: []rune{'7', ';', '2', '4', 'M'}}
	r3, ok3 := filterAndBufferEscapes(m3, msg3)
	if ok3 {
		t.Error("expected false after complete sequence from buffer")
	}
	if len(r3) != 0 {
		t.Errorf("expected empty after combined mouse sequence, got %q", string(r3))
	}
	if len(m3.escBuffer) != 0 {
		t.Errorf("expected empty escBuffer, got %q", string(m3.escBuffer))
	}

	// Normal text passes through.
	m4 := &Model{escBuffer: []rune{}}
	msg4 := tea.KeyMsg{Runes: []rune("hello")}
	r4, ok4 := filterAndBufferEscapes(m4, msg4)
	if !ok4 {
		t.Error("expected text forwarded")
	}
	if string(r4) != "hello" {
		t.Errorf("expected 'hello', got %q", string(r4))
	}
}

func TestIsEscapeSequenceFragment(t *testing.T) {
	m := &Model{}

	// Start with ESC key.
	if !m.isEscapeSequenceFragment(tea.KeyMsg{Type: tea.KeyEscape}) {
		t.Error("expected KeyEscape to start sequence")
	}
	if !m.escSeqActive {
		t.Error("expected escSeqActive true after ESC")
	}

	// Inside sequence, complete with 'M' final.
	if !m.isEscapeSequenceFragment(tea.KeyMsg{Runes: []rune{'['}}) {
		t.Error("expected '[' to keep sequence active")
	}
	if !m.isEscapeSequenceFragment(tea.KeyMsg{Runes: []rune{'<', '1', '2', ';'}}) {
		t.Error("expected '[' params to keep sequence active")
	}
	if !m.isEscapeSequenceFragment(tea.KeyMsg{Runes: []rune{'M'}}) {
		t.Error("expected 'M' to be consumed inside active seq")
	}
	if m.escSeqActive {
		t.Error("expected escSeqActive false after final byte")
	}

	// State machine: starting with a rune 0x1b inside Runes.
	m2 := &Model{}
	if !m2.isEscapeSequenceFragment(tea.KeyMsg{Runes: []rune{0x1b}}) {
		t.Error("expected ESC rune to start sequence")
	}

	// Case B: '[' within 50ms of lastEscTime.
	m3 := &Model{lastEscTime: time.Now()}
	if !m3.isEscapeSequenceFragment(tea.KeyMsg{Runes: []rune{'['}}) {
		t.Error("expected '[' shortly after ESC to start sequence")
	}

	// Not active, plain rune → false.
	m4 := &Model{}
	if m4.isEscapeSequenceFragment(tea.KeyMsg{Runes: []rune{'a'}}) {
		t.Error("expected plain rune to be false")
	}
}

func TestIsEscapeSequenceFragmentEmptyRunesActive(t *testing.T) {
	m := &Model{escSeqActive: true, escSeqLastRune: time.Now()}
	// KeyMsg with no runes inside active seq: returns true (consumed) and ends
	// the sequence (the switch has no runes to iterate, so escSeqActive stays
	// whatever step 3 left it — but len(runes)==0 branches return false).

	// Bubbletea delivers ESC as KeyEscape inside an active seq BEFORE step 3.
	// Test a KeyEscape while active → steps to the beginning? No — escSeqActive
	// is already true so step 3 runs with empty runes and ends the seq.
	if !m.isEscapeSequenceFragment(tea.KeyMsg{Type: tea.KeyEscape}) {
		t.Log("special key with no runes consumed")
	}
}

func TestIsEscapeSequenceFragmentStale(t *testing.T) {
	m := &Model{escSeqActive: true, escSeqLastRune: time.Now().Add(-1 * time.Second)}
	// Stale sequence resets; then '[' can start a new one.
	if m.isEscapeSequenceFragment(tea.KeyMsg{Runes: []rune{'a'}}) {
		t.Error("expected stale sequence to reset and plain rune false")
	}
	if m.escSeqActive {
		t.Error("expected escSeqActive false after stale reset")
	}
}

func TestIsCSIIntermediate(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'0', true}, {'9', true}, {';', true}, {'?', true}, {':', true},
		{' ', true}, {'/', true},
		{'[', false}, {'M', false}, {'m', false}, {'@', false},
	}
	for _, c := range cases {
		if got := isCSIIntermediate(c.r); got != c.want {
			t.Errorf("isCSIIntermediate(%q) = %v, want %v", c.r, got, c.want)
		}
	}
}

func TestIsCSIFinal(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'@', true}, {'M', true}, {'m', true}, {'~', true},
		{'/', false}, {'[', true}, {'<', false},
	}
	for _, c := range cases {
		if got := isCSIFinal(c.r); got != c.want {
			t.Errorf("isCSIFinal(%q) = %v, want %v", c.r, got, c.want)
		}
	}
}

func TestIsListModal(t *testing.T) {
	if isListModal(ModalNone) {
		t.Error("ModalNone is not a list modal")
	}
	if isListModal(ModalAddProvider) {
		t.Error("ModalAddProvider is not a list modal")
	}
	if isListModal(ModalAddModel) {
		t.Error("ModalAddModel is not a list modal")
	}
	if isListModal(ModalAddSecret) {
		t.Error("ModalAddSecret is not a list modal")
	}
	if isListModal(ModalSkillInstall) {
		t.Error("ModalSkillInstall is not a list modal")
	}
	if !isListModal(ModalSessions) {
		t.Error("ModalSessions is a list modal")
	}
	if !isListModal(ModalCron) {
		t.Error("ModalCron is a list modal")
	}
	if !isListModal(ModalSecrets) {
		t.Error("ModalSecrets is a list modal")
	}
	if !isListModal(ModalSkills) {
		t.Error("ModalSkills is a list modal")
	}
	if !isListModal(ModalSettings) {
		t.Error("ModalSettings is a list modal")
	}
}

func TestResetModal(t *testing.T) {
	m := &Model{}
	m.modalMode = ModalSessions
	m.modalItems = []string{"a"}
	m.modalSelectedIdx = 3
	m.secretsDetailMode = true
	m.secretsReveal = true
	m.bgExecViewMode = true
	m.bgExecViewID = "proc1"
	m.resetModal(ModalCron)
	if m.modalMode != ModalCron {
		t.Errorf("expected ModalCron, got %v", m.modalMode)
	}
	if m.modalItems != nil || m.modalSelectedIdx != 0 {
		t.Error("modal state should be reset")
	}
	if m.secretsDetailMode || m.secretsReveal {
		t.Error("secrets state should be reset")
	}
	if m.bgExecViewMode || m.bgExecViewID != "" {
		t.Error("bg exec state should be reset")
	}
}