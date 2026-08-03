package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAtomicSwapAndBackup(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	backupDir := filepath.Join(dir, "backups")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(binDir, "lele")
	if err := os.WriteFile(target, []byte("old-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	newBin := filepath.Join(dir, "new-lele")
	if err := os.WriteFile(newBin, []byte("new-binary"), 0755); err != nil {
		t.Fatal(err)
	}

	inst := NewInstaller(backupDir)
	backupPath, err := inst.Install(newBin, target, "0.1.0")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	// Target now has new content.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new-binary" {
		t.Errorf("target content = %q, want new-binary", data)
	}

	// Backup exists with old content.
	if backupPath == "" {
		t.Fatal("expected a backup path")
	}
	bdata, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(bdata) != "old-binary" {
		t.Errorf("backup content = %q, want old-binary", bdata)
	}

	// Backup is executable.
	info, _ := os.Stat(target)
	if info.Mode().Perm()&0100 == 0 {
		t.Error("installed binary should be executable")
	}
}

func TestBackupPruning(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	backupDir := filepath.Join(dir, "backups")
	os.MkdirAll(binDir, 0755)

	target := filepath.Join(binDir, "lele")
	os.WriteFile(target, []byte("v0"), 0755)

	inst := NewInstaller(backupDir)
	for i := 0; i < 5; i++ {
		newBin := filepath.Join(dir, "new")
		os.WriteFile(newBin, []byte{byte('v' + i)}, 0755)
		if _, err := inst.Install(newBin, target, "0.0.1"); err != nil {
			t.Fatalf("install %d: %v", i, err)
		}
	}

	backups, err := inst.ListBackups()
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) > maxBackups {
		t.Errorf("expected at most %d backups, got %d", maxBackups, len(backups))
	}
}

func TestLatestBackupEmpty(t *testing.T) {
	inst := NewInstaller(filepath.Join(t.TempDir(), "nonexistent"))
	b, err := inst.LatestBackup()
	if err != nil {
		t.Fatal(err)
	}
	if b != "" {
		t.Errorf("expected empty backup, got %q", b)
	}
}

func TestCheckWritable(t *testing.T) {
	dir := t.TempDir()
	if err := checkWritable(filepath.Join(dir, "lele")); err != nil {
		t.Errorf("expected writable: %v", err)
	}
}

func TestCurrentPlatformArchiveName(t *testing.T) {
	p, err := CurrentPlatform()
	if err != nil {
		t.Skipf("platform not supported in tests: %v", err)
	}
	name := ArchiveName(p)
	if !strings.HasPrefix(name, "lele_") {
		t.Errorf("archive name %q should start with lele_", name)
	}
	if p.OS == "Windows" && !strings.HasSuffix(name, ".zip") {
		t.Error("windows archive should be .zip")
	}
}
