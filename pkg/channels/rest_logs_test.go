package channels

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xilistudios/lele/pkg/logger"
)

// setTestLogsPath points the global logger at dir and returns a restore func.
func setTestLogsPath(t *testing.T, dir string) {
	t.Helper()
	original := logger.GetLogsPath()
	logger.SetLogsPath(dir)
	t.Cleanup(func() { logger.SetLogsPath(original) })
}

// TestHandleLogsReadsErrorsFile is the regression test for the empty WebUI
// Error tab: level=error must resolve errors-{date}.log (plural) — the file
// the logger writer actually creates — not error-{date}.log.
func TestHandleLogsReadsErrorsFile(t *testing.T) {
	tempDir := t.TempDir()
	setTestLogsPath(t, tempDir)

	date := time.Now().Format("2006-01-02")
	errorName, err := logger.LogFileName("error", date)
	if err != nil {
		t.Fatalf("logger.LogFileName failed: %v", err)
	}
	entry := logger.LogEntry{
		Level:     "ERROR",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Message:   "fixture error entry",
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("failed to marshal fixture entry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, errorName), append(data, '\n'), 0644); err != nil {
		t.Fatalf("failed to write fixture errors log: %v", err)
	}

	nc := &NativeChannel{}
	req := httptest.NewRequest("GET", "/api/v1/logs?level=error&date="+date, nil)
	w := httptest.NewRecorder()
	nc.handleLogs(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Fatalf("handleLogs(level=error) status = %d, want 200", resp.StatusCode)
	}
	var payload LogsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.File != errorName {
		t.Errorf("response File = %q, want %q", payload.File, errorName)
	}
	if len(payload.Entries) != 1 {
		t.Fatalf("response Entries len = %d, want 1", len(payload.Entries))
	}
	if payload.Entries[0].Message != "fixture error entry" {
		t.Errorf("entry message = %q, want %q", payload.Entries[0].Message, "fixture error entry")
	}
}

// TestHandleLogsMissingFileReturnsEmpty asserts the missing-file contract: an
// absent log file yields HTTP 200 with an empty entries list (and the
// canonical file name for the requested level).
func TestHandleLogsMissingFileReturnsEmpty(t *testing.T) {
	setTestLogsPath(t, t.TempDir())

	nc := &NativeChannel{}
	req := httptest.NewRequest("GET", "/api/v1/logs?level=error&date=2000-01-01", nil)
	w := httptest.NewRecorder()
	nc.handleLogs(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Fatalf("handleLogs status = %d, want 200", resp.StatusCode)
	}
	var payload LogsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload.File != "errors-2000-01-01.log" {
		t.Errorf("response File = %q, want %q", payload.File, "errors-2000-01-01.log")
	}
	if len(payload.Entries) != 0 {
		t.Errorf("response Entries len = %d, want 0", len(payload.Entries))
	}
}

// TestHandleLogsDatesIncludesErrorOnlyDates asserts that /logs/dates lists the
// union of dates present in info-* and errors-* files: a date with only error
// entries must be visible.
func TestHandleLogsDatesIncludesErrorOnlyDates(t *testing.T) {
	tempDir := t.TempDir()
	setTestLogsPath(t, tempDir)

	errorOnlyDate := "2026-01-02"
	infoDate := "2026-01-03"

	errorName, err := logger.LogFileName("error", errorOnlyDate)
	if err != nil {
		t.Fatalf("logger.LogFileName failed: %v", err)
	}
	infoName, err := logger.LogFileName("info", infoDate)
	if err != nil {
		t.Fatalf("logger.LogFileName failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, errorName), []byte("{}\n"), 0644); err != nil {
		t.Fatalf("failed to write fixture errors log: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, infoName), []byte("{}\n"), 0644); err != nil {
		t.Fatalf("failed to write fixture info log: %v", err)
	}

	nc := &NativeChannel{}
	req := httptest.NewRequest("GET", "/api/v1/logs/dates", nil)
	w := httptest.NewRecorder()
	nc.handleLogsDates(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Fatalf("handleLogsDates status = %d, want 200", resp.StatusCode)
	}
	var payload LogsDatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload.Dates) != 2 {
		t.Fatalf("response Dates = %v, want [%s %s] (info-only + error-only dates)", payload.Dates, infoDate, errorOnlyDate)
	}
	if payload.Dates[0] != infoDate || payload.Dates[1] != errorOnlyDate {
		t.Errorf("response Dates = %v, want [%s %s] sorted descending", payload.Dates, infoDate, errorOnlyDate)
	}
}
