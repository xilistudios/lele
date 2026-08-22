package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/xilistudios/lele/pkg/cron"
)

// setupCronModalModel creates a model with a /cron modal listing one job.
func setupCronModalModel(t *testing.T) (*Model, string) {
	t.Helper()
	m := newTestModel(t)
	if m.cronService == nil {
		t.Skip("cron service not initialized")
	}
	job, err := m.cronService.AddJob("cron-modal-job", cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", true, "", "")
	if err != nil {
		t.Fatalf("add job: %v", err)
	}
	m.modalMode = ModalCron
	m.loadCronJobs()
	// Ensure the job is selected.
	m.reselectCronJob(job.ID)
	return m, job.ID
}

// TestUpdate_CronToggleEnable drives the "e" key in the cron modal.
func TestUpdate_CronToggleEnable(t *testing.T) {
	m, jobID := setupCronModalModel(t)

	job := m.cronService.GetJob(jobID)
	if job == nil || !job.Enabled {
		t.Fatalf("expected job enabled, got %+v", job)
	}

	// Press "e" to toggle disable.
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	mm := upd.(*Model)
	job = mm.cronService.GetJob(jobID)
	if job.Enabled {
		t.Error("expected job disabled after e toggle")
	}

	// Press "e" again to toggle enable.
	upd, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	mm = upd.(*Model)
	job = mm.cronService.GetJob(jobID)
	if !job.Enabled {
		t.Error("expected job re-enabled after second e toggle")
	}
}

// TestUpdate_CronRunNow drives the "r" key in the cron modal.
func TestUpdate_CronRunNow(t *testing.T) {
	m, jobID := setupCronModalModel(t)
	_ = jobID
	// "r" runs the cron job now — should not panic and the list stays loaded.
	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	_ = upd.(*Model)
	if len(m.cronModalKeys) == 0 {
		t.Error("cron list should remain populated after run-now")
	}
}

// TestUpdate_CronDelete drives the "d" key in the cron modal.
func TestUpdate_CronDelete(t *testing.T) {
	m, jobID := setupCronModalModel(t)
	_ = jobID
	_, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	job := m.cronService.GetJob(jobID)
	if job != nil {
		t.Error("expected job to be removed after d key")
	}
	if m.cronDetailMode {
		t.Error("cronDetailMode should be reset after delete")
	}
}

// TestUpdate_CronDetailModeEnable ensures toggling in detail mode works too.
func TestUpdate_CronDetailModeEnable(t *testing.T) {
	m, jobID := setupCronModalModel(t)
	m.cronDetailMode = true
	m.cronDetailJobID = jobID

	upd, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	mm := upd.(*Model)
	job := mm.cronService.GetJob(jobID)
	if job.Enabled {
		t.Error("expected job disabled by e toggle in detail mode")
	}
	// After reload the job should be re-selected in the list.
	if mm.selectedCronJobID() == "" {
		t.Error("expected a cron job still selected after toggle in detail mode")
	}
}
