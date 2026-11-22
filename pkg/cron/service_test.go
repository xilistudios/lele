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

func TestExecuteJobByID_PanicDoesNotCrash(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := tmpDir + "/jobs.json"

	panicHandler := func(job *CronJob) (string, error) {
		panic("boom")
	}

	cs := NewCronService(storePath, panicHandler)

	job, err := cs.AddJob(
		"panicky",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(999_999_999_000)},
		"hello", false, "cli", "direct",
	)
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

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

	cs.executeJobByID(job.ID)

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

	cs.executeJobByID(panicJob.ID)

	if _, still := cs.executing.Load(panicJob.ID); still {
		t.Fatal("executing map still has panicky job")
	}

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

func TestRunLoop_PanicInTickKeepsScheduling(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "jobs.json")

	var handlerCalls int32
	cs := NewCronService(storePath, func(job *CronJob) (string, error) {
		atomic.AddInt32(&handlerCalls, 1)
		return "ok", nil
	})

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

	cs.mu.Lock()
	saved := cs.store
	cs.store = nil
	cs.mu.Unlock()

	time.Sleep(1500 * time.Millisecond)

	cs.mu.Lock()
	cs.store = saved
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

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

func TestCollectDueJobs_PanicDoesNotPoisonMutex(t *testing.T) {
	tmpDir := t.TempDir()

	cs := NewCronService(tmpDir+"/jobs.json", nil)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer cs.Stop()

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

	cs.checkJobs()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&handlerCalls) == 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if atomic.LoadInt32(&handlerCalls) == 0 {
		t.Fatal("handler never started")
	}

	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()
	cs.checkJobs()

	time.Sleep(300 * time.Millisecond)
	if got := atomic.LoadInt32(&handlerCalls); got != 1 {
		t.Fatalf("handler invoked %d times after skip tick, want 1 (executing guard failed)", got)
	}
	close(release)

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

func TestStop_DrainsInFlightJob(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "jobs.json")

	started := make(chan struct{})
	var completed atomic.Bool
	handler := func(job *CronJob) (string, error) {
		close(started)
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

	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start within 3s")
	}

	cs.Stop()

	if !completed.Load() {
		t.Error("job did not complete before Stop() returned — Stop did not drain in-flight jobs")
	}
}

func TestStop_GracePeriodExpiry(t *testing.T) {
	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "jobs.json")

	started := make(chan struct{})
	handler := func(job *CronJob) (string, error) {
		close(started)
		time.Sleep(30 * time.Second)
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

	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start within 3s")
	}

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

func TestListJobs_ReturnsCopyNotLiveSlice(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	if _, err := cs.AddJob("j1", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "hello", false, "cli", "direct"); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	got := cs.ListJobs(true)
	if len(got) != 1 {
		t.Fatalf("want 1 job, got %d", len(got))
	}
	got[0].Name = "MUTATED-BY-CALLER"
	got[0].Payload.Message = "rm -rf /"

	again := cs.ListJobs(true)
	if again[0].Name == "MUTATED-BY-CALLER" || again[0].Payload.Message == "rm -rf /" {
		t.Fatalf("caller mutation leaked into store: %+v", again[0])
	}
}
