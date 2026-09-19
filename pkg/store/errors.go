package store

import "errors"

// ErrDuplicate is returned by InsertPendingPIN when the PIN already
// exists in the database (UNIQUE constraint violation). Callers can
// use errors.Is(err, ErrDuplicate) to distinguish a collision (which
// should be retried with a different PIN) from other storage errors.
//
// Detection is done by inspecting the error text from the SQLite
// driver because modernc.org/sqlite types are not importable in
// files that compile on all platforms (driver_sqlite.go is behind
// //go:build !(linux && mips64); see driver_stub.go for the mips64
// fallback). This keeps the fragile text coupling in one place.
var ErrDuplicate = errors.New("store: duplicate key")
