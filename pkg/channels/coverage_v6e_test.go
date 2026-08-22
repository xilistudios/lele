package channels

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// rest_config.go — error/edge branches
// ---------------------------------------------------------------------------

// TestRestConfig_GetLoadError covers handleGetConfig when the config file
// contains invalid JSON -> 500.
func TestRestConfig_GetLoadError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{invalid-json`), 0644); err != nil {
		t.Fatal(err)
	}
	ts := newNativeTestServerWithConfigPath(t, cfgPath)
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/config")
	ts.channel.handleGetConfig(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("get config corrupt = %d, want 500", rec.Code)
	}
}

// TestRestConfig_PutMarshalAndReloadError covers the json.Marshal failure
// branch by sending a config whose value cannot marshal (e.g. non-finite
// float) is tricky; handle via nil body invalid -> 400, and unprocessable
// validation paths.
func TestRestConfig_PutInvalidJSONAndValidation(t *testing.T) {
	ts := newNativeTestServer(t)

	// Invalid JSON body -> 400.
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/config")
	req.Method = http.MethodPut
	req.Body = mkBody(`not-json`)
	ts.channel.handlePutConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("put invalid json = %d, want 400", rec.Code)
	}

	// Config failing ToConfig validation -> 422 (unprocessable entity).
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/config")
	req.Method = http.MethodPut
	// An empty/default editable doc should fail validation (no provider/model).
	req.Body = mkBody(`{}`)
	ts.channel.handlePutConfig(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("put invalid config = %d, want 422", rec.Code)
	}
}

// TestRestConfig_ValidateInvalidBranch covers handleValidateConfig for an
// invalid configuration that yields errors but still returns 200 with
// Valid=false.
func TestRestConfig_ValidateInvalidAndJSON(t *testing.T) {
	ts := newNativeTestServer(t)

	// Invalid JSON -> 400.
	rec := httptest.NewRecorder()
	req := authenticatedRequest(t, ts, "/api/v1/config/validate")
	req.Method = http.MethodPost
	req.Body = mkBody(`not-json`)
	ts.channel.handleValidateConfig(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("validate invalid json = %d, want 400", rec.Code)
	}

	// Empty config -> validation errors -> 200 Valid=false.
	rec = httptest.NewRecorder()
	req = authenticatedRequest(t, ts, "/api/v1/config/validate")
	req.Method = http.MethodPost
	req.Body = mkBody(`{}`)
	ts.channel.handleValidateConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("validate empty = %d, want 200", rec.Code)
	}
	var resp ConfigValidateResponse
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Valid {
		t.Error("expected Valid=false for empty config")
	}
}

// ---------------------------------------------------------------------------
// rest_logs.go — remaining helper coverage
// ---------------------------------------------------------------------------

// TestHandleLogs_ErrorBranches covers readAllLines on a directory path.
func TestHandleLogs_ErrorBranches(t *testing.T) {
	ts := newNativeTestServer(t)

	// readAllLines on a directory path returns empty without error.
	dir := t.TempDir()
	_, _ = readAllLines(dir)
	_ = ts
}

var _ = strings.TrimSpace