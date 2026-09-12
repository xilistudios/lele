package channels

// Tests for outbound attachment staging and the /api/v1/files/view and
// /api/v1/files/view-secure endpoints.
//
// Contract:
//   - PUBLIC  GET /api/v1/files/view?path=<abs>  → staging dir ONLY (no auth)
//   - SECURED GET /api/v1/files/view-secure?path=<abs> → broader leleDir (auth required)
//   - Both endpoints deny sensitive filenames (defence in depth).
//   - Files outside leleDir are copied to <leleDir>/tmp/attachments/<id>_<name>.
//   - Files under leleDir but outside staging are ALSO copied to staging
//     (invariant: "public == staging").

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/bus"
)

// newStagingTestServer returns a native test server whose LeleDir points at a
// temp dir (so staging never touches the real ~/.lele).
func newStagingTestServer(t *testing.T) *nativeTestServer {
	t.Helper()
	ts := newNativeTestServer(t)
	ts.channel.cfg.LeleDir = t.TempDir()
	return ts
}

// stagingDir returns the staging directory path for the given test server.
func stagingDir(ts *nativeTestServer) string {
	return filepath.Join(ts.channel.cfg.LeleDir, "tmp", "attachments")
}

// writeStagingFile creates a file under the staging directory and returns its path.
func writeStagingFile(t *testing.T, ts *nativeTestServer, name, content string) string {
	t.Helper()
	dir := stagingDir(ts)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// uploadDir returns the upload directory path for the given test server.
func uploadDir(ts *nativeTestServer) string {
	return filepath.Join(ts.channel.cfg.LeleDir, "tmp", "uploads")
}

// writeUploadFile creates a file under the uploads directory and returns its path.
func writeUploadFile(t *testing.T, ts *nativeTestServer, name, content string) string {
	t.Helper()
	dir := uploadDir(ts)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- stageAttachment unit tests ---

func TestStageAttachment_CopiesOutsideFileUnderLeleDir(t *testing.T) {
	leleDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "my report.txt") // outside leleDir
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}

	a := bus.FileAttachment{Name: "my report.txt", Path: src, MIMEType: "text/plain", Kind: "file"}
	if err := stageAttachment(&a, leleDir); err != nil {
		t.Fatalf("stageAttachment() error = %v", err)
	}

	if a.Path == src {
		t.Fatal("path must have been rewritten to the staged copy")
	}
	if !strings.HasPrefix(a.Path, filepath.Join(leleDir, "tmp", "attachments")) {
		t.Errorf("staged path %q not under <leleDir>/tmp/attachments", a.Path)
	}
	if a.Name != "my report.txt" {
		t.Errorf("name = %q, want original 'my report.txt'", a.Name)
	}
	data, err := os.ReadFile(a.Path)
	if err != nil || string(data) != "payload" {
		t.Errorf("staged copy content = %q, err %v, want payload", data, err)
	}
	// Original must remain untouched.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("original file was removed: %v", err)
	}
	// Staged file name carries the original base name (id prefix + name).
	base := filepath.Base(a.Path)
	if !strings.HasSuffix(base, "_my report.txt") || len(base) <= len("_my report.txt") {
		t.Errorf("staged base name = %q, want <id>_my report.txt", base)
	}
}

