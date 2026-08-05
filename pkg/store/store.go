package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// Store provides SQLite-backed persistence for lele service state.
//
// It owns a single *sql.DB connection pool limited to one connection
// (single writer) and exposes the repositories built on top of it.
type Store struct {
	db      *sql.DB
	kv      *KVRepo
	cron    *CronRepo
	goals   *GoalRepo
	groups  *GroupRepo
	auth    *AuthRepo
	clients *NativeClientRepo
}

// Open opens (or creates) the SQLite database at path, applies any
// pending schema migrations and returns a ready-to-use Store.
//
// The connection is configured with:
//   - busy_timeout(5000): wait up to 5s on locked database
//   - journal_mode(WAL): concurrent readers with a single writer
//   - synchronous(NORMAL): safe with WAL, faster than FULL
//   - foreign_keys(1): enforce FK constraints
//
// The pool is limited to a single connection: one writer at a time is
// a requirement of the storage plan.
func Open(path string) (*Store, error) {
	if !sqliteSupported {
		return nil, errors.New("store: SQLite is not supported on this platform (linux/mips64); use the legacy JSON backends")
	}

	dsn := path +
		"?_pragma=busy_timeout(5000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", path, err)
	}

	// Single writer: avoid lock contention between goroutines.
	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite database %q: %w", path, err)
	}

	return &Store{
		db:      db,
		kv:      &KVRepo{db: db},
		cron:    &CronRepo{db: db},
		goals:   &GoalRepo{db: db},
		groups:  &GroupRepo{db: db},
		auth:    &AuthRepo{db: db},
		clients: &NativeClientRepo{db: db},
	}, nil
}

// DB exposes the underlying database handle. Intended for tests and
// future one-off data migration helpers.
func (s *Store) DB() *sql.DB {
	return s.db
}

// KV returns the key/value repository.
func (s *Store) KV() *KVRepo {
	return s.kv
}

// Cron returns the cron jobs repository.
func (s *Store) Cron() *CronRepo {
	return s.cron
}

// Goals returns the goals repository.
func (s *Store) Goals() *GoalRepo {
	return s.goals
}

// Groups returns the group state repository.
func (s *Store) Groups() *GroupRepo {
	return s.groups
}

// Auth returns the authentication credentials repository.
func (s *Store) Auth() *AuthRepo {
	return s.auth
}

// NativeClients returns the native clients repository.
func (s *Store) NativeClients() *NativeClientRepo {
	return s.clients
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}
