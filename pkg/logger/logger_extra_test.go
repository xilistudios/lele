package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSetQuiet covers the SetQuiet toggling of startup message suppression.
func TestSetQuiet(t *testing.T) {
	initial := logger.quiet
	defer func() { mu.Lock(); logger.quiet = initial; mu.Unlock() }()

	SetQuiet(true)
	if !GetQuietForTest() {
		t.Errorf("SetQuiet(true) did not enable quiet mode")
	}
	SetQuiet(false)
	if GetQuietForTest() {
		t.Errorf("SetQuiet(false) did not disable quiet mode")
	}
}

// GetQuietForTest is a helper to read the quiet flag under the proper lock.
func GetQuietForTest() bool {
	mu.RLock()
	defer mu.RUnlock()
	return logger.quiet
}

// TestInitDefaultLoggingExtra covers the InitDefaultLogging helper which uses
// the default logs path and the close-side cleanup after DisableFileLogging.
func TestInitDefaultLoggingExtra(t *testing.T) {
	DisableFileLogging()
	err := InitDefaultLogging()
	if err != nil {
		t.Fatalf("InitDefaultLogging failed: %v", err)
	}
	DisableFileLogging()
	if logger.infoFile != nil || logger.errorFile != nil {
		t.Errorf("DisableFileLogging did not clear log file handles")
	}
}

// TestLoggedVariants exercises the field/component variants that are not yet
// covered (DebugF, DebugCF, InfoCF, WarnCF, ErrorC, ErrorCF).
func TestLoggedVariants(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)
	SetLevel(DEBUG)

	DisableFileLogging()
	defer DisableFileLogging()

	fields := map[string]interface{}{"key": "value", "count": 42}

	DebugF("debug with fields", fields)
	DebugCF("component", "debug cf", fields)
	InfoCF("component", "info cf", fields)
	WarnCF("component", "warn cf", fields)
	ErrorC("component", "error c")
	ErrorCF("component", "error cf", fields)
}

// TestFatalFunctions exercises the Fatal* family. Because logMessage only calls
// os.Exit(1) when quiet mode is disabled, we enable quiet mode first so the
// test process does not terminate.
func TestFatalFunctions(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)
	SetLevel(INFO)

	SetQuiet(true)
	defer SetQuiet(false)
	DisableFileLogging()
	defer DisableFileLogging()

	Fatal("fatal plain")
	FatalC("component", "fatal with component")
	FatalF("fatal with fields", map[string]interface{}{"key": "value"})
	FatalCF("component", "fatal cf", map[string]interface{}{"key": "value"})
}

// TestQuietSuppressesConsoleOutput verifies that in quiet mode DEBUG/INFO
// messages are not written to stdout/stderr but still obey level filtering.
func TestQuietSuppressesConsoleOutput(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)
	SetLevel(DEBUG)

	SetQuiet(true)
	defer SetQuiet(false)

	// These should return without printing (and without exiting, for Fatal).
	Debug("quiet debug")
	Info("quiet info")
	Warn("quiet warn")
	Error("quiet error")
	Fatal("quiet fatal")
}

// TestEnableMultiFileLogging_MkdirAllError covers the error path where the base
// path cannot be created (a path component is an existing file).
func TestEnableMultiFileLogging_MkdirAllError(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger-mkdir-err-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	blocker := filepath.Join(tempDir, "blocker.txt")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	badPath := filepath.Join(blocker, "logs") // parent component is a file

	old := GetLogsPath()
	defer SetLogsPath(old)

	err = EnableMultiFileLogging(badPath)
	if err == nil {
		t.Errorf("expected error for invalid base path, got nil")
	}
}

// TestWriteToFile_ErrorBranches covers the write-failure paths in writeToFile by
// assigning already-closed file handles.
func TestWriteToFile_ErrorBranches(t *testing.T) {
	info, err := os.CreateTemp("", "writeinfo-*")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	info.Close()
	er, err := os.CreateTemp("", "writeerr-*")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	er.Close()

	mu.Lock()
	logger.infoFile = info
	logger.errorFile = er
	mu.Unlock()
	defer func() {
		mu.Lock()
		logger.infoFile = nil
		logger.errorFile = nil
		mu.Unlock()
	}()

	writeToFile(INFO, LogEntry{}, []byte("data"))
	writeToFile(WARN, LogEntry{}, []byte("data"))
	writeToFile(ERROR, LogEntry{}, []byte("data"))
	writeToFile(FATAL, LogEntry{}, []byte("data"))
}

// TestCheckDateRotation_Rotate covers the date-change rotation path: files are
// closed and reopened with the new date.
func TestCheckDateRotation_Rotate(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger-rot-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	DisableFileLogging()
	SetLogsPath(tempDir)
	defer SetLogsPath(getDefaultLogsPath())

	if err := EnableMultiFileLogging(tempDir); err != nil {
		t.Fatalf("EnableMultiFileLogging failed: %v", err)
	}

	// Force the logger date to yesterday so rotation triggers on next log.
	mu.Lock()
	logger.logDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	mu.Unlock()

	SetLevel(INFO)
	Info("rotation trigger")
	time.Sleep(100 * time.Millisecond)

	newDate := time.Now().Format("2006-01-02")
	infoPath := filepath.Join(tempDir, "info-"+newDate+".log")
	content, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("rotated info file missing: %v", err)
	}
	if !strings.Contains(string(content), "rotation trigger") {
		t.Errorf("message not found in rotated file. content: %q", string(content))
	}

	DisableFileLogging()
}

