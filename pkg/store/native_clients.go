package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// NativeClientRepo provides CRUD operations on the native_clients table.
// The client column stores an opaque JSON string serialised by the domain
// layer; this package never interprets its contents.
type NativeClientRepo struct {
	db *sql.DB
}

// GetClient returns the client JSON for the given id. If no row exists
// the found return is false with a nil error.
func (r *NativeClientRepo) GetClient(id string) (string, bool, error) {
	var client string
	err := r.db.QueryRow(
		`SELECT client FROM native_clients WHERE id = ?`, id,
	).Scan(&client)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: get native client %q: %w", id, err)
	}
	return client, true, nil
}

// SetClient inserts or updates the client for the given id. On insert
// created_at is set to the current time; on update only the client
// column is changed (created_at is preserved).
func (r *NativeClientRepo) SetClient(id, clientJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(
		`INSERT INTO native_clients(id, client, created_at) VALUES(?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET client = excluded.client`,
		id, clientJSON, now,
	)
	if err != nil {
		return fmt.Errorf("store: set native client %q: %w", id, err)
	}
	return nil
}

// DeleteClient removes the client with the given id.
func (r *NativeClientRepo) DeleteClient(id string) error {
	if _, err := r.db.Exec(`DELETE FROM native_clients WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete native client %q: %w", id, err)
	}
	return nil
}

// DeleteAllClients removes all native clients.
func (r *NativeClientRepo) DeleteAllClients() error {
	if _, err := r.db.Exec(`DELETE FROM native_clients`); err != nil {
		return fmt.Errorf("store: delete all native clients: %w", err)
	}
	return nil
}

// ListClients returns all native clients keyed by id. The returned map
// is never nil.
func (r *NativeClientRepo) ListClients() (map[string]string, error) {
	rows, err := r.db.Query(`SELECT id, client FROM native_clients`)
	if err != nil {
		return nil, fmt.Errorf("store: list native clients: %w", err)
	}
	defer rows.Close()

	clients := make(map[string]string)
	for rows.Next() {
		var id, client string
		if err := rows.Scan(&id, &client); err != nil {
			return nil, fmt.Errorf("store: list native clients: scan: %w", err)
		}
		clients[id] = client
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list native clients: rows: %w", err)
	}
	return clients, nil
}
