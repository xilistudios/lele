package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// SchemaVersion is the latest schema version known to this build.
const SchemaVersion = 4

// migrations lists schema migrations in version order. Each entry is
// applied atomically inside a single transaction by migrate.
var migrations = []struct {
	Version int
	DDL     string
}{
	{
		Version: 1,
		DDL: `
CREATE TABLE sessions (
    key TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT 'agent',
    summary TEXT NOT NULL DEFAULT '',
    verbose_level TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    thinking_level TEXT NOT NULL DEFAULT '',
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    compaction_count INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_mode_updated ON sessions(mode, updated_at DESC);

CREATE TABLE session_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_key TEXT NOT NULL REFERENCES sessions(key) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    role TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL,
    excluded INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    UNIQUE(session_key, seq)
);
CREATE INDEX idx_messages_session ON session_messages(session_key, seq);

CREATE TABLE cron_jobs (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    schedule TEXT NOT NULL,
    payload TEXT NOT NULL,
    state TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'global',
    session_key TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE goals (
    session_key TEXT PRIMARY KEY,
    goal TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE groups_state (
    id TEXT PRIMARY KEY,
    state TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE auth_credentials (
    provider_key TEXT PRIMARY KEY,
    credential TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE native_clients (
    id TEXT PRIMARY KEY,
    client TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE kv (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- schema_meta is bootstrapped by migrate() before migrations run;
-- IF NOT EXISTS keeps the migration idempotent with that bootstrap.
CREATE TABLE IF NOT EXISTS schema_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`,
	},
	{
		Version: 2,
		DDL: `
-- Drop the unused 'content' column from session_messages.
-- The content is stored in the 'message' JSON blob and is never queried.
ALTER TABLE session_messages DROP COLUMN content;
`,
	},
	{
		Version: 3,
		DDL: `
-- Persist the in-memory eviction boundary so cold-load can skip evicted rows.
-- first_in_memory_seq = SQLite seq of the first message resident in memory.
-- Rows with seq < first_in_memory_seq were evicted from RAM (still persisted).
ALTER TABLE sessions ADD COLUMN first_in_memory_seq INTEGER NOT NULL DEFAULT 0;
`,
	},
	{
		Version: 4,
		DDL: `
-- Persist the per-session folder selected by the user (WebUI folder picker).
-- Its absolute path + first-level listing are injected into the session's
-- system prompt. Empty string means "no folder selected".
ALTER TABLE sessions ADD COLUMN folder TEXT NOT NULL DEFAULT '';
`,
	},
}

// migrate brings db up to SchemaVersion. It is idempotent: if the
// database is already at the latest version it does nothing. All
// pending migrations are applied inside a single transaction.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_meta table: %w", err)
	}

	current, err := schemaVersion(db)
	if err != nil {
		return err
	}

	if current >= SchemaVersion {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer tx.Rollback()

	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		if _, err := tx.Exec(m.DDL); err != nil {
			return fmt.Errorf("apply migration %d: %w", m.Version, err)
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO schema_meta(key, value) VALUES('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		SchemaVersion,
	); err != nil {
		return fmt.Errorf("update schema_version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}
	return nil
}

// schemaVersion reads the current schema version from schema_meta.
// A missing entry means version 0 (fresh database).
func schemaVersion(db *sql.DB) (int, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM schema_meta WHERE key = 'schema_version'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	version, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("parse schema_version %q: %w", value, err)
	}
	return version, nil
}
