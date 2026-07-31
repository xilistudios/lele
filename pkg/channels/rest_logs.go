package channels

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

var validDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// LogsResponse is the JSON structure returned by GET /api/v1/logs.
type LogsResponse struct {
	Entries       []logger.LogEntry `json:"entries"`
	TotalLines    int               `json:"total_lines"`
	ReturnedLines int               `json:"returned_lines"`
	File          string            `json:"file"`
	Date          string            `json:"date"`
	Level         string            `json:"level"`
}

// LogsDatesResponse is the JSON structure returned by GET /api/v1/logs/dates.
type LogsDatesResponse struct {
	Dates []string `json:"dates"`
}

// handleLogs returns the last N lines of a log file for a given date and level.
func (n *NativeChannel) handleLogs(w http.ResponseWriter, r *http.Request) {
	// Parse and validate query parameters.
	level := getQueryParam(r, "level")
	if level == "" {
		level = "info"
	}
	if level != "info" && level != "error" {
		writeError(w, http.StatusBadRequest, "level must be \"info\" or \"error\"", "invalid_level")
		return
	}

	date := getQueryParam(r, "date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if !validDateRe.MatchString(date) {
		writeError(w, http.StatusBadRequest, "date must be in YYYY-MM-DD format", "invalid_date")
		return
	}

	linesParam := getQueryParam(r, "lines")
	maxLines := 100
	if linesParam != "" {
		n, err := strconv.Atoi(linesParam)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "lines must be a positive integer", "invalid_lines")
			return
		}
		if n > 500 {
			n = 500
		}
		maxLines = n
	}

	// Build file path.
	fileName := level + "-" + date + ".log"
	logPath := filepath.Join(logger.GetLogsPath(), fileName)

	// Read the file. If it doesn't exist, return an empty response.
	allLines, err := readAllLines(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, LogsResponse{
				Entries:       []logger.LogEntry{},
				TotalLines:    0,
				ReturnedLines: 0,
				File:          fileName,
				Date:          date,
				Level:         level,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read log file", "read_error")
		return
	}

	totalLines := len(allLines)

	// Take the last maxLines lines.
	start := totalLines - maxLines
	if start < 0 {
		start = 0
	}
	tailLines := allLines[start:]

	// Parse each line as JSON into a LogEntry.
	entries := make([]logger.LogEntry, 0, len(tailLines))
	for _, line := range tailLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry logger.LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// Skip lines that aren't valid JSON (e.g. corrupted entries).
			continue
		}
		entries = append(entries, entry)
	}

	writeJSON(w, http.StatusOK, LogsResponse{
		Entries:       entries,
		TotalLines:    totalLines,
		ReturnedLines: len(entries),
		File:          fileName,
		Date:          date,
		Level:         level,
	})
}

// handleLogsDates returns the list of dates that have log files available.
func (n *NativeChannel) handleLogsDates(w http.ResponseWriter, r *http.Request) {
	logsPath := logger.GetLogsPath()

	entries, err := os.ReadDir(logsPath)
	if err != nil {
		// If the directory doesn't exist yet, return an empty list.
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, LogsDatesResponse{Dates: []string{}})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read logs directory", "read_error")
		return
	}

	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "info-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		// Extract date: "info-YYYY-MM-DD.log" → "YYYY-MM-DD"
		date := strings.TrimPrefix(name, "info-")
		date = strings.TrimSuffix(date, ".log")
		if validDateRe.MatchString(date) {
			seen[date] = true
		}
	}

	dates := make([]string, 0, len(seen))
	for d := range seen {
		dates = append(dates, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dates)))

	writeJSON(w, http.StatusOK, LogsDatesResponse{Dates: dates})
}

// readAllLines reads every line from a file and returns them as a slice.
func readAllLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return lines, err
	}
	return lines, nil
}
