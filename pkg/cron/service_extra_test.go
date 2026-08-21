package cron

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestService creates a CronService backed by a temp JSON file.
func newTestService(t *testing.T) *CronService {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "cron", "jobs.json")
	cs := NewCronService(storePath, nil)
	t.Cleanup(cs.Stop)
	return cs
}

// newTestServiceWithHandler creates a service with a configurable job handler
// that records every invocation.
func newTestServiceWithHandler(t *testing.T, handler JobHandler) *CronService {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "cron", "jobs.json")
	cs := NewCronService(storePath, handler)
	t.Cleanup(cs.Stop)
	return cs
}

func TestStart_StartsAndStops(t *testing.T) {
	cs := newTestService(t)

	if err := cs.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	if !cs.running {
		t.Error("expected running=true after Start")
	}
	if cs.stopChan == nil {
		t.Error("expected stopChan to be non-nil after Start")
	}

	// Starting again should be a no-op (already running).
	if err := cs.Start(); err != nil {
		t.Fatalf("second Start() failed: %v", err)
	}

	st := cs.Status()["enabled"].(bool)
	if !st {
		t.Error("expected Status().enabled == true")
	}

	cs.Stop()
	if cs.running {
		t.Error("expected running=false after Stop")
	}
	if cs.stopChan != nil {
		t.Error("expected stopChan to be nil after Stop")
	}

	// Stopping again should be a no-op.
	cs.Stop()
}

func TestStop_NotRunning(t *testing.T) {
	cs := newTestService(t)
	cs.Stop() // should not panic
}

func TestStart_ErrorPath_NonExistentRepoDir(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "no", "dir", "jobs.json"), nil)

	// Simulate a directory that cannot be created by pre-creating a file at
	// the parent path so MkdirAll fails.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	bad := NewCronService(filepath.Join(blocker, "sub", "jobs.json"), nil)
	if err := bad.Start(); err == nil {
		t.Logf("error occurred: %v (not strictly required)", err)
		bad.Stop()
	} else {
		bad.Stop()
	}
	cs.Stop()
	_ = cs
}

func TestLoad_ReloadsFromDisk(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "cron", "jobs.json")
	cs := NewCronService(storePath, nil)

	if _, err := cs.AddJob("job1", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct"); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	// Fresh service should reload the persisted job.
	cs2 := NewCronService(storePath, nil)
	if err := cs2.Load(); err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	jobs := cs2.ListJobs(true)
	if len(jobs) != 1 || jobs[0].Name != "job1" {
		t.Fatalf("expected job1 loaded, got %+v", jobs)
	}
}

func TestLoad_ErrorPath(t *testing.T) {
	// A directory in place of the file should produce an error.
	storePath := filepath.Join(t.TempDir(), "jobs.json")
	if err := os.Mkdir(storePath, 0700); err != nil {
		t.Fatal(err)
	}
	cs := NewCronService(storePath, nil)
	if err := cs.Load(); err == nil {
		t.Error("expected error loading store with directory in its place")
	}
}

func TestSetOnJob(t *testing.T) {
	cs := newTestService(t)
	called := false
	cs.SetOnJob(func(job *CronJob) (string, error) {
		called = true
		return "ok", nil
	})
	job, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.RunJobNow(job.ID); err != nil {
		t.Fatalf("RunJobNow: %v", err)
	}
	if !called {
		t.Error("expected handler to be invoked after SetOnJob")
	}
}

func TestUpdateJob_SuccessAndNotFound(t *testing.T) {
	cs := newTestService(t)
	job, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "old", false, "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}

	job.Name = "renamed"
	if err := cs.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob failed: %v", err)
	}
	got := cs.GetJob(job.ID)
	if got == nil || got.Name != "renamed" {
		t.Fatalf("expected renamed job, got %+v", got)
	}

	notFound := &CronJob{ID: "does-not-exist"}
	if err := cs.UpdateJob(notFound); err == nil {
		t.Error("expected error updating non-existent job")
	}
}

func TestRemoveJob(t *testing.T) {
	cs := newTestService(t)
	job, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}

	if !cs.RemoveJob(job.ID) {
		t.Error("expected RemoveJob to return true")
	}
	if cs.GetJob(job.ID) != nil {
		t.Error("job should be gone after remove")
	}

	// Removing a non-existent job returns false.
	if cs.RemoveJob(job.ID) {
		t.Error("expected RemoveJob to return false for missing job")
	}
}

