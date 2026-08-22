package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/store"
)

func openTestStore(t *testing.T, path string) (*store.Store, error) {
	t.Helper()
	s, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { s.Close() })
	return s, nil
}

func currentTimestamp() string {
	return time.Now().Format("20060102-150405")
}

// setupMigrationDir builds a legacy JSON layout inside dir that mirrors what
// an old lele install would have on disk, matching the exact filenames and
// JSON shapes that migrateStorageForward expects.
func setupMigrationDir(t *testing.T, dir string) {
	t.Helper()

	// Sessions.
	sessionsDir := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	session := `{
	  "key": "cli:test",
	  "name": "test session",
	  "mode": "cli",
	  "summary": "sum",
	  "verbose_level": "normal",
	  "model": "test:model",
	  "thinking_level": "low",
	  "input_tokens": 10,
	  "output_tokens": 20,
	  "compaction_count": 1,
	  "created": "2024-01-01T00:00:00Z",
	  "updated": "2024-01-02T00:00:00Z",
	  "messages": [
	    {"role": "user", "exclude_from_context": false, "content": "hi"}
	  ]
	}`
	if err := os.WriteFile(filepath.Join(sessionsDir, "cli:test.json"), []byte(session), 0644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	// Ignored entries.
	os.MkdirAll(filepath.Join(sessionsDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(sessionsDir, "_index.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(sessionsDir, "notes.txt"), []byte("x"), 0644)

	// Cron.
	cronDir := filepath.Join(dir, "cron")
	os.MkdirAll(cronDir, 0755)
	cronJobs := `{
	  "jobs": [
	    {
	      "id": "job1",
	      "name": "job1",
	      "enabled": true,
	      "schedule": {"kind": "every", "every_ms": 1000},
	      "payload": "{}",
	      "state": {},
	      "scope": "workspace",
	      "createdAtMs": 1,
	      "updatedAtMs": 2
	    }
	  ]
	}`
	if err := os.WriteFile(filepath.Join(cronDir, "jobs.json"), []byte(cronJobs), 0644); err != nil {
		t.Fatalf("write cron: %v", err)
	}

	// Goals.
	goalsDir := filepath.Join(dir, "goals")
	os.MkdirAll(goalsDir, 0755)
	os.WriteFile(filepath.Join(goalsDir, "cli:test.json"), []byte(`{"goal":"g"}`), 0644)

	// Groups.
	groupsDir := filepath.Join(dir, "groups")
	os.MkdirAll(groupsDir, 0755)
	os.WriteFile(filepath.Join(groupsDir, "grp1.json"), []byte(`{"state":1}`), 0644)

	// Auth.
	auth := `{"credentials":{"openai":"{\"method\":\"key\"}"}}`
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte(auth), 0644)

	// Native clients.
	clients := `{"clients":{"c1":"{\"id\":\"c1\"}"}}`
	os.WriteFile(filepath.Join(dir, "native_clients.json"), []byte(clients), 0644)

	// Telegram offset.
	os.WriteFile(filepath.Join(dir, "telegram_offset.txt"), []byte("100\n"), 0644)
}

func TestMigrateStorageHelp(t *testing.T) {
	out := runCmd(migrateStorageHelp)
	for _, want := range []string{"migrate-storage", "--dry-run", "--rollback", "--help"} {
		if !strings.Contains(out, want) {
			t.Errorf("migrateStorageHelp should contain %q, got: %s", want, out)
		}
	}
}

func TestMigrateStorageCmd_Help(t *testing.T) {
	dir := t.TempDir()
	replaceArgs(t, []string{"lele", "migrate-storage", "--help"})
	out := runCmd(migrateStorageCmd)
	if !strings.Contains(out, "Usage: lele migrate-storage") {
		t.Errorf("expected usage, got: %s", out)
	}
	_ = dir
}

func TestMigrateStorageCmd_DryRun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", dir)
	setupMigrationDir(t, dir)

	replaceArgs(t, []string{"lele", "migrate-storage", "--dry-run"})
	out := runCmd(migrateStorageCmd)
	if !strings.Contains(out, "DRY RUN") {
		t.Errorf("expected dry-run marker, got: %s", out)
	}
	// A dry run must NOT back up the JSON sources.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "backup-json-") {
			t.Errorf("dry run should not create a backup dir, got %s", e.Name())
		}
	}
}

