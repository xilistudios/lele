package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

// CronJobRow is a flat, domain-agnostic representation of a row in the
// cron_jobs table. JSON payloads (Schedule, Payload, State) are treated
// as opaque strings so this package never imports domain packages.
type CronJobRow struct {
	ID          string
	Name        string
	Enabled     bool
	Schedule    string // opaque JSON
	Payload     string // opaque JSON
	State       string // opaque JSON
	Scope       string
	SessionKey  string
	CreatedAtMS int64
	UpdatedAtMS int64
}

// CronRepo provides CRUD operations on the cron_jobs table. The
// created_at and updated_at columns are TEXT, storing epoch milliseconds
// as decimal strings (via strconv.FormatInt) to preserve the int64
// semantics of the domain layer.
type CronRepo struct {
	db *sql.DB
}

// ListCronJobs returns all cron jobs ordered by creation time. The
// returned slice is never nil.
func (r *CronRepo) ListCronJobs() ([]CronJobRow, error) {
	rows, err := r.db.Query(
		`SELECT id, name, enabled, schedule, payload, state, scope, session_key, created_at, updated_at FROM cron_jobs ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list cron jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]CronJobRow, 0)
	for rows.Next() {
		var row CronJobRow
		var enabledInt int
		var createdAt, updatedAt string
		if err := rows.Scan(
			&row.ID, &row.Name, &enabledInt, &row.Schedule,
			&row.Payload, &row.State, &row.Scope, &row.SessionKey,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("store: list cron jobs: scan: %w", err)
		}
		row.Enabled = enabledInt != 0
		row.CreatedAtMS = parseEpochMillis(createdAt)
		row.UpdatedAtMS = parseEpochMillis(updatedAt)
		jobs = append(jobs, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list cron jobs: rows: %w", err)
	}
	return jobs, nil
}

// GetCronJob returns the cron job with the given id. If no row exists
// the found return is false with a nil error.
func (r *CronRepo) GetCronJob(id string) (*CronJobRow, bool, error) {
	var row CronJobRow
	var enabledInt int
	var createdAt, updatedAt string
	err := r.db.QueryRow(
		`SELECT id, name, enabled, schedule, payload, state, scope, session_key, created_at, updated_at FROM cron_jobs WHERE id = ?`, id,
	).Scan(
		&row.ID, &row.Name, &enabledInt, &row.Schedule,
		&row.Payload, &row.State, &row.Scope, &row.SessionKey,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: get cron job %q: %w", id, err)
	}
	row.Enabled = enabledInt != 0
	row.CreatedAtMS = parseEpochMillis(createdAt)
	row.UpdatedAtMS = parseEpochMillis(updatedAt)
	return &row, true, nil
}

// UpsertCronJob inserts or replaces the given cron job row.
func (r *CronRepo) UpsertCronJob(row *CronJobRow) error {
	enabledInt := 0
	if row.Enabled {
		enabledInt = 1
	}
	_, err := r.db.Exec(
		`INSERT INTO cron_jobs(id, name, enabled, schedule, payload, state, scope, session_key, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   name = excluded.name, enabled = excluded.enabled,
		   schedule = excluded.schedule, payload = excluded.payload,
		   state = excluded.state, scope = excluded.scope,
		   session_key = excluded.session_key,
		   created_at = excluded.created_at, updated_at = excluded.updated_at`,
		row.ID, row.Name, enabledInt, row.Schedule,
		row.Payload, row.State, row.Scope, row.SessionKey,
		strconv.FormatInt(row.CreatedAtMS, 10),
		strconv.FormatInt(row.UpdatedAtMS, 10),
	)
	if err != nil {
		return fmt.Errorf("store: upsert cron job %q: %w", row.ID, err)
	}
	return nil
}

// DeleteCronJob removes the cron job with the given id. Deleting a
// missing id is not an error.
func (r *CronRepo) DeleteCronJob(id string) error {
	if _, err := r.db.Exec(`DELETE FROM cron_jobs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete cron job %q: %w", id, err)
	}
	return nil
}

// DeleteAllCronJobs removes all cron jobs.
func (r *CronRepo) DeleteAllCronJobs() error {
	if _, err := r.db.Exec(`DELETE FROM cron_jobs`); err != nil {
		return fmt.Errorf("store: delete all cron jobs: %w", err)
	}
	return nil
}

// parseEpochMillis parses a decimal epoch-milliseconds string. The
// cron_jobs.created_at/updated_at columns are TEXT; the domain stores
// int64 epoch millis, which are persisted as decimal strings via
// strconv.FormatInt. Unparseable values degrade to 0.
func parseEpochMillis(s string) int64 {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return ms
}
