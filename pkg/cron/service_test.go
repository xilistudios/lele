package cron

import (
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
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

// TestRunLoop_PanicInTickKeepsScheduling is the regression test for CRN-01.
//
// A panic inside checkJobs (the scheduler tick) used to kill the runLoop
// goroutine silently — permanently stopping all job scheduling — and, because
// checkJobs unlocked cs.mu manually mid-function, it also left the mutex
// held forever, deadlocking every subsequent cron API call.
//
// Now the tick runs under a recover (logged, scheduling continues) and the
// locked section uses defer-unlock, so after a panicking tick the service
// must still schedule jobs and still respond to API calls.
func TestRunLoop_PanicInTickKeepsScheduling(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "jobs.json")

	var handlerCalls int32
	cs := NewCronService(storePath, func(job *CronJob) (string, error) {
		atomic.AddInt32(&handlerCalls, 1)
		return "ok", nil
	})

	// One normal due job.
	job, err := cs.AddJob(
		"healthy",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(60_000)},
		"hello", false, "cli", "direct",
	)
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	// Snapshot the store, then sabotage it so the NEXT tick panics
	// mid-collection: a nil store makes the range over cs.store.Jobs
	// dereference a nil pointer.
	cs.mu.Lock()
	saved := cs.store
	cs.store = nil
	cs.mu.Unlock()

	// Give the scheduler one tick with the sabotaged store: the tick panics,
	// but the recover must keep runLoop alive.
	time.Sleep(1500 * time.Millisecond)

	// Restore the store and make the healthy job due.
	cs.mu.Lock()
	cs.store = saved
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	// The runLoop must still be ticking: the due job gets executed within a
	// couple of scheduler ticks.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&handlerCalls) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if atomic.LoadInt32(&handlerCalls) == 0 {
		t.Fatal("runLoop died after a panicking tick — scheduling stopped permanently")
	}
}

// TestCollectDueJobs_PanicDoesNotPoisonMutex verifies that a panic inside the
// locked section of collectDueJobs releases cs.mu (defer-unlock), so later
// API calls do not deadlock. Under the old manually-unlocked checkJobs, this
// panic left the mutex held forever.
func TestCollectDueJobs_PanicDoesNotPoisonMutex(t *testing.T) {
	tmpDir := t.TempDir()

	cs := NewCronService(tmpDir+"/jobs.json", nil)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

	// Sabotage: nil store panics inside the locked section.
	cs.mu.Lock()
	cs.store = nil
	cs.mu.Unlock()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected collectDueJobs to panic with nil store")
			}
		}()
		cs.collectDueJobs()
	}()

	// The mutex must NOT be poisoned: these calls would hang forever under
	// the old code. Use a timeout so the test fails instead of hanging.
	done := make(chan struct{})
	go func() {
		cs.mu.Lock()
		cs.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cs.mu still locked after panic in collectDueJobs — deadlock")
	}
}

// TestCheckJobs_SkipsExecutingJob is a behavioral guard for the scheduling
// loop: a job already marked executing must not be collected twice by the
// 1-second ticker while its handler is still running.
func TestCheckJobs_SkipsExecutingJob(t *testing.T) {
	tmpDir := t.TempDir()

	release := make(chan struct{})
	var handlerCalls int32
	cs := NewCronService(tmpDir+"/jobs.json", func(job *CronJob) (string, error) {
		atomic.AddInt32(&handlerCalls, 1)
		<-release
		return "ok", nil
	})

	job, err := cs.AddJob(
		"slow",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(60_000)},
		"hello", false, "cli", "direct",
	)
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	// checkJobs is a no-op unless the service is running.
	cs.mu.Lock()
	cs.running = true
	cs.stopChan = make(chan struct{})
	cs.mu.Unlock()
	defer func() {
		cs.mu.Lock()
		cs.running = false
		cs.mu.Unlock()
	}()

	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	cs.checkJobs() // spawns executeJobByID goroutine; NextRunAtMS reset to nil

	// Wait until the handler is inside the blocking section.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&handlerCalls) == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if atomic.LoadInt32(&handlerCalls) == 0 {
		t.Fatal("handler never started")
	}

	// Make the job due again and re-tick: since the previous execution is
	// still running (we hold it open via release), this tick must skip it.
	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()
	cs.checkJobs()

	// Give the second execution goroutine a moment to (wrongly) start.
	time.Sleep(300 * time.Millisecond)
	if got := atomic.LoadInt32(&handlerCalls); got != 1 {
		t.Fatalf("handler invoked %d times after skip tick, want 1 (executing guard failed)", got)
	}
	close(release)

	// The executing slot must be clean after the first execution finishes.
	cleanupDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(cleanupDeadline) {
		if _, busy := cs.executing.Load(job.ID); !busy {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, busy := cs.executing.Load(job.ID); busy {
		t.Error("executing slot not cleaned up after job finished")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// CHT-04: Stop() drains in-flight jobs with bounded grace period
// ──────────────────────────────────────────────────────────────────────────────

// TestStop_DrainsInFlightJob verifies that Stop() blocks until a short-running
// in-flight job completes (within the grace period), so the caller can safely
// close resources after Stop() returns.
func TestStop_DrainsInFlightJob(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "jobs.json")

	started := make(chan struct{})
	var completed atomic.Bool
	handler := func(job *CronJob) (string, error) {
		close(started) // signal that the handler is running
		time.Sleep(800 * time.Millisecond)
		completed.Store(true)
		return "ok", nil
	}

	cs := NewCronService(storePath, handler)

	job, err := cs.AddJob(
		"fast",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(999_999_999_000)},
		"hello", false, "cli", "direct",
	)
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Make the job due.
	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	// Wait until the handler actually starts (up to 3s for the scheduler tick).
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start within 3s")
	}

	// Stop() must wait for the in-flight job to finish.
	cs.Stop()

	if !completed.Load() {
		t.Error("job did not complete before Stop() returned — Stop did not drain in-flight jobs")
	}
}

// TestStop_GracePeriodExpiry verifies that Stop() returns within a bounded
// grace period (~10s) even when a job is slow, logging the timeout instead of
// blocking forever.
func TestStop_GracePeriodExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "jobs.json")

	started := make(chan struct{})
	handler := func(job *CronJob) (string, error) {
		close(started) // signal that the handler is running
		time.Sleep(30 * time.Second) // Way longer than the 10s grace period
		return "ok", nil
	}

	cs := NewCronService(storePath, handler)

	job, err := cs.AddJob(
		"slow",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(999_999_999_000)},
		"hello", false, "cli", "direct",
	)
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Make the job due.
	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	// Wait until handler starts.
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start within 3s")
	}

	// Stop() must return within ~12s (10s grace + 2s slack), NOT block for 30s.
	start := time.Now()
	cs.Stop()
	elapsed := time.Since(start)

	if elapsed > 15*time.Second {
		t.Errorf("Stop() took %v, expected < 15s (grace period should have expired)", elapsed)
	}
	if elapsed < 8*time.Second {
		t.Errorf("Stop() took %v, expected >= 8s (grace period should have been waited)", elapsed)
	}
}
