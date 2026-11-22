package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type KVRepo struct {
	db *sql.DB
}

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

func (r *KVRepo) Delete(key string) error {
	if _, err := r.db.Exec(`DELETE FROM kv WHERE key = ?`, key); err != nil {
		return fmt.Errorf("kv delete %q: %w", key, err)
	}
	return nil
}

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

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
