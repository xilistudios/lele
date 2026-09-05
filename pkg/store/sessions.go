package store

import (
	"database/sql"
	"fmt"
	"time"
)

// SessionRepo provides SQLite-backed persistence for sessions and their messages.
//
// It replaces the JSON file-based storage with incremental message appending.
// Session metadata lives in the `sessions` table; individual messages in
// `session_messages`. Messages are stored as opaque JSON strings — this
// package never interprets their contents.
type SessionRepo struct {
	db *sql.DB
}

// SessionMeta holds lightweight session metadata (no messages).
type SessionMeta struct {
	Key              string
	Name             string
	Mode             string
	Summary          string
	VerboseLevel     string
	Model            string
	ThinkingLevel    string
	Folder           string
	InputTokens      int
	OutputTokens     int
	CompactionCount  int
	FirstInMemorySeq int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// UpsertSession inserts or updates session metadata.
func (r *SessionRepo) UpsertSession(meta SessionMeta) error {
	if _, err := r.db.Exec(
		`INSERT INTO sessions(key, name, mode, summary, verbose_level, model,
		 thinking_level, folder, input_tokens, output_tokens, compaction_count,
		 first_in_memory_seq, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		   name = excluded.name,
		   mode = excluded.mode,
		   summary = excluded.summary,
		   verbose_level = excluded.verbose_level,
		   model = excluded.model,
		   thinking_level = excluded.thinking_level,
		   folder = excluded.folder,
		   input_tokens = excluded.input_tokens,
		   output_tokens = excluded.output_tokens,
		   compaction_count = excluded.compaction_count,
		   first_in_memory_seq = excluded.first_in_memory_seq,
		   updated_at = excluded.updated_at`,
		meta.Key,
		meta.Name,
		meta.Mode,
		meta.Summary,
		meta.VerboseLevel,
		meta.Model,
		meta.ThinkingLevel,
		meta.Folder,
		meta.InputTokens,
		meta.OutputTokens,
		meta.CompactionCount,
		meta.FirstInMemorySeq,
		meta.CreatedAt.Format(time.RFC3339Nano),
		meta.UpdatedAt.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("upsert session %q: %w", meta.Key, err)
	}
	return nil
}

// GetSessionMeta returns the metadata for a session, or (nil, nil) if not found.
func (r *SessionRepo) GetSessionMeta(key string) (*SessionMeta, error) {
	var meta SessionMeta
	var createdAt, updatedAt string
	err := r.db.QueryRow(
		`SELECT key, name, mode, summary, verbose_level, model,
		 thinking_level, folder, input_tokens, output_tokens, compaction_count,
		 first_in_memory_seq, created_at, updated_at
		 FROM sessions WHERE key = ?`, key,
	).Scan(
		&meta.Key, &meta.Name, &meta.Mode, &meta.Summary,
		&meta.VerboseLevel, &meta.Model, &meta.ThinkingLevel, &meta.Folder,
		&meta.InputTokens, &meta.OutputTokens, &meta.CompactionCount,
		&meta.FirstInMemorySeq,
		&createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get session meta %q: %w", key, err)
	}
	meta.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	meta.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return &meta, nil
}

// ListSessionMeta returns metadata for all sessions, sorted by updated_at descending.
func (r *SessionRepo) ListSessionMeta() ([]SessionMeta, error) {
	rows, err := r.db.Query(
		`SELECT key, name, mode, summary, verbose_level, model,
		 thinking_level, folder, input_tokens, output_tokens, compaction_count,
		 first_in_memory_seq, created_at, updated_at
		 FROM sessions ORDER BY updated_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	metas := make([]SessionMeta, 0)
	for rows.Next() {
		var meta SessionMeta
		var createdAt, updatedAt string
		if err := rows.Scan(
			&meta.Key, &meta.Name, &meta.Mode, &meta.Summary,
			&meta.VerboseLevel, &meta.Model, &meta.ThinkingLevel, &meta.Folder,
			&meta.InputTokens, &meta.OutputTokens, &meta.CompactionCount,
			&meta.FirstInMemorySeq,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("list sessions scan: %w", err)
		}
		meta.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		meta.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		metas = append(metas, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions rows: %w", err)
	}
	return metas, nil
}

// ListSessionMetaByMode returns metadata for sessions with the given mode,
// sorted by updated_at descending. Empty mode matches "agent" sessions.
func (r *SessionRepo) ListSessionMetaByMode(mode string) ([]SessionMeta, error) {
	// Normalize: empty mode matches "agent"
	queryMode := mode
	if queryMode == "" {
		queryMode = "agent"
	}

	rows, err := r.db.Query(
		`SELECT key, name, mode, summary, verbose_level, model,
		 thinking_level, folder, input_tokens, output_tokens, compaction_count,
		 first_in_memory_seq, created_at, updated_at
		 FROM sessions WHERE mode = ? ORDER BY updated_at DESC`, queryMode,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions by mode: %w", err)
	}
	defer rows.Close()

	metas := make([]SessionMeta, 0)
	for rows.Next() {
		var meta SessionMeta
		var createdAt, updatedAt string
		if err := rows.Scan(
			&meta.Key, &meta.Name, &meta.Mode, &meta.Summary,
			&meta.VerboseLevel, &meta.Model, &meta.ThinkingLevel, &meta.Folder,
			&meta.InputTokens, &meta.OutputTokens, &meta.CompactionCount,
			&meta.FirstInMemorySeq,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("list sessions by mode scan: %w", err)
		}
		meta.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		meta.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		metas = append(metas, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions by mode rows: %w", err)
	}
	return metas, nil
}

// DeleteSession removes a session and all its messages (via FK cascade).
func (r *SessionRepo) DeleteSession(key string) error {
	if _, err := r.db.Exec(`DELETE FROM sessions WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete session %q: %w", key, err)
	}
	return nil
}

// SessionExists reports whether a session exists in the database.
func (r *SessionRepo) SessionExists(key string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE key = ?)`, key).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("session exists %q: %w", key, err)
	}
	return exists, nil
}

// MessageRow holds the data needed to insert a message row.
type MessageRow struct {
	Seq      int
	Role     string
	JSON     string
	Excluded bool
}

// ReplaceMessages atomically replaces all messages for a session inside a
// transaction. This is used by saveToSQLite to sync the full message list
// in a crash-safe way (the old delete-then-insert-outside-tx pattern could
// lose all messages if the process crashed between the DELETE and the last
// INSERT).
func (r *SessionRepo) ReplaceMessages(sessionKey string, messages []MessageRow) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx for replace messages %q: %w", sessionKey, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(
		`DELETE FROM session_messages WHERE session_key = ?`, sessionKey,
	); err != nil {
		return fmt.Errorf("delete old messages %q: %w", sessionKey, err)
	}

	stmt, err := tx.Prepare(
		`INSERT INTO session_messages(session_key, seq, role, message, excluded, created_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare insert messages %q: %w", sessionKey, err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339Nano)
	for _, m := range messages {
		exclInt := 0
		if m.Excluded {
			exclInt = 1
		}
		if _, err := stmt.Exec(sessionKey, m.Seq, m.Role, m.JSON, exclInt, now); err != nil {
			return fmt.Errorf("insert message %q seq=%d: %w", sessionKey, m.Seq, err)
		}
	}

	return tx.Commit()
}

// InsertMessage appends a message to the session. The seq number determines
// the ordering within the session. The messageJSON is the serialized
// providers.Message. The excluded flag marks messages excluded from context.
func (r *SessionRepo) InsertMessage(sessionKey string, seq int, role, messageJSON string, excluded bool) error {
	exclInt := 0
	if excluded {
		exclInt = 1
	}

	if _, err := r.db.Exec(
		`INSERT OR REPLACE INTO session_messages(session_key, seq, role, message, excluded, created_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		sessionKey,
		seq,
		role,
		messageJSON,
		exclInt,
		time.Now().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("insert message session=%q seq=%d: %w", sessionKey, seq, err)
	}
	return nil
}

// InsertMessages batch-inserts multiple messages in a single transaction.
// Used by the incremental save path to avoid per-message lock release.
func (r *SessionRepo) InsertMessages(sessionKey string, messages []MessageRow) error {
	if len(messages) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx for insert messages %q: %w", sessionKey, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	stmt, err := tx.Prepare(
		`INSERT OR REPLACE INTO session_messages(session_key, seq, role, message, excluded, created_at)
		 VALUES(?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare insert messages %q: %w", sessionKey, err)
	}
	defer stmt.Close()

	now := time.Now().Format(time.RFC3339Nano)
	for _, m := range messages {
		exclInt := 0
		if m.Excluded {
			exclInt = 1
		}
		if _, err := stmt.Exec(sessionKey, m.Seq, m.Role, m.JSON, exclInt, now); err != nil {
			return fmt.Errorf("insert message %q seq=%d: %w", sessionKey, m.Seq, err)
		}
	}
	return tx.Commit()
}

// UpdateMessage updates an existing message in-place (used for streaming).
func (r *SessionRepo) UpdateMessage(sessionKey string, seq int, role, messageJSON string, excluded bool) error {
	exclInt := 0
	if excluded {
		exclInt = 1
	}

	if _, err := r.db.Exec(
		`UPDATE session_messages SET role = ?, message = ?, excluded = ?
		 WHERE session_key = ? AND seq = ?`,
		role,
		messageJSON,
		exclInt,
		sessionKey,
		seq,
	); err != nil {
		return fmt.Errorf("update message session=%q seq=%d: %w", sessionKey, seq, err)
	}
	return nil
}

// UpdateMessages updates a batch of existing messages in-place in a single
// transaction (used for streaming finalization where tool_calls arrive after
// the partial content was already persisted).
func (r *SessionRepo) UpdateMessages(sessionKey string, messages []MessageRow) error {
	if len(messages) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx for update messages %q: %w", sessionKey, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	stmt, err := tx.Prepare(
		`UPDATE session_messages SET role = ?, message = ?, excluded = ?
		 WHERE session_key = ? AND seq = ?`,
	)
	if err != nil {
		return fmt.Errorf("prepare update messages %q: %w", sessionKey, err)
	}
	defer stmt.Close()

	for _, m := range messages {
		exclInt := 0
		if m.Excluded {
			exclInt = 1
		}
		if _, err := stmt.Exec(m.Role, m.JSON, exclInt, sessionKey, m.Seq); err != nil {
			return fmt.Errorf("update message %q seq=%d: %w", sessionKey, m.Seq, err)
		}
	}
	return tx.Commit()
}

// UpdateMessagesExcluded updates the excluded flag for a range of messages.
// Used by ExcludeOldMessagesFromContext.
func (r *SessionRepo) UpdateMessagesExcluded(sessionKey string, fromSeq, toSeq int, excluded bool) error {
	exclInt := 0
	if excluded {
		exclInt = 1
	}
	if _, err := r.db.Exec(
		`UPDATE session_messages SET excluded = ?
		 WHERE session_key = ? AND seq >= ? AND seq < ?`,
		exclInt, sessionKey, fromSeq, toSeq,
	); err != nil {
		return fmt.Errorf("update excluded session=%q seq=%d..%d: %w", sessionKey, fromSeq, toSeq, err)
	}
	return nil
}

// UpdateMessagesExcludedWithJSON updates both the excluded flag and the
// serialized JSON for a batch of messages in a single transaction. Used by
// the incremental save path when the excluded flag changes on existing messages,
// keeping the denormalized excluded column and the JSON blob in sync.
func (r *SessionRepo) UpdateMessagesExcludedWithJSON(sessionKey string, messages []MessageRow) error {
	if len(messages) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx for update excluded %q: %w", sessionKey, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	stmt, err := tx.Prepare(
		`UPDATE session_messages SET excluded = ?, message = ?
		 WHERE session_key = ? AND seq = ?`,
	)
	if err != nil {
		return fmt.Errorf("prepare update excluded %q: %w", sessionKey, err)
	}
	defer stmt.Close()

	for _, m := range messages {
		exclInt := 0
		if m.Excluded {
			exclInt = 1
		}
		if _, err := stmt.Exec(exclInt, m.JSON, sessionKey, m.Seq); err != nil {
			return fmt.Errorf("update excluded %q seq=%d: %w", sessionKey, m.Seq, err)
		}
	}
	return tx.Commit()
}

