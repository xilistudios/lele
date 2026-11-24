package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

const SchemaVersion = 10

var migrations = []struct {
	Version int
	DDL     string
	Apply   func(*sql.Tx) error
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
	{
		Version: 5,
		DDL: `
-- Durable message spool: an at-least-once queue for inbound and outbound
-- chat messages. Without it, anything in flight when the gateway stops is
-- lost (user messages never answered, replies never delivered). Persisting
-- it means a restart replays exactly the work that was still pending.
--
-- processed_messages is the dedupe ledger that turns at-least-once delivery
-- into exactly-once *processing*: a consumer that replays a message it has
-- already handled finds the (channel, msg_id) pair here and skips it.
--
-- Rows are deleted on completion rather than flagged as done, so the table
-- only ever holds live work; the partial index on claimed_by = '' therefore
-- stays small and the pending scan (direction, id) remains cheap.
CREATE TABLE spool (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    direction   TEXT NOT NULL CHECK (direction IN ('inbound','outbound')),
    channel     TEXT NOT NULL DEFAULT '',
    chat_id     TEXT NOT NULL DEFAULT '',
    session_key TEXT NOT NULL DEFAULT '',
    msg_id      TEXT NOT NULL DEFAULT '',
    payload     TEXT NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    claimed_by  TEXT NOT NULL DEFAULT '',
    claimed_at  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_spool_pending ON spool(direction, id) WHERE claimed_by = '';

CREATE TABLE processed_messages (
    channel      TEXT NOT NULL,
    msg_id       TEXT NOT NULL,
    processed_at TEXT NOT NULL,
    PRIMARY KEY (channel, msg_id)
);
`,
	},
	{
		Version: 6,
		DDL: `
-- Persist the terminal status of subagent sessions. The WebUI subagents
-- sidebar used to show every persisted subagent as "completed" because the
-- real status only lived in the in-memory SubagentManager, which is lost on
-- eviction/restart. Empty string means the session is not a subagent (or
-- predates this column): the reader falls back to "completed" for those.
ALTER TABLE sessions ADD COLUMN subagent_status TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_sessions_subagent_status ON sessions(subagent_status) WHERE subagent_status != '';
`,
	},
	{
		Version: 7,
		DDL: `
-- Pending native pairing PINs move from native_clients.json into the shared
-- SQLite store. The JSON channel broke when the CLI started opening the DB
-- (a0e256f): saveStoreUnlocked's SQLite branch persists Clients only and
-- returns before the JSON write, so 'lele client pin' stopped persisting the
-- PIN anywhere and the server answered 400 "invalid PIN".
--
-- created_at/expires_at are promoted to INTEGER unix-nanosecond columns so the
-- atomic redeem predicate and the FIFO eviction order are computed in SQL.
-- RFC3339 text is NOT lexicographically orderable when the fractional-digit
-- count varies ('.' 0x2E < 'Z' 0x5A), which would silently corrupt both.
-- The 'pending' column keeps the opaque JSON blob the domain layer owns.
CREATE TABLE native_pending_pins (
    pin        TEXT PRIMARY KEY,
    pending    TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX idx_pending_pins_expires ON native_pending_pins(expires_at);
CREATE INDEX idx_pending_pins_created ON native_pending_pins(created_at);
`,
	},
	{
		Version: 8,
		DDL: `
-- Persist the agent that EXECUTED a subagent session. The WebUI header and
-- subagent panel used to show an arbitrary agent for historical subagents:
-- every agent shares one SessionManager, so the persisted scan in
-- GetSessionSubagents saw the same rows for each registry agent and reported
-- the first one (the storage owner) instead of the executor.
-- Empty string means "not recorded" (a non-subagent session, or a row that
-- predates this column): the reader falls back to the storage-owning agent,
-- which is the historical behavior for old data.
-- No index: nothing filters sessions by agent_id, and the column is written
-- on every session upsert.
ALTER TABLE sessions ADD COLUMN agent_id TEXT NOT NULL DEFAULT '';
`,
	},
	{
		Version: 9,
		// Outbound spool backoff: a row that failed is deferred until
		// next_attempt_at instead of being re-claimed on the next 250 ms pump
		// tick. The pump used to burn all MaxOutboundAttempts in ~1.25 s, so any
		// failure lasting longer than that (429 with retry_after, a 2 s DNS blip)
		// silently lost the reply. The empty string means "claimable now"; every
		// writer uses spoolTimeLayout, whose fractional-digit width is fixed, so
		// the lexicographic comparison in the claim predicate is exact (the v7
		// PIN lesson: variable-width RFC3339 text is not orderable).
		//
		// spool_dead_letter holds exhausted, expired and partially-sent outbound
		// rows instead of deleting them: the payload stays inspectable and
		// re-sendable by hand, and the live-work spool (with its partial pending
		// index) stays small. Rows are pruned on the same 7-day retention as the
		// dedupe ledger.
		//
		// Both statements are additive and re-runnable: SQLite has no
		// ALTER TABLE ... ADD COLUMN IF NOT EXISTS, so the column is added only
		// when pragma_table_info says it is absent.
		Apply: func(tx *sql.Tx) error {
			var columns int
			if err := tx.QueryRow(
				`SELECT COUNT(*) FROM pragma_table_info('spool') WHERE name = 'next_attempt_at'`,
			).Scan(&columns); err != nil {
				return fmt.Errorf("inspect spool columns: %w", err)
			}
			if columns == 0 {
				if _, err := tx.Exec(`ALTER TABLE spool ADD COLUMN next_attempt_at TEXT NOT NULL DEFAULT ''`); err != nil {
					return fmt.Errorf("add spool.next_attempt_at: %w", err)
				}
			}

			if _, err := tx.Exec(`
CREATE TABLE IF NOT EXISTS spool_dead_letter (
    id          INTEGER PRIMARY KEY,
    direction   TEXT NOT NULL,
    channel     TEXT NOT NULL DEFAULT '',
    chat_id     TEXT NOT NULL DEFAULT '',
    session_key TEXT NOT NULL DEFAULT '',
    msg_id      TEXT NOT NULL DEFAULT '',
    payload     TEXT NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    dead_at     TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_spool_dead_letter_dead_at ON spool_dead_letter(dead_at);
`); err != nil {
				return fmt.Errorf("create spool_dead_letter: %w", err)
			}
			return nil
		},
	},
	{
		Version: 10,
		DDL: `
-- Usage counters that used to live in JSON files next to config.json:
-- command_usage.json (scope "command"), skills_usage.json (scope "skills",
-- grp = agent ID) and mcp_usage.json (scope "mcp", grp = server name).
-- config.json stays the only JSON configuration file; the counters move
-- into the shared store so concurrent processes merge inside SQLite
-- instead of read-modify-write cycles over files.
CREATE TABLE usage_counters (
    scope TEXT NOT NULL,
    grp TEXT NOT NULL DEFAULT '',
    key TEXT NOT NULL,
    count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (scope, grp, key)
);
`,
	},
}

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
		if m.Apply != nil {
			if err := m.Apply(tx); err != nil {
				return fmt.Errorf("apply migration %d: %w", m.Version, err)
			}
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
