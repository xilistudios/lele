package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogLevelFiltering(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)

	SetLevel(WARN)

	tests := []struct {
		name      string
		level     LogLevel
		shouldLog bool
	}{
		{"DEBUG message", DEBUG, false},
		{"INFO message", INFO, false},
		{"WARN message", WARN, true},
		{"ERROR message", ERROR, true},
		{"FATAL message", FATAL, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch tt.level {
			case DEBUG:
				Debug(tt.name)
			case INFO:
				Info(tt.name)
			case WARN:
				Warn(tt.name)
			case ERROR:
				Error(tt.name)
			case FATAL:
				if tt.shouldLog {
					t.Logf("FATAL test skipped to prevent program exit")
				}
			}
		})
	}

	SetLevel(INFO)
}

func TestLoggerWithComponent(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)

	SetLevel(DEBUG)

	tests := []struct {
		name      string
		component string
		message   string
		fields    map[string]interface{}
	}{
		{"Simple message", "test", "Hello, world!", nil},
		{"Message with component", "discord", "Discord message", nil},
		{"Message with fields", "telegram", "Telegram message", map[string]interface{}{
			"user_id": "12345",
			"count":   42,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			switch {
			case tt.fields == nil && tt.component != "":
				InfoC(tt.component, tt.message)
			case tt.fields != nil:
				InfoF(tt.message, tt.fields)
			default:
				Info(tt.message)
			}
		})
	}

	SetLevel(INFO)
}

func TestLogLevels(t *testing.T) {
	tests := []struct {
		name  string
		level LogLevel
		want  string
	}{
		{"DEBUG level", DEBUG, "DEBUG"},
		{"INFO level", INFO, "INFO"},
		{"WARN level", WARN, "WARN"},
		{"ERROR level", ERROR, "ERROR"},
		{"FATAL level", FATAL, "FATAL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if logLevelNames[tt.level] != tt.want {
				t.Errorf("logLevelNames[%d] = %s, want %s", tt.level, logLevelNames[tt.level], tt.want)
			}
		})
	}
}

func TestSetGetLevel(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)

	tests := []LogLevel{DEBUG, INFO, WARN, ERROR, FATAL}

	for _, level := range tests {
		SetLevel(level)
		if GetLevel() != level {
			t.Errorf("SetLevel(%v) -> GetLevel() = %v, want %v", level, GetLevel(), level)
		}
	}
}

func TestLoggerHelperFunctions(t *testing.T) {
	initialLevel := GetLevel()
	defer SetLevel(initialLevel)

	SetLevel(INFO)

	Debug("This should not log")
	Info("This should log")
	Warn("This should log")
	Error("This should log")

	InfoC("test", "Component message")
	InfoF("Fields message", map[string]interface{}{"key": "value"})

	WarnC("test", "Warning with component")
	ErrorF("Error with fields", map[string]interface{}{"error": "test"})

	SetLevel(DEBUG)
	DebugC("test", "Debug with component")
	WarnF("Warning with fields", map[string]interface{}{"key": "value"})
}

func TestEnableMultiFileLogging(t *testing.T) {
	// Create temporary directory for testing
	tempDir, err := os.MkdirTemp("", "logger-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Disable file logging first
	DisableFileLogging()

	// Enable multi-file logging
	err = EnableMultiFileLogging(tempDir)
	if err != nil {
		t.Fatalf("EnableMultiFileLogging failed: %v", err)
	}

	// Verify directory was created
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		t.Errorf("Logs directory was not created")
	}

	// Check that log files were created with correct date format
	currentDate := time.Now().Format("2006-01-02")
	infoPath := filepath.Join(tempDir, fmt.Sprintf("info-%s.log", currentDate))
	errorPath := filepath.Join(tempDir, fmt.Sprintf("errors-%s.log", currentDate))

	if _, err := os.Stat(infoPath); os.IsNotExist(err) {
		t.Errorf("Info log file was not created at %s", infoPath)
	}

	if _, err := os.Stat(errorPath); os.IsNotExist(err) {
		t.Errorf("Error log file was not created at %s", errorPath)
	}

	// Test logging to files
	SetLevel(INFO)
	Info("Test info message for file")
	Warn("Test warn message for file")
	Error("Test error message for file")

	// Give files time to flush
	time.Sleep(100 * time.Millisecond)

	// Verify info log contains INFO message
	infoContent, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("Failed to read info log: %v", err)
	}
	if !strings.Contains(string(infoContent), "Test info message") {
		t.Errorf("Info log does not contain expected message. Content: %s", string(infoContent))
	}

	// Verify error log contains WARN and ERROR messages
	errorContent, err := os.ReadFile(errorPath)
	if err != nil {
		t.Fatalf("Failed to read error log: %v", err)
	}
	if !strings.Contains(string(errorContent), "Test warn message") {
		t.Errorf("Error log does not contain warn message. Content: %s", string(errorContent))
	}
	if !strings.Contains(string(errorContent), "Test error message") {
		t.Errorf("Error log does not contain error message. Content: %s", string(errorContent))
	}

	// Cleanup
	DisableFileLogging()
}

func TestGetLogsPath(t *testing.T) {
	// Test default path calculation
	defaultPath := getDefaultLogsPath()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("Could not get home directory: %v", err)
	}
	expectedPath := filepath.Join(home, ".lele", "logs")

	if defaultPath != expectedPath {
		t.Errorf("getDefaultLogsPath() = %s, want %s", defaultPath, expectedPath)
	}

	// Test that SetLogsPath/GetLogsPath work correctly
	initialPath := GetLogsPath()
	customPath := "/custom/logs/path"
	SetLogsPath(customPath)

	if GetLogsPath() != customPath {
		t.Errorf("GetLogsPath() = %s, want %s", GetLogsPath(), customPath)
	}

	// Restore initial path
	SetLogsPath(initialPath)
}