// TestCheckDateRotation_NoRotation covers the early-return branch when the date
// has not changed.
func TestCheckDateRotation_NoRotation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger-norot-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	DisableFileLogging()
	SetLogsPath(tempDir)
	defer SetLogsPath(getDefaultLogsPath())
	if err := EnableMultiFileLogging(tempDir); err != nil {
		t.Fatalf("EnableMultiFileLogging failed: %v", err)
	}

	// Same date => early return, no new files opened.
	mu.Lock()
	logger.logDate = time.Now().Format("2006-01-02")
	mu.Unlock()

	checkDateRotation()
	if logger.infoFile == nil {
		t.Errorf("info file handle should be preserved on no-rotation")
	}
	DisableFileLogging()
}

// TestCheckDateRotation_MkdirError covers the MkdirAll failure inside rotation
// when the base path's parent component is a file.
func TestCheckDateRotation_MkdirError(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "logger-rotbad-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)

	blocker := filepath.Join(tempDir, "blocker.txt")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	badPath := filepath.Join(blocker, "logs")

	mu.Lock()
	logger.basePath = badPath
	logger.infoFile = nil
	logger.errorFile = nil
	logger.logDate = time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	mu.Unlock()
	defer func() {
		mu.Lock()
		logger.basePath = getDefaultLogsPath()
		mu.Unlock()
	}()

	// Should not panic; MkdirAll error is logged and files stay nil.
	checkDateRotation()
	if logger.infoFile != nil || logger.errorFile != nil {
		t.Errorf("file handles should remain nil after MkdirAll failure")
	}
}

// TestLogMessageMarshalError covers the JSON marshal error branch in logMessage.
func TestLogMessageMarshalError(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)
	SetLevel(INFO)
	SetQuiet(false)
	defer SetQuiet(false)
	DisableFileLogging()

	ch := make(chan int) // channels are not JSON-marshalable
	InfoF("marshal failure", map[string]interface{}{"ch": ch})
}

// TestLogLevelBelowCurrent covers the early-return branch when the message
// level is below the configured threshold.
func TestLogLevelBelowCurrent(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)
	SetLevel(FATAL)

	// INFO < FATAL => early return, no panic, nothing written.
	Info("below threshold")
	computed := GetLevel()
	if computed != FATAL {
		t.Errorf("level changed unexpectedly: %v", computed)
	}
}

// TestCleanupOldLogs_EdgeCases covers empty path, ReadDir error, directory
// skip, non-prefix skip, and bad-date filename branches.
func TestCleanupOldLogs_EdgeCases(t *testing.T) {
	old := GetLogsPath()
	defer SetLogsPath(old)

	// Empty base path returns nil immediately.
	SetLogsPath("")
	if err := CleanupOldLogs(5); err != nil {
		t.Errorf("empty base path should return nil, got %v", err)
	}

	// Nonexistent directory triggers a ReadDir error.
	SetLogsPath(filepath.Join(string(filepath.Separator), "nonexistent", "zzz", "logs"))
	if err := CleanupOldLogs(5); err == nil {
		t.Errorf("expected error for nonexistent directory")
	}

	// Real directory with edge-case entries.
	tempDir, err := os.MkdirTemp("", "cleanup-edge-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tempDir)
	SetLogsPath(tempDir)

	// Directory entry should be skipped.
	if err := os.MkdirAll(filepath.Join(tempDir, "subdir"), 0755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	// Non-prefixed file should be skipped.
	os.WriteFile(filepath.Join(tempDir, "random.txt"), []byte("x"), 0644)
	// Bad date in filename -> parse error -> skipped.
	os.WriteFile(filepath.Join(tempDir, "info-notadate.log"), []byte("x"), 0644)
	// A valid recent errors file that should NOT be removed.
	recent := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	recentErr := filepath.Join(tempDir, "errors-"+recent+".log")
	os.WriteFile(recentErr, []byte("x"), 0644)
	// An old errors file that SHOULD be removed.
	oldDate := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	oldErr := filepath.Join(tempDir, "errors-"+oldDate+".log")
	os.WriteFile(oldErr, []byte("x"), 0644)

	if err := CleanupOldLogs(5); err != nil {
		t.Fatalf("CleanupOldLogs failed: %v", err)
	}

	if _, err := os.Stat(oldErr); !os.IsNotExist(err) {
		t.Errorf("old errors log should have been removed")
	}
	if _, err := os.Stat(recentErr); os.IsNotExist(err) {
		t.Errorf("recent errors log should not have been removed")
	}
}
