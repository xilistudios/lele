package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// closedDB opens a real SQLite database then closes it, so any later
// operation fails with sql.ErrConnDone. This exercises the error branches
// of every repository method without needing mocks.
func closedDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "closed.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", path, err)
	}
	// Grab the raw handle then close the store; the handle is now closed.
	raw := db.DB()
	if err := db.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}
	return raw
}

func TestRepos_ErrorPaths(t *testing.T) {
	db := closedDB(t)

	auth := &AuthRepo{db: db}
	goals := &GoalRepo{db: db}
	groups := &GroupRepo{db: db}
	clients := &NativeClientRepo{db: db}
	kv := &KVRepo{db: db}
	cron := &CronRepo{db: db}

	// ── Auth ──
	if _, _, err := auth.GetCredential("k"); err == nil {
		t.Error("Auth.GetCredential on closed DB: want error, got nil")
	}
	if err := auth.SetCredential("k", "{}"); err == nil {
		t.Error("Auth.SetCredential on closed DB: want error, got nil")
	}
	if err := auth.DeleteCredential("k"); err == nil {
		t.Error("Auth.DeleteCredential on closed DB: want error, got nil")
	}
	if err := auth.DeleteAllCredentials(); err == nil {
		t.Error("Auth.DeleteAllCredentials on closed DB: want error, got nil")
	}
	if _, err := auth.ListCredentials(); err == nil {
		t.Error("Auth.ListCredentials on closed DB: want error, got nil")
	}

	// ── Goals ──
	if _, _, err := goals.GetGoal("k"); err == nil {
		t.Error("Goals.GetGoal on closed DB: want error, got nil")
	}
	if err := goals.SetGoal("k", "{}"); err == nil {
		t.Error("Goals.SetGoal on closed DB: want error, got nil")
	}
	if err := goals.DeleteGoal("k"); err == nil {
		t.Error("Goals.DeleteGoal on closed DB: want error, got nil")
	}
	if _, err := goals.ListGoals(); err == nil {
		t.Error("Goals.ListGoals on closed DB: want error, got nil")
	}

	// ── Groups ──
	if _, _, err := groups.GetGroupState("k"); err == nil {
		t.Error("Groups.GetGroupState on closed DB: want error, got nil")
	}
	if err := groups.SetGroupState("k", "{}"); err == nil {
		t.Error("Groups.SetGroupState on closed DB: want error, got nil")
	}
	if err := groups.DeleteGroupState("k"); err == nil {
		t.Error("Groups.DeleteGroupState on closed DB: want error, got nil")
	}
	if _, err := groups.ListGroupStates(); err == nil {
		t.Error("Groups.ListGroupStates on closed DB: want error, got nil")
	}

	// ── NativeClients ──
	if _, _, err := clients.GetClient("k"); err == nil {
		t.Error("Clients.GetClient on closed DB: want error, got nil")
	}
	if err := clients.SetClient("k", "{}"); err == nil {
		t.Error("Clients.SetClient on closed DB: want error, got nil")
	}
	if err := clients.DeleteClient("k"); err == nil {
		t.Error("Clients.DeleteClient on closed DB: want error, got nil")
	}
	if err := clients.DeleteAllClients(); err == nil {
		t.Error("Clients.DeleteAllClients on closed DB: want error, got nil")
	}
	if _, err := clients.ListClients(); err == nil {
		t.Error("Clients.ListClients on closed DB: want error, got nil")
	}

	// ── KV ──
	if _, _, err := kv.Get("k"); err == nil {
		t.Error("KV.Get on closed DB: want error, got nil")
	}
	if err := kv.Set("k", "v"); err == nil {
		t.Error("KV.Set on closed DB: want error, got nil")
	}
	if err := kv.Delete("k"); err == nil {
		t.Error("KV.Delete on closed DB: want error, got nil")
	}
	if _, err := kv.Keys("p:"); err == nil {
		t.Error("KV.Keys on closed DB: want error, got nil")
	}

	// ── Cron ──
	if _, err := cron.ListCronJobs(); err == nil {
		t.Error("Cron.ListCronJobs on closed DB: want error, got nil")
	}
	if _, _, err := cron.GetCronJob("k"); err == nil {
		t.Error("Cron.GetCronJob on closed DB: want error, got nil")
	}
	if err := cron.UpsertCronJob(&CronJobRow{ID: "k"}); err == nil {
		t.Error("Cron.UpsertCronJob on closed DB: want error, got nil")
	}
	if err := cron.DeleteCronJob("k"); err == nil {
		t.Error("Cron.DeleteCronJob on closed DB: want error, got nil")
	}
	if err := cron.DeleteAllCronJobs(); err == nil {
		t.Error("Cron.DeleteAllCronJobs on closed DB: want error, got nil")
	}
}