// DeleteMessagesFrom removes all messages with seq >= fromSeq.
// Used for TruncateHistory.
func (r *SessionRepo) DeleteMessagesFrom(sessionKey string, fromSeq int) error {
	if _, err := r.db.Exec(
		`DELETE FROM session_messages WHERE session_key = ? AND seq >= ?`,
		sessionKey, fromSeq,
	); err != nil {
		return fmt.Errorf("delete messages session=%q from_seq=%d: %w", sessionKey, fromSeq, err)
	}
	return nil
}

// DeleteLastMessage removes the message with the highest seq for a session.
// Returns the deleted seq, or -1 if no messages exist.
func (r *SessionRepo) DeleteLastMessage(sessionKey string) (int, error) {
	var seq int
	err := r.db.QueryRow(
		`SELECT seq FROM session_messages WHERE session_key = ?
		 ORDER BY seq DESC LIMIT 1`, sessionKey,
	).Scan(&seq)
	if err == sql.ErrNoRows {
		return -1, nil
	}
	if err != nil {
		return -1, fmt.Errorf("find last message session=%q: %w", sessionKey, err)
	}
	if _, err := r.db.Exec(
		`DELETE FROM session_messages WHERE session_key = ? AND seq = ?`,
		sessionKey, seq,
	); err != nil {
		return -1, fmt.Errorf("delete last message session=%q seq=%d: %w", sessionKey, seq, err)
	}
	return seq, nil
}

