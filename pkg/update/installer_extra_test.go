package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentBinaryPath(t *testing.T) {
	p, err := CurrentBinaryPath()
	if err != nil {
		t.Fatalf("CurrentBinaryPath: %v", err)
	}
	if p == "" {
		t.Error("expected a non-empty path")
	}
	if !filepath.IsAbs(p) {
		t.Errorf("expected absolute path, got %q", p)
	}
}

func TestInstall_BackupDirEmpty(t *testing.T) {
	// When BackupDir == "", no backup is created and install still works.
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0755)
	target := filepath.Join(binDir, "lele")
	os.WriteFile(target, []byte("old"), 0755)
	newBin := filepath.Join(dir, "new")
	os.WriteFile(newBin, []byte("new"), 0755)

	inst := NewInstaller("")
	backupPath, err := inst.Install(newBin, target, "0.1.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if backupPath != "" {
		t.Errorf("expected empty backup path when BackupDir empty, got %q", backupPath)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("target = %q, want new", got)
	}
}

func TestInstall_CheckWritableFails(t *testing.T) {
	inst := NewInstaller(t.TempDir())
	if _, err := inst.Install("/tmp/x", filepath.Join(t.TempDir(), "sub", "lele"), "0.1.0"); err == nil {
		t.Fatal("expected error for non-existent target dir")
	}
}

func TestInstall_TargetDirIsFile(t *testing.T) {
	dir := t.TempDir()
	// Make the target's parent a file so checkWritable fails.
	parent := filepath.Join(dir, "notadir")
	os.WriteFile(parent, []byte("file"), 0644)
	target := filepath.Join(parent, "lele")
	inst := NewInstaller(t.TempDir())
	if _, err := inst.Install("/tmp/new", target, "0.1.0"); err == nil {
		t.Fatal("expected error when target dir is a file")
	}
}

func TestInstall_CopySourceMissing(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0755)
	target := filepath.Join(binDir, "lele")
	os.WriteFile(target, []byte("old"), 0755)
	inst := NewInstaller(t.TempDir())
	// newBinaryPath does not exist -> copyFile fails.
	if _, err := inst.Install(filepath.Join(dir, "nope"), target, "0.1.0"); err == nil {
		t.Fatal("expected error when source binary missing")
	}
}

func TestBackup_DevVersion(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	target := filepath.Join(dir, "lele")
	os.WriteFile(target, []byte("old"), 0755)

	inst := NewInstaller(backupDir)
	// dev version -> backup name uses "dev".
	b, err := inst.backup(target, "dev")
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if !filepath.IsAbs(b) {
		t.Error("expected absolute backup path")
	}
	if b == "" {
		t.Error("expected non-empty backup path")
	}
}

func TestBackup_MkdirAllFails(t *testing.T) {
	dir := t.TempDir()
	// Parent is a file, so MkdirAll on backupDir fails.
	parent := filepath.Join(dir, "f")
	os.WriteFile(parent, []byte("x"), 0644)
	inst := NewInstaller(filepath.Join(parent, "backups"))
	if _, err := inst.backup(filepath.Join(dir, "lele"), "0.1.0"); err == nil {
		t.Fatal("expected error when backup dir cannot be created")
	}
}

func TestListBackups_FiltersNonLele(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)
	// Real lele backup.
	os.WriteFile(filepath.Join(backupDir, "lele-0.1.0-20250101-010101"), []byte("a"), 0755)
	// Subdirectory and non-lele file should be ignored.
	os.MkdirAll(filepath.Join(backupDir, "lele-subdir"), 0755)
	os.WriteFile(filepath.Join(backupDir, "README"), []byte("b"), 0644)

	inst := NewInstaller(backupDir)
	list, err := inst.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 backup, got %d: %v", len(list), list)
	}
	base := filepath.Base(list[0])
	if base != "lele-0.1.0-20250101-010101" {
		t.Errorf("unexpected backup: %q", base)
	}
}

func TestListBackups_ReadDirError(t *testing.T) {
	// Point BackupDir at a file (not a dir) so ReadDir fails with a
	// non-IsNotExist error.
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	os.WriteFile(f, []byte("x"), 0644)
	inst := NewInstaller(f)
	if _, err := inst.ListBackups(); err == nil {
		t.Fatal("expected error reading non-directory backup dir")
	}
}

func TestPruneBackups(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(backupDir, 0755)
	for i := 0; i < maxBackups+3; i++ {
		os.WriteFile(filepath.Join(backupDir, "lele-0.1.0-2025010"+string(rune('0'+i))), []byte("x"), 0755)
	}
	inst := NewInstaller(backupDir)
	inst.pruneBackups()
	backups, err := inst.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) > maxBackups {
		t.Errorf("expected at most %d backups after prune, got %d", maxBackups, len(backups))
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	os.WriteFile(src, []byte("hello"), 0644)
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "hello" {
		t.Errorf("dst = %q", got)
	}
	if err := copyFile(filepath.Join(dir, "gun"), dst); err == nil {
		t.Fatal("expected error copying missing source")
	}
}
