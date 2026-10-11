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

	"github.com/xilistudios/lele/pkg/config"
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
	quiet     bool   // Suppress stdout/stderr messages (for TUI mode)
	enabled   bool   // File logging enabled: gates lazy opens and writes (see DisableFileLogging)
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
	})
}

func getDefaultLogsPath() string {
	return filepath.Join(config.GetLeleDir(), "logs")
}

// SetLevel sets the minimum log level to output
func SetLevel(level LogLevel) {
	mu.Lock()
	defer mu.Unlock()
	currentLevel = level
}

// SetQuiet suppresses stdout/stderr startup messages (for TUI mode).
// Must be called before InitDefaultLogging or EnableMultiFileLogging.
func SetQuiet(q bool) {
	mu.Lock()
	defer mu.Unlock()
	logger.quiet = q
}

// GetLevel returns the current log level
func GetLevel() LogLevel {
	mu.RLock()
	defer mu.RUnlock()
	return currentLevel
}

// GetLogsPath returns the base path for logs.
//
// Locking: it takes mu.RLock. Callers that already hold the logger write lock
// (mu.Lock — the write path, EnableMultiFileLogging, checkDateRotation) must
// NOT call it or LogPathFor: they must use the internal basePath-based
// builder (logFilePath/openLogFile) instead, otherwise mu self-deadlocks.
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

// logFilePrefixes maps the public API level strings to the canonical log file
// prefixes. The error log uses the historical "errors-" (plural) spelling so
// existing files on disk stay valid.
var logFilePrefixes = map[string]string{
	"info":  "info-",
	"error": "errors-",
}

// LogFileName returns the canonical log file name for the given API level
// ("info" or "error") and date (YYYY-MM-DD). It is the single source of truth
// for the log filename contract shared by the writer and log readers (REST
// API / WebUI).
func LogFileName(level string, date string) (string, error) {
	prefix, ok := logFilePrefixes[level]
	if !ok {
		return "", fmt.Errorf("unknown log level %q: must be \"info\" or \"error\"", level)
	}
	return prefix + date + ".log", nil
}

// LogPathFor returns the full path of the log file for the given API level and
// date under the configured logs directory (see GetLogsPath).
//
// It resolves the base path through GetLogsPath (mu.RLock), so callers that
// already hold mu (the write path, EnableMultiFileLogging, checkDateRotation)
// must use the lock-free logFilePath(basePath, ...) builder instead: calling
// LogPathFor while holding mu would self-deadlock.
func LogPathFor(level string, date string) (string, error) {
	name, err := LogFileName(level, date)
	if err != nil {
		return "", err
	}
	return filepath.Join(GetLogsPath(), name), nil
}

// logFilePath builds the path of the canonical log file for apiLevel and date
// under basePath without taking any lock. It is the lock-free counterpart of
// LogPathFor for code that already holds mu (see the LogPathFor doc comment).
func logFilePath(basePath, apiLevel, date string) (string, error) {
	name, err := LogFileName(apiLevel, date)
	if err != nil {
		return "", err
	}
	return filepath.Join(basePath, name), nil
}

