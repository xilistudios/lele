package channels

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

func TestHandleFileUpload_Success(t *testing.T) {
	ts := newNativeTestServer(t)

	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("files", "test.txt")
	if err != nil {
		t.Fatalf("Failed to create form file: %v", err)
	}

	file, err := os.Open(tmpFile)
	if err != nil {
		t.Fatalf("Failed to open test file: %v", err)
	}
	defer file.Close()

	if _, err := io.Copy(part, file); err != nil {
		t.Fatalf("Failed to copy file content: %v", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", ts.server.URL+"/api/v1/files/upload", body)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	var payload FileUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(payload.Files) != 1 {
		t.Fatalf("Expected 1 file, got %d", len(payload.Files))
	}

	uploaded := payload.Files[0]
	if uploaded.Name != "test.txt" {
		t.Errorf("Expected name 'test.txt', got '%s'", uploaded.Name)
	}
	if uploaded.Size != 12 {
		t.Errorf("Expected size 12, got %d", uploaded.Size)
	}
	if uploaded.MIMEType == "" {
		t.Error("Expected MIME type to be set")
	}

	if _, err := os.Stat(uploaded.Path); os.IsNotExist(err) {
		t.Errorf("Uploaded file should exist at '%s'", uploaded.Path)
	}
}

func TestHandleFileUpload_MultipleFiles(t *testing.T) {
	ts := newNativeTestServer(t)

	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")
	if err := os.WriteFile(file1, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create file1: %v", err)
	}
	if err := os.WriteFile(file2, []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create file2: %v", err)
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for _, filename := range []string{file1, file2} {
		part, err := writer.CreateFormFile("files", filepath.Base(filename))
		if err != nil {
			t.Fatalf("Failed to create form file: %v", err)
		}

		file, err := os.Open(filename)
		if err != nil {
			t.Fatalf("Failed to open file: %v", err)
		}
		defer file.Close()

		if _, err := io.Copy(part, file); err != nil {
			t.Fatalf("Failed to copy file content: %v", err)
		}
	}
	writer.Close()

	req, err := http.NewRequest("POST", ts.server.URL+"/api/v1/files/upload", body)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	var payload FileUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(payload.Files) != 2 {
		t.Fatalf("Expected 2 files, got %d", len(payload.Files))
	}
}

func TestHandleFileUpload_NoFiles(t *testing.T) {
	ts := newNativeTestServer(t)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.Close()

	req, err := http.NewRequest("POST", ts.server.URL+"/api/v1/files/upload", body)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}
}

func TestHandleFileUpload_Unauthorized(t *testing.T) {
	ts := newNativeTestServer(t)

	req, err := http.NewRequest("POST", ts.server.URL+"/api/v1/files/upload", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", resp.StatusCode)
	}
}

// --- GW-M7: MaxBytesReader enforcement ---

// newUploadTestServer returns a test server with a small MaxUploadSizeMB
// (1 MB) and a temp LeleDir so we can verify disk side effects.
func newUploadTestServer(t *testing.T) *nativeTestServer {
	t.Helper()
	ts := newNativeTestServer(t)
	ts.channel.cfg.LeleDir = t.TempDir()
	ts.channel.cfg.MaxUploadSizeMB = 1 // 1 MB
	return ts
}

// uploadDirPath returns the uploads directory for the test server.
func uploadDirPath(ts *nativeTestServer) string {
	return filepath.Join(ts.channel.cfg.LeleDir, "tmp", "uploads")
}

// TestHandleFileUpload_OversizedReturns413AndWritesNothing verifies that
// sending a body larger than MaxUploadSizeMB returns HTTP 413 and does NOT
// write any file to tmp/uploads. This tests the MaxBytesReader fix (GW-M7);
// without MaxBytesReader, ParseMultipartForm would silently spool the
// oversized body to disk and return 200.
func TestHandleFileUpload_OversizedReturns413AndWritesNothing(t *testing.T) {
	ts := newUploadTestServer(t)

	// Build a 2 MB payload (limit is 1 MB).
	bigContent := strings.Repeat("A", 2*1024*1024)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("files", "big.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.WriteString(part, bigContent); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", ts.server.URL+"/api/v1/files/upload", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 413; body = %s", resp.StatusCode, respBody)
	}

	// Verify nothing was written to tmp/uploads.
	dir := uploadDirPath(ts)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return // directory doesn't exist — nothing written, good
		}
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) > 0 {
		t.Errorf("tmp/uploads has %d entries after oversized upload; expected 0", len(entries))
	}
}

