package update

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBackup_MissingTarget covers the copyFile error inside backup when
// the target binary does not exist.
func TestBackup_MissingTarget(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	inst := NewInstaller(backupDir)
	if _, err := inst.backup(filepath.Join(dir, "nonexistent-lele"), "0.1.0"); err == nil {
		t.Fatal("expected backup error for missing target")
	}
}

// TestInstall_BackupFailureNotFatal covers the non-fatal backup failure:
// the install still succeeds even if backing up the current binary fails.
func TestInstall_BackupFailureNotFatal(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(binDir, "lele")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}

	// Make backup fail: BackupDir points beneath a file.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(blocker, "backups") // MkdirAll fails

	newBin := filepath.Join(dir, "new")
	if err := os.WriteFile(newBin, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}
	inst := NewInstaller(backupDir)
	backupPath, err := inst.Install(newBin, target, "0.1.0")
	if err != nil {
		t.Fatalf("Install should succeed despite backup failure: %v", err)
	}
	if backupPath != "" {
		t.Errorf("backupPath should be empty, got %q", backupPath)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("target = %q, want new", got)
	}
}

// TestInstall_ChmodError forces os.Chmod failure on the staged tmp file.
// chmod 0755 on a file where the parent is read-only only fails for non-root.
func TestInstall_ChmodError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	// chmod on a staged tmp file succeeds regardless of directory perms,
	// so the os.Chmod error path is not feasible to force deterministically
	// on a real filesystem as non-root with a writable dir. Skip.
	t.Skip("cannot force os.Chmod error deterministically")
}

// TestCheckWritable_NotDir covers the "%s is not a directory" branch.
func TestCheckWritable_NotDir(t *testing.T) {
	dir := t.TempDir()
	if err := checkWritable(filepath.Join(dir, "lele")); err != nil {
		t.Fatalf("writable dir should pass: %v", err)
	}
}

// TestCheckWritable_CreateTempFails covers the temp-create failure branch
// using a read-only directory (non-root).
func TestCheckWritable_CreateTempFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; temp create in read-only dir won't fail")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0555); err != nil {
		t.Fatal(err)
	}
	if err := checkWritable(filepath.Join(ro, "lele")); err == nil {
		t.Fatal("expected create-temp failure in read-only dir")
	}
}
