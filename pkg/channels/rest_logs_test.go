package channels

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/logger"
)

// writeLogFile writes JSON log lines to path.
func writeLogFile(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
}

func TestHandleLogs_Dates(t *testing.T) {
	dir := t.TempDir()
	logger.SetLogsPath(dir)
	t.Cleanup(func() { logger.SetLogsPath("") })

	// Create log files.
	writeLogFile(t, filepath.Join(dir, "info-2026-01-01.log"), []string{`{"level":"info","message":"a"}`})
	writeLogFile(t, filepath.Join(dir, "info-2026-01-02.log"), []string{`{"level":"info","message":"b"}`})
	writeLogFile(t, filepath.Join(dir, "error-2026-01-03.log"), []string{`{"level":"error","message":"c"}`}) // ignored
	writeLogFile(t, filepath.Join(dir, "random.txt"), []string{"x"})                                       // ignored

	ts := newNativeTestServer(t)
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/logs/dates", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body LogsDatesResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	if len(body.Dates) != 2 {
		t.Fatalf("dates = %v", body.Dates)
	}
	// Reverse sorted: 2026-01-02 before 2026-01-01.
	if body.Dates[0] != "2026-01-02" || body.Dates[1] != "2026-01-01" {
		t.Errorf("dates order = %v", body.Dates)
	}
}

func TestHandleLogs_ReadAndFilter(t *testing.T) {
	dir := t.TempDir()
	logger.SetLogsPath(dir)
	t.Cleanup(func() { logger.SetLogsPath("") })

	date := "2026-01-05"
	writeLogFile(t, filepath.Join(dir, fmt.Sprintf("info-%s.log", date)), []string{
		`{"level":"info","message":"line1","time":"2026-01-05T00:00:00Z"}`,
		`{"level":"info","message":"line2","time":"2026-01-05T00:00:01Z"}`,
		"this is not valid json",
	})

	ts := newNativeTestServer(t)

	// Level validation.
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/logs?level=bogus", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad level status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Date validation.
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/logs?date=not-a-date", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad date status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Lines validation.
	resp = doSecretsRequest(t, ts, http.MethodGet, "/api/v1/logs?lines=abc", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad lines status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Valid read: parses JSON entries, skips invalid.
	resp = doSecretsRequest(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/logs?date=%s", date), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid read status = %d", resp.StatusCode)
	}
	var body LogsResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()

	if body.File != "info-"+date+".log" {
		t.Errorf("File = %q", body.File)
	}
	if body.TotalLines != 3 {
		t.Errorf("TotalLines = %d", body.TotalLines)
	}
	// 2 valid JSON lines, 1 invalid skipped.
	if body.ReturnedLines != 2 {
		t.Errorf("ReturnedLines = %d", body.ReturnedLines)
	}
	if len(body.Entries) != 2 || body.Entries[0].Message != "line1" {
		t.Errorf("entries = %+v", body.Entries)
	}
	if body.Level != "info" || body.Date != date {
		t.Errorf("level/date = %q/%q", body.Level, body.Date)
	}
}

func TestHandleLogs_MissingFile_Empty(t *testing.T) {
	dir := t.TempDir()
	logger.SetLogsPath(dir)
	t.Cleanup(func() { logger.SetLogsPath("") })

	ts := newNativeTestServer(t)
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/logs?date=2030-12-31", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body LogsResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if len(body.Entries) != 0 || body.TotalLines != 0 {
		t.Errorf("expected empty body, got %+v", body)
	}
}

func TestHandleLogs_DatesNoDir_Empty(t *testing.T) {
	dir := t.TempDir()
	logger.SetLogsPath(dir)
	t.Cleanup(func() { logger.SetLogsPath("") })

	ts := newNativeTestServer(t)
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/logs/dates", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var body LogsDatesResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if len(body.Dates) != 0 {
		t.Errorf("expected empty dates, got %v", body.Dates)
	}
}// TestHandleLogs_LinesEdge runs the lines=0 (n<1) and lines>500 (clamp)
// branches in handleLogs.
func TestHandleLogs_LinesEdge(t *testing.T) {
	dir := t.TempDir()
	logger.SetLogsPath(dir)
	t.Cleanup(func() { logger.SetLogsPath("") })

	date := "2026-01-06"
	writeLogFile(t, filepath.Join(dir, fmt.Sprintf("info-%s.log", date)), []string{
		`{"level":"info","message":"line1","time":"2026-01-06T00:00:00Z"}`,
	})

	ts := newNativeTestServer(t)

	// lines=0 -> 400 (n<1 branch).
	resp := doSecretsRequest(t, ts, http.MethodGet, "/api/v1/logs?lines=0", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("lines=0 status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()

	// lines=999 -> clamped to 500, returns 200.
	resp = doSecretsRequest(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/logs?date=%s&lines=999", date), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("lines=999 status = %d, want 200", resp.StatusCode)
	}
	var body LogsResponse
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.TotalLines != 1 {
		t.Errorf("TotalLines = %d, want 1", body.TotalLines)
	}
}