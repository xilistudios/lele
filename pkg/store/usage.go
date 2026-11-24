package store

import (
	"database/sql"
	"fmt"
)

// UsageRepo persists the usage counters that used to live in
// command_usage.json, skills_usage.json and mcp_usage.json.
//
// scope identifies the feature ("command", "skills", "mcp"); grp is the
// inner grouping key (agent ID for skills, server name for MCP, "" for
// commands); key is the counted item. The shape mirrors the legacy
// counterstore files: map[grp]map[key]uint64 per scope.
type UsageRepo struct {
	db *sql.DB
}

// LoadUsage returns all counters for a scope as map[grp]map[key]count.
func (r *UsageRepo) LoadUsage(scope string) (map[string]map[string]uint64, error) {
	rows, err := r.db.Query(
		`SELECT grp, key, count FROM usage_counters WHERE scope = ? ORDER BY grp, key`, scope)
	if err != nil {
		return nil, fmt.Errorf("usage load %q: %w", scope, err)
	}
	defer rows.Close()

	counts := make(map[string]map[string]uint64)
	for rows.Next() {
		var grp, key string
		var count uint64
		if err := rows.Scan(&grp, &key, &count); err != nil {
			return nil, fmt.Errorf("usage load %q: scan: %w", scope, err)
		}
		if counts[grp] == nil {
			counts[grp] = make(map[string]uint64)
		}
		counts[grp][key] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("usage load %q: rows: %w", scope, err)
	}
	return counts, nil
}

// MergeUsage atomically adds pending deltas to the stored counters and
// returns the merged result for the scope. It is the SQLite equivalent of
// counterstore.MergeAndSave: concurrent processes converge because the
// increment happens inside the database, not in a read-modify-write cycle.
func (r *UsageRepo) MergeUsage(scope string, pending map[string]map[string]uint64) (map[string]map[string]uint64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("usage merge %q: begin: %w", scope, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	for grp, keys := range pending {
		for key, delta := range keys {
			if delta == 0 {
				continue
			}
			if _, err := tx.Exec(
				`INSERT INTO usage_counters(scope, grp, key, count) VALUES(?, ?, ?, ?)
				 ON CONFLICT(scope, grp, key) DO UPDATE SET count = count + excluded.count`,
				scope, grp, key, delta,
			); err != nil {
				return nil, fmt.Errorf("usage merge %q: upsert %q/%q: %w", scope, grp, key, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("usage merge %q: commit: %w", scope, err)
	}
	return r.LoadUsage(scope)
}

// ReplaceUsage overwrites the whole scope with counts (used by the TUI
// command-usage pruning, which caps the number of tracked entries).
func (r *UsageRepo) ReplaceUsage(scope string, counts map[string]map[string]uint64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("usage replace %q: begin: %w", scope, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(`DELETE FROM usage_counters WHERE scope = ?`, scope); err != nil {
		return fmt.Errorf("usage replace %q: delete: %w", scope, err)
	}
	for grp, keys := range counts {
		for key, count := range keys {
			if _, err := tx.Exec(
				`INSERT INTO usage_counters(scope, grp, key, count) VALUES(?, ?, ?, ?)`,
				scope, grp, key, count,
			); err != nil {
				return fmt.Errorf("usage replace %q: insert %q/%q: %w", scope, grp, key, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("usage replace %q: commit: %w", scope, err)
	}
	return nil
}