func TestMigrateStorageForward_MissingSessionDir(t *testing.T) {
	dir := t.TempDir()
	out := runCmd(func() { migrateStorageForward(dir, true) })
	if !strings.Contains(out, "directory not found") {
		t.Errorf("expected missing dir message, got: %s", out)
	}
}

func TestMigrateStorageForward_Actual(t *testing.T) {
	dir := t.TempDir()
	setupMigrationDir(t, dir)

	out := runCmd(func() { migrateStorageForward(dir, false) })
	for _, want := range []string{
		"cli:test",
		"job1",
		"grp1",
		"openai",
		"c1",
		"Migration complete",
		"Backup at:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("migrate output should contain %q, got: %s", want, out)
		}
	}

	// Verify DB was created and populated.
	s, err := openTestStore(t, filepath.Join(dir, "lele.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	meta, err := s.Sessions().GetSessionMeta("cli:test")
	if err != nil || meta == nil {
		t.Fatalf("expected migrated session meta, err=%v", err)
	}
	if meta.Mode != "cli" {
		t.Errorf("session mode = %q, want cli", meta.Mode)
	}
	cronRows, err := s.Cron().ListCronJobs()
	if err != nil || len(cronRows) != 1 {
		t.Fatalf("expected 1 cron job, got %d err=%v", len(cronRows), err)
	}
	goal, ok, err := s.Goals().GetGoal("cli:test")
	if err != nil || !ok {
		t.Fatalf("expected migrated goal, ok=%v err=%v", ok, err)
	}
	_ = goal
	cred, ok, err := s.Auth().GetCredential("openai")
	if err != nil || !ok {
		t.Fatalf("expected migrated auth cred, ok=%v err=%v", ok, err)
	}
	_ = cred
	client, ok, err := s.NativeClients().GetClient("c1")
	if err != nil || !ok {
		t.Fatalf("expected migrated client, ok=%v err=%v", ok, err)
	}
	_ = client
	migrated, ok, _ := s.KV().Get("migrated_from_json")
	if !ok || migrated == "" {
		t.Errorf("expected migration marker in kv")
	}

	// JSON sources should now be moved to a backup dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	foundBackup := false
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "backup-json-") {
			foundBackup = true
		}
	}
	if !foundBackup {
		t.Errorf("expected backup dir to be created")
	}
}

func TestMigrateStorageForward_BackupSuffix(t *testing.T) {
	dir := t.TempDir()
	setupMigrationDir(t, dir)
	// Pre-create a backup dir at the current timestamp pattern to force the
	// incrementing-suffix branch.
	pre := filepath.Join(dir, "backup-json-"+currentTimestamp())
	if err := os.MkdirAll(pre, 0755); err != nil {
		t.Fatalf("mkdir pre backup: %v", err)
	}
	out := runCmd(func() { migrateStorageForward(dir, false) })
	if !strings.Contains(out, "Migration complete") {
		t.Errorf("expected completion, got: %s", out)
	}
}

func TestMigrateStorageRollback_NoBackup(t *testing.T) {
	dir := t.TempDir()
	out := runCmd(func() { migrateStorageRollback(dir) })
	if !strings.Contains(out, "No backup found") {
		t.Errorf("expected no backup message, got: %s", out)
	}
}

func TestMigrateStorageRollback_Success(t *testing.T) {
	dir := t.TempDir()
	setupMigrationDir(t, dir)

	// Run forward first to produce a backup directory and a DB to delete.
	runCmd(func() { migrateStorageForward(dir, false) })
	if _, err := os.Stat(filepath.Join(dir, "lele.db")); err != nil {
		t.Fatalf("expected lele.db to exist before rollback: %v", err)
	}

	out := runCmd(func() { migrateStorageRollback(dir) })
	if !strings.Contains(out, "Rollback complete") {
		t.Errorf("expected rollback completion, got: %s", out)
	}
	// DB should be gone.
	if _, err := os.Stat(filepath.Join(dir, "lele.db")); !os.IsNotExist(err) {
		t.Errorf("lele.db should be deleted after rollback")
	}
	// sessions dir should be restored.
	if _, err := os.Stat(filepath.Join(dir, "sessions")); err != nil {
		t.Errorf("sessions dir should be restored: %v", err)
	}
}
