package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SpoolDirection values for the spool table's direction column.
const (
	// SpoolInbound marks a message received from a channel that still
	// has to be processed by the agent loop.
	SpoolInbound = "inbound"
	// SpoolOutbound marks a reply produced by the agent that still has
	// to be delivered to a channel.
	SpoolOutbound = "outbound"
)

// ErrUnknownDirection is returned for a spool direction that is neither
// SpoolInbound nor SpoolOutbound. The schema also enforces it with a
// CHECK constraint; validating in Go gives callers a clear error instead
// of a raw SQLite constraint message.
var ErrUnknownDirection = errors.New("store: unknown spool direction")

// spoolTimeLayout is the timestamp layout for every column in the spool
// tables. It is RFC3339 in UTC but with a mandatory nine-digit fraction:
// unlike time.RFC3339Nano it never trims trailing zeros, so all stored
// values share one fixed width.
//
// This matters because ReclaimStale and PruneProcessed compare the stored
// TEXT against a formatted cutoff directly in SQL, and SQLite compares
// strings byte-wise. Equal width is what makes byte order equal
// chronological order; with RFC3339Nano's trimming, a whole-second
// "...:00Z" would sort *after* a fractional "...:00.5Z" ('Z' > '.') and
// staleness would be judged wrongly in both directions. Values written in
// this layout still parse with time.RFC3339Nano.
const spoolTimeLayout = "2006-01-02T15:04:05.000000000Z07:00"

// SpoolItem is one row of the spool table.
type SpoolItem struct {
	ID         int64
	Direction  string
	Channel    string
	ChatID     string
	SessionKey string
	MsgID      string
	Payload    string // opaque JSON, never interpreted by this package
	Attempts   int
	CreatedAt  time.Time
}

// SpoolStats reports queue depth for observability. Pending counts rows
// nobody has claimed; Claimed counts rows handed out to an instance and
// still in flight, including rows orphaned by a crashed instance until
// ReclaimStale returns them to the pending set.
type SpoolStats struct {
	PendingInbound  int
	PendingOutbound int
	ClaimedInbound  int
	ClaimedOutbound int
	ProcessedCount  int
}

// SpoolRepo provides durable at-least-once queueing for chat messages
// plus the dedupe ledger that upgrades that guarantee to exactly-once
// processing.
//
// The lifecycle of a row is: Enqueue -> ClaimBatch (a worker takes
// ownership) -> Complete (delivered, row deleted). A worker that dies
// mid-flight leaves its claims behind: ReleaseClaims returns them on
// graceful shutdown, ReclaimStale returns them after a timeout. Because
// delivery can therefore happen twice, consumers consult WasProcessed /
// MarkProcessed before acting on an inbound message.
//
// Timestamps are TEXT in the fixed-width UTC layout described on
// spoolTimeLayout, as everywhere else in this schema. Time-dependent
// methods take now from the caller so behaviour is deterministic under test.
type SpoolRepo struct {
	db *sql.DB
}

// Enqueue appends a message to the spool and returns the new row id. The
// payload is opaque to this package. The row starts unclaimed with zero
// attempts. An unknown direction returns ErrUnknownDirection.
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

// ClaimBatch atomically claims up to limit unclaimed rows of direction,
// oldest id first (FIFO), on behalf of instanceID. Claimed rows are
// invisible to later claims, by this or any other instance, until they
// are completed, released or reclaimed.
//
// now is recorded as claimed_at and is what ReclaimStale compares
// against; it is converted to UTC before being stored, so any zone is
// safe. A limit of zero or less claims nothing and returns an empty slice
// without error.
//
// Contract on the returned slice: it holds AT MOST limit items, and the
// claim (the UPDATE) and the read-back (the SELECT) are separate
// statements — the read happens after the commit, so in a hypothetical
// multi-instance deployment another holder could Complete (delete) or
// ReleaseClaims one of the just-claimed rows in that window, making the
// returned slice a subset of the rows this call actually claimed. Callers
// must therefore treat the result as "the items I now own that are still
// present", never as an exact mirror of the claim. Today the gateway is
// single-instance and the spool has no production consumer yet (Phase 2),
// so the window is theoretical; this documents the guarantee so a future
// replay handler does not assume more than it gets.
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

	// Pick the next FIFO candidates. The partial index on claimed_by = ''
	// keeps this scan cheap no matter how many rows are in flight.
	var ids []int64
	rows, err := tx.Query(
		`SELECT id FROM spool WHERE direction = ? AND claimed_by = '' ORDER BY id LIMIT ?`,
		direction, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("store: claim spool %s: select ids: %w", direction, err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: claim spool %s: select ids: scan: %w", direction, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: claim spool %s: select ids: rows: %w", direction, err)
	}
	rows.Close()

	if len(ids) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("store: claim spool %s: commit: %w", direction, err)
		}
		return []SpoolItem{}, nil
	}

	// Take ownership of exactly those ids. The pool hands out a single
	// connection, so no other writer can slip in between the SELECT above
	// and this UPDATE.
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

	// Read the claimed rows back, oldest first.
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

