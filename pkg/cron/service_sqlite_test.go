package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	writeLegacyJobsFile(t, jsonPath, legacyJob("legacy-1"))

	cs := NewCronService(jsonPath, nil)
	if len(cs.ListJobs(true)) != 1 {
		t.Fatalf("expected 1 job from legacy JSON, got %d", len(cs.ListJobs(true)))
	}

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

	if len(cs.ListJobs(true)) != 1 {
		t.Errorf("expected 1 job in memory, got %d", len(cs.ListJobs(true)))
	}

	if _, statErr := os.Stat(jsonPath + legacyMigratedSuffix); statErr != nil {
		t.Errorf("legacy JSON should be retired as %s%s: %v", jsonPath, legacyMigratedSuffix, statErr)
	}
	if _, statErr := os.Stat(jsonPath); !os.IsNotExist(statErr) {
		t.Errorf("legacy JSON still in place after the migration (err=%v), want it renamed so it cannot re-import", statErr)
	}

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

	if _, err := cs.AddJob("json-before", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg1", false, "cli", "direct"); err != nil {
		t.Fatalf("first AddJob: %v", err)
	}

	s := openCronTestStore(t)
	cs.SetStore(s.Cron())

	if _, err := cs.AddJob("sqlite-job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg2", false, "cli", "direct"); err != nil {
		t.Fatalf("second AddJob: %v", err)
	}

	cs.SetStore(nil)

	if _, err := cs.AddJob("json-job", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "msg3", false, "cli", "direct"); err != nil {
		t.Fatalf("third AddJob: %v", err)
	}

	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("JSON file should exist after SetStore(nil): %v", err)
	}

	rows, err := s.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs: %v", err)
	}
	for _, row := range rows {
		if row.Name == "json-job" {
			t.Error("repo should not contain 'json-job' added after SetStore(nil)")
		}
	}

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

func repoService(t *testing.T, s *store.Store, jsonName string) *CronService {
	t.Helper()

	cs := NewCronService(filepath.Join(t.TempDir(), jsonName), nil)
	if err := cs.SetStore(s.Cron()); err != nil {
		t.Fatalf("SetStore() failed: %v", err)
	}
	return cs
}

func runTicks(cs *CronService, ticks int) {
	for i := 0; i < ticks; i++ {
		cs.checkJobs()
	}
}

func startGatewayService(cs *CronService) {
	cs.mu.Lock()
	cs.running = true
	cs.stopChan = make(chan struct{})
	cs.mu.Unlock()
}

func repoJobIDs(t *testing.T, s *store.Store) []string {
	t.Helper()

	rows, err := s.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs() failed: %v", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func everyHour() CronSchedule {
	return CronSchedule{Kind: "every", EveryMS: int64Ptr(3600_000)}
}

