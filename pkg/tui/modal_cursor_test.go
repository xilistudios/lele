package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
)

func TestClampModalCursor(t *testing.T) {
	tests := []struct {
		name        string
		items       []string
		idx, offset int
		wantIdx     int
		wantOffset  int
	}{
		{
			name:  "idx past end clamps to last item",
			items: []string{"a", "b"},
			idx:   2, offset: 0,
			wantIdx: 1, wantOffset: 0,
		},
		{
			name:  "empty list resets idx to 0",
			items: nil,
			idx:   5, offset: 3,
			wantIdx: 0, wantOffset: 0,
		},
		{
			name:  "negative idx resets to 0",
			items: []string{"a", "b", "c"},
			idx:   -2, offset: 0,
			wantIdx: 0, wantOffset: 0,
		},
		{
			name:  "scroll offset follows clamped idx",
			items: []string{"a", "b"},
			idx:   1, offset: 7,
			wantIdx: 1, wantOffset: 1,
		},
		{
			name:  "negative scroll offset clamped to 0",
			items: []string{"a", "b", "c"},
			idx:   2, offset: -4,
			wantIdx: 2, wantOffset: 0,
		},
		{
			name:  "in-range cursor untouched",
			items: []string{"a", "b", "c"},
			idx:   1, offset: 1,
			wantIdx: 1, wantOffset: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &Model{}
			m.modalItems = tc.items
			m.modalSelectedIdx = tc.idx
			m.modalScrollOffset = tc.offset
			m.clampModalCursor()
			if m.modalSelectedIdx != tc.wantIdx {
				t.Errorf("modalSelectedIdx = %d, want %d", m.modalSelectedIdx, tc.wantIdx)
			}
			if m.modalScrollOffset != tc.wantOffset {
				t.Errorf("modalScrollOffset = %d, want %d", m.modalScrollOffset, tc.wantOffset)
			}
		})
	}
}

func TestCronDeleteLastJobThenEnterNoPanic(t *testing.T) {
	m := newTestModelWithDenyPatterns(t)
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = up.(*Model)
	if m.cronService == nil {
		t.Skip("no cron service")
	}

	every := int64(3600_000)
	sched := cron.CronSchedule{Kind: "every", EveryMS: &every}
	for _, n := range []string{"job-a", "job-b"} {
		if _, err := m.cronService.AddJob(n, sched, "hello", false, "native", "x"); err != nil {
			t.Fatalf("add %s: %v", n, err)
		}
	}

	m.modalMode = ModalCron
	m.loadCronJobs()
	m.modalSelectedIdx = 0
	if len(m.modalItems) != 2 || len(m.cronModalKeys) != 2 {
		t.Fatalf("setup: items=%d keys=%d, want 2/2", len(m.modalItems), len(m.cronModalKeys))
	}

	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = up.(*Model)
	if m.modalSelectedIdx != len(m.modalItems)-1 {
		t.Fatalf("cursor not on last row after Down: idx=%d items=%d", m.modalSelectedIdx, len(m.modalItems))
	}

	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = up.(*Model)

	if len(m.modalItems) != 1 {
		t.Fatalf("list length = %d after delete, want 1", len(m.modalItems))
	}
	if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.modalItems) {
		t.Fatalf("cursor out of range after delete: idx=%d items=%d", m.modalSelectedIdx, len(m.modalItems))
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC on Enter after deleting last cron job: %v", r)
			}
		}()
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}()
}

func TestCronDeleteOnlyJobEmptyListNoPanic(t *testing.T) {
	m := newTestModelWithDenyPatterns(t)
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = up.(*Model)
	if m.cronService == nil {
		t.Skip("no cron service")
	}

	every := int64(3600_000)
	sched := cron.CronSchedule{Kind: "every", EveryMS: &every}
	if _, err := m.cronService.AddJob("only-job", sched, "hello", false, "native", "x"); err != nil {
		t.Fatalf("add job: %v", err)
	}

	m.modalMode = ModalCron
	m.loadCronJobs()
	if len(m.modalItems) != 1 {
		t.Fatalf("setup: items=%d, want 1", len(m.modalItems))
	}

	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = up.(*Model)
	if len(m.cronModalKeys) != 0 {
		t.Fatalf("cronModalKeys = %d after delete, want 0", len(m.cronModalKeys))
	}
	if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.modalItems) {
		t.Fatalf("cursor out of range after delete: idx=%d items=%d", m.modalSelectedIdx, len(m.modalItems))
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC on Enter after emptying cron list: %v", r)
			}
		}()
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}()
}

func TestSecretsDeleteLastSecretThenEnterNoPanic(t *testing.T) {
	cfg := testModelConfig(t)
	cfg.Keyring = config.KeyringConfig{Enabled: true, Backend: "file", AuditLogSize: 50}
	m := newTestModelWithConfig(t, cfg, true)
	up, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 36})
	m = up.(*Model)

	svc := m.keyringSvc()
	if svc == nil {
		t.Skip("keyring service not wired in test model")
	}
	for _, n := range []string{"sec-a", "sec-b"} {
		if err := svc.SetFromUI(n, "v", "", nil, nil, "tui"); err != nil {
			t.Skipf("keyring unavailable in this environment: %v", err)
		}
	}

	m.modalMode = ModalSecrets
	m.loadSecrets()
	m.modalSelectedIdx = 0
	if len(m.modalItems) < 2 || len(m.secretsModalKeys) < 2 {
		t.Fatalf("setup: items=%d keys=%d, want >=2", len(m.modalItems), len(m.secretsModalKeys))
	}
	last := len(m.modalItems) - 1

	for i := 0; i < last; i++ {
		up, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = up.(*Model)
	}
	if m.modalSelectedIdx != last {
		t.Fatalf("cursor not on last row: idx=%d want %d", m.modalSelectedIdx, last)
	}
	target := m.secretsModalKeys[m.modalSelectedIdx]

	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = up.(*Model)

	if len(m.modalItems) != last {
		t.Fatalf("list length = %d after delete, want %d", len(m.modalItems), last)
	}
	if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.modalItems) {
		t.Fatalf("cursor out of range after delete: idx=%d items=%d", m.modalSelectedIdx, len(m.modalItems))
	}
	for _, k := range m.secretsModalKeys {
		if k == target {
			t.Fatalf("secret %q still present after delete", target)
		}
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC on Enter after deleting last secret: %v", r)
			}
		}()
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}()
}