// Complete deletes the given spool rows: their messages were delivered,
// so the queue no longer has to carry them across a restart. Completing
// an unknown id is not an error; an empty or nil slice is a no-op.
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

// ReleaseClaims un-claims every row currently held by instanceID so a
// graceful shutdown hands its work back to the successor immediately
// instead of waiting for ReclaimStale to notice stale claims. It returns
// the number of rows released.
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

// ReclaimStale un-claims rows whose claim is older than now - olderThan,
// i.e. rows left behind by a claimer that crashed. It returns the number
// of rows handed back to the pending set.
//
// claimed_at is TEXT in the fixed-width UTC layout spoolTimeLayout, so its
// byte order equals chronological order and it can be compared against the
// formatted cutoff directly in SQL - no datetime() conversion and no
// Go-side scan needed. Both sides are normalised to UTC, so the zone of
// the caller's now is irrelevant.
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

// IncAttempt records one more delivery attempt for a row, letting the
// caller apply a retry ceiling before parking a poison message.
func (r *SpoolRepo) IncAttempt(id int64) error {
	if _, err := r.db.Exec(`UPDATE spool SET attempts = attempts + 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: increment spool attempts %d: %w", id, err)
	}
	return nil
}

// WasProcessed reports whether the ledger already holds (channel, msgID),
// meaning a previous consumer - possibly one before a restart - already
// handled the message.
//
// An empty msgID means the channel supplied no dedupe key, so nothing can
// be deduped: the answer is false and the database is not touched.
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

// MarkProcessed adds (channel, msgID) to the dedupe ledger. It is
// idempotent: marking a pair that is already there is not an error. An
// empty msgID is a no-op, mirroring WasProcessed.
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

// PruneProcessed deletes ledger entries older than now - olderThan and
// returns how many were removed. The ledger only has to cover the replay
// window, so older entries are dropped to keep it bounded.
//
// The cutoff comparison relies on the same fixed-width lexicographic-order
// property of spoolTimeLayout described on ReclaimStale.
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

// Stats returns the queue depth per direction and claim state plus the
// size of the dedupe ledger.
func (r *SpoolRepo) Stats() (SpoolStats, error) {
	// COALESCE because SUM over no rows is NULL, which cannot be scanned
	// into an int.
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

// PendingBySession returns the number of unclaimed rows per session_key
// for one direction. The resume/pump layer uses it to learn which
// sessions still have outstanding work after a restart. Rows without a
// session key are grouped under "".
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

// queryItems runs a SELECT over the spool table and materialises the
// resulting rows. The returned slice is never nil.
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

// validSpoolDirection reports whether direction is one of the two
// spool directions.
func validSpoolDirection(direction string) bool {
	return direction == SpoolInbound || direction == SpoolOutbound
}

// spoolIDArgs builds the "?,?,?" placeholder list for an IN clause and
// the matching argument slice. Values are always passed as parameters,
// never interpolated into the SQL.
func spoolIDArgs(ids []int64) (string, []any) {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}

// parseSpoolTime converts a stored RFC3339Nano timestamp into a time.
// A value that cannot be parsed yields the zero time rather than an
// error: the row itself is still deliverable, and failing a claim over a
// cosmetic timestamp would drop live work.
func parseSpoolTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}
