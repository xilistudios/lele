package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AuthRepo provides CRUD operations on the auth_credentials table. The
// credential column stores an opaque JSON string serialised by the
// domain layer; this package never interprets its contents.
type AuthRepo struct {
	db *sql.DB
}

// GetCredential returns the credential JSON for the given provider key.
// If no row exists the found return is false with a nil error.
func (r *AuthRepo) GetCredential(providerKey string) (string, bool, error) {
	var credential string
	err := r.db.QueryRow(
		`SELECT credential FROM auth_credentials WHERE provider_key = ?`, providerKey,
	).Scan(&credential)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: get credential %q: %w", providerKey, err)
	}
	return credential, true, nil
}

// SetCredential inserts or updates the credential for the given provider
// key.
func (r *AuthRepo) SetCredential(providerKey, credentialJSON string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := r.db.Exec(
		`INSERT INTO auth_credentials(provider_key, credential, updated_at) VALUES(?, ?, ?)
		 ON CONFLICT(provider_key) DO UPDATE SET credential = excluded.credential, updated_at = excluded.updated_at`,
		providerKey, credentialJSON, now,
	)
	if err != nil {
		return fmt.Errorf("store: set credential %q: %w", providerKey, err)
	}
	return nil
}

// DeleteCredential removes the credential for the given provider key.
func (r *AuthRepo) DeleteCredential(providerKey string) error {
	if _, err := r.db.Exec(`DELETE FROM auth_credentials WHERE provider_key = ?`, providerKey); err != nil {
		return fmt.Errorf("store: delete credential %q: %w", providerKey, err)
	}
	return nil
}

// DeleteAllCredentials removes all credentials.
func (r *AuthRepo) DeleteAllCredentials() error {
	if _, err := r.db.Exec(`DELETE FROM auth_credentials`); err != nil {
		return fmt.Errorf("store: delete all credentials: %w", err)
	}
	return nil
}

// ListCredentials returns all credentials keyed by provider key. The
// returned map is never nil.
func (r *AuthRepo) ListCredentials() (map[string]string, error) {
	rows, err := r.db.Query(`SELECT provider_key, credential FROM auth_credentials`)
	if err != nil {
		return nil, fmt.Errorf("store: list credentials: %w", err)
	}
	defer rows.Close()

	creds := make(map[string]string)
	for rows.Next() {
		var providerKey, credential string
		if err := rows.Scan(&providerKey, &credential); err != nil {
			return nil, fmt.Errorf("store: list credentials: scan: %w", err)
		}
		creds[providerKey] = credential
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list credentials: rows: %w", err)
	}
	return creds, nil
}
