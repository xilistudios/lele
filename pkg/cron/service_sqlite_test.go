package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/store"
)

func openCronTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open() failed: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("store.Close() failed: %v", err)
		}
	})
	return s
}

func TestSetStore_PersistAndReload(t *testing.T) {
	jsonPath := filepath.Join(t.TempDir(), "jobs.json")
	cs := NewCronService(jsonPath, nil)

	job, err := cs.AddJobWithOptions(
		"sqlite-job",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)},
		"hello sqlite",
		true,
		"telegram",
		"user1",
		"session",
		"sess-123",
	)
	if err != nil {
		t.Fatalf("AddJobWithOptions failed: %v", err)
	}

	s := openCronTestStore(t)
	cs.SetStore(s.Cron())

	rows, err := s.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].ID != job.ID {
		t.Errorf("row ID = %q, want %q", rows[0].ID, job.ID)
	}
	if rows[0].Name != "sqlite-job" {
		t.Errorf("row Name = %q, want %q", rows[0].Name, "sqlite-job")
	}
	if rows[0].SessionKey != "sess-123" {
		t.Errorf("row SessionKey = %q, want %q", rows[0].SessionKey, "sess-123")
	}
	if !rows[0].Enabled {
		t.Error("row should be enabled")
	}

	// Verify round-trip of payload (DeleteAfterRun is false for "every" schedule).
	var meta cronPayloadWithMeta
	if err := json.Unmarshal([]byte(rows[0].Payload), &meta); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if meta.Message != "hello sqlite" {
		t.Errorf("payload Message = %q, want %q", meta.Message, "hello sqlite")
	}
	if meta.DeleteAfterRun {
		t.Error("DeleteAfterRun should be false for 'every' schedule")
	}

	// Reload into a fresh service to verify persistence survives reload.
	cs2 := NewCronService(filepath.Join(t.TempDir(), "other.json"), nil)
	cs2.SetStore(s.Cron())
	jobs := cs2.ListJobs(true)
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job after reload, got %d", len(jobs))
	}
	if jobs[0].ID != job.ID {
		t.Errorf("reloaded job ID = %q, want %q", jobs[0].ID, job.ID)
	}
}

func TestSetStore_LazyMigration(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "jobs.json")

	legacyStore := &CronStore{
		Version: 1,
		Jobs: []CronJob{
			{
				ID:      "legacy-1",
				Name:    "legacy-job",
				Enabled: true,
				Schedule: CronSchedule{
					Kind:    "every",
					EveryMS: int64Ptr(30000),
				},
				Payload: CronPayload{
					Kind:    "agent_turn",
					Message: "legacy",
					Deliver: false,
				},
				DeleteAfterRun: false,
				CreatedAtMS:    1700000000000,
				UpdatedAtMS:    1700000000000,
			},
		},
	}
	data, err := json.MarshalIndent(legacyStore, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy store: %v", err)
	}
	if err := os.WriteFile(jsonPath, data, 0600); err != nil {
		t.Fatalf("write legacy JSON: %v", err)
	}

	// Sanity: legacy JSON is loadable.
	cs := NewCronService(jsonPath, nil)
	if len(cs.ListJobs(true)) != 1 {
		t.Fatalf("expected 1 job from legacy JSON, got %d", len(cs.ListJobs(true)))
	}

	// Switch to SQLite – should migrate lazily.
	s := openCronTestStore(t)
	cs.SetStore(s.Cron())

	rows, err := s.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after migration, got %d", len(rows))
	}
	if rows[0].ID != "legacy-1" {
		t.Errorf("migrated row ID = %q, want %q", rows[0].ID, "legacy-1")
	}

	// In-memory store should also have the job.
	if len(cs.ListJobs(true)) != 1 {
		t.Errorf("expected 1 job in memory, got %d", len(cs.ListJobs(true)))
	}

	// Legacy JSON file should still exist on disk.
	if _, statErr := os.Stat(jsonPath); statErr != nil {
		t.Errorf("legacy JSON should still exist: %v", statErr)
	}

	// Second service switching to same repo should NOT duplicate.
	cs2 := NewCronService(jsonPath, nil)
	cs2.SetStore(s.Cron())
	rows2, err := s.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs (second service) failed: %v", err)
	}
	if len(rows2) != 1 {
		t.Errorf("expected 1 row (no duplicate), got %d", len(rows2))
	}
}

func TestSetStore_NilFallback(t *testing.T) {
	jsonPath := filepath.Join(t.TempDir(), "jobs.json")
	cs := NewCronService(jsonPath, nil)

	// Add a job while using JSON backend.
	if _, err := cs.AddJob("json-before", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg1", false, "cli", "direct"); err != nil {
		t.Fatalf("first AddJob: %v", err)
	}

	// Switch to SQLite.
	s := openCronTestStore(t)
	cs.SetStore(s.Cron())

	// Add another job in SQLite.
	if _, err := cs.AddJob("sqlite-job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg2", false, "cli", "direct"); err != nil {
		t.Fatalf("second AddJob: %v", err)
	}

	// Switch back to JSON.
	cs.SetStore(nil)

	// Add a job via JSON backend.
	if _, err := cs.AddJob("json-job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg3", false, "cli", "direct"); err != nil {
		t.Fatalf("third AddJob: %v", err)
	}

	// JSON file should exist.
	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("JSON file should exist after SetStore(nil): %v", err)
	}

	// The repo should NOT contain "json-job".
	rows, err := s.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs: %v", err)
	}
	for _, row := range rows {
		if row.Name == "json-job" {
			t.Error("repo should not contain 'json-job' added after SetStore(nil)")
		}
	}

	// In-memory list should contain json-job.
	jobs := cs.ListJobs(true)
	found := false
	for _, j := range jobs {
		if j.Name == "json-job" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListJobs should contain 'json-job' after SetStore(nil)")
	}
}
