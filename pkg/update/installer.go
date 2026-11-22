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
	maxBackups = 3
)

type Installer struct {
	BackupDir string
}

func NewInstaller(backupDir string) *Installer {
	return &Installer{BackupDir: backupDir}
}

func CurrentBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

func (in *Installer) Install(newBinaryPath, targetPath string, currentVersion string) (backupPath string, err error) {
	if err := checkWritable(targetPath); err != nil {
		return "", err
	}

	if in.BackupDir != "" {
		backupPath, err = in.backup(targetPath, currentVersion)
		if err != nil {
			backupPath = ""
		}
	}

	dir := filepath.Dir(targetPath)
	tmp, err := os.CreateTemp(dir, ".lele-new-*")
	if err != nil {
		return backupPath, fmt.Errorf("staging new binary: %w", err)
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)

	if err := copyFile(newBinaryPath, tmpName); err != nil {
		return backupPath, fmt.Errorf("copying new binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0755); err != nil {
		return backupPath, err
	}

	if runtime.GOOS == "windows" {
		oldPath := targetPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(targetPath, oldPath); err != nil {
			return backupPath, fmt.Errorf("moving current binary aside: %w", err)
		}
		if err := os.Rename(tmpName, targetPath); err != nil {
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
	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	return paths, nil
}

func (in *Installer) LatestBackup() (string, error) {
	backups, err := in.ListBackups()
	if err != nil || len(backups) == 0 {
		return "", err
	}
	return backups[0], nil
}

func (in *Installer) pruneBackups() {
	backups, err := in.ListBackups()
	if err != nil || len(backups) <= maxBackups {
		return
	}
	for _, old := range backups[maxBackups:] {
		_ = os.Remove(old)
	}
}

func checkWritable(targetPath string) error {
	dir := filepath.Dir(targetPath)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("target directory %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
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
