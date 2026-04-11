package channels

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
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
