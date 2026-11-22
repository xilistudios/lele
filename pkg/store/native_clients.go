package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type NativeClientRepo struct {
	db *sql.DB
}

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

func (r *NativeClientRepo) DeleteClient(id string) error {
	if _, err := r.db.Exec(`DELETE FROM native_clients WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete native client %q: %w", id, err)
	}
	return nil
}

func (r *NativeClientRepo) DeleteAllClients() error {
	if _, err := r.db.Exec(`DELETE FROM native_clients`); err != nil {
		return fmt.Errorf("store: delete all native clients: %w", err)
	}
	return nil
}

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

func (r *NativeClientRepo) InsertPendingPIN(pin, pendingJSON string, createdAt, expiresAt int64) error {
	_, err := r.db.Exec(
		`INSERT INTO native_pending_pins(pin, pending, created_at, expires_at)
		 VALUES(?, ?, ?, ?)`,
		pin, pendingJSON, createdAt, expiresAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("store: insert pending PIN %q: %w", pin, ErrDuplicate)
		}
		return fmt.Errorf("store: insert pending PIN %q: %w", pin, err)
	}
	return nil
}

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

func (r *NativeClientRepo) DeletePendingPIN(pin string) error {
	if _, err := r.db.Exec(`DELETE FROM native_pending_pins WHERE pin = ?`, pin); err != nil {
		return fmt.Errorf("store: delete pending PIN: %w", err)
	}
	return nil
}

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

func (r *NativeClientRepo) CountClients() (int, error) {
	var count int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM native_clients`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count native clients: %w", err)
	}
	return count, nil
}
