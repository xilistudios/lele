package store

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func openRawDB(path string) (*sql.DB, error) {
	dsn := path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"
	return sql.Open("sqlite", dsn)
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	})
	return s
}

func TestOpen_CreatesSchema(t *testing.T) {
	s := openTestStore(t)

	wantTables := []string{
		"sessions",
		"session_messages",
		"cron_jobs",
		"goals",
		"groups_state",
		"auth_credentials",
		"native_clients",
		"native_pending_pins",
		"kv",
		"schema_meta",
	}

	for _, table := range wantTables {
		var name string
		err := s.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing from sqlite_master: %v", table, err)
		}
	}
}

func TestMigrations_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("first Open(%q) failed: %v", path, err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first Close() failed: %v", err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open(%q) failed: %v", path, err)
	}
	defer func() {
		if err := s2.Close(); err != nil {
			t.Errorf("second Close() failed: %v", err)
		}
	}()

	var version string
	if err := s2.DB().QueryRow(
		`SELECT value FROM schema_meta WHERE key = 'schema_version'`,
	).Scan(&version); err != nil {
		t.Fatalf("read schema_version failed: %v", err)
	}
	if want := strconv.Itoa(SchemaVersion); version != want {
		t.Errorf("schema_version = %q, want %q", version, want)
	}
}

func TestKV_SetGetDeleteKeys(t *testing.T) {
	s := openTestStore(t)
	kv := s.KV()

	if _, ok, err := kv.Get("missing"); err != nil || ok {
		t.Fatalf("Get(missing) = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	if err := kv.Set("telegram_offset", "42"); err != nil {
		t.Fatalf("Set(telegram_offset) failed: %v", err)
	}
	val, ok, err := kv.Get("telegram_offset")
	if err != nil || !ok || val != "42" {
		t.Fatalf("Get(telegram_offset) = (%q, %v, %v), want (\"42\", true, nil)", val, ok, err)
	}

	if err := kv.Set("telegram_offset", "43"); err != nil {
		t.Fatalf("Set(telegram_offset) upsert failed: %v", err)
	}
	val, ok, err = kv.Get("telegram_offset")
	if err != nil || !ok || val != "43" {
		t.Fatalf("Get(telegram_offset) after upsert = (%q, %v, %v), want (\"43\", true, nil)", val, ok, err)
	}

	for k, v := range map[string]string{
		"workspace:foo": "1",
		"workspace:bar": "2",
		"other":         "3",
	} {
		if err := kv.Set(k, v); err != nil {
			t.Fatalf("Set(%q) failed: %v", k, err)
		}
	}
	keys, err := kv.Keys("workspace:")
	if err != nil {
		t.Fatalf("Keys(workspace:) failed: %v", err)
	}
	wantKeys := []string{"workspace:bar", "workspace:foo"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("Keys(workspace:) = %v, want %v", keys, wantKeys)
	}

	if err := kv.Delete("telegram_offset"); err != nil {
		t.Fatalf("Delete(telegram_offset) failed: %v", err)
	}
	if _, ok, err := kv.Get("telegram_offset"); err != nil || ok {
		t.Fatalf("Get(telegram_offset) after delete = (_, %v, %v), want (_, false, nil)", ok, err)
	}
	if err := kv.Delete("telegram_offset"); err != nil {
		t.Fatalf("second Delete(telegram_offset) failed: %v", err)
	}
}

func TestOpen_PragmasApplied(t *testing.T) {
	s := openTestStore(t)

	var journalMode string
	if err := s.DB().QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode failed: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var foreignKeys int
	if err := s.DB().QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("PRAGMA foreign_keys failed: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestMigrations_UpgradeV6ToV7_PreservesClients(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", path, err)
	}
	if err := s.NativeClients().SetClient("client-1", `{"name":"test"}`); err != nil {
		t.Fatalf("SetClient failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close() failed: %v", err)
	}

	db, err := openRawDB(path)
	if err != nil {
		t.Fatalf("openRawDB(%q) failed: %v", path, err)
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS native_pending_pins`); err != nil {
		db.Close()
		t.Fatalf("DROP native_pending_pins: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE sessions DROP COLUMN agent_id`); err != nil {
		db.Close()
		t.Fatalf("DROP sessions.agent_id: %v", err)
	}
	if _, err := db.Exec(
		`UPDATE schema_meta SET value = '6' WHERE key = 'schema_version'`,
	); err != nil {
		db.Close()
		t.Fatalf("downgrade schema_version: %v", err)
	}
	db.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) after downgrade failed: %v", path, err)
	}
	defer func() {
		if err := s2.Close(); err != nil {
			t.Errorf("Close() failed: %v", err)
		}
	}()

	var version string
	if err := s2.DB().QueryRow(
		`SELECT value FROM schema_meta WHERE key = 'schema_version'`,
	).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if want := strconv.Itoa(SchemaVersion); version != want {
		t.Errorf("schema_version = %q, want %q", version, want)
	}

	var tableName string
	if err := s2.DB().QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='native_pending_pins'`,
	).Scan(&tableName); err != nil {
		t.Fatalf("native_pending_pins table missing: %v", err)
	}

	client, found, err := s2.NativeClients().GetClient("client-1")
	if err != nil {
		t.Fatalf("GetClient(client-1) error: %v", err)
	}
	if !found {
		t.Fatalf("GetClient(client-1) not found after v6→v7 upgrade")
	}
	if client != `{"name":"test"}` {
		t.Errorf("GetClient(client-1) = %q, want %q", client, `{"name":"test"}`)
	}
}
