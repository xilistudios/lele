package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

type CronJobRow struct {
	ID          string
	Name        string
	Enabled     bool
	Schedule    string
	Payload     string
	State       string
	Scope       string
	SessionKey  string
	CreatedAtMS int64
	UpdatedAtMS int64
}

type CronRepo struct {
	db *sql.DB
}

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

func (r *CronRepo) DeleteCronJob(id string) error {
	if _, err := r.db.Exec(`DELETE FROM cron_jobs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete cron job %q: %w", id, err)
	}
	return nil
}

func (r *CronRepo) DeleteAllCronJobs() error {
	if _, err := r.db.Exec(`DELETE FROM cron_jobs`); err != nil {
		return fmt.Errorf("store: delete all cron jobs: %w", err)
	}
	return nil
}

func (r *CronRepo) CronRevision() (int, int64, error) {
	var count int
	var maxUpdated int64
	if err := r.db.QueryRow(
		`SELECT COUNT(*), COALESCE(MAX(CAST(updated_at AS INTEGER)), 0) FROM cron_jobs`,
	).Scan(&count, &maxUpdated); err != nil {
		return 0, 0, fmt.Errorf("store: cron revision: %w", err)
	}
	return count, maxUpdated, nil
}

func parseEpochMillis(s string) int64 {
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return ms
}