// UpdateFirstInMemorySeq persists the in-memory eviction boundary for a
// session without touching messages. Memory-only eviction/lazy-load call this
// to keep the boundary durable across restarts.
func (r *SessionRepo) UpdateFirstInMemorySeq(sessionKey string, seq int) error {
	if _, err := r.db.Exec(
		`UPDATE sessions SET first_in_memory_seq = ? WHERE key = ?`,
		seq, sessionKey,
	); err != nil {
		return fmt.Errorf("update first_in_memory_seq session=%q: %w", sessionKey, err)
	}
	return nil
}

// LoadMessages returns all messages as JSON strings for a session in seq order.
func (r *SessionRepo) LoadMessages(sessionKey string) ([]string, error) {
	rows, err := r.db.Query(
		`SELECT message FROM session_messages WHERE session_key = ?
		 ORDER BY seq ASC`, sessionKey,
	)
	if err != nil {
		return nil, fmt.Errorf("load messages session=%q: %w", sessionKey, err)
	}
	defer rows.Close()

	var messages []string
	for rows.Next() {
		var messageJSON string
		if err := rows.Scan(&messageJSON); err != nil {
			return nil, fmt.Errorf("load messages scan session=%q: %w", sessionKey, err)
		}
		messages = append(messages, messageJSON)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load messages rows session=%q: %w", sessionKey, err)
	}
	return messages, nil
}

