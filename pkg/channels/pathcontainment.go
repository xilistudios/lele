package channels

// Canonical path containment for attachment paths.
//
// THE BUG THIS FILE EXISTS TO PREVENT (2026-10-17): filepath.Abs does NOT
// resolve symlinks while filepath.EvalSymlinks DOES. Using Abs() on one side of
// a containment check and EvalSymlinks() on the other produces a predicate that
// is correct only while no component of the prefix is a symlink. It silently
// rejected every attachment once LeleDir went through a symlink (~/.lele ->
// /storage/lele), because the incoming path resolved to /storage/lele/... and
// the allowed directory stayed /home/alfredo/.lele/... .
//
// Rule: both sides of a containment comparison must be canonicalized, and the
// comparison must be Rel-based (a raw prefix test accepts sibling directories
// such as "uploads" vs "uploads-evil"). Callers must use isContainedIn instead
// of re-implementing the comparison locally.

import (
	"path/filepath"
	"strings"
)

// canonicalContainmentRoot resolves dir for containment comparisons, tolerating
// a directory that does not exist yet: EvalSymlinks on the deepest existing
// ancestor plus the unresolved remainder re-appended. Falling back to the
// unresolved Abs would silently reintroduce the bug when only a leaf is missing
// (a fully unresolved root compared against a resolved path fails).
//
// An empty dir (or one that cannot be made absolute) returns "" so that
// callers fail closed instead of accepting everything.
func canonicalContainmentRoot(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil || abs == "" {
		return ""
	}
	return canonicalizeExistingPrefix(abs)
}

// resolvedForContainment resolves path the same way as canonicalContainmentRoot
// (EvalSymlinks when the path exists, ancestor-walk otherwise). The ancestor
// walk is what keeps a not-yet-created file comparable instead of silently
// skipped, and an empty/unresolvable path returns "" (fail closed).
//
// The two functions are deliberately separate entry points: callers document
// intent (root directory vs candidate path) and each one is independently
// testable, but both must stay canonical on the SAME rules.
func resolvedForContainment(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil || abs == "" {
		return ""
	}
	return canonicalizeExistingPrefix(abs)
}

// canonicalizeExistingPrefix resolves the deepest existing ancestor of abs via
// EvalSymlinks and re-appends the components that do not exist yet. abs must be
// absolute. If nothing along the chain exists (not even the filesystem root) it
// returns abs unchanged.
func canonicalizeExistingPrefix(abs string) string {
	rest := ""
	cur := abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// isContainedIn reports whether path is dir itself or lives strictly inside it.
//
// Both sides are compared in canonical form (see canonicalContainmentRoot /
// resolvedForContainment) and the verdict is Rel-based, so a sibling directory
// sharing a prefix with dir ("uploads" vs "uploads-evil") is NOT contained.
// Every error path returns false (fail closed): unknown root, unknown path,
// Rel failure (different volumes on Windows) or an absolute remainder.
//
// Note the deliberate semantics: if dir itself is a symlink to another disk the
// canonical comparison accepts it (an administrator's decision), but a symlink
// INSIDE dir pointing outside is still rejected, because its canonical form
// leaves the root.
func isContainedIn(path, dir string) bool {
	root := canonicalContainmentRoot(dir)
	if root == "" {
		return false
	}
	p := resolvedForContainment(path)
	if p == "" {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	sep := string(filepath.Separator)
	if rel == ".." || strings.HasPrefix(rel, ".."+sep) || filepath.IsAbs(rel) {
		return false
	}
	return true
}
