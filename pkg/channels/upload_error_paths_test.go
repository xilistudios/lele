package channels

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/config"
)

// testUploadChannel builds a NativeChannel configured with cfgForNative and a
// real auth manager so handleFileUpload can be invoked directly.
func testUploadChannel(t *testing.T) *NativeChannel {
	t.Helper()
	cfg := defaultNativeConfigForTest()
	cfg.LeleDir = t.TempDir()
	cfg.MaxUploadSizeMB = 1
	auth, err := NewAuthManager(&cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	return &NativeChannel{cfg: &cfg, auth: auth}
}

// buildUploadRequest returns a multipart request writer payload for the given
// filename/content pairs, ready for handleFileUpload.
func buildUploadRequest(t *testing.T, files map[string]string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for name, content := range files {
		part, err := writer.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// handleFileUpload relies on cfg.LeleDir to build the upload dir. When
// LeleDir points to a path whose parent can't be created, MkdirAll fails.
func TestHandleFileUpload_AllFilesFailedErrorPaths(t *testing.T) {
	n := testUploadChannel(t)

	// Point LeleDir under a regular file so MkdirAll(tmpt/uploads) fails.
	firewall := filepath.Join(n.cfg.LeleDir, "blocker")
	if err := os.WriteFile(firewall, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	n.cfg.LeleDir = filepath.Join(firewall, "deeper")

	rec := httptest.NewRecorder()
	n.handleFileUpload(rec, buildUploadRequest(t, map[string]string{"a.txt": "hello"}))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("mkdir failure status = %d, want 500", rec.Code)
	}
}

// When the destination directory can't be written but the upload dir already
// exists, os.Create fails and all files are skipped -> "all files failed".
func TestHandleFileUpload_AllFilesFailedCreate(t *testing.T) {
	n := testUploadChannel(t)

	uploadDir := filepath.Join(n.cfg.LeleDir, "tmp", "uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Make the upload dir read-only so os.Create inside it fails.
	if err := os.Chmod(uploadDir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(uploadDir, 0755) })

	rec := httptest.NewRecorder()
	n.handleFileUpload(rec, buildUploadRequest(t, map[string]string{"a.txt": "hello"}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("all files failed status = %d, want 400", rec.Code)
	}
}

// SanitizeFilename fallback to "attachment" when the original name is empty
// or just a dot.
func TestHandleFileUpload_SanitizeFallback(t *testing.T) {
	n := testUploadChannel(t)
	rec := httptest.NewRecorder()
	n.handleFileUpload(rec, buildUploadRequest(t, map[string]string{"..": "data"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var payload FileUploadResponse
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(payload.Files))
	}
	if payload.Files[0].Name == "" {
		t.Error("expected a resulting file name")
	}
	// The file should still have been created on disk.
	if _, err := os.Stat(payload.Files[0].Path); os.IsNotExist(err) {
		t.Errorf("uploaded file missing at %s", payload.Files[0].Path)
	}
}

// buildUploadRequest body should fail if MaxUploadSizeMB is exceeded - but
// that's handled at the http layer (ParseMultipartForm). Verify the "form
// invalid" branch instead with a malformed body.
func TestHandleFileUpload_InvalidForm(t *testing.T) {
	n := testUploadChannel(t)
	rec := httptest.NewRecorder()
	body := &bytes.Buffer{}
	body.WriteString("this is not a valid multipart body")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files/upload", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=doesnotexist")
	n.handleFileUpload(rec, req)
	// A garbage body returns either 400 (form_invalid) or a server error;
	// both are acceptable — it must not be 200.
	if rec.Code == http.StatusOK {
		t.Error("invalid multipart form returned 200")
	}
}

// defaultNativeConfigForTest with explicit file size bytes helper.
var _ = config.NativeConfig{}