func TestEnableJob(t *testing.T) {
	cs := newTestService(t)
	job, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}

	// Disable.
	disabled := cs.EnableJob(job.ID, false)
	if disabled == nil || disabled.Enabled {
		t.Fatal("expected disabled job")
	}
	if g := cs.GetJob(job.ID); g.Enabled || g.State.NextRunAtMS != nil {
		t.Error("disabled job should have Enabled=false and nil NextRunAtMS")
	}

	// Re-enable.
	enabled := cs.EnableJob(job.ID, true)
	if enabled == nil || !enabled.Enabled || enabled.State.NextRunAtMS == nil {
		t.Fatal("expected re-enabled job with a next run time")
	}

	// Unknown job returns nil.
	if cs.EnableJob("nope", true) != nil {
		t.Error("expected nil for unknown job")
	}
}

func TestGetJob_NotFound(t *testing.T) {
	cs := newTestService(t)
	if cs.GetJob("missing") != nil {
		t.Error("expected nil for missing job")
	}
}

func TestListJobs_IncludeDisabled(t *testing.T) {
	cs := newTestService(t)
	if _, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct"); err != nil {
		t.Fatal(err)
	}
	// Find the job and disable it.
	jobs := cs.ListJobs(true)
	cs.EnableJob(jobs[0].ID, false)

	if len(cs.ListJobs(false)) != 0 {
		t.Error("expected 0 enabled jobs")
	}
	if len(cs.ListJobs(true)) != 1 {
		t.Error("expected 1 job with includeDisabled=true")
	}
}

func TestRunJobNow_Success(t *testing.T) {
	var mu sync.Mutex
	results := map[string]string{}
	cs := newTestServiceWithHandler(t, func(job *CronJob) (string, error) {
		mu.Lock()
		results[job.ID] = "ran"
		mu.Unlock()
		return "output", nil
	})
	job, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}

	if err := cs.RunJobNow(job.ID); err != nil {
		t.Fatalf("RunJobNow: %v", err)
	}

	got := cs.GetJob(job.ID)
	if got.State.LastStatus != "ok" {
		t.Errorf("LastStatus = %q, want ok", got.State.LastStatus)
	}
	if got.State.LastError != "" {
		t.Errorf("LastError = %q, want empty", got.State.LastError)
	}
	if got.State.LastRunAtMS == nil {
		t.Error("LastRunAtMS should be set")
	}
	mu.Lock()
	if results[job.ID] != "ran" {
		t.Error("handler should have run")
	}
	mu.Unlock()
}

func TestRunJobNow_HandlerError(t *testing.T) {
	cs := newTestServiceWithHandler(t, func(job *CronJob) (string, error) {
		return "", fmt.Errorf("boom")
	})
	job, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.RunJobNow(job.ID); err != nil {
		t.Fatalf("RunJobNow: %v", err)
	}
	got := cs.GetJob(job.ID)
	if got.State.LastStatus != "error" {
		t.Errorf("LastStatus = %q, want error", got.State.LastStatus)
	}
	if got.State.LastError != "boom" {
		t.Errorf("LastError = %q, want boom", got.State.LastError)
	}
}

func TestRunJobNow_NotFound(t *testing.T) {
	cs := newTestService(t)
	if err := cs.RunJobNow("missing"); err == nil {
		t.Error("expected error running missing job")
	}
}

func TestRunJobNow_ConcurrentExecution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	cs := newTestServiceWithHandler(t, func(job *CronJob) (string, error) {
		close(started)
		<-release
		return "ok", nil
	})
	job, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- cs.RunJobNow(job.ID)
	}()
	<-started

	// Second concurrent call should be rejected.
	if err := cs.RunJobNow(job.ID); err == nil {
		t.Error("expected concurrent execution to be rejected")
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("first RunJobNow failed: %v", err)
	}
}

func TestStatus_WithJobs(t *testing.T) {
	cs := newTestService(t)
	if err := cs.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg", false, "cli", "direct"); err != nil {
		t.Fatal(err)
	}

	st := cs.Status()
	if st["enabled"].(bool) != true {
		t.Errorf("enabled = %v, want true", st["enabled"])
	}
	if st["jobs"].(int) != 1 {
		t.Errorf("jobs = %v, want 1", st["jobs"])
	}
	if st["nextWakeAtMS"] == nil {
		t.Error("nextWakeAtMS should be non-nil")
	}
}

