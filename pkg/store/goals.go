package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GoalRepo provides CRUD operations on the goals table. The goal column
// stores an opaque JSON string serialised by the domain layer (pkg/agent);
// this package never interprets its contents.
type GoalRepo struct {
	db *sql.DB
}

// GetGoal returns the goal JSON for the given session key. If no row
// exists the found return is false with a nil error.
func (r *GoalRepo) GetGoal(sessionKey string) (string, bool, error) {
	var goal string
	err := r.db.QueryRow(
		`SELECT goal FROM goals WHERE session_key = ?`, sessionKey,
	).Scan(&goal)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: get goal %q: %w", sessionKey, err)
	}
	return goal, true, nil
}

// SetGoal inserts or updates the goal for the given session key.
func (r *GoalRepo) SetGoal(sessionKey, goalJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(
		`INSERT INTO goals(session_key, goal, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT(session_key) DO UPDATE SET goal = excluded.goal, updated_at = excluded.updated_at`,
		sessionKey, goalJSON, now,
	)
	if err != nil {
		return fmt.Errorf("store: set goal %q: %w", sessionKey, err)
	}
	return nil
}

// DeleteGoal removes the goal for the given session key.
func (r *GoalRepo) DeleteGoal(sessionKey string) error {
	if _, err := r.db.Exec(`DELETE FROM goals WHERE session_key = ?`, sessionKey); err != nil {
		return fmt.Errorf("store: delete goal %q: %w", sessionKey, err)
	}
	return nil
}

// ListGoals returns all goals keyed by session key. The returned map is
// never nil.
func (r *GoalRepo) ListGoals() (map[string]string, error) {
	rows, err := r.db.Query(`SELECT session_key, goal FROM goals`)
	if err != nil {
		return nil, fmt.Errorf("store: list goals: %w", err)
	}
	defer rows.Close()

	goals := make(map[string]string)
	for rows.Next() {
		var sessionKey, goal string
		if err := rows.Scan(&sessionKey, &goal); err != nil {
			return nil, fmt.Errorf("store: list goals: scan: %w", err)
		}
		goals[sessionKey] = goal
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list goals: rows: %w", err)
	}
	return goals, nil
}
