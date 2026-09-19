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

// ---------------------------------------------------------------------------
// Pending native pairing PINs (native_pending_pins table)
// ---------------------------------------------------------------------------

// InsertPendingPIN stores a new pending PIN. The caller owns the JSON
// serialisation of pendingJSON and the unix-nanosecond timestamps.
// A duplicate pin propagates as an error (no silent upsert).
func (r *NativeClientRepo) InsertPendingPIN(pin, pendingJSON string, createdAt, expiresAt int64) error {
	_, err := r.db.Exec(
		`INSERT INTO native_pending_pins(pin, pending, created_at, expires_at)
		 VALUES(?, ?, ?, ?)`,
		pin, pendingJSON, createdAt, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("store: insert pending PIN: %w", err)
	}
	return nil
}

// GetPendingPIN retrieves a pending PIN. When the pin does not exist
// found is false with a nil error (same contract as GetClient).
func (r *NativeClientRepo) GetPendingPIN(pin string) (pendingJSON string, expiresAt int64, found bool, err error) {
	err = r.db.QueryRow(
		`SELECT pending, expires_at FROM native_pending_pins WHERE pin = ?`, pin,
	).Scan(&pendingJSON, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("store: get pending PIN: %w", err)
	}
	return pendingJSON, expiresAt, true, nil
}

// TakePendingPIN atomically deletes and returns a non-expired pending
// PIN in a single statement. This is the core single-use guarantee:
// two concurrent callers on different connections see exactly one
// winner. Uses an explicit transaction so the caller can distinguish
// "pin not found or expired" (found=false, err=nil) from real errors.
func (r *NativeClientRepo) TakePendingPIN(pin string, nowNanos int64) (string, bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", false, fmt.Errorf("store: begin take pending PIN tx: %w", err)
	}
	defer tx.Rollback()

	var pendingJSON string
	err = tx.QueryRow(
		`DELETE FROM native_pending_pins
		 WHERE pin = ? AND expires_at > ?
		 RETURNING pending`, pin, nowNanos,
	).Scan(&pendingJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: take pending PIN: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", false, fmt.Errorf("store: commit take pending PIN tx: %w", err)
	}
	return pendingJSON, true, nil
}

// DeletePendingPIN removes a pending PIN. Deleting a non-existent pin
// is not an error (consistent with KVRepo.Delete).
func (r *NativeClientRepo) DeletePendingPIN(pin string) error {
	if _, err := r.db.Exec(`DELETE FROM native_pending_pins WHERE pin = ?`, pin); err != nil {
		return fmt.Errorf("store: delete pending PIN: %w", err)
	}
	return nil
}

// ListPendingPINs returns all non-expired pending PINs keyed by pin.
// The returned map is never nil. Results are ordered by created_at ASC,
// pin ASC for deterministic FIFO eviction.
func (r *NativeClientRepo) ListPendingPINs(nowNanos int64) (map[string]string, error) {
	rows, err := r.db.Query(
		`SELECT pin, pending FROM native_pending_pins
		 WHERE expires_at > ?
		 ORDER BY created_at ASC, pin ASC`, nowNanos,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list pending PINs: %w", err)
	}
	defer rows.Close()

	pins := make(map[string]string)
	for rows.Next() {
		var pin, pending string
		if err := rows.Scan(&pin, &pending); err != nil {
			return nil, fmt.Errorf("store: list pending PINs: scan: %w", err)
		}
		pins[pin] = pending
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list pending PINs: rows: %w", err)
	}
	return pins, nil
}

// DeleteExpiredPendingPINs removes all expired pending PINs and returns
// the number of rows deleted.
func (r *NativeClientRepo) DeleteExpiredPendingPINs(nowNanos int64) (int64, error) {
	res, err := r.db.Exec(
		`DELETE FROM native_pending_pins WHERE expires_at <= ?`, nowNanos,
	)
	if err != nil {
		return 0, fmt.Errorf("store: delete expired pending PINs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: delete expired pending PINs: rows affected: %w", err)
	}
	return n, nil
}

// CountPendingPINs returns the number of non-expired pending PINs.
func (r *NativeClientRepo) CountPendingPINs(nowNanos int64) (int, error) {
	var count int
	err := r.db.QueryRow(
		`SELECT COUNT(*) FROM native_pending_pins WHERE expires_at > ?`, nowNanos,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count pending PINs: %w", err)
	}
	return count, nil
}

// EvictOldestPendingPIN deletes and returns the non-expired pending PIN
// with the smallest (created_at, pin) — deterministic FIFO order.
// Uses an explicit transaction for atomicity. Returns ("", nil) when
// there is nothing to evict.
func (r *NativeClientRepo) EvictOldestPendingPIN(nowNanos int64) (string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", fmt.Errorf("store: begin evict oldest pending PIN tx: %w", err)
	}
	defer tx.Rollback()

	var pin string
	err = tx.QueryRow(
		`DELETE FROM native_pending_pins
		 WHERE pin = (
		   SELECT pin FROM native_pending_pins
		   WHERE expires_at > ?
		   ORDER BY created_at ASC, pin ASC
		   LIMIT 1
		 )
		 RETURNING pin`, nowNanos,
	).Scan(&pin)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: evict oldest pending PIN: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: commit evict oldest pending PIN tx: %w", err)
	}
	return pin, nil
}

// CountClients returns the number of rows in native_clients. This is
// the authoritative inter-process counter for the MaxClients check
// (the in-memory map of the gateway is stale against other processes).
func (r *NativeClientRepo) CountClients() (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM native_clients`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count native clients: %w", err)
	}
	return count, nil
}