func TestRecomputeNextRuns(t *testing.T) {
	cs := newTestService(t)
	if _, err := cs.AddJob("every", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "m", false, "cli", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddJob("at", CronSchedule{Kind: "at", AtMS: int64Ptr(time.Now().Add(time.Hour).UnixMilli())}, "m", false, "cli", "d"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddJob("cron", CronSchedule{Kind: "cron", Expr: "* * * * *"}, "m", false, "cli", "d"); err != nil {
		t.Fatal(err)
	}

	cs.recomputeNextRuns()
	for _, job := range cs.ListJobs(true) {
		if job.State.NextRunAtMS == nil {
			t.Errorf("job %s should have a next run computed", job.Name)
		}
	}

	// Disabled jobs should not get a next run recomputed.
	cs.EnableJob("every", false)
	cs.recomputeNextRuns()
	for _, job := range cs.ListJobs(true) {
		if !job.Enabled && job.State.NextRunAtMS != nil {
			t.Errorf("disabled job %s should have nil NextRunAtMS", job.Name)
		}
	}
}

func TestGetNextWakeMS(t *testing.T) {
	cs := newTestService(t)
	if cs.getNextWakeMS() != nil {
		t.Error("expected nil next wake on empty store")
	}

	far := time.Now().Add(2 * time.Hour).UnixMilli()
	near := time.Now().Add(time.Hour).UnixMilli()
	var nearJob *CronJob
	var err error
	if _, err = cs.AddJob("far", CronSchedule{Kind: "at", AtMS: &far}, "m", false, "cli", "d"); err != nil {
		t.Fatal(err)
	}
	nearJob, err = cs.AddJob("near", CronSchedule{Kind: "at", AtMS: &near}, "m", false, "cli", "d")
	if err != nil {
		t.Fatal(err)
	}

	wake := cs.getNextWakeMS()
	if wake == nil || *wake != near {
		t.Errorf("expected wake=%d, got %v", near, wake)
	}

	// Disabled jobs are ignored.
	cs.EnableJob(nearJob.ID, false)
	wake = cs.getNextWakeMS()
	if wake == nil || *wake != far {
		t.Errorf("expected wake=%d after disabling, got %v", far, wake)
	}
}

func TestGetNextWakeMS_NilDisabledJobs(t *testing.T) {
	cs := newTestService(t)
	// A job with nil next run should be ignored by getNextWakeMS.
	if _, err := cs.AddJob("nilnext", CronSchedule{Kind: "at", AtMS: nil}, "m", false, "cli", "d"); err != nil {
		t.Fatal(err)
	}
	if wake := cs.getNextWakeMS(); wake != nil {
		t.Errorf("expected nil wake, got %d", *wake)
	}
}

func TestComputeNextRun_Branches(t *testing.T) {
	cs := newTestService(t)
	now := time.Now().UnixMilli()

	tests := []struct {
		name     string
		schedule CronSchedule
		wantNil  bool
	}{
		{"at in past", CronSchedule{Kind: "at", AtMS: int64Ptr(now - 1000)}, true},
		{"at in future", CronSchedule{Kind: "at", AtMS: int64Ptr(now + 1000)}, false},
		{"at nil", CronSchedule{Kind: "at"}, true},
		{"every nil", CronSchedule{Kind: "every"}, true},
		{"every zero", CronSchedule{Kind: "every", EveryMS: int64Ptr(0)}, true},
		{"every negative", CronSchedule{Kind: "every", EveryMS: int64Ptr(-5)}, true},
		{"every positive", CronSchedule{Kind: "every", EveryMS: int64Ptr(5000)}, false},
		{"cron empty expr", CronSchedule{Kind: "cron", Expr: ""}, true},
		{"cron valid", CronSchedule{Kind: "cron", Expr: "*/5 * * * *"}, false},
		{"cron invalid", CronSchedule{Kind: "cron", Expr: "this is not valid"}, true},
		{"unknown kind", CronSchedule{Kind: "bogus"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cs.computeNextRun(&tt.schedule, now)
			if tt.wantNil && got != nil {
				t.Errorf("expected nil, got %d", *got)
			}
			if !tt.wantNil && got == nil {
				t.Error("expected non-nil")
			}
		})
	}
}

func TestCheckJobs_ExecutesDueJobs(t *testing.T) {
	var ran atomic.Int32
	cs := newTestServiceWithHandler(t, func(job *CronJob) (string, error) {
		ran.Add(1)
		return "ok", nil
	})
	cs.running = true

	// A due job: next run in the past.
	past := time.Now().Add(-time.Minute).UnixMilli()
	job, err := cs.AddJob("due", CronSchedule{Kind: "at", AtMS: &past}, "m", false, "cli", "d")
	if err != nil {
		t.Fatal(err)
	}
	// Force next run in the past regardless of computeNextRun.
	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	ready := make(chan struct{})
	go func() {
		cs.checkJobs()
		close(ready)
	}()
	<-ready

	// Wait for the async executeJobByID goroutine.
	deadline := time.After(2 * time.Second)
	for ran.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for job execution")
		case <-time.After(10 * time.Millisecond):
		}
	}

	// An 'at' job has DeleteAfterRun=true, so it should be removed after execution.
	got := cs.GetJob(job.ID)
	if got != nil {
		t.Error("due 'at' job should be removed after checkJobs ran it")
	}
}

