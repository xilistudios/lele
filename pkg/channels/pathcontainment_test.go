package channels

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalContainmentRoot_EmptyDirFailsClosed(t *testing.T) {
	if got := canonicalContainmentRoot(""); got != "" {
		t.Errorf("canonicalContainmentRoot(%q) = %q, want empty", "", got)
	}
	if isContainedIn("/anywhere/file.txt", "") {
		t.Error("isContainedIn must fail closed for an empty root")
	}
}

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

func TestIsContainedIn_EmptyPathFailsClosed(t *testing.T) {
	root := t.TempDir()
	if isContainedIn("", root) {
		t.Error("empty path must not be contained")
	}
	if got := resolvedForContainment(""); got != "" {
		t.Errorf("resolvedForContainment(%q) = %q, want empty", "", got)
	}
}

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
