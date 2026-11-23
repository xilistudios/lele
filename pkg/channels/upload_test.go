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

func newUploadTestServer(t *testing.T) *nativeTestServer {
	t.Helper()
	ts := newNativeTestServer(t)
	ts.channel.cfg.LeleDir = t.TempDir()
	ts.channel.cfg.MaxUploadSizeMB = 1
	return ts
}

func uploadDirPath(ts *nativeTestServer) string {
	return filepath.Join(ts.channel.cfg.LeleDir, "tmp", "uploads")
}

func TestHandleFileUpload_OversizedReturns413AndWritesNothing(t *testing.T) {
	ts := newUploadTestServer(t)

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

	dir := uploadDirPath(ts)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) > 0 {
		t.Errorf("tmp/uploads has %d entries after oversized upload; expected 0", len(entries))
	}
}

func TestHandleFileUpload_JustUnderLimitSucceeds(t *testing.T) {
	ts := newUploadTestServer(t)

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

	if _, err := os.Stat(payload.Files[0].Path); os.IsNotExist(err) {
		t.Errorf("uploaded file not found at %s", payload.Files[0].Path)
	}
}

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
func TestPersistOutboundAttachments_CopiesIntoDurableDir(t *testing.T) {
	leleDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "report.csv")
	if err := os.WriteFile(src, []byte("a,b\n1,2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	out := []bus.FileAttachment{{Name: "report.csv", Path: src, MIMEType: "text/csv", Kind: "file"}}
	got := persistOutboundAttachments(out, leleDir)

	if len(got) != 1 {
		t.Fatalf("attachments = %d, want 1", len(got))
	}
	dir := durableAttachmentsDir(leleDir)
	if filepath.Dir(got[0].Path) != dir {
		t.Fatalf("path = %q, want a copy directly inside %q", got[0].Path, dir)
	}
	if got[0].Name != "report.csv" {
		t.Errorf("name = %q, want report.csv", got[0].Name)
	}
	if got[0].MIMEType != "text/csv" || got[0].Kind != "file" {
		t.Errorf("mime/kind not preserved: %+v", got[0])
	}
	body, err := os.ReadFile(got[0].Path)
	if err != nil {
		t.Fatalf("read durable copy: %v", err)
	}
	if string(body) != "a,b\n1,2\n" {
		t.Errorf("durable copy = %q, want the source payload", body)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("source file must be left in place: %v", err)
	}
}

func TestPersistOutboundAttachments_KeepsProvidedName(t *testing.T) {
	leleDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "ab12cd34_chart.png")
	if err := os.WriteFile(src, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	got := persistOutboundAttachments([]bus.FileAttachment{{Name: "chart.png", Path: src}}, leleDir)

	if got[0].Name != "chart.png" {
		t.Errorf("name = %q, want chart.png: the caller-provided display name must win over the staged basename", got[0].Name)
	}
}

func TestPersistOutboundAttachments_DeterministicDestination(t *testing.T) {
	leleDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "chart.png")
	if err := os.WriteFile(src, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	first := persistOutboundAttachments([]bus.FileAttachment{{Name: "chart.png", Path: src}}, leleDir)
	second := persistOutboundAttachments([]bus.FileAttachment{{Name: "chart.png", Path: src}}, leleDir)

	if first[0].Path != second[0].Path {
		t.Fatalf("second call wrote %q, want the same path %q", second[0].Path, first[0].Path)
	}
	entries, err := os.ReadDir(durableAttachmentsDir(leleDir))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("durable dir holds %d entries, want 1: redelivery must not duplicate files", len(entries))
	}
}

func TestPersistOutboundAttachments_LeavesDurablePathUntouched(t *testing.T) {
	leleDir := t.TempDir()
	dir := durableAttachmentsDir(leleDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(dir, "abcd1234_report.csv")
	if err := os.WriteFile(existing, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}

	got := persistOutboundAttachments([]bus.FileAttachment{{Name: "report.csv", Path: existing}}, leleDir)

	if got[0].Path != existing {
		t.Errorf("path = %q, want %q: a durable file must be left alone", got[0].Path, existing)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("durable dir holds %d entries, want 1", len(entries))
	}
}

func TestPersistOutboundAttachments_MissingFileKeepsOriginal(t *testing.T) {
	leleDir := t.TempDir()
	ghost := filepath.Join(t.TempDir(), "gone.txt")

	got := persistOutboundAttachments([]bus.FileAttachment{{Name: "gone.txt", Path: ghost}}, leleDir)

	if len(got) != 1 {
		t.Fatalf("attachments = %d, want 1", len(got))
	}
	if got[0].Path != ghost || got[0].Name != "gone.txt" {
		t.Errorf("attachment = %+v, want the original untouched", got[0])
	}
	if _, err := os.Stat(durableAttachmentsDir(leleDir)); !os.IsNotExist(err) {
		t.Errorf("durable dir created for a missing source (err=%v)", err)
	}
}

func TestPersistOutboundAttachments_DirectoryKeepsOriginal(t *testing.T) {
	leleDir := t.TempDir()
	dir := t.TempDir()

	got := persistOutboundAttachments([]bus.FileAttachment{{Name: filepath.Base(dir), Path: dir}}, leleDir)

	if len(got) != 1 {
		t.Fatalf("attachments = %d, want 1", len(got))
	}
	if got[0].Path != dir {
		t.Errorf("path = %q, want the directory untouched", got[0].Path)
	}
}

func TestPersistOutboundAttachments_HashPrefixAvoidsDeniedViewNames(t *testing.T) {
	leleDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "config.png")
	if err := os.WriteFile(src, []byte("png"), 0644); err != nil {
		t.Fatal(err)
	}

	got := persistOutboundAttachments([]bus.FileAttachment{{Name: "config.png", Path: src}}, leleDir)

	base := filepath.Base(got[0].Path)
	if strings.HasPrefix(base, "config.") {
		t.Fatalf("basename %q is denied by the file view", base)
	}
	if !strings.HasSuffix(base, "_config.png") {
		t.Fatalf("basename = %q, want a hash prefix plus _config.png", base)
	}
	if len(base) != len("0123456789abcdef_config.png") {
		t.Errorf("basename = %q, want a 16 hex char prefix", base)
	}
}

func TestPersistOutboundAttachments_EmptyLeleDirIsNoop(t *testing.T) {
	src := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(src, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	got := persistOutboundAttachments([]bus.FileAttachment{{Name: "keep.txt", Path: src}}, "")

	if got[0].Path != src {
		t.Errorf("path = %q, want the original when leleDir is empty", got[0].Path)
	}
}
