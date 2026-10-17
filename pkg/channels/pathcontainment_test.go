package channels

// Unit tests for the canonical containment helper. The boundary itself is
// covered end-to-end in processattachments_test.go; these tests pin the helper
// semantics (empty values fail closed, sibling prefixes are rejected, missing
// leaves still compare canonically).

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCanonicalContainmentRoot_EmptyDirFailsClosed: a caller that hands an
// empty (or not yet configured) directory must not get a root that matches
// anything. LeleDir == "" is a real configuration state (config documents it).
func TestCanonicalContainmentRoot_EmptyDirFailsClosed(t *testing.T) {
	if got := canonicalContainmentRoot(""); got != "" {
		t.Errorf("canonicalContainmentRoot(%q) = %q, want empty", "", got)
	}
	if isContainedIn("/anywhere/file.txt", "") {
		t.Error("isContainedIn must fail closed for an empty root")
	}
}

// TestCanonicalContainmentRoot_ExistentDir: a plain, existing directory is
// canonicalized and a file inside it (and the directory itself) is contained.
func TestCanonicalContainmentRoot_ExistentDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tmp", "uploads")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "abcd1234_report.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := canonicalContainmentRoot(root); got != root {
		t.Errorf("canonicalContainmentRoot(%q) = %q, want %q", root, got, root)
	}
	if !isContainedIn(file, root) {
		t.Error("file inside the root must be contained")
	}
	if !isContainedIn(root, root) {
		t.Error("the root itself must be contained in itself")
	}
}

// TestCanonicalContainmentRoot_MissingDirWalksAncestors: the root does not
// exist yet (uploads dir before the first upload). The canonical form must
// still be built from the deepest existing ancestor, otherwise the check
// compares an unresolved root against a resolved path.
func TestCanonicalContainmentRoot_MissingDirWalksAncestors(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "not", "created", "yet")

	got := canonicalContainmentRoot(root)
	if got == "" {
		t.Fatal("canonicalContainmentRoot returned empty for a missing directory")
	}
	if got != root {
		t.Errorf("canonicalContainmentRoot(%q) = %q, want %q (base has no symlinks)", root, got, root)
	}
	if !isContainedIn(filepath.Join(root, "file.txt"), root) {
		t.Error("a file under a not-yet-created root must be contained")
	}
}

// TestIsContainedIn_SymlinkedRoot: LeleDir traverses a symlink (the incident).
// Mutations M2 (no resolution on the candidate side) and M6 (ancestor-walk
// drops the unresolved remainder) turn this test red.
// Paths spelled through the symlink and paths spelled through the real
// directory must both be contained. A missing leaf through a symlinked root
// must be contained as well (this is the case that a naive ancestor-walk that
// returns the unresolved abs path gets wrong).
func TestIsContainedIn_SymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "lele")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root := filepath.Join(real, "tmp", "uploads")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rootViaLink := filepath.Join(link, "tmp", "uploads")
	if !isContainedIn(filepath.Join(rootViaLink, "existing.txt"), rootViaLink) {
		t.Error("existing file through a symlinked root must be contained")
	}
	if !isContainedIn(filepath.Join(root, "existing.txt"), rootViaLink) {
		t.Error("real spelling of the file must be contained in the symlinked root")
	}
	// Missing leaf: EvalSymlinks fails on the file, the ancestor-walk must
	// still produce a canonical path inside the canonical root.
	if !isContainedIn(filepath.Join(rootViaLink, "missing.txt"), rootViaLink) {
		t.Error("missing file under an existing symlinked root must be contained (D1)")
	}
	if !isContainedIn(filepath.Join(rootViaLink, "missing.txt"), root) {
		t.Error("missing file under the real root spelling must be contained (D1)")
	}
	if isContainedIn(filepath.Join(t.TempDir(), "elsewhere.txt"), rootViaLink) {
		t.Error("a file outside the symlinked root must not be contained")
	}
}

// TestIsContainedIn_RejectsSiblingPrefix: a sibling directory whose name shares
// a prefix with the root ("uploads" vs "uploads-evil") must be rejected. With a
// raw strings.HasPrefix comparison it was accepted (mutation M3, which also
// turns the S4 row of the processAttachments matrix red).
func TestIsContainedIn_RejectsSiblingPrefix(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "tmp", "uploads")
	sibling := filepath.Join(base, "tmp", "uploads-evil")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(sibling, "pass.txt")
	if err := os.WriteFile(evil, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if isContainedIn(evil, root) {
		t.Errorf("sibling path %q must not be contained in %q", evil, root)
	}
	if isContainedIn(sibling, root) {
		t.Errorf("sibling directory %q must not be contained in %q", sibling, root)
	}
}

// TestIsContainedIn_RejectsParentTraversal: ".." escapes stay rejected, both
// when they leave the root and when they are spelled inside it (mutation M4
// accepts the ".." remainder and turns this test red).
func TestIsContainedIn_RejectsParentTraversal(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "tmp", "uploads")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "sub", "..", "file.txt")

	if !isContainedIn(inside, root) {
		t.Errorf("%q normalizes to a path inside the root and must be contained", inside)
	}
	escape := filepath.Join(root, "..", "..", "etc", "passwd")
	if isContainedIn(escape, root) {
		t.Errorf("traversal path %q must not be contained in %q", escape, root)
	}
	if isContainedIn(base, root) {
		t.Errorf("the parent of the root (%q) must not be contained in %q", base, root)
	}
}

// TestIsContainedIn_EmptyPathFailsClosed: an empty candidate path must be
// rejected rather than treated as contained.
func TestIsContainedIn_EmptyPathFailsClosed(t *testing.T) {
	root := t.TempDir()
	if isContainedIn("", root) {
		t.Error("empty path must not be contained")
	}
	if got := resolvedForContainment(""); got != "" {
		t.Errorf("resolvedForContainment(%q) = %q, want empty", "", got)
	}
}

// TestIsContainedIn_RelativeRoot: a relative LeleDir is resolved against the
// process working directory (filepath.Abs semantics, unchanged by this helper),
// so a file under the same relative spelling stays contained.
func TestIsContainedIn_RelativeRoot(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "tmp", "uploads")
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, abs)
	if err != nil {
		t.Fatalf("cannot relativize %q against %q: %v", abs, wd, err)
	}

	if !isContainedIn(filepath.Join(abs, "file.txt"), rel) {
		t.Errorf("absolute file must be contained under the relative root %q", rel)
	}
	if !isContainedIn(filepath.Join(rel, "file.txt"), rel) {
		t.Errorf("file must be contained under the relative root %q", rel)
	}
	if isContainedIn(filepath.Join(t.TempDir(), "other.txt"), rel) {
		t.Error("an unrelated file must not be contained under a relative root")
	}
}
