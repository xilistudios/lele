package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type SessionRepo struct {
	db *sql.DB
}

type SessionMeta struct {
	Key              string
	Name             string
	Mode             string
	Summary          string
	VerboseLevel     string
	Model            string
	ThinkingLevel    string
	Folder           string
	AgentID          string
	SubagentStatus   string
	InputTokens      int
	OutputTokens     int
	CompactionCount  int
	FirstInMemorySeq int
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (r *SessionRepo) UpsertSession(meta SessionMeta) error {
	if _, err := r.db.Exec(
		`INSERT INTO sessions(key, name, mode, summary, verbose_level, model,
		 thinking_level, folder, agent_id, subagent_status, input_tokens, output_tokens, compaction_count,
		 first_in_memory_seq, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		   name = excluded.name,
		   mode = excluded.mode,
		   summary = excluded.summary,
		   verbose_level = excluded.verbose_level,
		   model = excluded.model,
		   thinking_level = excluded.thinking_level,
		   folder = excluded.folder,
		   agent_id = excluded.agent_id,
		   subagent_status = excluded.subagent_status,
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
		meta.AgentID,
		meta.SubagentStatus,
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

func (r *SessionRepo) GetSessionMeta(key string) (*SessionMeta, error) {
	var meta SessionMeta
	var createdAt, updatedAt string
	err := r.db.QueryRow(
		`SELECT key, name, mode, summary, verbose_level, model,
		 thinking_level, folder, agent_id, subagent_status, input_tokens, output_tokens, compaction_count,
		 first_in_memory_seq, created_at, updated_at
		 FROM sessions WHERE key = ?`, key,
	).Scan(
		&meta.Key, &meta.Name, &meta.Mode, &meta.Summary,
		&meta.VerboseLevel, &meta.Model, &meta.ThinkingLevel, &meta.Folder, &meta.AgentID,
		&meta.SubagentStatus,
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

func (r *SessionRepo) ListSessionMeta() ([]SessionMeta, error) {
	rows, err := r.db.Query(
		`SELECT key, name, mode, summary, verbose_level, model,
		 thinking_level, folder, agent_id, subagent_status, input_tokens, output_tokens, compaction_count,
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
			&meta.VerboseLevel, &meta.Model, &meta.ThinkingLevel, &meta.Folder, &meta.AgentID,
			&meta.SubagentStatus,
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

func (r *SessionRepo) ListSessionMetaByMode(mode string) ([]SessionMeta, error) {
	queryMode := mode
	if queryMode == "" {
		queryMode = "agent"
	}

	rows, err := r.db.Query(
		`SELECT key, name, mode, summary, verbose_level, model,
		 thinking_level, folder, agent_id, subagent_status, input_tokens, output_tokens, compaction_count,
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
			&meta.VerboseLevel, &meta.Model, &meta.ThinkingLevel, &meta.Folder, &meta.AgentID,
			&meta.SubagentStatus,
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

func (r *SessionRepo) DeleteSession(key string) error {
	if _, err := r.db.Exec(`DELETE FROM sessions WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete session %q: %w", key, err)
	}
	return nil
}

func (r *SessionRepo) SessionExists(key string) (bool, error) {
	var exists bool
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE key = ?)`, key).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("session exists %q: %w", key, err)
	}
	return exists, nil
}

type MessageRow struct {
	Seq      int
	Role     string
	JSON     string
	Excluded bool
}

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

func (r *SessionRepo) DeleteMessagesFrom(sessionKey string, fromSeq int) error {
	if _, err := r.db.Exec(
		`DELETE FROM session_messages WHERE session_key = ? AND seq >= ?`,
		sessionKey, fromSeq,
	); err != nil {
		return fmt.Errorf("delete messages session=%q from_seq=%d: %w", sessionKey, fromSeq, err)
	}
	return nil
}

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

func (r *SessionRepo) UpdateFirstInMemorySeq(sessionKey string, seq int) error {
	if _, err := r.db.Exec(
		`UPDATE sessions SET first_in_memory_seq = ? WHERE key = ?`,
		seq, sessionKey,
	); err != nil {
		return fmt.Errorf("update first_in_memory_seq session=%q: %w", sessionKey, err)
	}
	return nil
}

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

type MessageRowFull struct {
	Seq      int
	JSON     string
	Excluded bool
}

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

func (r *SessionRepo) PruneExcluded(sessionKey string, keepCount int) (int, error) {
	total, err := r.MessageCount(sessionKey)
	if err != nil {
		return 0, err
	}
	if total <= keepCount {
		return 0, nil
	}

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

func (r *SessionRepo) SessionKeysWithMessages() (map[string]bool, error) {
	rows, err := r.db.Query(`
		SELECT s.key FROM sessions s
		 WHERE EXISTS (SELECT 1 FROM session_messages m WHERE m.session_key = s.key)`)
	if err != nil {
		return nil, fmt.Errorf("session keys with messages: %w", err)
	}
	defer rows.Close()

	keys := make(map[string]bool)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("session keys with messages scan: %w", err)
		}
		keys[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("session keys with messages rows: %w", err)
	}
	return keys, nil
}

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

const sessionListingStatsChunk = 200

type SessionListingStats struct {
	Summary           string
	AssistantCount    int
	LastAssistantJSON string
}

func (r *SessionRepo) SessionListingStats(keys []string) (map[string]SessionListingStats, error) {
	out := make(map[string]SessionListingStats, len(keys))
	for start := 0; start < len(keys); start += sessionListingStatsChunk {
		end := start + sessionListingStatsChunk
		if end > len(keys) {
			end = len(keys)
		}
		if err := r.sessionListingStatsChunk(out, keys[start:end]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *SessionRepo) sessionListingStatsChunk(out map[string]SessionListingStats, keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(keys)), ",")
	args := make([]any, 0, len(keys))
	for _, key := range keys {
		args = append(args, key)
	}

	rows, err := r.db.Query(`
		SELECT s.key, s.summary,
		       (SELECT COUNT(*) FROM session_messages m
		         WHERE m.session_key = s.key
		           AND m.role = 'assistant'
		           AND (s.first_in_memory_seq > 0 OR m.excluded = 0)
		           AND m.seq >= s.first_in_memory_seq
		           AND json_valid(m.message)),
		       COALESCE((SELECT m.message FROM session_messages m
		          WHERE m.session_key = s.key
		            AND m.role = 'assistant'
		            AND (s.first_in_memory_seq > 0 OR m.excluded = 0)
		            AND m.seq >= s.first_in_memory_seq
		            AND json_valid(m.message)
		          ORDER BY m.seq DESC LIMIT 1), '')
		  FROM sessions s
		 WHERE s.key IN (`+placeholders+`)`, args...)
	if err != nil {
		return fmt.Errorf("session listing stats: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			key  string
			stat SessionListingStats
		)
		if err := rows.Scan(&key, &stat.Summary, &stat.AssistantCount, &stat.LastAssistantJSON); err != nil {
			return fmt.Errorf("session listing stats scan: %w", err)
		}
		out[key] = stat
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("session listing stats rows: %w", err)
	}
	return nil
}

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
