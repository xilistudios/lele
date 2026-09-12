package cron

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSaveStore_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "cron", "jobs.json")

	cs := NewCronService(storePath, nil)

	_, err := cs.AddJob("test", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "hello", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	info, err := os.Stat(storePath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("cron store has permission %04o, want 0600", perm)
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}

// ──────────────────────────────────────────────────────────────────────────────
// Panic recovery tests (CHT-01)
// ──────────────────────────────────────────────────────────────────────────────

func TestExecuteJobByID_PanicDoesNotCrash(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := tmpDir + "/jobs.json"

	panicHandler := func(job *CronJob) (string, error) {
		panic("boom")
	}

	cs := NewCronService(storePath, panicHandler)

	// Use "every" schedule so the job is NOT deleted after execution
	// (unlike "at" which sets DeleteAfterRun=true).
	job, err := cs.AddJob(
		"panicky",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(999_999_999_000)},
		"hello", false, "cli", "direct",
	)
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	// Force NextRunAtMS into the past so executeJobByID will find it.
	cs.EnableJob(job.ID, true)
	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
			break
		}
	}
	cs.mu.Unlock()

	// Execute directly — must not crash the test process.
	cs.executeJobByID(job.ID)

	// Job must be marked as failed with the panic message.
	got := cs.GetJob(job.ID)
	if got == nil {
		t.Fatal("job disappeared after panic recovery")
	}
	if got.State.LastStatus != "error" {
		t.Errorf("LastStatus = %q, want %q", got.State.LastStatus, "error")
	}
	if got.State.LastError == "" {
		t.Error("LastError is empty; expected panic message")
	}

	// The executing map must be clean so subsequent jobs are not blocked.
	if _, stillExecuting := cs.executing.Load(job.ID); stillExecuting {
		t.Error("executing map still has the panicked job; it should have been cleaned up")
	}
}

func TestExecuteJobByID_PanicThenNormalJobSucceeds(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := tmpDir + "/jobs.json"

	normalRan := make(chan string, 1)

	handler := func(job *CronJob) (string, error) {
		if job.Name == "panicky" {
			panic("boom")
		}
		normalRan <- job.Name
		return "ok", nil
	}

	cs := NewCronService(storePath, handler)

	// Use "every" schedule to avoid DeleteAfterRun=true.
	panicJob, err := cs.AddJob(
		"panicky",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(999_999_999_000)},
		"msg", false, "cli", "direct",
	)
	if err != nil {
		t.Fatalf("AddJob panicky failed: %v", err)
	}
	normalJob, err := cs.AddJob(
		"normal",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(999_999_999_000)},
		"msg", false, "cli", "direct",
	)
	if err != nil {
		t.Fatalf("AddJob normal failed: %v", err)
	}

	// Run the panicking job first.
	cs.executeJobByID(panicJob.ID)

	// Verify executing map is clean for the panicky job.
	if _, still := cs.executing.Load(panicJob.ID); still {
		t.Fatal("executing map still has panicky job")
	}

	// The normal job must run and succeed (not blocked by stale executing entry).
	cs.executeJobByID(normalJob.ID)

	select {
	case name := <-normalRan:
		if name != "normal" {
			t.Errorf("expected normal job, got %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("normal job was never executed; executing map may be dirty from panicky job")
	}

	got := cs.GetJob(normalJob.ID)
	if got == nil {
		t.Fatal("normal job disappeared")
	}
	if got.State.LastStatus != "ok" {
		t.Errorf("normal LastStatus = %q, want %q", got.State.LastStatus, "ok")
	}
}
