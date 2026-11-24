package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SpoolInbound  = "inbound"
	SpoolOutbound = "outbound"
)

var ErrUnknownDirection = errors.New("store: unknown spool direction")

const spoolTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

type SpoolItem struct {
	ID         int64
	Direction  string
	Channel    string
	ChatID     string
	SessionKey string
	MsgID      string
	Payload    string
	Attempts   int
	CreatedAt  time.Time
}

type SpoolStats struct {
	PendingInbound  int
	PendingOutbound int
	ClaimedInbound  int
	ClaimedOutbound int
	ProcessedCount  int
}

type SpoolRepo struct {
	db *sql.DB
}

func (r *SpoolRepo) Enqueue(direction, channel, chatID, sessionKey, msgID, payload string) (int64, error) {
	if !validSpoolDirection(direction) {
		return 0, fmt.Errorf("%w %q", ErrUnknownDirection, direction)
	}

	now := time.Now().UTC().Format(spoolTimeLayout)
	res, err := r.db.Exec(
		`INSERT INTO spool(direction, channel, chat_id, session_key, msg_id,
		 payload, attempts, claimed_by, claimed_at, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, 0, '', '', ?)`,
		direction, channel, chatID, sessionKey, msgID, payload, now,
	)
	if err != nil {
		return 0, fmt.Errorf("store: enqueue spool %s session %q: %w", direction, sessionKey, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: enqueue spool %s session %q: last insert id: %w", direction, sessionKey, err)
	}
	return id, nil
}

func (r *SpoolRepo) EnqueueClaimed(direction, channel, chatID, sessionKey, msgID, payload, claimedBy string, now time.Time) (int64, error) {
	if !validSpoolDirection(direction) {
		return 0, fmt.Errorf("%w %q", ErrUnknownDirection, direction)
	}

	stamp := now.UTC().Format(spoolTimeLayout)
	res, err := r.db.Exec(
		`INSERT INTO spool(direction, channel, chat_id, session_key, msg_id,
		 payload, attempts, claimed_by, claimed_at, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, 0, ?, ?, ?)`,
		direction, channel, chatID, sessionKey, msgID, payload, claimedBy, stamp, stamp,
	)
	if err != nil {
		return 0, fmt.Errorf("store: enqueue claimed spool %s session %q: %w", direction, sessionKey, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: enqueue claimed spool %s session %q: last insert id: %w", direction, sessionKey, err)
	}
	return id, nil
}

func (r *SpoolRepo) ClaimBatch(direction string, limit int, instanceID string, now time.Time) ([]SpoolItem, error) {
	if !validSpoolDirection(direction) {
		return nil, fmt.Errorf("%w %q", ErrUnknownDirection, direction)
	}
	if limit <= 0 {
		return []SpoolItem{}, nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("store: claim spool %s: begin tx: %w", direction, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	var ids []int64
	err = func() error {
		rows, err := tx.Query(
			`SELECT id FROM spool
			 WHERE direction = ? AND claimed_by = ''
			   AND (next_attempt_at = '' OR next_attempt_at <= ?)
			 ORDER BY id LIMIT ?`,
			direction, now.UTC().Format(spoolTimeLayout), limit,
		)
		if err != nil {
			return fmt.Errorf("store: claim spool %s: select ids: %w", direction, err)
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("store: claim spool %s: select ids: scan: %w", direction, err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("store: claim spool %s: select ids: rows: %w", direction, err)
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("store: claim spool %s: commit: %w", direction, err)
		}
		return []SpoolItem{}, nil
	}

	placeholders, idArgs := spoolIDArgs(ids)
	claimArgs := make([]any, 0, len(idArgs)+2)
	claimArgs = append(claimArgs, instanceID, now.UTC().Format(spoolTimeLayout))
	claimArgs = append(claimArgs, idArgs...)

	if _, err := tx.Exec(
		fmt.Sprintf(
			`UPDATE spool SET claimed_by = ?, claimed_at = ? WHERE id IN (%s)`,
			placeholders,
		),
		claimArgs...,
	); err != nil {
		return nil, fmt.Errorf("store: claim spool %s: update: %w", direction, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: claim spool %s: commit: %w", direction, err)
	}

	items, err := r.queryItems(
		fmt.Sprintf(
			`SELECT id, direction, channel, chat_id, session_key, msg_id, payload, attempts, created_at
			 FROM spool WHERE id IN (%s) ORDER BY id`,
			placeholders,
		),
		idArgs...,
	)
	if err != nil {
		return nil, fmt.Errorf("store: claim spool %s: %w", direction, err)
	}
	return items, nil
}

func (r *SpoolRepo) Complete(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}

	placeholders, args := spoolIDArgs(ids)
	if _, err := r.db.Exec(
		fmt.Sprintf(`DELETE FROM spool WHERE id IN (%s)`, placeholders),
		args...,
	); err != nil {
		return fmt.Errorf("store: complete spool rows: %w", err)
	}
	return nil
}

func (r *SpoolRepo) ReleaseClaims(instanceID string) (int, error) {
	res, err := r.db.Exec(
		`UPDATE spool SET claimed_by = '', claimed_at = '' WHERE claimed_by = ?`,
		instanceID,
	)
	if err != nil {
		return 0, fmt.Errorf("store: release spool claims %q: %w", instanceID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: release spool claims %q: rows affected: %w", instanceID, err)
	}
	return int(affected), nil
}

func (r *SpoolRepo) ReleaseIDs(ids []int64, instanceID string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	placeholders, idArgs := spoolIDArgs(ids)
	args := make([]any, 0, len(idArgs)+1)
	args = append(args, instanceID)
	args = append(args, idArgs...)

	res, err := r.db.Exec(
		fmt.Sprintf(
			`UPDATE spool SET claimed_by = '', claimed_at = ''
			 WHERE claimed_by = ? AND id IN (%s)`,
			placeholders,
		),
		args...,
	)
	if err != nil {
		return 0, fmt.Errorf("store: release spool ids for %q: %w", instanceID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: release spool ids for %q: rows affected: %w", instanceID, err)
	}
	return int(affected), nil
}

func (r *SpoolRepo) ReclaimStale(olderThan time.Duration, now time.Time) (int, error) {
	cutoff := now.UTC().Add(-olderThan).Format(spoolTimeLayout)
	res, err := r.db.Exec(
		`UPDATE spool SET claimed_by = '', claimed_at = ''
		 WHERE claimed_by <> '' AND claimed_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("store: reclaim stale spool rows: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: reclaim stale spool rows: rows affected: %w", err)
	}
	return int(affected), nil
}

func (r *SpoolRepo) IncAttempt(id int64) error {
	if _, err := r.db.Exec(`UPDATE spool SET attempts = attempts + 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: increment spool attempts %d: %w", id, err)
	}
	return nil
}

func (r *SpoolRepo) IncAttemptWithBackoff(id int64, nextAt time.Time) error {
	if _, err := r.db.Exec(
		`UPDATE spool SET attempts = attempts + 1, next_attempt_at = ? WHERE id = ?`,
		nextAt.UTC().Format(spoolTimeLayout), id,
	); err != nil {
		return fmt.Errorf("store: increment spool attempts %d with backoff: %w", id, err)
	}
	return nil
}

func (r *SpoolRepo) DeadLetter(ids []int64, reason string, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("store: dead-letter spool rows: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	placeholders, args := spoolIDArgs(ids)
	deadAt := now.UTC().Format(spoolTimeLayout)
	insertArgs := make([]any, 0, len(args)+2)
	insertArgs = append(insertArgs, deadAt, reason)
	insertArgs = append(insertArgs, args...)

	if _, err := tx.Exec(
		fmt.Sprintf(
			`INSERT INTO spool_dead_letter(id, direction, channel, chat_id, session_key, msg_id,
			 payload, attempts, created_at, dead_at, reason)
			 SELECT id, direction, channel, chat_id, session_key, msg_id,
			        payload, attempts, created_at, ?, ?
			 FROM spool WHERE id IN (%s)`,
			placeholders,
		),
		insertArgs...,
	); err != nil {
		return fmt.Errorf("store: dead-letter spool rows: insert: %w", err)
	}

	if _, err := tx.Exec(
		fmt.Sprintf(`DELETE FROM spool WHERE id IN (%s)`, placeholders),
		args...,
	); err != nil {
		return fmt.Errorf("store: dead-letter spool rows: delete: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: dead-letter spool rows: commit: %w", err)
	}
	return nil
}

func (r *SpoolRepo) PruneDeadLetter(olderThan time.Duration, now time.Time) (int, error) {
	cutoff := now.UTC().Add(-olderThan).Format(spoolTimeLayout)
	res, err := r.db.Exec(`DELETE FROM spool_dead_letter WHERE dead_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: prune dead-letter rows: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune dead-letter rows: rows affected: %w", err)
	}
	return int(affected), nil
}

func (r *SpoolRepo) WasProcessed(channel, msgID string) (bool, error) {
	if msgID == "" {
		return false, nil
	}

	var one int
	err := r.db.QueryRow(
		`SELECT 1 FROM processed_messages WHERE channel = ? AND msg_id = ? LIMIT 1`,
		channel, msgID,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: was processed %s/%q: %w", channel, msgID, err)
	}
	return true, nil
}

func (r *SpoolRepo) MarkProcessed(channel, msgID string) error {
	if msgID == "" {
		return nil
	}

	now := time.Now().UTC().Format(spoolTimeLayout)
	if _, err := r.db.Exec(
		`INSERT INTO processed_messages(channel, msg_id, processed_at) VALUES(?, ?, ?)
		 ON CONFLICT(channel, msg_id) DO NOTHING`,
		channel, msgID, now,
	); err != nil {
		return fmt.Errorf("store: mark processed %s/%q: %w", channel, msgID, err)
	}
	return nil
}

func (r *SpoolRepo) PruneProcessed(olderThan time.Duration, now time.Time) (int, error) {
	cutoff := now.UTC().Add(-olderThan).Format(spoolTimeLayout)
	res, err := r.db.Exec(`DELETE FROM processed_messages WHERE processed_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: prune processed messages: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: prune processed messages: rows affected: %w", err)
	}
	return int(affected), nil
}

func (r *SpoolRepo) Stats() (SpoolStats, error) {
	var stats SpoolStats
	err := r.db.QueryRow(
		`SELECT
		   COALESCE(SUM(CASE WHEN direction = 'inbound'  AND claimed_by = ''  THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN direction = 'outbound' AND claimed_by = ''  THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN direction = 'inbound'  AND claimed_by <> '' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN direction = 'outbound' AND claimed_by <> '' THEN 1 ELSE 0 END), 0)
		 FROM spool`,
	).Scan(&stats.PendingInbound, &stats.PendingOutbound, &stats.ClaimedInbound, &stats.ClaimedOutbound)
	if err != nil {
		return SpoolStats{}, fmt.Errorf("store: spool stats: %w", err)
	}

	if err := r.db.QueryRow(`SELECT COUNT(*) FROM processed_messages`).Scan(&stats.ProcessedCount); err != nil {
		return SpoolStats{}, fmt.Errorf("store: spool stats: processed count: %w", err)
	}
	return stats, nil
}

func (r *SpoolRepo) PendingBySession(direction string) (map[string]int, error) {
	if !validSpoolDirection(direction) {
		return nil, fmt.Errorf("%w %q", ErrUnknownDirection, direction)
	}

	rows, err := r.db.Query(
		`SELECT session_key, COUNT(*) FROM spool
		 WHERE direction = ? AND claimed_by = ''
		 GROUP BY session_key`,
		direction,
	)
	if err != nil {
		return nil, fmt.Errorf("store: spool pending by session %s: %w", direction, err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, fmt.Errorf("store: spool pending by session %s: scan: %w", direction, err)
		}
		counts[key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: spool pending by session %s: rows: %w", direction, err)
	}
	return counts, nil
}

func (r *SpoolRepo) queryItems(query string, args ...any) ([]SpoolItem, error) {
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list spool items: %w", err)
	}
	defer rows.Close()

	items := []SpoolItem{}
	for rows.Next() {
		var item SpoolItem
		var createdAt string
		if err := rows.Scan(
			&item.ID, &item.Direction, &item.Channel, &item.ChatID,
			&item.SessionKey, &item.MsgID, &item.Payload, &item.Attempts, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("store: list spool items: scan: %w", err)
		}
		item.CreatedAt = parseSpoolTime(createdAt)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list spool items: rows: %w", err)
	}
	return items, nil
}

func validSpoolDirection(direction string) bool {
	return direction == SpoolInbound || direction == SpoolOutbound
}

func spoolIDArgs(ids []int64) (string, []any) {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

func parseSpoolTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
