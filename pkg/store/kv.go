package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// KVRepo is a small key/value store backed by the kv table. It holds
// leftover small state (telegram_offset, workspace state, etc.).
type KVRepo struct {
	db *sql.DB
}

// Get returns the value stored for key and whether it was found.
func (r *KVRepo) Get(key string) (string, bool, error) {
	var value string
	err := r.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("kv get %q: %w", key, err)
	}
	return value, true, nil
}

// Set stores value under key, inserting or updating as needed.
func (r *KVRepo) Set(key, value string) error {
	if _, err := r.db.Exec(
		`INSERT INTO kv(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return fmt.Errorf("kv set %q: %w", key, err)
	}
	return nil
}

// Delete removes key from the store. Deleting a missing key is not an
// error.
func (r *KVRepo) Delete(key string) error {
	if _, err := r.db.Exec(`DELETE FROM kv WHERE key = ?`, key); err != nil {
		return fmt.Errorf("kv delete %q: %w", key, err)
	}
	return nil
}

// Keys returns all keys that start with prefix, ordered
// lexicographically. An empty prefix returns all keys.
func (r *KVRepo) Keys(prefix string) ([]string, error) {
	rows, err := r.db.Query(
		`SELECT key FROM kv WHERE key LIKE ? ESCAPE '\' ORDER BY key`,
		likeEscape(prefix)+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("kv keys %q: %w", prefix, err)
	}
	defer rows.Close()

	keys := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("kv keys %q: scan: %w", prefix, err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("kv keys %q: rows: %w", prefix, err)
	}
	return keys, nil
}

// likeEscape escapes LIKE wildcards so prefix is matched literally.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
