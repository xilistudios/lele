package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GroupRepo provides CRUD operations on the groups_state table. The
// state column stores an opaque JSON string serialised by the domain
// layer; this package never interprets its contents.
type GroupRepo struct {
	db *sql.DB
}

// GetGroupState returns the state JSON for the given group id. If no
// row exists the found return is false with a nil error.
func (r *GroupRepo) GetGroupState(id string) (string, bool, error) {
	var state string
	err := r.db.QueryRow(
		`SELECT state FROM groups_state WHERE id = ?`, id,
	).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: get group state %q: %w", id, err)
	}
	return state, true, nil
}

// SetGroupState inserts or updates the state for the given group id.
func (r *GroupRepo) SetGroupState(id, stateJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(
		`INSERT INTO groups_state(id, state, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
		id, stateJSON, now,
	)
	if err != nil {
		return fmt.Errorf("store: set group state %q: %w", id, err)
	}
	return nil
}

// DeleteGroupState removes the state for the given group id.
func (r *GroupRepo) DeleteGroupState(id string) error {
	if _, err := r.db.Exec(`DELETE FROM groups_state WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete group state %q: %w", id, err)
	}
	return nil
}

// ListGroupStates returns all group states keyed by group id. The
// returned map is never nil.
func (r *GroupRepo) ListGroupStates() (map[string]string, error) {
	rows, err := r.db.Query(`SELECT id, state FROM groups_state`)
	if err != nil {
		return nil, fmt.Errorf("store: list group states: %w", err)
	}
	defer rows.Close()

	states := make(map[string]string)
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, fmt.Errorf("store: list group states: scan: %w", err)
		}
		states[id] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list group states: rows: %w", err)
	}
	return states, nil
}
