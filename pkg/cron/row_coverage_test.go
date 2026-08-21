package cron

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

func TestRowToCronJob_ScheduleUnmarshalError(t *testing.T) {
	row := &store.CronJobRow{
		ID:       "j1",
		Name:     "bad",
		Enabled:  true,
		Schedule: "{not-json",
		Payload:  "{}",
		State:    "{}",
	}
	_, err := rowToCronJob(row)
	if err == nil {
		t.Fatal("expected unmarshal error for invalid schedule JSON")
	}
}

func TestRowToCronJob_PayloadUnmarshalError(t *testing.T) {
	row := &store.CronJobRow{
		ID:       "j1",
		Name:     "bad",
		Schedule: `{"kind":"every","everyMs":1000}`,
		Payload:  "{not-json",
		State:    "{}",
	}
	_, err := rowToCronJob(row)
	if err == nil {
		t.Fatal("expected unmarshal error for invalid payload JSON")
	}
}

func TestRowToCronJob_StateUnmarshalError(t *testing.T) {
	row := &store.CronJobRow{
		ID:       "j1",
		Name:     "bad",
		Schedule: `{"kind":"every","everyMs":1000}`,
		Payload:  `{"kind":"agent_turn"}`,
		State:    "{not-json",
	}
	_, err := rowToCronJob(row)
	if err == nil {
		t.Fatal("expected unmarshal error for invalid state JSON")
	}
}

func TestRowToCronJob_RoundTripMeta(t *testing.T) {
	// Schedule uses 'at' with a time-based AtMS field; DeleteAfterRun is tested
	// through the payload meta round trip below.
	row := &store.CronJobRow{
		ID:       "j1",
		Name:     "rt",
		Enabled:  true,
		Scope:    "session",
		Schedule: `{"kind":"at","atMs":1000}`,
		Payload:  `{"kind":"agent_turn","message":"hi","delete_after_run":true}`,
		State:    `{"lastStatus":"ok"}`,
	}
	job, err := rowToCronJob(row)
	if err != nil {
		t.Fatalf("rowToCronJob() error: %v", err)
	}
	if job.ID != "j1" || job.Name != "rt" {
		t.Errorf("job identity mismatch: %+v", job)
	}
	if job.Schedule.Kind != "at" || job.Schedule.AtMS == nil || *job.Schedule.AtMS != 1000 {
		t.Errorf("schedule = %+v", job.Schedule)
	}
	if !job.DeleteAfterRun {
		t.Error("DeleteAfterRun should be true")
	}
	if job.Payload.Message != "hi" {
		t.Errorf("payload message = %q, want hi", job.Payload.Message)
	}
	if job.State.LastStatus != "ok" {
		t.Errorf("state lastStatus = %q, want ok", job.State.LastStatus)
	}
}

func TestCronJobToRow_RoundTrip(t *testing.T) {
	at := int64Ptr(5000)
	job := &CronJob{
		ID:      "job-1",
		Name:    "sample",
		Enabled: true,
		Scope:   "global",
		Schedule: CronSchedule{
			Kind: "at",
			AtMS: at,
		},
		Payload: CronPayload{
			Kind:    "agent_turn",
			Message: "hello",
			Deliver: true,
			Channel: "telegram",
			To:      "user-1",
		},
		State:          CronJobState{LastStatus: "ok"},
		CreatedAtMS:    100,
		UpdatedAtMS:    200,
		DeleteAfterRun: true,
	}
	row, err := cronJobToRow(job)
	if err != nil {
		t.Fatalf("cronJobToRow() error: %v", err)
	}
	if row.ID != "job-1" || !row.Enabled {
		t.Errorf("row identity: %+v", row)
	}
	if row.SessionKey != "" {
		t.Errorf("SessionKey = %q, want empty", row.SessionKey)
	}
	// Round-trip back
	back, err := rowToCronJob(row)
	if err != nil {
		t.Fatalf("rowToCronJob() error: %v", err)
	}
	if back.Payload.Message != "hello" || back.Payload.Channel != "telegram" {
		t.Errorf("round-trip payload: %+v", back.Payload)
	}
	if back.State.LastStatus != "ok" {
		t.Errorf("round-trip state: %+v", back.State)
	}
}