// LoadMessagesBeforeSeq returns the JSON of all messages with seq strictly
// less than beforeSeq, ordered by seq ASC. Used to lazy-load evicted
// (excluded) messages that precede the first in-memory message.
func (r *SessionRepo) LoadMessagesBeforeSeq(sessionKey string, beforeSeq int) ([]string, error) {
	rows, err := r.db.Query(
		`SELECT message FROM session_messages WHERE session_key = ? AND seq < ?
		 ORDER BY seq ASC`, sessionKey, beforeSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("load messages before seq session=%q: %w", sessionKey, err)
	}
	defer rows.Close()

	var messages []string
	for rows.Next() {
		var messageJSON string
		if err := rows.Scan(&messageJSON); err != nil {
			return nil, fmt.Errorf("load messages before seq scan session=%q: %w", sessionKey, err)
		}
		messages = append(messages, messageJSON)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load messages before seq rows session=%q: %w", sessionKey, err)
	}
	return messages, nil
}

// LoadMessagesFromSeq returns the JSON of all messages with seq >= fromSeq,
// ordered by seq ASC. Used for cold-load to skip evicted rows
// (seq < first_in_memory_seq) so they are not re-inflated into RAM.
func (r *SessionRepo) LoadMessagesFromSeq(sessionKey string, fromSeq int) ([]string, error) {
	rows, err := r.db.Query(
		`SELECT message FROM session_messages WHERE session_key = ? AND seq >= ?
		 ORDER BY seq ASC`, sessionKey, fromSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("load messages from seq session=%q: %w", sessionKey, err)
	}
	defer rows.Close()

	var messages []string
	for rows.Next() {
		var messageJSON string
		if err := rows.Scan(&messageJSON); err != nil {
			return nil, fmt.Errorf("load messages from seq scan session=%q: %w", sessionKey, err)
		}
		messages = append(messages, messageJSON)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load messages from seq rows session=%q: %w", sessionKey, err)
	}
	return messages, nil
}

