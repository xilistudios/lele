package channels

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
