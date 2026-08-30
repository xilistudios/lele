package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/cron"
)

// ── clampModalCursor unit tests ─────────────────────────────────────────

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

// ── Regression: audit C2 — delete last cron job then Enter must not panic ──

// TestCronDeleteLastJobThenEnterNoPanic ports the audit repro
// (zz_audit_panic_test.go): with the real cron service and real keypresses,
// pressing "d" on the LAST row of the cron modal reloads the list (N-1 items)
// but previously left modalSelectedIdx at N-1, so the next Enter panicked at
// handlers.go ("index out of range [N-1] with length N-1"). The fix clamps
// the cursor inside loadCronJobs (plus a defensive clamp on Enter).
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

	// Down -> move onto the LAST job (real key path).
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = up.(*Model)
	if m.modalSelectedIdx != len(m.modalItems)-1 {
		t.Fatalf("cursor not on last row after Down: idx=%d items=%d", m.modalSelectedIdx, len(m.modalItems))
	}

	// "d" -> delete that job.
	up, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = up.(*Model)

	// Post-conditions of the fix: list shrank and the cursor is in range.
	if len(m.modalItems) != 1 {
		t.Fatalf("list length = %d after delete, want 1", len(m.modalItems))
	}
	if m.modalSelectedIdx < 0 || m.modalSelectedIdx >= len(m.modalItems) {
		t.Fatalf("cursor out of range after delete: idx=%d items=%d", m.modalSelectedIdx, len(m.modalItems))
	}

	// Enter on the remaining row must not panic (pre-fix: index out of range).
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PANIC on Enter after deleting last cron job: %v", r)
			}
		}()
		m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	}()
}

// TestCronDeleteOnlyJobEmptyListNoPanic covers the shrink-to-empty edge: the
// list reload falls back to the "no jobs" placeholder row and Enter must be a
// no-op (no panic).
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

	// "d" deletes the only job.
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

// ── Regression: same flow in the secrets modal (real file-backed keyring) ──

// TestSecretsDeleteLastSecretThenEnterNoPanic mirrors the cron repro for
// ModalSecrets: delete on the last row reloads via loadSecrets(), which now
// clamps the cursor; Enter must not panic.
//
// Harness note: m.keyringSvc() is taken from the agent loop, which builds the
// keyring service from cfg at NewAgentLoop time, so the test enables the
// keyring in the config up front and pins the "file" backend (same approach
// as pkg/keyring's own tests) so no OS keychain is touched.
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

	// Down until the cursor reaches the LAST row (real key path).
	for i := 0; i < last; i++ {
		up, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = up.(*Model)
	}
	if m.modalSelectedIdx != last {
		t.Fatalf("cursor not on last row: idx=%d want %d", m.modalSelectedIdx, last)
	}
	target := m.secretsModalKeys[m.modalSelectedIdx]

	// "d" -> delete the last secret.
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