// MessageRowFull is a loaded message row including its seq and excluded flag.
type MessageRowFull struct {
	Seq      int
	JSON     string
	Excluded bool
}

// LoadMessagesWithSeq returns all messages for a session with their seq and
// excluded flag, ordered by seq ASC. Used to recover firstInMemorySeq after
// reload and for in-context-only cold loads.
func (r *SessionRepo) LoadMessagesWithSeq(sessionKey string) ([]MessageRowFull, error) {
	rows, err := r.db.Query(
		`SELECT seq, message, excluded FROM session_messages WHERE session_key = ?
		 ORDER BY seq ASC`, sessionKey,
	)
	if err != nil {
		return nil, fmt.Errorf("load messages with seq session=%q: %w", sessionKey, err)
	}
	defer rows.Close()

	var messages []MessageRowFull
	for rows.Next() {
		var m MessageRowFull
		var exclInt int
		if err := rows.Scan(&m.Seq, &m.JSON, &exclInt); err != nil {
			return nil, fmt.Errorf("load messages with seq scan session=%q: %w", sessionKey, err)
		}
		m.Excluded = exclInt != 0
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load messages with seq rows session=%q: %w", sessionKey, err)
	}
	return messages, nil
}

// CountExcludedMessages returns the number of messages marked excluded for a session.
func (r *SessionRepo) CountExcludedMessages(sessionKey string) (int, error) {
	var count int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM session_messages WHERE session_key = ? AND excluded = 1`,
		sessionKey,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count excluded messages session=%q: %w", sessionKey, err)
	}
	return count, nil
}

// MessageCount returns the number of messages for a session.
func (r *SessionRepo) MessageCount(sessionKey string) (int, error) {
	var count int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM session_messages WHERE session_key = ?`, sessionKey,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("message count session=%q: %w", sessionKey, err)
	}
	return count, nil
}

// MaxSeq returns the maximum seq number for a session, or -1 if no messages.
func (r *SessionRepo) MaxSeq(sessionKey string) (int, error) {
	var seq sql.NullInt64
	err := r.db.QueryRow(
		`SELECT MAX(seq) FROM session_messages WHERE session_key = ?`, sessionKey,
	).Scan(&seq)
	if err != nil {
		return -1, fmt.Errorf("max seq session=%q: %w", sessionKey, err)
	}
	if !seq.Valid {
		return -1, nil
	}
	return int(seq.Int64), nil
}

// PruneExcluded removes excluded messages beyond the keepCount limit.
// Returns the number of messages pruned.
func (r *SessionRepo) PruneExcluded(sessionKey string, keepCount int) (int, error) {
	// Get total count
	total, err := r.MessageCount(sessionKey)
	if err != nil {
		return 0, err
	}
	if total <= keepCount {
		return 0, nil
	}

	// Delete oldest excluded messages
	toDelete := total - keepCount
	result, err := r.db.Exec(
		`DELETE FROM session_messages WHERE session_key = ? AND excluded = 1
		 AND seq IN (
		   SELECT seq FROM session_messages WHERE session_key = ? AND excluded = 1
		   ORDER BY seq ASC LIMIT ?
		 )`,
		sessionKey, sessionKey, toDelete,
	)
	if err != nil {
		return 0, fmt.Errorf("prune excluded session=%q: %w", sessionKey, err)
	}
	affected, _ := result.RowsAffected()
	return int(affected), nil
}