func TestInitDefaultLogging(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "logger-default-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Store current state
	currentPath := GetLogsPath()

	// Disable and test with custom path using EnableMultiFileLogging
	DisableFileLogging()

	err = EnableMultiFileLogging(tempDir)
	if err != nil {
		t.Fatalf("EnableMultiFileLogging failed: %v", err)
	}

	// Verify log files exist in the temp directory
	currentDate := time.Now().Format("2006-01-02")
	infoPath := filepath.Join(tempDir, fmt.Sprintf("info-%s.log", currentDate))
	errorPath := filepath.Join(tempDir, fmt.Sprintf("errors-%s.log", currentDate))

	if _, err := os.Stat(infoPath); os.IsNotExist(err) {
		t.Errorf("Info log file was not created at %s", infoPath)
	}

	if _, err := os.Stat(errorPath); os.IsNotExist(err) {
		t.Errorf("Error log file was not created at %s", errorPath)
	}

	// Cleanup
	DisableFileLogging()
	SetLogsPath(currentPath)
}

func TestCleanupOldLogs(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "logger-cleanup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create old log files
	oldDate := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	recentDate := time.Now().AddDate(0, 0, -1).Format("2006-01-02")

	oldInfoLog := filepath.Join(tempDir, fmt.Sprintf("info-%s.log", oldDate))
	oldErrorLog := filepath.Join(tempDir, fmt.Sprintf("errors-%s.log", oldDate))
	recentInfoLog := filepath.Join(tempDir, fmt.Sprintf("info-%s.log", recentDate))

	// Create files
	os.WriteFile(oldInfoLog, []byte("old info"), 0644)
	os.WriteFile(oldErrorLog, []byte("old error"), 0644)
	os.WriteFile(recentInfoLog, []byte("recent info"), 0644)

	// Set up logger with temp path
	SetLogsPath(tempDir)
	defer SetLogsPath(getDefaultLogsPath())

	// Run cleanup with maxDays=5
	err = CleanupOldLogs(5)
	if err != nil {
		t.Fatalf("CleanupOldLogs failed: %v", err)
	}

	// Verify old files were deleted
	if _, err := os.Stat(oldInfoLog); !os.IsNotExist(err) {
		t.Errorf("Old info log should have been deleted")
	}
	if _, err := os.Stat(oldErrorLog); !os.IsNotExist(err) {
		t.Errorf("Old error log should have been deleted")
	}

	// Verify recent file still exists
	if _, err := os.Stat(recentInfoLog); os.IsNotExist(err) {
		t.Errorf("Recent info log should not have been deleted")
	}
}

func TestCheckDateRotation(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "logger-rotation-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Disable and set up initial logging
	DisableFileLogging()
	SetLogsPath(tempDir)
	defer SetLogsPath(getDefaultLogsPath())

	// Enable logging
	err = EnableMultiFileLogging(tempDir)
	if err != nil {
		t.Fatalf("EnableMultiFileLogging failed: %v", err)
	}

	// Get current log files
	currentDate := time.Now().Format("2006-01-02")
	infoPath := filepath.Join(tempDir, fmt.Sprintf("info-%s.log", currentDate))

	// Write a message to verify it's working
	Info("Test before rotation")
	time.Sleep(100 * time.Millisecond)

	// Verify message was written
	content, _ := os.ReadFile(infoPath)
	if !strings.Contains(string(content), "Test before rotation") {
		t.Errorf("Message was not written to log file")
	}

	DisableFileLogging()
}

func TestDebugOnlyConsole(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "logger-debug-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Set up logging
	DisableFileLogging()
	SetLevel(DEBUG)
	defer SetLevel(INFO)

	err = EnableMultiFileLogging(tempDir)
	if err != nil {
		t.Fatalf("EnableMultiFileLogging failed: %v", err)
	}

	// Write debug message (should NOT go to file)
	Debug("This debug message should not be in file")
	DebugC("test", "This debug with component should not be in file")

	// Write info message (should go to file)
	Info("This info message should be in file")

	time.Sleep(100 * time.Millisecond)

	// Check info log
	currentDate := time.Now().Format("2006-01-02")
	infoPath := filepath.Join(tempDir, fmt.Sprintf("info-%s.log", currentDate))
	content, _ := os.ReadFile(infoPath)

	if strings.Contains(string(content), "debug message") {
		t.Errorf("DEBUG message should not be in log file")
	}

	if !strings.Contains(string(content), "info message") {
		t.Errorf("INFO message should be in log file")
	}

	DisableFileLogging()
}

func TestErrorInBothFiles(t *testing.T) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "logger-error-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Set up logging
	DisableFileLogging()
	err = EnableMultiFileLogging(tempDir)
	if err != nil {
		t.Fatalf("EnableMultiFileLogging failed: %v", err)
	}

	// Write error message (should be in both files)
	Error("This error should be in both files")

	time.Sleep(100 * time.Millisecond)

	// Check info log
	currentDate := time.Now().Format("2006-01-02")
	infoPath := filepath.Join(tempDir, fmt.Sprintf("info-%s.log", currentDate))
	infoContent, _ := os.ReadFile(infoPath)

	// Check error log
	errorPath := filepath.Join(tempDir, fmt.Sprintf("errors-%s.log", currentDate))
	errorContent, _ := os.ReadFile(errorPath)

	if !strings.Contains(string(infoContent), "This error should be in both files") {
		t.Errorf("ERROR message should be in info log")
	}

	if !strings.Contains(string(errorContent), "This error should be in both files") {
		t.Errorf("ERROR message should be in error log")
	}

	DisableFileLogging()
}
