// Lele - Ultra-lightweight personal AI agent
//
// Copyright (c) 2026 Lele contributors
// License: MIT

// Package version is the single source of truth for the running program's
// version string.
//
// Rationale: the binaries are stamped at link time with
// `-ldflags "-X main.version=..."`, but only package main can see that
// variable. Every other consumer used to re-derive the version from
// debug.ReadBuildInfo(), whose Main.Version is computed by the Go toolchain
// from the VCS state — and the toolchain appends "+dirty" whenever the working
// tree has changed. Release builds mutate their own tree during the build
// (go mod tidy, embedded frontend assets, generated files), so published
// binaries reported "v0.7.10+dirty" even though the tag was clean.
//
// The rule this package encodes: the value injected at link time wins; build
// info is only a fallback for builds that were not stamped at all (e.g. `go
// run`, test binaries).
package version

import (
	"runtime/debug"
	"strings"
	"sync/atomic"
)

// unknown is returned when no version is known.
const unknown = "dev"

// injected holds the link-time value set by Set(). Accessed atomically because
// Set() may be called after goroutines that read the version have started.
var injected atomic.Value // string

// Set records the authoritative version for this process.
//
// cmd/lele calls it once at startup with the ldflags-injected value. Empty
// strings and placeholders ("(devel)", the package's own "dev" default) are
// ignored so Get() can still fall back to build info, which may know the tag.
func Set(v string) {
	if v == "" || v == "(devel)" || v == unknown {
		return
	}
	injected.Store(v)
}

// Get returns the version to display to users.
//
// Resolution order:
//  1. the value injected at link time via Set() (authoritative);
//  2. debug.ReadBuildInfo() Main.Version, with the toolchain's VCS "-dirty"
//     marker stripped (see StripDirty);
//  3. "dev".
func Get() string {
	if v, ok := injected.Load().(string); ok && v != "" {
		return v
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info == nil || info.Main.Version == "" {
		return unknown
	}
	if info.Main.Version == "(devel)" {
		return unknown
	}
	return StripDirty(info.Main.Version)
}

// StripDirty removes the VCS "dirty" marker that Go appends to module
// versions when the tree is modified at build time.
//
// Go stamps versions as "<ver>+dirty" (and git describe itself uses
// "<ver>-dirty"), so both separators are handled. Only the exact trailing
// marker is removed — a real pre-release such as "v1.2.3-dirty.fix1" is left
// untouched because the marker is not at the end.
func StripDirty(v string) string {
	for _, suffix := range []string{"+dirty", "-dirty"} {
		if strings.HasSuffix(v, suffix) {
			return strings.TrimSuffix(v, suffix)
		}
	}
	return v
}
