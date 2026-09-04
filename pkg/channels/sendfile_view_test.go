package channels

// Tests for outbound attachment staging and the /api/v1/files/view download
// endpoint (WebUI file download feature).
//
// Contract (sendfile-contract.md):
//   - attachment paths emitted by message.complete and persisted in history
//     are ALWAYS servable by /api/v1/files/view (i.e. under leleDir). Files
//     that live outside leleDir are copied to <leleDir>/tmp/attachments/<id>_<name>.
//   - GET /api/v1/files/view?path=<abs>&download=1 -> Content-Disposition:
//     attachment; filename="<sanitized>"; without download -> inline.

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

func TestStageAttachment_LeavesInsidePathsUnchanged(t *testing.T) {
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
	if a.Path != inside {
		t.Errorf("path inside leleDir must not change: %q -> %q", inside, a.Path)
	}
	if a.Name != "cd.png" {
		t.Errorf("name changed: %q", a.Name)
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

// --- view endpoint tests ---

func getView(t *testing.T, ts *nativeTestServer, params url.Values) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("GET", ts.server.URL+"/api/v1/files/view?"+params.Encode(), nil)
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
	path := filepath.Join(ts.channel.cfg.LeleDir, "inline.txt")
	if err := os.WriteFile(path, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

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
	path := filepath.Join(ts.channel.cfg.LeleDir, "report file.txt")
	if err := os.WriteFile(path, []byte("bytes"), 0644); err != nil {
		t.Fatal(err)
	}

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
	path := filepath.Join(ts.channel.cfg.LeleDir, "aa_report.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	resp, _ := getView(t, ts, url.Values{"path": {path}, "download": {"1"}, "name": {"report.txt"}})
	want := `attachment; filename="report.txt"`
	if cd := resp.Header.Get("Content-Disposition"); cd != want {
		t.Errorf("Content-Disposition = %q, want %q", cd, want)
	}
}

func TestFileView_NameOverrideSanitized(t *testing.T) {
	ts := newStagingTestServer(t)
	path := filepath.Join(ts.channel.cfg.LeleDir, "safe.txt")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

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
	// leleDir=/tmp/x/lele ; /tmp/x/lele-evil/file must be 403 even though it
	// passes a naive strings.HasPrefix containment check.
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
	// <leleDir>/../<tmpdir>/top.txt resolves OUTSIDE leleDir after Abs/Clean.
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
	missing := filepath.Join(ts.channel.cfg.LeleDir, "nope.txt")
	resp, _ := getView(t, ts, url.Values{"path": {missing}})
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// --- symlink containment (MEDIUM-2 fixes) ---

// A symlink inside leleDir that points OUTSIDE it must not be servable: the
// view endpoint resolves the real target and re-checks containment.
func TestFileView_RejectsSymlinkEscape(t *testing.T) {
	ts := newStagingTestServer(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("top secret"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ts.channel.cfg.LeleDir, "sneaky.txt")
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

// A regular file under leleDir served through a symlink that stays under
// leleDir still works (no false positives).
func TestFileView_AllowsSymlinkWithinLeleDir(t *testing.T) {
	ts := newStagingTestServer(t)
	real := filepath.Join(ts.channel.cfg.LeleDir, "real.txt")
	if err := os.WriteFile(real, []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ts.channel.cfg.LeleDir, "link.txt")
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

// stageAttachment must treat a symlink under leleDir pointing outside as
// OUTSIDE: the served path becomes a real copy inside the lele dir while the
// display name stays the original base name.
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