func TestStore_CloseError(t *testing.T) {
	s := openTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("first Close() failed: %v", err)
	}
	// database/sql Close is idempotent: a repeated Close is a no-op with a
	// nil error (the driver's underlying handle is already closed).
	if err := s.Close(); err != nil {
		t.Errorf("second Close() returned error, want nil (idempotent close): %v", err)
	}
}

func TestCron_parseEpochMillis(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1754500000000", 1754500000000}, // valid decimal
		{"0", 0},
		{"", 0},         // empty → 0
		{"abc", 0},      // non-numeric → 0
		{"12.5", 0},     // float → parse failure → 0
		{"-123", -123},  // valid negative
		{"1e3", 0},      // exponent not accepted by ParseInt → 0
		{"99999999999999999999999", 0}, // overflow → 0
	}
	for _, c := range cases {
		if got := parseEpochMillis(c.in); got != c.want {
			t.Errorf("parseEpochMillis(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSchemaVersion_ParseError(t *testing.T) {
	// Create a store, then corrupt schema_version and verify both
	// schemaVersion and migrate surface the error.
	s := openTestStore(t)
	if _, err := s.DB().Exec(`UPDATE schema_meta SET value = 'not-a-number' WHERE key = 'schema_version'`); err != nil {
		t.Fatalf("corrupting schema_version failed: %v", err)
	}

	if _, err := schemaVersion(s.DB()); err == nil {
		t.Error("schemaVersion() on corrupt value: want error, got nil")
	}

	// migrate runs through schemaVersion → returns the parse error.
	if err := migrate(s.DB()); err == nil {
		t.Error("migrate() on corrupt value: want error, got nil")
	}
}

func TestSchemaVersion_NoRows(t *testing.T) {
	// Create a DB with the schema_meta table but no schema_version row.
	db := openRawSQLite(t)
	defer db.Close()
	if _, err := db.Exec(
		`CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
	); err != nil {
		t.Fatalf("create schema_meta failed: %v", err)
	}

	v, err := schemaVersion(db)
	if err != nil {
		t.Fatalf("schemaVersion() on empty schema_meta failed: %v", err)
	}
	if v != 0 {
		t.Errorf("schemaVersion() = %d, want 0", v)
	}
}

func TestSchemaVersion_CleanDB(t *testing.T) {
	// A freshly opened store is already migrated to SchemaVersion.
	s := openTestStore(t)
	v, err := schemaVersion(s.DB())
	if err != nil {
		t.Fatalf("schemaVersion() failed: %v", err)
	}
	if v != SchemaVersion {
		t.Errorf("schemaVersion() = %d, want %d", v, SchemaVersion)
	}
}

// openRawSQLite returns a raw *sql.DB over a temp file WITHOUT running
// migrations so the schema_meta table does not exist yet.
func openRawSQLite(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "raw.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(%q) failed: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCron_ListCronJobs_InvalidEpoch(t *testing.T) {
	s := openTestStore(t)
	repo := s.Cron()

	// Insert a job directly with a non-numeric created_at to exercise the
	// parseEpochMillis error path inside ListCronJobs/GetCronJob.
	if _, err := s.DB().Exec(
		`INSERT INTO cron_jobs(id, name, enabled, schedule, payload, state, scope, session_key, created_at, updated_at)
		 VALUES('bad', 'j', 1, '{}', '{}', '{}', 'global', '', 'not-a-number', 'also-bad')`,
	); err != nil {
		t.Fatalf("direct INSERT into cron_jobs failed: %v", err)
	}

	jobs, err := repo.ListCronJobs()
	if err != nil {
		t.Fatalf("ListCronJobs() failed: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("ListCronJobs() returned %d jobs, want 1", len(jobs))
	}
	if jobs[0].CreatedAtMS != 0 || jobs[0].UpdatedAtMS != 0 {
		t.Errorf("invalid epoch not degraded to 0: created=%d updated=%d", jobs[0].CreatedAtMS, jobs[0].UpdatedAtMS)
	}
	if !jobs[0].Enabled {
		t.Error("Enabled = false, want true (enabled=1 stored)")
	}

	got, ok, err := repo.GetCronJob("bad")
	if err != nil || !ok {
		t.Fatalf("GetCronJob(bad) = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if got.CreatedAtMS != 0 {
		t.Errorf("GetCronJob CreatedAtMS = %d, want 0 (invalid epoch)", got.CreatedAtMS)
	}
}

func TestKV_Keys_LikeEscape(t *testing.T) {
	s := openTestStore(t)
	kv := s.KV()

	// Keys containing LIKE wildcards must be matched literally.
	for _, k := range []string{"a%_b", "c\\d", "prefix:%x", "plain"} {
		if err := kv.Set(k, "v"); err != nil {
			t.Fatalf("Set(%q) failed: %v", k, err)
		}
	}

	// Prefix "a%" should only match literal "a%_b", not "plain".
	keys, err := kv.Keys("a%_")
	if err != nil {
		t.Fatalf("Keys(a%%_) failed: %v", err)
	}
	if len(keys) != 1 || keys[0] != "a%_b" {
		t.Errorf("Keys(a%%_) = %v, want [a%%_b]", keys)
	}

	// Empty prefix returns all keys.
	all, err := kv.Keys("")
	if err != nil {
		t.Fatalf("Keys('') failed: %v", err)
	}
	if len(all) < 4 {
		t.Errorf("Keys('') returned %d keys, want at least 4", len(all))
	}
}

func TestKV_Get_Missing(t *testing.T) {
	s := openTestStore(t)
	kv := s.KV()

	if _, ok, err := kv.Get("nope"); err != nil || ok {
		t.Fatalf("Get(nope) = (_, %v, %v), want (_, false, nil)", ok, err)
	}
}

func TestSessionRepo_PruneExcluded_KeepAll(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	key := "test:prune-keep"
	if err := repo.UpsertSession(SessionMeta{Key: key, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("UpsertSession() failed: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := repo.InsertMessage(key, i, "user", `{"role":"user","content":"m"}`, i == 0); err != nil {
			t.Fatalf("InsertMessage() failed: %v", err)
		}
	}

	// keepCount >= total → nothing pruned.
	pruned, err := repo.PruneExcluded(key, 5)
	if err != nil {
		t.Fatalf("PruneExcluded(keep=5) failed: %v", err)
	}
	if pruned != 0 {
		t.Errorf("PruneExcluded(keep=5) pruned %d, want 0", pruned)
	}
}

func TestSessionRepo_DeleteLastMessage_None(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	// No messages → returns -1 without error.
	seq, err := repo.DeleteLastMessage("missing")
	if err != nil {
		t.Fatalf("DeleteLastMessage(missing) failed: %v", err)
	}
	if seq != -1 {
		t.Errorf("DeleteLastMessage(missing) seq = %d, want -1", seq)
	}
}

func TestSessionRepo_MaxSeq_Empty(t *testing.T) {
	s := openTestStore(t)
	repo := s.Sessions()

	// No messages → MAX(seq) is NULL → -1 without error.
	seq, err := repo.MaxSeq("missing")
	if err != nil {
		t.Fatalf("MaxSeq(missing) failed: %v", err)
	}
	if seq != -1 {
		t.Errorf("MaxSeq(missing) = %d, want -1", seq)
	}
}