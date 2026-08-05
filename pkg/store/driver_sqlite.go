//go:build !(linux && mips64)

package store

import (
	_ "modernc.org/sqlite"
)

// sqliteSupported reports whether the pure-Go SQLite driver is linked
// into the binary for the current platform. See driver_stub.go for
// the platforms where it is not available.
const sqliteSupported = true
