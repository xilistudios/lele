package store

import (
	"database/sql"
	"fmt"
)

type Store struct {
	db       *sql.DB
	kv       *KVRepo
	cron     *CronRepo
	goals    *GoalRepo
	groups   *GroupRepo
	auth     *AuthRepo
	clients  *NativeClientRepo
	sessions *SessionRepo
	spool    *SpoolRepo

	usage *UsageRepo
}

func Open(path string) (*Store, error) {
	if !sqliteSupported {
		return nil, fmt.Errorf("%w: SQLite is not supported on this platform (linux/mips64); use the legacy JSON backends", ErrUnsupportedPlatform)
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

	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate sqlite database %q: %w", path, err)
	}

	return &Store{
		db:       db,
		kv:       &KVRepo{db: db},
		cron:     &CronRepo{db: db},
		goals:    &GoalRepo{db: db},
		groups:   &GroupRepo{db: db},
		auth:     &AuthRepo{db: db},
		clients:  &NativeClientRepo{db: db},
		sessions: &SessionRepo{db: db},
		spool:    &SpoolRepo{db: db},

		usage: &UsageRepo{db: db},
	}, nil
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) KV() *KVRepo {
	return s.kv
}

func (s *Store) Cron() *CronRepo {
	return s.cron
}

func (s *Store) Goals() *GoalRepo {
	return s.goals
}

func (s *Store) Groups() *GroupRepo {
	return s.groups
}

func (s *Store) Auth() *AuthRepo {
	return s.auth
}

func (s *Store) NativeClients() *NativeClientRepo {
	return s.clients
}

func (s *Store) Sessions() *SessionRepo {
	return s.sessions
}

func (s *Store) Spool() *SpoolRepo {
	return s.spool
}

func (s *Store) Usage() *UsageRepo {
	return s.usage
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}
