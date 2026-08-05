//go:build linux && mips64

package store

// The pure-Go SQLite driver (modernc.org/sqlite -> modernc.org/libc)
// does not support linux/mips64 (big-endian); modernc.org/libc only
// covers mips64le. The package still compiles on this platform, but
// Open returns an explicit error and every consumer falls back to its
// legacy JSON backend.

// sqliteSupported reports whether the pure-Go SQLite driver is linked
// into the binary for the current platform.
const sqliteSupported = false