// openLogFile creates basePath if needed and opens the canonical log file for
// apiLevel/date in append mode. It takes no lock: callers must hold mu (or
// otherwise have exclusive access to the logger state).
func openLogFile(basePath, apiLevel, date string) (*os.File, error) {
	path, err := logFilePath(basePath, apiLevel, date)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("%s: could not create logs directory: %w", basePath, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

// LogDateFromFileName returns the date encoded in a canonical log file name
// produced by LogFileName (e.g. "info-2026-10-10.log" → "2026-10-10"), or
// ok=false if name is not a canonical log file with a valid YYYY-MM-DD date.
// It is the single source of truth for the reverse of the filename contract,
// shared by the writer (log cleanup) and log readers (REST API / WebUI).
func LogDateFromFileName(name string) (string, bool) {
	if !strings.HasSuffix(name, ".log") {
		return "", false
	}
	for _, level := range []string{"info", "error"} {
		fname, err := LogFileName(level, "")
		if err != nil {
			continue
		}
		prefix := strings.TrimSuffix(fname, ".log")
		if strings.HasPrefix(name, prefix) {
			date := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".log")
			if _, err := time.Parse("2006-01-02", date); err != nil {
				return "", false
			}
			return date, true
		}
	}
	return "", false
}

// InitDefaultLogging initializes logging with default path (~/.lele/logs)
func InitDefaultLogging() error {
	return EnableMultiFileLogging(getDefaultLogsPath())
}

// EnableMultiFileLogging configures multi-file logging with daily rotation.
// Routing is strictly by level, with no duplication:
//   - INFO logs go to info-{date}.log only
//   - WARN, ERROR, FATAL logs go to errors-{date}.log only
//
// The filename contract lives in LogFileName/LogPathFor, shared with log
// readers (REST API / WebUI).
func EnableMultiFileLogging(basePath string) error {
	mu.Lock()
	defer mu.Unlock()

	logger.basePath = basePath

	// Create logs directory if it doesn't exist
	if err := os.MkdirAll(basePath, 0755); err != nil {
		log.Printf("Warning: could not create logs directory: %v\n", err)
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// File logging is now on: this must be set before (and even when) the
	// opens below fail, because a failed open is retried lazily by the next
	// write (H1 fix), and that lazy open is gated on enabled. All of this
	// runs under mu, so no write can observe a half-enabled state.
	logger.enabled = true

	// Close existing files and clear the handles so the struct never keeps
	// stale (closed) fds around.
	if logger.infoFile != nil {
		logger.infoFile.Close()
		logger.infoFile = nil
	}
	if logger.errorFile != nil {
		logger.errorFile.Close()
		logger.errorFile = nil
	}

	// Get current date
	currentDate := time.Now().Format("2006-01-02")
	logger.logDate = currentDate

	// Open info log file (INFO level)
	infoFile, err := openLogFile(basePath, "info", currentDate)
	if err != nil {
		log.Printf("Warning: could not open info log file: %v\n", err)
		return fmt.Errorf("failed to open info log file: %w", err)
	}

	// Open error log file (WARN, ERROR, FATAL levels). On failure, drop the
	// freshly opened info handle too so we don't leak an fd. The handles stay
	// nil afterwards and the next write lazily retries the open (see
	// writeToFile), so a failed startup open is no longer permanent.
	errorFile, err := openLogFile(basePath, "error", currentDate)
	if err != nil {
		infoFile.Close()
		log.Printf("Warning: could not open error log file: %v\n", err)
		return fmt.Errorf("failed to open error log file: %w", err)
	}

	logger.infoFile = infoFile
	logger.errorFile = errorFile

	if !logger.quiet {
		log.Printf("Multi-file logging enabled: info-%s.log, errors-%s.log in %s\n", currentDate, currentDate, basePath)
	}
	return nil
}

// DisableFileLogging disables file logging.
//
// It closes the handles and flips the enabled flag, but deliberately keeps
// basePath (and logDate) set: basePath is the read-side contract used by
// LogPathFor/GetLogsPath (REST API / WebUI log readers) and must keep
// resolving after a Disable. The enabled flag is what stops the lazy open in
// the write path from recreating the log files on the next write.
func DisableFileLogging() {
	mu.Lock()
	defer mu.Unlock()

	// Flip the flag first, under the same lock as the write path: no write
	// can slip through between closing the handles and disabling logging.
	logger.enabled = false

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

	// When file logging is disabled, rotation must be a no-op: reopening
	// files here would resurrect the very files DisableFileLogging closed.
	if !logger.enabled {
		return
	}

	currentDate := time.Now().Format("2006-01-02")
	if currentDate == logger.logDate {
		return
	}

	// Day has changed, rotate files
	logger.logDate = currentDate

	// Close existing files and clear the handles; a failed reopen below must
	// not leave closed fds behind (writeToFile would then error on every
	// entry). A handle left nil by a failed reopen is retried by the next
	// write (lazy open in writeToFile), not only on the next rotation.
	if logger.infoFile != nil {
		logger.infoFile.Close()
		logger.infoFile = nil
	}
	if logger.errorFile != nil {
		logger.errorFile.Close()
		logger.errorFile = nil
	}

	// Open new files with current date. openLogFile creates the directory if
	// needed; a failed open leaves the handle nil so the next write retries.
	if logger.basePath != "" {
		if f, err := openLogFile(logger.basePath, "info", currentDate); err == nil {
			logger.infoFile = f
		} else {
			log.Printf("Error: could not open info log file: %v (will retry on the next write)\n", err)
		}

		if f, err := openLogFile(logger.basePath, "error", currentDate); err == nil {
			logger.errorFile = f
		} else {
			log.Printf("Error: could not open error log file: %v (will retry on the next write)\n", err)
		}
	}
}

// writeToFile writes the log entry to the appropriate file based on level:
// INFO goes to the info file only; WARN/ERROR/FATAL go to the errors file
// only (no duplication).
//
// Retry semantics: a nil handle (left by a failed open at startup or during
// rotation) is lazily opened as part of the write itself. If that open fails,
// the entry is dropped and the handle stays nil, so the NEXT write retries.
// A non-nil handle whose write fails gets ONE best-effort reopen via
// writeWithReopen before the entry is dropped.
//
// Locking: holds mu for the whole call, so it must not call GetLogsPath or
// LogPathFor (mu.RLock → self-deadlock); the lazy open and the reopen go
// through openLogFile, which is lock-free and uses logger.basePath directly.
func writeToFile(level LogLevel, entry LogEntry, jsonData []byte) {
	// Full lock (not RLock): a failed write may reopen handles in place.
	mu.Lock()
	defer mu.Unlock()

	// File logging disabled: drop the entry, create no file, write nothing.
	if !logger.enabled {
		return
	}

	data := string(jsonData) + "\n"

	// Only write to file for INFO and above (DEBUG only goes to console)
	switch level {
	case INFO:
		writeToLogFile(&logger.infoFile, "info", data)
	case WARN, ERROR, FATAL:
		writeToLogFile(&logger.errorFile, "error", data)
	}
}

// writeToLogFile writes data to *f. If *f is nil (a previous open failed) it
// attempts ONE lazy open first; on failure the entry is dropped and *f stays
// nil so the next write retries. On a write failure it delegates the single
// reopen to writeWithReopen. Callers must hold mu.
func writeToLogFile(f **os.File, apiLevel, data string) {
	// Disabled logger: no lazy open, no write — Disable must stay disabled.
	if !logger.enabled {
		return
	}
	if *f == nil {
		nf, err := openLogFile(logger.basePath, apiLevel, logger.logDate)
		if err != nil {
			log.Printf("Error: could not open %s log file (entry dropped, will retry on the next write): %v\n", apiLevel, err)
			return
		}
		*f = nf
	}
	writeWithReopen(f, apiLevel, data)
}

// writeWithReopen writes data to *f. On write failure it closes and nils the
// handle, then tries ONE reopen (via openLogFile, using the canonical filename
// contract) and a single retry. Failures are logged to stderr; entries may be
// dropped but the logger never panics and never keeps a closed fd around.
// Callers must hold mu.
func writeWithReopen(f **os.File, apiLevel, data string) {
	// Belt and braces: a reopen while disabled would recreate the files
	// DisableFileLogging closed (normally unreachable — writeToFile and
	// writeToLogFile are already gated on enabled).
	if !logger.enabled {
		return
	}
	if _, err := (*f).WriteString(data); err != nil {
		log.Printf("Error: failed to write to %s log file: %v\n", apiLevel, err)
		(*f).Close()
		*f = nil

		nf, oerr := openLogFile(logger.basePath, apiLevel, logger.logDate)
		if oerr != nil {
			log.Printf("Error: could not reopen %s log file: %v (entry dropped, will retry on the next write)\n", apiLevel, oerr)
			return
		}
		*f = nf
		if _, werr := nf.WriteString(data); werr != nil {
			log.Printf("Error: failed to write to %s log file after reopen: %v\n", apiLevel, werr)
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
	jsonData, err := json.Marshal(entry)
	if err != nil {
		// Log error to stderr in non-quiet mode
		if !logger.quiet {
			log.Printf("Failed to marshal log entry: %v", err)
		}
		return
	}
	if level >= INFO {
		writeToFile(level, entry, jsonData)
	}

	var fieldStr string
	if len(fields) > 0 {
		fieldStr = " " + formatFields(fields)
	}

	// In quiet mode (TUI), only write to file, don't output to stderr
	if logger.quiet {
		return
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
		// Check if it's a canonical log file (info-{date}.log / errors-{date}.log)
		dateStr, ok := LogDateFromFileName(name)
		if !ok {
			continue
		}

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