func TestSetStore_ReloadsFromRepoWithBadRow(t *testing.T) {
	// Seed a repo with a corrupt schedule row so SetStore's loadStore logs the
	// error (loadFromRepo error path). We assert the service still holds an empty
	// store (no panic).
	s := openCronTestStore(t)
	repo := s.Cron()

	// Insert a bad row directly through the repo using valid upsert of a raw bad row.
	if err := repo.UpsertCronJob(&store.CronJobRow{
		ID:       "bad-1",
		Name:     "bad",
		Enabled:  true,
		Schedule: "not-json",
		Payload:  "{}",
		State:    "{}",
	}); err != nil {
		t.Fatalf("UpsertCronJob: %v", err)
	}

	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	cs.SetStore(repo)
	if len(cs.ListJobs(true)) != 0 {
		t.Errorf("expected empty store after load error, got %d jobs", len(cs.ListJobs(true)))
	}
}

func TestSaveToRepo_StaleRowDeleted(t *testing.T) {
	s := openCronTestStore(t)
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	cs.SetStore(s.Cron())

	_, err := cs.AddJobWithOptions(
		"job-a",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)},
		"hi",
		false,
		"telegram",
		"u",
		"global",
		"",
	)
	if err != nil {
		t.Fatalf("AddJobWithOptions: %v", err)
	}

	// Simulate a stale row in the repo that isn't in the store.
	if err := s.Cron().UpsertCronJob(&store.CronJobRow{
		ID:       "stale-row",
		Name:     "stale",
		Enabled:  true,
		Schedule: `{"kind":"every","everyMs":1000}`,
		Payload:  `{"kind":"agent_turn"}`,
		State:    `{}`,
	}); err != nil {
		t.Fatalf("seed stale row: %v", err)
	}

	// Trigger saveStoreUnsafe via SetStore on a second service sharing the repo.
	cs2 := NewCronService(filepath.Join(t.TempDir(), "other.json"), nil)
	cs2.SetStore(s.Cron())
	// Manually set and save to trigger the delete sweep.
	cs.mu.Lock()
	err = cs.saveToRepo()
	cs.mu.Unlock()
	if err != nil {
		t.Fatalf("saveToRepo: %v", err)
	}

	rows, err := s.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs: %v", err)
	}
	for _, r := range rows {
		if r.ID == "stale-row" {
			t.Errorf("stale row was not deleted")
		}
	}
}

func TestListJobs_FiltersDisabled(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	atReq := CronSchedule{Kind: "at", AtMS: int64Ptr(time.Now().Add(time.Hour).UnixMilli())}
	job, err := cs.AddJobWithOptions("j1", atReq, "m", false, "t", "u", "", "")
	if err != nil {
		t.Fatalf("AddJobWithOptions: %v", err)
	}

	// Disable an existing job directly in the store.
	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			cs.store.Jobs[i].Enabled = false
		}
	}
	cs.mu.Unlock()

	if got := cs.ListJobs(false); len(got) != 0 {
		t.Errorf("ListJobs(false) = %d, want 0 (disabled filtered)", len(got))
	}
	if got := cs.ListJobs(true); len(got) != 1 {
		t.Errorf("ListJobs(true) = %d, want 1 (disabled included)", len(got))
	}
}

func TestGenerateID(t *testing.T) {
	if id := generateID(); id == "" {
		t.Fatal("generateID() returned empty")
	}
}

func TestCronPayloadWithMeta_RoundTrip(t *testing.T) {
	meta := cronPayloadWithMeta{
		CronPayload: CronPayload{Kind: "agent_turn", Message: "x"},
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back cronPayloadWithMeta
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Message != "x" {
		t.Errorf("message = %q, want x", back.Message)
	}
}