//go:build !(linux && mips64)

package store

import (
	_ "modernc.org/sqlite"
)

const sqliteSupported = true