func TestStageAttachment_CopiesInsideLeleDirToStaging(t *testing.T) {
	// After FIX-1: files under leleDir but OUTSIDE the staging dir are copied
	// to staging so the public endpoint can serve them. This enforces the
	// "public == staging" invariant.
	leleDir := t.TempDir()
	inside := filepath.Join(leleDir, "tmp", "uploads", "ab_cd.png")
	if err := os.MkdirAll(filepath.Dir(inside), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	a := bus.FileAttachment{Name: "cd.png", Path: inside}
	if err := stageAttachment(&a, leleDir); err != nil {
		t.Fatalf("stageAttachment() error = %v", err)
	}
	// Path must now point to staging, not the original upload dir.
	if a.Path == inside {
		t.Fatalf("expected path to be rewritten to staging, got %q", a.Path)
	}
	if !strings.HasPrefix(a.Path, filepath.Join(leleDir, "tmp", "attachments")) {
		t.Errorf("staged path %q not under staging dir", a.Path)
	}
	// Name is set to the base name of the original path ("ab_cd.png")
	// because stageAttachment uses filepath.Base(absPath) for the display name.
	if a.Name != "ab_cd.png" {
		t.Errorf("name = %q, want ab_cd.png (base of original path)", a.Name)
	}
}

func TestStageAttachment_IdempotentOnStagedPath(t *testing.T) {
	leleDir := t.TempDir()
	src := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(src, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	a := bus.FileAttachment{Name: "f.bin", Path: src}
	if err := stageAttachment(&a, leleDir); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	first := a.Path
	if err := stageAttachment(&a, leleDir); err != nil {
		t.Fatalf("second stage: %v", err)
	}
	if a.Path != first {
		t.Errorf("second staging re-copied: %q -> %q", first, a.Path)
	}
}

func TestStageAttachment_MissingFileKeepsPath(t *testing.T) {
	leleDir := t.TempDir()
	ghost := filepath.Join(t.TempDir(), "nope.txt")

	a := bus.FileAttachment{Name: "nope.txt", Path: ghost}
	if err := stageAttachment(&a, leleDir); err == nil {
		t.Fatal("expected error for missing file")
	}
	if a.Path != ghost {
		t.Errorf("path must stay intact on failure: %q", a.Path)
	}
}

func TestStageAttachment_RejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	a := bus.FileAttachment{Name: filepath.Base(dir), Path: dir}
	if err := stageAttachment(&a, t.TempDir()); err == nil {
		t.Fatal("expected error when staging a directory")
	}
}

func TestStageAttachment_EmptyPath(t *testing.T) {
	a := bus.FileAttachment{Name: "x"}
	if err := stageAttachment(&a, t.TempDir()); err == nil {
		t.Fatal("expected error for empty path")
	}
}

// --- public view endpoint tests (/api/v1/files/view — staging only) ---

func getView(t *testing.T, ts *nativeTestServer, params url.Values) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("GET", ts.server.URL+"/api/v1/files/view?"+params.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

// getSecureView hits the authenticated /api/v1/files/view-secure endpoint.
func getSecureView(t *testing.T, ts *nativeTestServer, params url.Values) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("GET", ts.server.URL+"/api/v1/files/view-secure?"+params.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestFileView_InlineByDefault(t *testing.T) {
	ts := newStagingTestServer(t)
	path := writeStagingFile(t, ts, "inline.txt", "hello")

	resp, body := getView(t, ts, url.Values{"path": {path}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "inline" {
		t.Errorf("Content-Disposition = %q, want inline", cd)
	}
	if body != "hello" {
		t.Errorf("body = %q, want hello", body)
	}
}

func TestFileView_DownloadQueryParam(t *testing.T) {
	ts := newStagingTestServer(t)
	path := writeStagingFile(t, ts, "report file.txt", "bytes")

	resp, body := getView(t, ts, url.Values{"path": {path}, "download": {"1"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	want := `attachment; filename="report file.txt"`
	if cd := resp.Header.Get("Content-Disposition"); cd != want {
		t.Errorf("Content-Disposition = %q, want %q", cd, want)
	}
}

func TestFileView_NameOverride(t *testing.T) {
	ts := newStagingTestServer(t)
	path := writeStagingFile(t, ts, "aa_report.txt", "x")

	resp, _ := getView(t, ts, url.Values{"path": {path}, "download": {"1"}, "name": {"report.txt"}})
	want := `attachment; filename="report.txt"`
	if cd := resp.Header.Get("Content-Disposition"); cd != want {
		t.Errorf("Content-Disposition = %q, want %q", cd, want)
	}
}

func TestFileView_NameOverrideSanitized(t *testing.T) {
	ts := newStagingTestServer(t)
	path := writeStagingFile(t, ts, "safe.txt", "x")

	// Header injection attempt in the display name must be neutralized.
	resp, _ := getView(t, ts, url.Values{"path": {path}, "download": {"1"}, "name": {"ev\nil.txt"}})
	cd := resp.Header.Get("Content-Disposition")
	if strings.ContainsAny(cd, "\r\n") {
		t.Fatalf("Content-Disposition carries CR/LF: %q", cd)
	}
	if !strings.HasPrefix(cd, `attachment; filename="`) {
		t.Errorf("unexpected Content-Disposition: %q", cd)
	}
}

func TestFileView_RejectsOutsideLeleDir(t *testing.T) {
	ts := newStagingTestServer(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("s"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, _ := getView(t, ts, url.Values{"path": {outside}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestFileView_RejectsSiblingPrefixEscape(t *testing.T) {
	ts := newStagingTestServer(t)
	leleDir := ts.channel.cfg.LeleDir
	evilDir := leleDir + "-evil"
	if err := os.MkdirAll(evilDir, 0755); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(evilDir, "pass.txt")
	if err := os.WriteFile(evil, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, _ := getView(t, ts, url.Values{"path": {evil}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for prefix-escape path", resp.StatusCode)
	}
}

func TestFileView_RejectsTraversal(t *testing.T) {
	ts := newStagingTestServer(t)
	outside := filepath.Join(t.TempDir(), "top.txt")
	if err := os.WriteFile(outside, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	tricky := filepath.Join(ts.channel.cfg.LeleDir, "..", outside)
	resp, _ := getView(t, ts, url.Values{"path": {tricky}})
	if resp.StatusCode == http.StatusOK {
		t.Errorf("traversal path served: %q", tricky)
	}
}

func TestFileView_MissingPathParam(t *testing.T) {
	ts := newStagingTestServer(t)
	resp, _ := getView(t, ts, url.Values{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestFileView_NotFound(t *testing.T) {
	ts := newStagingTestServer(t)
	// Must be under staging dir to get 404 (not 403).
	missing := writeStagingFile(t, ts, "exists.txt", "x")
	os.Remove(missing) // file existed during write, now gone
	resp, _ := getView(t, ts, url.Values{"path": {missing}})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// --- symlink containment ---

func TestFileView_RejectsSymlinkEscape(t *testing.T) {
	ts := newStagingTestServer(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("top secret"), 0644); err != nil {
		t.Fatal(err)
	}
	// Symlink inside staging that points outside.
	dir := stagingDir(ts)
	os.MkdirAll(dir, 0755)
	link := filepath.Join(dir, "sneaky.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	resp, body := getView(t, ts, url.Values{"path": {link}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (%s)", resp.StatusCode, body)
	}
	if strings.Contains(body, "top secret") {
		t.Errorf("symlink target content leaked: %q", body)
	}
}

func TestFileView_AllowsSymlinkWithinStaging(t *testing.T) {
	ts := newStagingTestServer(t)
	dir := stagingDir(ts)
	os.MkdirAll(dir, 0755)
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	resp, body := getView(t, ts, url.Values{"path": {link}, "download": {"1"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.StatusCode, body)
	}
	if body != "ok" {
		t.Errorf("body = %q, want ok", body)
	}
}

// --- symlink in stageAttachment ---

func TestStageAttachment_SymlinkUnderLeleDirIsCopied(t *testing.T) {
	leleDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "data.bin")
	if err := os.WriteFile(outside, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(leleDir, "via-link.bin")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	a := bus.FileAttachment{Name: filepath.Base(link), Path: link, Kind: "file"}
	if err := stageAttachment(&a, leleDir); err != nil {
		t.Fatalf("stageAttachment: %v", err)
	}
	if a.Path == link {
		t.Fatalf("symlink escape was not staged: path = %q", a.Path)
	}
	if !isUnderDir(a.Path, leleDir) {
		t.Errorf("staged path %q escaped leleDir %q", a.Path, leleDir)
	}
	if a.Name != "via-link.bin" {
		t.Errorf("Name = %q, want via-link.bin (original base name)", a.Name)
	}
	got, err := os.ReadFile(a.Path)
	if err != nil || string(got) != "payload" {
		t.Errorf("staged copy content = %q, err = %v", got, err)
	}
}

// --- FIX-1: public endpoint rejects non-staging paths under leleDir ---

func TestFileView_PublicRejectsNonStagingPathUnderLeleDir(t *testing.T) {
	ts := newStagingTestServer(t)
	// File is under leleDir but NOT under tmp/attachments.
	path := filepath.Join(ts.channel.cfg.LeleDir, "secret.txt")
	if err := os.WriteFile(path, []byte("sensitive"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, _ := getView(t, ts, url.Values{"path": {path}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("public endpoint served non-staging file: status = %d, want 403", resp.StatusCode)
	}
}

// --- FIX-1: secure endpoint serves broader leleDir ---

func TestFileView_SecureServesNonStagingPath(t *testing.T) {
	ts := newStagingTestServer(t)
	path := filepath.Join(ts.channel.cfg.LeleDir, "workspace", "doc.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, body := getSecureView(t, ts, url.Values{"path": {path}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("secure endpoint rejected leleDir file: status = %d, body = %s", resp.StatusCode, body)
	}
	if body != "content" {
		t.Errorf("body = %q, want content", body)
	}
}

func TestFileView_SecureRejectsWithoutAuth(t *testing.T) {
	ts := newStagingTestServer(t)
	path := filepath.Join(ts.channel.cfg.LeleDir, "workspace", "doc.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("content"), 0644); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", ts.server.URL+"/api/v1/files/view-secure?url.QueryEscape(path)="+url.QueryEscape(path), nil)
	// No Authorization header
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// --- FIX-1: denylist ---

func TestFileView_PublicRejectsDeniedFilenames(t *testing.T) {
	ts := newStagingTestServer(t)
	dir := stagingDir(ts)
	os.MkdirAll(dir, 0755)

	denied := []string{
		"keyring.key",
		"keyring.enc",
		"config.json",
		"native_clients.json",
		"lele.db",
		"lele.db-wal",
		"lele.db-shm",
		"server.key",
		"cert.pem",
		"config.yaml",
		"config.toml",
	}

	for _, name := range denied {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("secret"), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		resp, _ := getView(t, ts, url.Values{"path": {path}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("denied file %s served: status = %d, want 403", name, resp.StatusCode)
		}
		os.Remove(path)
	}
}

func TestFileView_SecureRejectsDeniedFilenames(t *testing.T) {
	ts := newStagingTestServer(t)

	denied := map[string]string{
		"keyring.key":     "key",
		"keyring.enc":     "enc",
		"config.json":     "config",
		"lele.db":         "db",
		"server.key":      "tls key",
		"certificate.pem": "pem",
	}

	for name, content := range denied {
		path := filepath.Join(ts.channel.cfg.LeleDir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		resp, _ := getSecureView(t, ts, url.Values{"path": {path}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("secure endpoint served denied file %s: status = %d, want 403", name, resp.StatusCode)
		}
		os.Remove(path)
	}
}

// --- FIX-1: isDeniedViewPath unit tests ---

func TestIsDeniedViewPath(t *testing.T) {
	denied := []string{
		"/home/user/.lele/keyring.key",
		"/home/user/.lele/keyring.enc",
		"/home/user/.lele/config.json",
		"/home/user/.lele/native_clients.json",
		"/tmp/lele/lele.db",
		"/tmp/lele/lele.db-wal",
		"/tmp/lele/lele.db-shm",
		"/etc/ssl/server.key",
		"/etc/ssl/cert.pem",
		"/home/user/.lele/config.yaml",
		"/home/user/.lele/config.toml",
		"/home/user/.lele/subdir/config.json",
	}
	for _, p := range denied {
		if !isDeniedViewPath(p) {
			t.Errorf("expected isDeniedViewPath(%q) = true", p)
		}
	}

	allowed := []string{
		"/home/user/.lele/tmp/attachments/abc_photo.png",
		"/home/user/.lele/workspace-coder/main.go",
		"/tmp/lele/report.pdf",
		"/home/user/.lele/notes.txt",
		"/home/user/.lele/cache.json", // not config.json
	}
	for _, p := range allowed {
		if isDeniedViewPath(p) {
			t.Errorf("expected isDeniedViewPath(%q) = false", p)
		}
	}
}

// --- FIXA-2: uploads dir must be publicly viewable ---

// TestFileView_RegressionUploadsDirServed verifies that files under
// <leleDir>/tmp/uploads (the second staging dir used by handleFileUpload)
// are servable by the public GET /api/v1/files/view endpoint.
//
// This test FAILS (403) on code before the fixa-2 patch because the jail
// only allowed <leleDir>/tmp/attachments.
func TestFileView_RegressionUploadsDirServed(t *testing.T) {
	ts := newStagingTestServer(t)
	// Simulate a file as it would exist after handleFileUpload: under
	// tmp/uploads with a uuid-prefixed name.
	path := writeUploadFile(t, ts, "abcd1234_photo.png", "image-bytes")

	resp, body := getView(t, ts, url.Values{"path": {path}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("uploads dir file rejected: status = %d, want 200; body = %s", resp.StatusCode, body)
	}
	if body != "image-bytes" {
		t.Errorf("body = %q, want image-bytes", body)
	}
}

// TestFileView_UploadsDirDeniedSensitiveFiles verifies that the defence-in-depth
// denylist (keyring.key, config.json, *.db, etc.) still applies to files
// under the uploads directory — the broader jail must not bypass denylist.
// Filenames in the uploads dir carry a uuid prefix (e.g. abcd_keyring.key),
// but the denylist checks the base name, so files that still end with a
// denied suffix must be rejected.
func TestFileView_UploadsDirDeniedSensitiveFiles(t *testing.T) {
	ts := newStagingTestServer(t)

	denied := []string{
		"keyring.key",
		"keyring.enc",
		"config.json",
		"native_clients.json",
		"data.db",
		"data.db-wal",
		"data.db-shm",
		"server.key",
		"cert.pem",
		"config.yaml",
		"config.toml",
	}

	for _, name := range denied {
		// Write directly to the uploads dir with the exact denied name
		// (no uuid prefix) to test that the denylist fires.
		dir := uploadDir(ts)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("secret-data"), 0644); err != nil {
			t.Fatal(err)
		}
		resp, _ := getView(t, ts, url.Values{"path": {path}})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("denied file %s in uploads served: status = %d, want 403", name, resp.StatusCode)
		}
		os.Remove(path)
	}
}

// TestFileView_RejectsPathOutsideBothStagingRoots verifies that paths under
// leleDir but outside both tmp/attachments and tmp/uploads are rejected.
func TestFileView_RejectsPathOutsideBothStagingRoots(t *testing.T) {
	ts := newStagingTestServer(t)
	// Directly under leleDir
	secret := filepath.Join(ts.channel.cfg.LeleDir, "config.json")
	if err := os.WriteFile(secret, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	resp, _ := getView(t, ts, url.Values{"path": {secret}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("config.json under leleDir: status = %d, want 403", resp.StatusCode)
	}

	// Under tmp/ but in a different subdir
	other := filepath.Join(ts.channel.cfg.LeleDir, "tmp", "otro", "x.png")
	if err := os.MkdirAll(filepath.Dir(other), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("img"), 0644); err != nil {
		t.Fatal(err)
	}
	resp, _ = getView(t, ts, url.Values{"path": {other}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("tmp/otro/x.png: status = %d, want 403", resp.StatusCode)
	}
}

// TestFileView_RejectsUploadsSiblingPrefixEscape verifies that a directory
// with a name like "tmp/uploads_evil" is not accepted under the uploads jail.
func TestFileView_RejectsUploadsSiblingPrefixEscape(t *testing.T) {
	ts := newStagingTestServer(t)
	leleDir := ts.channel.cfg.LeleDir
	evilDir := filepath.Join(leleDir, "tmp", "uploads_evil")
	if err := os.MkdirAll(evilDir, 0755); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(evilDir, "payload.png")
	if err := os.WriteFile(evil, []byte("evil"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, _ := getView(t, ts, url.Values{"path": {evil}})
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("uploads_evil sibling prefix: status = %d, want 403", resp.StatusCode)
	}
}

// TestFileView_UploadsSymlinkEscape verifies that a symlink inside
// tmp/uploads pointing outside both staging roots is rejected.
func TestFileView_UploadsSymlinkEscape(t *testing.T) {
	ts := newStagingTestServer(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("leaked"), 0644); err != nil {
		t.Fatal(err)
	}
	dir := uploadDir(ts)
	os.MkdirAll(dir, 0755)
	link := filepath.Join(dir, "sneaky.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	resp, body := getView(t, ts, url.Values{"path": {link}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("symlink escape via uploads: status = %d, want 403 (%s)", resp.StatusCode, body)
	}
	if strings.Contains(body, "leaked") {
		t.Errorf("symlink target content leaked: %q", body)
	}
}