func installCronWriteProbe(t *testing.T, s *store.Store) func() int {
	t.Helper()

	if _, err := s.DB().Exec(`CREATE TABLE cron_write_probe (id INTEGER PRIMARY KEY AUTOINCREMENT)`); err != nil {
		t.Fatalf("create cron_write_probe failed: %v", err)
	}
	if _, err := s.DB().Exec(`CREATE TRIGGER cron_write_probe_ins AFTER INSERT ON cron_jobs
		BEGIN INSERT INTO cron_write_probe(id) VALUES (NULL); END`); err != nil {
		t.Fatalf("create insert trigger failed: %v", err)
	}
	if _, err := s.DB().Exec(`CREATE TRIGGER cron_write_probe_upd AFTER UPDATE ON cron_jobs
		BEGIN INSERT INTO cron_write_probe(id) VALUES (NULL); END`); err != nil {
		t.Fatalf("create update trigger failed: %v", err)
	}
	if _, err := s.DB().Exec(`CREATE TRIGGER cron_write_probe_del AFTER DELETE ON cron_jobs
		BEGIN INSERT INTO cron_write_probe(id) VALUES (NULL); END`); err != nil {
		t.Fatalf("create delete trigger failed: %v", err)
	}

	return func() int {
		t.Helper()

		var n int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM cron_write_probe`).Scan(&n); err != nil {
			t.Fatalf("count cron_write_probe failed: %v", err)
		}
		return n
	}
}

func TestCronCLIAddSurvivesGatewayTick(t *testing.T) {
	s := openCronTestStore(t)
	gateway := repoService(t, s, "gateway.json")
	startGatewayService(gateway)
	defer gateway.Stop()

	if _, err := gateway.AddJob("gateway-job", everyHour(), "gw", false, "cli", "direct"); err != nil {
		t.Fatalf("gateway AddJob: %v", err)
	}

	cli := repoService(t, s, "cli.json")
	added, err := cli.AddJob("cli-job", everyHour(), "cli", false, "cli", "direct")
	if err != nil {
		t.Fatalf("cli AddJob: %v", err)
	}

	runTicks(gateway, 3)

	ids := repoJobIDs(t, s)
	if len(ids) != 2 {
		t.Fatalf("repo jobs after gateway ticks = %v, want the gateway job and the CLI job", ids)
	}
	if gateway.GetJob(added.ID) == nil {
		t.Fatalf("gateway memory lost the CLI job %s: the ticker never reloaded", added.ID)
	}
	if cli.GetJob(added.ID) == nil {
		t.Errorf("cli instance lost its own job %s", added.ID)
	}
}

func TestCronCLIRemoveNotResurrected(t *testing.T) {
	s := openCronTestStore(t)
	gateway := repoService(t, s, "gateway.json")
	startGatewayService(gateway)
	defer gateway.Stop()

	doomed, err := gateway.AddJob("gateway-job", everyHour(), "gw", false, "cli", "direct")
	if err != nil {
		t.Fatalf("gateway AddJob: %v", err)
	}
	survivor, err := gateway.AddJob("survivor", everyHour(), "gw", false, "cli", "direct")
	if err != nil {
		t.Fatalf("gateway AddJob: %v", err)
	}

	cli := repoService(t, s, "cli.json")
	if !cli.RemoveJob(doomed.ID) {
		t.Fatalf("cli RemoveJob(%s) = false, want the job removed", doomed.ID)
	}

	runTicks(gateway, 2)
	gateway.executeJobByID(survivor.ID)
	runTicks(gateway, 2)

	ids := repoJobIDs(t, s)
	if len(ids) != 1 || ids[0] != survivor.ID {
		t.Fatalf("repo jobs after a CLI remove and gateway ticks = %v, want only %s", ids, survivor.ID)
	}
	if gateway.GetJob(doomed.ID) != nil {
		t.Errorf("gateway memory resurrected the removed job %s", doomed.ID)
	}
}

func TestCronNoWriteWhenIdle(t *testing.T) {
	s := openCronTestStore(t)
	gateway := repoService(t, s, "gateway.json")
	startGatewayService(gateway)
	defer gateway.Stop()

	for i, name := range []string{"first", "second", "third"} {
		if _, err := gateway.AddJob(name, everyHour(), "msg", false, "cli", "direct"); err != nil {
			t.Fatalf("AddJob %d: %v", i, err)
		}
	}

	writes := installCronWriteProbe(t, s)
	before := writes()

	runTicks(gateway, 5)

	if got := writes() - before; got != 0 {
		t.Fatalf("idle ticks issued %d cron_jobs writes, want 0", got)
	}
	if ids := repoJobIDs(t, s); len(ids) != 3 {
		t.Errorf("repo jobs after idle ticks = %v, want the three untouched jobs", ids)
	}
}

func TestCronDueJobPersistsOnlyItsRow(t *testing.T) {
	s := openCronTestStore(t)
	var ran []string
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), func(job *CronJob) (string, error) {
		ran = append(ran, job.ID)
		return "ok", nil
	})
	if err := cs.SetStore(s.Cron()); err != nil {
		t.Fatalf("SetStore() failed: %v", err)
	}
	startGatewayService(cs)
	defer cs.Stop()

	due, err := cs.AddJob("due", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob due: %v", err)
	}
	idle, err := cs.AddJob("idle", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob idle: %v", err)
	}

	writes := installCronWriteProbe(t, s)
	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == due.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	before := writes()
	cs.checkJobs()

	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		cs.mu.RLock()
		busy := len(ran) == 0
		cs.mu.RUnlock()
		if !busy {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	for {
		if _, still := cs.executing.Load(due.ID); !still {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := writes() - before; got == 0 {
		t.Fatalf("a due job produced 0 cron_jobs writes, want its row persisted")
	}

	rows, err := s.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs() failed: %v", err)
	}
	for _, row := range rows {
		if row.ID == idle.ID && row.UpdatedAtMS != idle.UpdatedAtMS {
			t.Errorf("idle job updated_at = %d, want %d untouched by another job's tick", row.UpdatedAtMS, idle.UpdatedAtMS)
		}
	}
}

func TestCronOwnWritesRefreshRevision(t *testing.T) {
	s := openCronTestStore(t)
	cs := repoService(t, s, "gateway.json")

	assertRevisionCurrent := func(stage string) {
		t.Helper()

		count, maxUpdated, err := s.Cron().CronRevision()
		if err != nil {
			t.Fatalf("%s: CronRevision() failed: %v", stage, err)
		}
		cs.mu.RLock()
		got := cs.lastRevision
		cs.mu.RUnlock()
		if got.count != count || got.maxUpdated != maxUpdated {
			t.Fatalf("%s: tracked revision = %+v, want the repo's (%d, %d): own writes must not trigger a self-reload",
				stage, got, count, maxUpdated)
		}
	}

	job, err := cs.AddJob("job", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	assertRevisionCurrent("after AddJob")

	if got := cs.EnableJob(job.ID, false); got == nil {
		t.Fatalf("EnableJob(%s) = nil, want the job", job.ID)
	}
	assertRevisionCurrent("after EnableJob")

	if !cs.RemoveJob(job.ID) {
		t.Fatalf("RemoveJob(%s) = false, want it removed", job.ID)
	}
	assertRevisionCurrent("after RemoveJob")
}

func TestCronLoadFailureKeepsSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failed-load.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open(%q) failed: %v", path, err)
	}

	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	if err := cs.SetStore(s.Cron()); err != nil {
		s.Close()
		t.Fatalf("SetStore() failed: %v", err)
	}
	job, err := cs.AddJob("survivor", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		s.Close()
		t.Fatalf("AddJob: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	if err := cs.Load(); err == nil {
		t.Fatal("Load() = nil after the store was closed, want the read error")
	}
	jobs := cs.ListJobs(true)
	if len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("in-memory jobs after a failed load = %+v, want the previous snapshot kept", jobs)
	}

	reopened, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen %q failed: %v", path, err)
	}
	defer func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	}()

	rows, err := reopened.Cron().ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs() after reopen failed: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != job.ID {
		t.Fatalf("persisted rows after a failed load = %+v, want the job intact", rows)
	}
}

func TestSetStoreReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "broken.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open(%q) failed: %v", path, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	jsonJob, err := cs.AddJob("json-job", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	if err := cs.SetStore(s.Cron()); err == nil {
		t.Fatal("SetStore(broken repo) = nil, want the load error surfaced")
	}

	jobs := cs.ListJobs(true)
	if len(jobs) != 1 || jobs[0].ID != jsonJob.ID {
		t.Fatalf("in-memory jobs after a failed SetStore = %+v, want the previous snapshot kept", jobs)
	}
}

func writeLegacyJobsFile(t *testing.T, path string, jobs ...CronJob) {
	t.Helper()

	data, err := json.MarshalIndent(&CronStore{Version: 1, Jobs: jobs}, "", "  ")
	if err != nil {
		t.Fatalf("marshal legacy store: %v", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("write legacy JSON: %v", err)
	}
}

func legacyJob(id string) CronJob {
	return CronJob{
		ID:          id,
		Name:        id,
		Enabled:     true,
		Schedule:    everyHour(),
		Payload:     CronPayload{Kind: "agent_turn", Message: "legacy"},
		CreatedAtMS: 1700000000000,
		UpdatedAtMS: 1700000000000,
	}
}

func TestCronRemoveDuringExecutionNotResurrected(t *testing.T) {
	s := openCronTestStore(t)
	started := make(chan struct{})
	release := make(chan struct{})
	cs := NewCronService(filepath.Join(t.TempDir(), "gateway.json"), func(job *CronJob) (string, error) {
		close(started)
		<-release
		return "ok", nil
	})
	if err := cs.SetStore(s.Cron()); err != nil {
		t.Fatalf("SetStore() failed: %v", err)
	}
	startGatewayService(cs)
	defer cs.Stop()

	job, err := cs.AddJob("victim", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	go cs.executeJobByID(job.ID)
	<-started

	cli := repoService(t, s, "cli.json")
	if !cli.RemoveJob(job.ID) {
		t.Fatalf("cli.RemoveJob(%s) = false, want the job removed", job.ID)
	}

	close(release)
	cs.jobWg.Wait()

	if ids := repoJobIDs(t, s); len(ids) != 0 {
		t.Fatalf("repo jobs after removing an executing job = %v, want none: the in-flight state write must not recreate the row", ids)
	}

	runTicks(cs, 3)
	if ids := repoJobIDs(t, s); len(ids) != 0 {
		t.Fatalf("repo jobs after gateway ticks = %v, want none", ids)
	}
	if cs.GetJob(job.ID) != nil {
		t.Errorf("gateway memory resurrected the removed job %s", job.ID)
	}
}

func TestCronRunJobNowDoesNotResurrectRemovedJob(t *testing.T) {
	s := openCronTestStore(t)
	cs := repoService(t, s, "gateway.json")
	startGatewayService(cs)
	defer cs.Stop()

	job, err := cs.AddJob("victim", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	cli := repoService(t, s, "cli.json")
	if !cli.RemoveJob(job.ID) {
		t.Fatalf("cli.RemoveJob(%s) = false, want the job removed", job.ID)
	}

	if err := cs.RunJobNow(job.ID); err != nil {
		t.Fatalf("RunJobNow(%s) on a job removed from the store = %v, want the disappeared path", job.ID, err)
	}
	if ids := repoJobIDs(t, s); len(ids) != 0 {
		t.Fatalf("repo jobs after RunJobNow on a removed job = %v, want none", ids)
	}
}

func TestCronDueStatePersistenceDoesNotResurrectRemovedJob(t *testing.T) {
	s := openCronTestStore(t)
	cs := repoService(t, s, "gateway.json")
	startGatewayService(cs)
	defer cs.Stop()

	job, err := cs.AddJob("victim", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	cli := repoService(t, s, "cli.json")
	if !cli.RemoveJob(job.ID) {
		t.Fatalf("cli.RemoveJob(%s) = false, want the job removed", job.ID)
	}

	cs.mu.Lock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			past := time.Now().UnixMilli() - 1000
			cs.store.Jobs[i].State.NextRunAtMS = &past
		}
	}
	cs.mu.Unlock()

	if due := cs.collectDueJobs(); len(due) != 1 || due[0] != job.ID {
		t.Fatalf("collectDueJobs() = %v, want the stale in-memory job %s", due, job.ID)
	}
	if ids := repoJobIDs(t, s); len(ids) != 0 {
		t.Fatalf("repo jobs after persisting due state = %v, want none: the due state write must not recreate the row", ids)
	}
}

func TestCronLegacyFileImportsOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "jobs.json")
	writeLegacyJobsFile(t, jsonPath, legacyJob("legacy-1"))

	s := openCronTestStore(t)
	gateway := NewCronService(jsonPath, nil)
	if err := gateway.SetStore(s.Cron()); err != nil {
		t.Fatalf("SetStore() failed: %v", err)
	}
	startGatewayService(gateway)
	defer gateway.Stop()

	if ids := repoJobIDs(t, s); len(ids) != 1 || ids[0] != "legacy-1" {
		t.Fatalf("repo jobs after the legacy import = %v, want [legacy-1]", ids)
	}

	cli := repoService(t, s, "cli.json")
	if !cli.RemoveJob("legacy-1") {
		t.Fatal("cli.RemoveJob(legacy-1) = false, want the last job removed")
	}

	runTicks(gateway, 3)

	if ids := repoJobIDs(t, s); len(ids) != 0 {
		t.Fatalf("repo jobs after removing the last migrated job = %v, want none: the legacy file must not be re-imported", ids)
	}
	if gateway.GetJob("legacy-1") != nil {
		t.Error("gateway memory resurrected the removed legacy job")
	}
	if _, err := os.Stat(jsonPath + legacyMigratedSuffix); err != nil {
		t.Errorf("retired legacy file %s%s is missing: %v", jsonPath, legacyMigratedSuffix, err)
	}
}

func TestCronRemoveDuringPanickingExecutionNotResurrected(t *testing.T) {
	s := openCronTestStore(t)
	started := make(chan struct{})
	release := make(chan struct{})
	cs := NewCronService(filepath.Join(t.TempDir(), "gateway.json"), func(job *CronJob) (string, error) {
		close(started)
		<-release
		panic("job blew up")
	})
	if err := cs.SetStore(s.Cron()); err != nil {
		t.Fatalf("SetStore() failed: %v", err)
	}
	startGatewayService(cs)
	defer cs.Stop()

	job, err := cs.AddJob("victim", everyHour(), "msg", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	go cs.executeJobByID(job.ID)
	<-started

	cli := repoService(t, s, "cli.json")
	if !cli.RemoveJob(job.ID) {
		t.Fatalf("cli.RemoveJob(%s) = false, want the job removed", job.ID)
	}

	close(release)
	cs.jobWg.Wait()

	if ids := repoJobIDs(t, s); len(ids) != 0 {
		t.Fatalf("repo jobs after a panicking execution of a removed job = %v, want none: the panic state write must not recreate the row", ids)
	}
}
