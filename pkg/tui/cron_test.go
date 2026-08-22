package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/cron"
)

func TestShortDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "30s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours", 2 * time.Hour, "2h"},
		{"days", 30 * time.Hour, "1d"},
		{"many days", 72 * time.Hour, "3d"},
		{"sub second", 500 * time.Millisecond, "0s"},
		{"zero", 0, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortDuration(tt.d); got != tt.want {
				t.Errorf("shortDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatCronSchedule(t *testing.T) {
	at := int64(1700000000000)
	every := int64(5 * 60 * 1000) // 5 min in ms
	tests := []struct {
		name string
		s    cron.CronSchedule
		want string
	}{
		{"at with ms", cron.CronSchedule{Kind: "at", AtMS: &at}, "at "},
		{"at without ms", cron.CronSchedule{Kind: "at"}, "at ?"},
		{"every with ms", cron.CronSchedule{Kind: "every", EveryMS: &every}, "every 5m"},
		{"every without ms", cron.CronSchedule{Kind: "every"}, "every ?"},
		{"cron expr", cron.CronSchedule{Kind: "cron", Expr: "0 9 * * *"}, "cron 0 9 * * *"},
		{"cron empty", cron.CronSchedule{Kind: "cron"}, "cron ?"},
		{"unknown kind", cron.CronSchedule{Kind: "custom"}, "custom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCronSchedule(tt.s)
			if tt.want == "at " {
				// check prefix + valid time format
				if !strings.HasPrefix(got, "at ") {
					t.Errorf("expected 'at ' prefix, got %q", got)
				}
				if _, err := time.Parse("2006-01-02 15:04", strings.TrimPrefix(got, "at ")); err != nil {
					t.Errorf("at schedule time not parseable: %q (%v)", got, err)
				}
				return
			}
			if got != tt.want {
				t.Errorf("formatCronSchedule(%+v) = %q, want %q", tt.s, got, tt.want)
			}
		})
	}
}

func TestFormatCronJobLine(t *testing.T) {
	tests := []struct {
		name  string
		job   cron.CronJob
		check string
	}{
		{"enabled with name", cron.CronJob{ID: "j1", Name: "backup", Enabled: true, Schedule: cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(3600000)}, Payload: cron.CronPayload{Message: "run"}}, "●"},
		{"disabled", cron.CronJob{ID: "j2", Enabled: false, Schedule: cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, Payload: cron.CronPayload{Message: "m"}}, "○"},
		{"name from message", cron.CronJob{ID: "j3", Enabled: true, Schedule: cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, Payload: cron.CronPayload{Message: "hello task"}}, "hello task"},
		{"name from command", cron.CronJob{ID: "j4", Enabled: true, Schedule: cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, Payload: cron.CronPayload{Command: "ls -la"}}, "ls -la"},
		{"long name truncated", cron.CronJob{ID: "j5", Enabled: true, Name: "this is an extremely long job name that should be truncated by the format function", Schedule: cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}}, "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatCronJobLine(tt.job)
			if !strings.Contains(got, tt.check) {
				t.Errorf("formatCronJobLine() = %q, want contains %q", got, tt.check)
			}
		})
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestLoadCronJobs(t *testing.T) {
	m := newTestModel(t)
	// With the test model, cronService is set up in NewModel. Use it.
	if m.cronService == nil {
		t.Skip("cron service not initialized")
	}
	cs := m.cronService
	job, err := cs.AddJob("job-a", cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg a", true, "", "")
	if err != nil {
		t.Fatalf("add job: %v", err)
	}
	m.loadCronJobs()
	if len(m.cronModalKeys) != 1 || m.cronModalKeys[0] != job.ID {
		t.Fatalf("expected job ID %q in cronModalKeys, got %v", job.ID, m.cronModalKeys)
	}
	if m.cronDetailMode {
		t.Error("cronDetailMode should be reset")
	}
}

func TestLoadCronJobsEmpty(t *testing.T) {
	m := newTestModel(t)
	if m.cronService == nil {
		t.Skip("cron service not initialized")
	}
	m.loadCronJobs()
	// modalItems should contain the no-jobs localized text
	if len(m.modalItems) == 0 {
		t.Fatal("expected modalItems to have an entry")
	}
	if m.modalItems[0] == "" {
		t.Error("expected non-empty no-jobs message")
	}
}

func TestLoadCronJobsNoService(t *testing.T) {
	m := &Model{}
	m.loadCronJobs()
	if len(m.modalItems) == 0 {
		t.Fatal("expected unavailable message when cron service is nil")
	}
}

func TestSelectedCronJobID(t *testing.T) {
	m := &Model{cronModalKeys: []string{"a", "b", "c"}, modalSelectedIdx: 1}
	if got := m.selectedCronJobID(); got != "b" {
		t.Errorf("selectedCronJobID = %q, want %q", got, "b")
	}
	// out of range
	m2 := &Model{cronModalKeys: []string{"a"}, modalSelectedIdx: 5}
	if got := m2.selectedCronJobID(); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
	// detail mode
	m3 := &Model{cronModalKeys: []string{"a"}, cronDetailMode: true, cronDetailJobID: "zz"}
	if got := m3.selectedCronJobID(); got != "zz" {
		t.Errorf("detail mode selected = %q, want zz", got)
	}
	// detail mode with empty key falls through to list
	m4 := &Model{cronModalKeys: []string{"a"}, cronDetailMode: true, cronDetailJobID: "", modalSelectedIdx: 0}
	if got := m4.selectedCronJobID(); got != "a" {
		t.Errorf("expected list selection 'a', got %q", got)
	}
}

func TestReselectCronJob(t *testing.T) {
	m := &Model{cronModalKeys: []string{"a", "b", "c"}, modalSelectedIdx: 0, cronDetailMode: true}
	m.reselectCronJob("b")
	if m.modalSelectedIdx != 1 || m.cronDetailJobID != "b" {
		t.Errorf("reselect b failed: idx=%d detail=%q", m.modalSelectedIdx, m.cronDetailJobID)
	}
	// Not in list, reset to last when past end
	m2 := &Model{cronModalKeys: []string{"a"}, modalSelectedIdx: 5}
	m2.reselectCronJob("nonexistent")
	if m2.modalSelectedIdx != 0 {
		t.Errorf("expected reset to index 0, got %d", m2.modalSelectedIdx)
	}
	// Empty keys → no change
	m3 := &Model{cronModalKeys: nil, modalSelectedIdx: 0}
	m3.reselectCronJob("x")
	if m3.modalSelectedIdx != 0 {
		t.Error("empty keys should leave selection unchanged")
	}
}

func TestRenderCronDetailJobMissing(t *testing.T) {
	m := newTestModel(t)
	if m.cronService == nil {
		t.Skip("cron service not initialized")
	}
	m.cronDetailMode = true
	m.cronDetailJobID = "missing-job"
	m.cronService.AddJob("j", cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "m", true, "", "")
	// renderCronDetail should fall back to list when job not found
	m.width = 100
	m.height = 24
	out := m.renderCronDetail()
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if m.cronDetailMode {
		t.Error("cronDetailMode should be false after fallback")
	}
}

func TestRenderCronDetailComplete(t *testing.T) {
	m := newTestModel(t)
	if m.cronService == nil {
		t.Skip("cron service not initialized")
	}
	next := int64(time.Now().UnixMilli() + 3600000)
	last := int64(time.Now().UnixMilli() - 60000)
	job, err := m.cronService.AddJobWithOptions("detail-job", cron.CronSchedule{Kind: "cron", Expr: "0 9 * * *", TZ: "UTC"}, "hello msg", true, "chan", "to", "session", "sess-key")
	if err != nil {
		t.Fatalf("add job: %v", err)
	}
	job.State.NextRunAtMS = &next
	job.State.LastRunAtMS = &last
	job.State.LastStatus = "ok"
	job.State.LastError = "some error"
	job.Payload.Command = "some command"
	job.Scope = "session"
	m.cronService.UpdateJob(job)

	m.width = 100
	m.height = 24
	m.cronDetailMode = true
	m.cronDetailJobID = job.ID
	out := m.renderCronDetail()
	if !strings.Contains(out, "detail-job") {
		t.Errorf("expected job name in detail output, got %q", out)
	}
}

func TestRenderCronDetailDisabled(t *testing.T) {
	m := newTestModel(t)
	if m.cronService == nil {
		t.Skip("cron service not initialized")
	}
	job, err := m.cronService.AddJobWithOptions("disabled-job", cron.CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "", false, "", "", "", "")
	if err != nil {
		t.Fatalf("add job: %v", err)
	}
	m.cronService.EnableJob(job.ID, false)
	m.width = 100
	m.height = 24
	m.cronDetailMode = true
	m.cronDetailJobID = job.ID
	out := m.renderCronDetail()
	if !strings.Contains(out, "disabled-job") {
		t.Errorf("expected disabled job name, got %q", out)
	}
}
