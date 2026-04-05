package logger

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

var (
	logLevelNames = map[LogLevel]string{
		DEBUG: "DEBUG",
		INFO:  "INFO",
		WARN:  "WARN",
		ERROR: "ERROR",
		FATAL: "FATAL",
	}

	currentLevel = INFO
	logger       *Logger
	once         sync.Once
	mu           sync.RWMutex
)

type Logger struct {
	infoFile  *os.File
	errorFile *os.File
	logDate   string // Current date for rotation (format: "2006-01-02")
	basePath  string // Base path for logs directory
}

type LogEntry struct {
	Level     string                 `json:"level"`
	Timestamp string                 `json:"timestamp"`
	Component string                 `json:"component,omitempty"`
	Message   string                 `json:"message"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	Caller    string                 `json:"caller,omitempty"`
}

func init() {
	once.Do(func() {
		logger = &Logger{
			basePath: getDefaultLogsPath(),
		}
		// Initialize default logging automatically (silent if fails)
		_ = InitDefaultLogging()
	})
}

func getDefaultLogsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".lele", "logs")
	}
	return filepath.Join(home, ".lele", "logs")
}

// SetLevel sets the minimum log level to output
func SetLevel(level LogLevel) {
	mu.Lock()
	defer mu.Unlock()
	currentLevel = level
}

// GetLevel returns the current log level
func GetLevel() LogLevel {
	mu.RLock()
	defer mu.RUnlock()
	return currentLevel
}

// GetLogsPath returns the base path for logs
func GetLogsPath() string {
	mu.RLock()
	defer mu.RUnlock()
	return logger.basePath
}

// SetLogsPath sets a custom base path for logs
func SetLogsPath(basePath string) {
	mu.Lock()
	defer mu.Unlock()
	logger.basePath = basePath
}

// InitDefaultLogging initializes logging with default path (~/.lele/logs)
func InitDefaultLogging() error {
	return EnableMultiFileLogging(getDefaultLogsPath())
}

// EnableMultiFileLogging configures dual-file logging with daily rotation
// INFO logs go to info-{date}.log
// WARN, ERROR, FATAL logs go to errors-{date}.log
func EnableMultiFileLogging(basePath string) error {
	mu.Lock()
	defer mu.Unlock()

	logger.basePath = basePath

	// Create logs directory if it doesn't exist
	if err := os.MkdirAll(basePath, 0755); err != nil {
		log.Printf("Warning: could not create logs directory: %v\n", err)
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Close existing files
	if logger.infoFile != nil {
		logger.infoFile.Close()
	}
	if logger.errorFile != nil {
		logger.errorFile.Close()
	}

	// Get current date
	currentDate := time.Now().Format("2006-01-02")
	logger.logDate = currentDate

	// Open info log file (INFO level)
	infoPath := filepath.Join(basePath, fmt.Sprintf("info-%s.log", currentDate))
	infoFile, err := os.OpenFile(infoPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Warning: could not open info log file: %v\n", err)
		return fmt.Errorf("failed to open info log file: %w", err)
	}

	// Open error log file (WARN, ERROR, FATAL levels)
	errorPath := filepath.Join(basePath, fmt.Sprintf("errors-%s.log", currentDate))
	errorFile, err := os.OpenFile(errorPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		infoFile.Close()
		log.Printf("Warning: could not open error log file: %v\n", err)
		return fmt.Errorf("failed to open error log file: %w", err)
	}

	logger.infoFile = infoFile
	logger.errorFile = errorFile

	log.Printf("Multi-file logging enabled: info-%s.log, errors-%s.log in %s\n", currentDate, currentDate, basePath)
	return nil
}

// DisableFileLogging disables file logging
func DisableFileLogging() {
	mu.Lock()
	defer mu.Unlock()

	if logger.infoFile != nil {
		logger.infoFile.Close()
		logger.infoFile = nil
	}
	if logger.errorFile != nil {
		logger.errorFile.Close()
		logger.errorFile = nil
	}
	log.Println("File logging disabled")
}

// checkDateRotation checks if the day has changed and rotates files if needed
func checkDateRotation() {
	mu.Lock()
	defer mu.Unlock()

	currentDate := time.Now().Format("2006-01-02")
	if currentDate == logger.logDate {
		return
	}

	// Day has changed, rotate files
	logger.logDate = currentDate

	// Close existing files
	if logger.infoFile != nil {
		logger.infoFile.Close()
	}
	if logger.errorFile != nil {
		logger.errorFile.Close()
	}

	// Open new files with current date
	if logger.basePath != "" {
		// Ensure directory exists
		os.MkdirAll(logger.basePath, 0755)

		infoPath := filepath.Join(logger.basePath, fmt.Sprintf("info-%s.log", currentDate))
		if infoFile, err := os.OpenFile(infoPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			logger.infoFile = infoFile
		}

		errorPath := filepath.Join(logger.basePath, fmt.Sprintf("errors-%s.log", currentDate))
		if errorFile, err := os.OpenFile(errorPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
			logger.errorFile = errorFile
		}
	}
}

// writeToFile writes the log entry to appropriate file based on level
func writeToFile(level LogLevel, entry LogEntry, jsonData []byte) {
	mu.RLock()
	defer mu.RUnlock()

	// Only write to file for INFO and above (DEBUG only goes to console)
	switch level {
	case INFO:
		if logger.infoFile != nil {
			logger.infoFile.WriteString(string(jsonData) + "\n")
		}
	case WARN, ERROR, FATAL:
		// Write to both files for errors (errors are important)
		if logger.errorFile != nil {
			logger.errorFile.WriteString(string(jsonData) + "\n")
		}
		if logger.infoFile != nil {
			logger.infoFile.WriteString(string(jsonData) + "\n")
		}
	}
}

func logMessage(level LogLevel, component string, message string, fields map[string]interface{}) {
	if level < currentLevel {
		return
	}

	// Check for date rotation before logging
	checkDateRotation()

	entry := LogEntry{
		Level:     logLevelNames[level],
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Component: component,
		Message:   message,
		Fields:    fields,
	}

	if pc, file, line, ok := runtime.Caller(2); ok {
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			entry.Caller = fmt.Sprintf("%s:%d (%s)", file, line, fn.Name())
		}
	}

	// Write to file (for INFO and above)
	if level >= INFO {
		jsonData, err := json.Marshal(entry)
		if err == nil {
			writeToFile(level, entry, jsonData)
		}
	}

	var fieldStr string
	if len(fields) > 0 {
		fieldStr = " " + formatFields(fields)
	}

	logLine := fmt.Sprintf("[%s] [%s]%s %s%s",
		entry.Timestamp,
		logLevelNames[level],
		formatComponent(component),
		message,
		fieldStr,
	)

	log.Println(logLine)

	if level == FATAL {
		os.Exit(1)
	}
}

func formatComponent(component string) string {
	if component == "" {
		return ""
	}
	return fmt.Sprintf(" %s:", component)
}

func formatFields(fields map[string]interface{}) string {
	var parts []string
	for k, v := range fields {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return fmt.Sprintf("{%s}", strings.Join(parts, ", "))
}

// CleanupOldLogs removes log files older than maxDays
func CleanupOldLogs(maxDays int) error {
	mu.RLock()
	basePath := logger.basePath
	mu.RUnlock()

	if basePath == "" {
		return nil
	}

	entries, err := os.ReadDir(basePath)
	if err != nil {
		return err
	}

	cutoffTime := time.Now().AddDate(0, 0, -maxDays)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Check if it's a log file
		if !(strings.HasPrefix(name, "info-") || strings.HasPrefix(name, "errors-")) {
			continue
		}

		// Parse date from filename
		dateStr := ""
		if strings.HasPrefix(name, "info-") {
			dateStr = strings.TrimPrefix(name, "info-")
		} else {
			dateStr = strings.TrimPrefix(name, "errors-")
		}
		dateStr = strings.TrimSuffix(dateStr, ".log")

		fileDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}

		if fileDate.Before(cutoffTime) {
			os.Remove(filepath.Join(basePath, name))
		}
	}

	return nil
}

// Debug logs a debug message (console only, not written to file)
func Debug(message string) {
	logMessage(DEBUG, "", message, nil)
}

func DebugC(component string, message string) {
	logMessage(DEBUG, component, message, nil)
}

func DebugF(message string, fields map[string]interface{}) {
	logMessage(DEBUG, "", message, fields)
}

func DebugCF(component string, message string, fields map[string]interface{}) {
	logMessage(DEBUG, component, message, fields)
}

func Info(message string) {
	logMessage(INFO, "", message, nil)
}

func InfoC(component string, message string) {
	logMessage(INFO, component, message, nil)
}

func InfoF(message string, fields map[string]interface{}) {
	logMessage(INFO, "", message, fields)
}

func InfoCF(component string, message string, fields map[string]interface{}) {
	logMessage(INFO, component, message, fields)
}

func Warn(message string) {
	logMessage(WARN, "", message, nil)
}

func WarnC(component string, message string) {
	logMessage(WARN, component, message, nil)
}

func WarnF(message string, fields map[string]interface{}) {
	logMessage(WARN, "", message, fields)
}

func WarnCF(component string, message string, fields map[string]interface{}) {
	logMessage(WARN, component, message, fields)
}

func Error(message string) {
	logMessage(ERROR, "", message, nil)
}

func ErrorC(component string, message string) {
	logMessage(ERROR, component, message, nil)
}

func ErrorF(message string, fields map[string]interface{}) {
	logMessage(ERROR, "", message, fields)
}

func ErrorCF(component string, message string, fields map[string]interface{}) {
	logMessage(ERROR, component, message, fields)
}

func Fatal(message string) {
	logMessage(FATAL, "", message, nil)
}

func FatalC(component string, message string) {
	logMessage(FATAL, component, message, nil)
}

func FatalF(message string, fields map[string]interface{}) {
	logMessage(FATAL, "", message, fields)
}

func FatalCF(component string, message string, fields map[string]interface{}) {
	logMessage(FATAL, component, message, fields)
}