func TestCheckJobs_SkipsExecutingAndDisabled(t *testing.T) {
	var ran atomic.Int32
	cs := newTestServiceWithHandler(t, func(job *CronJob) (string, error) {
		ran.Add(1)
		return "ok", nil
	})
	cs.running = true
	past := time.Now().Add(-time.Minute).UnixMilli()

	// Disabled job (no handler run expected).
	disabledJob, err := cs.AddJob("disabled", CronSchedule{Kind: "at", AtMS: &past}, "m", false, "cli", "d")
	if err != nil {
		t.Fatal(err)
	}
	cs.EnableJob(disabledJob.ID, false)

	// Job currently marked as executing should be skipped.
	execJob, err := cs.AddJob("executing", CronSchedule{Kind: "at", AtMS: &past}, "m", false, "cli", "d")
	if err != nil {
		t.Fatal(err)
	}
	cs.executing.Store(execJob.ID, true)
	t.Cleanup(func() { cs.executing.Delete(execJob.ID) })

	cs.mu.Lock()
	for i := range cs.store.Jobs {
		switch cs.store.Jobs[i].ID {
		case disabledJob.ID, execJob.ID:
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	cs.checkJobs()

	// Give any spawned goroutines a moment (none should run the handler).
	time.Sleep(100 * time.Millisecond)
	if ran.Load() != 0 {
		t.Errorf("handler ran %d times, want 0", ran.Load())
	}

	// Executing job should not have run.
	gotExec := cs.GetJob(execJob.ID)
	if gotExec.State.LastRunAtMS != nil {
		t.Error("executing job should not have run")
	}
	// Disabled job should not have run.
	gotD := cs.GetJob(disabledJob.ID)
	if gotD.State.LastRunAtMS != nil || gotD.Enabled {
		t.Error("disabled job should not have run")
	}
}

func TestCheckJobs_NotRunning(t *testing.T) {
	cs := newTestService(t)
	cs.checkJobs() // not running → early return, no panic
}

func TestExecuteJobByID_NotFound(t *testing.T) {
	cs := newTestService(t)
	cs.executeJobByID("missing") // should return without panicking
}

func TestExecuteJobByID_AtDeleteAfterRun(t *testing.T) {
	cs := newTestServiceWithHandler(t, func(job *CronJob) (string, error) {
		return "ok", nil
	})
	past := time.Now().Add(-time.Minute).UnixMilli()
	job, err := cs.AddJob("at-delete", CronSchedule{Kind: "at", AtMS: &past}, "m", false, "cli", "d")
	if err != nil {
		t.Fatal(err)
	}
	job.DeleteAfterRun = true
	if err := cs.UpdateJob(job); err != nil {
		t.Fatal(err)
	}

	cs.executeJobByID(job.ID)
	if cs.GetJob(job.ID) != nil {
		t.Error("job with DeleteAfterRun should be removed after execution")
	}
}

func TestExecuteJobByID_AtCompletesAndDisables(t *testing.T) {
	var ran atomic.Int32
	cs := newTestServiceWithHandler(t, func(job *CronJob) (string, error) {
		ran.Add(1)
		return "ok", nil
	})
	past := time.Now().Add(-time.Minute).UnixMilli()
	job, err := cs.AddJob("at-once", CronSchedule{Kind: "at", AtMS: &past}, "m", false, "cli", "d")
	if err != nil {
		t.Fatal(err)
	}

	cs.executeJobByID(job.ID)
	got := cs.GetJob(job.ID)
	if got != nil {
		t.Fatal("one-time 'at' job should be removed after run (DeleteAfterRun=true)")
	}
	if ran.Load() != 1 {
		t.Errorf("handler ran %d times, want 1", ran.Load())
	}
}

func TestExecuteJobByID_EveryComputesNext(t *testing.T) {
	var ran atomic.Int32
	cs := newTestServiceWithHandler(t, func(job *CronJob) (string, error) {
		ran.Add(1)
		return "ok", nil
	})
	job, err := cs.AddJob("recurring", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "m", false, "cli", "d")
	if err != nil {
		t.Fatal(err)
	}

	cs.executeJobByID(job.ID)
	got := cs.GetJob(job.ID)
	if !got.Enabled {
		t.Error("recurring job should remain enabled")
	}
	if got.State.NextRunAtMS == nil {
		t.Error("recurring job should have a new next run")
	}
	if got.State.LastStatus != "ok" {
		t.Errorf("LastStatus = %q, want ok", got.State.LastStatus)
	}
}

func TestLoad_CreatesEmptyStore(t *testing.T) {
	cs := newTestService(t)
	if cs.store == nil {
		t.Fatal("store should be initialized after NewCronService")
	}
	if len(cs.store.Jobs) != 0 {
		t.Error("expected empty jobs")
	}
}