// AllMessageCounts returns a map of session_key → message count for all
// sessions in a single query. This avoids N+1 queries when listing sessions
// for the WebUI sidebar/history.
func (r *SessionRepo) AllMessageCounts() (map[string]int, error) {
	rows, err := r.db.Query(
		`SELECT session_key, COUNT(*) FROM session_messages GROUP BY session_key`,
	)
	if err != nil {
		return nil, fmt.Errorf("all message counts: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, fmt.Errorf("all message counts scan: %w", err)
		}
		counts[key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("all message counts rows: %w", err)
	}
	return counts, nil
}

// LoadMessagesBeforeLimited returns up to limit message rows with seq <
// beforeSeq, ordered by seq DESC (newest first). Used by the session manager
// to page through messages that are not resident in memory.
func (r *SessionRepo) LoadMessagesBeforeLimited(sessionKey string, beforeSeq, limit int) ([]MessageRowFull, error) {
	rows, err := r.db.Query(
		`SELECT seq, message, excluded FROM session_messages
		 WHERE session_key = ? AND seq < ?
		 ORDER BY seq DESC LIMIT ?`, sessionKey, beforeSeq, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("load messages before seq session=%q: %w", sessionKey, err)
	}
	defer rows.Close()

	var out []MessageRowFull
	for rows.Next() {
		var row MessageRowFull
		if err := rows.Scan(&row.Seq, &row.JSON, &row.Excluded); err != nil {
			return nil, fmt.Errorf("load messages before seq scan session=%q: %w", sessionKey, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load messages before seq rows session=%q: %w", sessionKey, err)
	}
	return out, nil
}

// LoadMessagesBetweenLimited returns up to limit message rows with
// afterSeq < seq < beforeSeq (beforeSeq < 0 means unbounded above), ordered
// by seq ASC (oldest first).
func (r *SessionRepo) LoadMessagesBetweenLimited(sessionKey string, afterSeq, beforeSeq, limit int) ([]MessageRowFull, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if beforeSeq >= 0 {
		rows, err = r.db.Query(
			`SELECT seq, message, excluded FROM session_messages
			 WHERE session_key = ? AND seq > ? AND seq < ?
			 ORDER BY seq ASC LIMIT ?`, sessionKey, afterSeq, beforeSeq, limit)
	} else {
		rows, err = r.db.Query(
			`SELECT seq, message, excluded FROM session_messages
			 WHERE session_key = ? AND seq > ?
			 ORDER BY seq ASC LIMIT ?`, sessionKey, afterSeq, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("load messages between seq session=%q: %w", sessionKey, err)
	}
	defer rows.Close()

	var out []MessageRowFull
	for rows.Next() {
		var row MessageRowFull
		if err := rows.Scan(&row.Seq, &row.JSON, &row.Excluded); err != nil {
			return nil, fmt.Errorf("load messages between seq scan session=%q: %w", sessionKey, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load messages between seq rows session=%q: %w", sessionKey, err)
	}
	return out, nil
}

// CountMessagesBefore returns the number of persisted rows with seq <
// beforeSeq (beforeSeq < 0 counts everything).
func (r *SessionRepo) CountMessagesBefore(sessionKey string, beforeSeq int) (int, error) {
	var count int
	var err error
	if beforeSeq >= 0 {
		err = r.db.QueryRow(
			`SELECT COUNT(*) FROM session_messages WHERE session_key = ? AND seq < ?`,
			sessionKey, beforeSeq).Scan(&count)
	} else {
		err = r.db.QueryRow(
			`SELECT COUNT(*) FROM session_messages WHERE session_key = ?`,
			sessionKey).Scan(&count)
	}
	if err != nil {
		return 0, fmt.Errorf("count messages before seq session=%q: %w", sessionKey, err)
	}
	return count, nil
}

// LoadMessagesFullBeforeSeq returns seq + JSON + excluded flag of all
// messages with seq strictly less than beforeSeq, ordered by seq ASC. Unlike
// LoadMessagesBeforeSeq it preserves the persisted excluded state, which a
// full rewrite must carry over: the evicted prefix can contain non-excluded
// rows (e.g. the original request folded into a summary but never excluded),
// and hardcoding excluded=true would silently drop them from future context
// rebuilds that filter on the flag.
func (r *SessionRepo) LoadMessagesFullBeforeSeq(sessionKey string, beforeSeq int) ([]MessageRowFull, error) {
	rows, err := r.db.Query(
		`SELECT seq, message, excluded FROM session_messages WHERE session_key = ? AND seq < ?
		 ORDER BY seq ASC`, sessionKey, beforeSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("load full messages before seq session=%q: %w", sessionKey, err)
	}
	defer rows.Close()

	var out []MessageRowFull
	for rows.Next() {
		var row MessageRowFull
		if err := rows.Scan(&row.Seq, &row.JSON, &row.Excluded); err != nil {
			return nil, fmt.Errorf("load full messages before seq scan session=%q: %w", sessionKey, err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load full messages before seq rows session=%q: %w", sessionKey, err)
	}
	return out, nil
}
