package update

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	// maxBackups is how many old binaries to keep in the backup dir.
	maxBackups = 3
)

// Installer atomically replaces the running binary and keeps backups.
type Installer struct {
	// BackupDir stores old binaries (default: <leleDir>/backups).
	BackupDir string
}

// NewInstaller creates an Installer storing backups in backupDir.
func NewInstaller(backupDir string) *Installer {
	return &Installer{BackupDir: backupDir}
}

// CurrentBinaryPath resolves the real path of the running executable,
// following symlinks.
func CurrentBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		// Fall back to the unresolved path if symlink resolution fails.
		return exe, nil
	}
	return resolved, nil
}

// Install atomically replaces targetPath with newBinaryPath.
// The previous binary is backed up first (unless it's a dev build path
// or backup fails non-fatally). Returns the backup path if created.
func (in *Installer) Install(newBinaryPath, targetPath string, currentVersion string) (backupPath string, err error) {
	if err := checkWritable(targetPath); err != nil {
		return "", err
	}

	// 1. Backup current binary.
	if in.BackupDir != "" {
		backupPath, err = in.backup(targetPath, currentVersion)
		if err != nil {
			// Backup failure is not fatal, but log-worthy.
			backupPath = ""
		}
	}

	// 2. Stage new binary next to target (same filesystem → atomic rename).
	dir := filepath.Dir(targetPath)
	tmp, err := os.CreateTemp(dir, ".lele-new-*")
	if err != nil {
		return backupPath, fmt.Errorf("staging new binary: %w", err)
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName) // no-op after successful rename

	if err := copyFile(newBinaryPath, tmpName); err != nil {
		return backupPath, fmt.Errorf("copying new binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0755); err != nil {
		return backupPath, err
	}

	// 3. Atomic swap.
	if runtime.GOOS == "windows" {
		// Windows cannot overwrite a running exe; rename it out of the way.
		oldPath := targetPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(targetPath, oldPath); err != nil {
			return backupPath, fmt.Errorf("moving current binary aside: %w", err)
		}
		if err := os.Rename(tmpName, targetPath); err != nil {
			// Try to restore.
			_ = os.Rename(oldPath, targetPath)
			return backupPath, fmt.Errorf("installing new binary: %w", err)
		}
		_ = os.Remove(oldPath)
	} else {
		if err := os.Rename(tmpName, targetPath); err != nil {
			return backupPath, fmt.Errorf("installing new binary: %w", err)
		}
	}

	in.pruneBackups()
	return backupPath, nil
}

// backup copies the current binary to BackupDir and returns its path.
func (in *Installer) backup(targetPath, currentVersion string) (string, error) {
	if err := os.MkdirAll(in.BackupDir, 0755); err != nil {
		return "", err
	}
	ver := strings.TrimPrefix(currentVersion, "v")
	if ver == "" || ver == "dev" {
		ver = "dev"
	}
	name := fmt.Sprintf("lele-%s-%s", ver, time.Now().Format("20060102-150405"))
	dst := filepath.Join(in.BackupDir, name)
	if err := copyFile(targetPath, dst); err != nil {
		return "", err
	}
	_ = os.Chmod(dst, 0755)
	return dst, nil
}

// ListBackups returns backup binaries, newest first.
func (in *Installer) ListBackups() ([]string, error) {
	entries, err := os.ReadDir(in.BackupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "lele-") {
			continue
		}
		paths = append(paths, filepath.Join(in.BackupDir, e.Name()))
	}
	// Newest first (names embed timestamps).
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths, nil
}

// LatestBackup returns the most recent backup path, or "" if none.
func (in *Installer) LatestBackup() (string, error) {
	backups, err := in.ListBackups()
	if err != nil || len(backups) == 0 {
		return "", err
	}
	return backups[0], nil
}

// pruneBackups keeps only the newest maxBackups backups.
func (in *Installer) pruneBackups() {
	backups, err := in.ListBackups()
	if err != nil || len(backups) <= maxBackups {
		return
	}
	for _, old := range backups[maxBackups:] {
		_ = os.Remove(old)
	}
}

// checkWritable verifies the target location can be replaced.
func checkWritable(targetPath string) error {
	dir := filepath.Dir(targetPath)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("target directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	// Probe write access by creating a temp file.
	probe, err := os.CreateTemp(dir, ".lele-write-test-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s (permissions?); try running with appropriate privileges: %w", dir, err)
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0755)
}
