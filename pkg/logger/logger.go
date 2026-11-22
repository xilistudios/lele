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
	logDate   string
	basePath  string
	quiet     bool
	enabled   bool
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

func SetLevel(level LogLevel) {
	mu.Lock()
	defer mu.Unlock()
	currentLevel = level
}

func SetQuiet(q bool) {
	mu.Lock()
	defer mu.Unlock()
	logger.quiet = q
}

func GetLevel() LogLevel {
	mu.RLock()
	defer mu.RUnlock()
	return currentLevel
}

func GetLogsPath() string {
	mu.RLock()
	defer mu.RUnlock()
	return logger.basePath
}

func SetLogsPath(basePath string) {
	mu.Lock()
	defer mu.Unlock()
	logger.basePath = basePath
}

var logFilePrefixes = map[string]string{
	"info":  "info-",
	"error": "errors-",
}

func LogFileName(level string, date string) (string, error) {
	prefix, ok := logFilePrefixes[level]
	if !ok {
		return "", fmt.Errorf("unknown log level %q: must be \"info\" or \"error\"", level)
	}
	return prefix + date + ".log", nil
}

func LogPathFor(level string, date string) (string, error) {
	name, err := LogFileName(level, date)
	if err != nil {
		return "", err
	}
	return filepath.Join(GetLogsPath(), name), nil
}

func logFilePath(basePath, apiLevel, date string) (string, error) {
	name, err := LogFileName(apiLevel, date)
	if err != nil {
		return "", err
	}
	return filepath.Join(basePath, name), nil
}

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

func InitDefaultLogging() error {
	return EnableMultiFileLogging(getDefaultLogsPath())
}

func EnableMultiFileLogging(basePath string) error {
	mu.Lock()
	defer mu.Unlock()

	logger.basePath = basePath

	if err := os.MkdirAll(basePath, 0755); err != nil {
		log.Printf("Warning: could not create logs directory: %v\n", err)
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	logger.enabled = true

	if logger.infoFile != nil {
		logger.infoFile.Close()
		logger.infoFile = nil
	}
	if logger.errorFile != nil {
		logger.errorFile.Close()
		logger.errorFile = nil
	}

	currentDate := time.Now().Format("2006-01-02")
	logger.logDate = currentDate

	infoFile, err := openLogFile(basePath, "info", currentDate)
	if err != nil {
		log.Printf("Warning: could not open info log file: %v\n", err)
		return fmt.Errorf("failed to open info log file: %w", err)
	}

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

func DisableFileLogging() {
	mu.Lock()
	defer mu.Unlock()

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

func checkDateRotation() {
	mu.Lock()
	defer mu.Unlock()

	if !logger.enabled {
		return
	}

	currentDate := time.Now().Format("2006-01-02")
	if currentDate == logger.logDate {
		return
	}

	logger.logDate = currentDate

	if logger.infoFile != nil {
		logger.infoFile.Close()
		logger.infoFile = nil
	}
	if logger.errorFile != nil {
		logger.errorFile.Close()
		logger.errorFile = nil
	}

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

func writeToFile(level LogLevel, entry LogEntry, jsonData []byte) {
	mu.Lock()
	defer mu.Unlock()

	if !logger.enabled {
		return
	}

	data := string(jsonData) + "\n"

	switch level {
	case INFO:
		writeToLogFile(&logger.infoFile, "info", data)
	case WARN, ERROR, FATAL:
		writeToLogFile(&logger.errorFile, "error", data)
	}
}

func writeToLogFile(f **os.File, apiLevel, data string) {
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

func writeWithReopen(f **os.File, apiLevel, data string) {
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

	jsonData, err := json.Marshal(entry)
	if err != nil {
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