// TestHandleFileUpload_JustUnderLimitSucceeds verifies that a file just
// under the MaxUploadSizeMB limit uploads successfully (200). This is the
// counterpart to the oversized test: normal uploads must not be broken by
// MaxBytesReader.
func TestHandleFileUpload_JustUnderLimitSucceeds(t *testing.T) {
	ts := newUploadTestServer(t) // 1 MB limit

	// 900 KB content (well under 1 MB; multipart overhead adds some bytes
	// but stays under the limit).
	content := strings.Repeat("B", 900*1024)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("files", "medium.bin")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := io.WriteString(part, content); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", ts.server.URL+"/api/v1/files/upload", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}

	var payload FileUploadResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(payload.Files))
	}
	if payload.Files[0].Size != int64(len(content)) {
		t.Errorf("size = %d, want %d", payload.Files[0].Size, len(content))
	}

	// Verify the file exists on disk.
	if _, err := os.Stat(payload.Files[0].Path); os.IsNotExist(err) {
		t.Errorf("uploaded file not found at %s", payload.Files[0].Path)
	}
}

// --- T1: symlinked LeleDir (incident 2026-10-17) ---
//
// Reproduction contract for the view endpoints and for staging: the client
// always sends the path /upload returned, i.e. the UNRESOLVED spelling through
// the configured LeleDir. When that directory traverses a symlink, comparing
// the resolved file path against the unresolved root denied every preview (403)
// and made staging copy the file again on every call.

// newSymlinkedStagingTestServer returns a native test server whose LeleDir is a
// symlink (<tmp>/lele -> <tmp>/real) plus the real directory behind it. Both
// live under t.TempDir().
func newSymlinkedStagingTestServer(t *testing.T) (ts *nativeTestServer, real string) {
	t.Helper()
	real = t.TempDir()
	link := filepath.Join(t.TempDir(), "lele")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	ts = newNativeTestServer(t)
	ts.channel.cfg.LeleDir = link
	return ts, real
}

// Mutation M8 (handleFileView without a canonical root) turns this test red.
func TestFileView_SymlinkedLeleDirServesUpload(t *testing.T) {
	ts, real := newSymlinkedStagingTestServer(t)
	dir := filepath.Join(real, "tmp", "uploads")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	const name = "abcd1234_image.txt"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	// Path spelled through the symlinked LeleDir, exactly as the upload
	// endpoint returned it (unresolved).
	p := filepath.Join(ts.channel.cfg.LeleDir, "tmp", "uploads", name)
	resp, body := getView(t, ts, url.Values{"path": {p}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (symlinked LeleDir must be served): %s", resp.StatusCode, body)
	}
	if body != "hello" {
		t.Errorf("body = %q, want hello", body)
	}
}

func TestFileView_SymlinkedLeleDirRejectsOutsidePath(t *testing.T) {
	ts, _ := newSymlinkedStagingTestServer(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, _ := getView(t, ts, url.Values{"path": {outside}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a path outside the symlinked LeleDir", resp.StatusCode)
	}
}

func TestFileViewSecure_SymlinkedLeleDirServesNonStagingPath(t *testing.T) {
	ts, real := newSymlinkedStagingTestServer(t)
	dir := filepath.Join(real, "workspace")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc.txt"), []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(ts.channel.cfg.LeleDir, "workspace", "doc.txt")
	resp, body := getSecureView(t, ts, url.Values{"path": {p}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (symlinked LeleDir must be served): %s", resp.StatusCode, body)
	}
	if body != "content" {
		t.Errorf("body = %q, want content", body)
	}
}

// Mutation M9 (stageAttachment without a canonical staging dir) turns this
// test red: the second call copies the file again.
func TestStageAttachment_IdempotentUnderSymlinkedLeleDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "lele")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	staging := filepath.Join(real, "tmp", "attachments")
	if err := os.MkdirAll(staging, 0755); err != nil {
		t.Fatal(err)
	}
	const name = "abcd1234_already_staged.txt"
	if err := os.WriteFile(filepath.Join(staging, name), []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}

	// Spelled through the symlinked LeleDir, as an outbound attachment would be.
	staged := filepath.Join(link, "tmp", "attachments", name)
	a := bus.FileAttachment{Name: name, Path: staged, Kind: "file"}

	if err := stageAttachment(&a, link); err != nil {
		t.Fatalf("stageAttachment: %v", err)
	}
	if err := stageAttachment(&a, link); err != nil {
		t.Fatalf("stageAttachment (second call): %v", err)
	}

	if a.Path != staged {
		t.Errorf("Path = %q, want %q: a file already in staging must be left alone", a.Path, staged)
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("staging holds %d entries, want 1: staging must be idempotent", len(entries))
	}
}
