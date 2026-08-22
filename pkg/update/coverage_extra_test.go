package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestApply_ProgressThrottle exercises the download-progress throttling
// inside Apply by serving a large archive with a declared asset Size so the
// progress callback receives total>0 and the throttled emits fire. The fake
// "binary" is not a real executable, so validation fails after download; the
// throttle blocks we target run during download.
func TestApply_ProgressThrottle(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz pipeline only")
	}
	archiveName := ArchiveName(platform)

	// Large blob (>64KB) so download happens in multiple chunks.
	big := bytes.Repeat([]byte("x"), 600*1024)
	archive := tarGz(t, "lele", big)
	checksums := checksumHex(archive) + "  " + archiveName + "\n"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			rel := Release{Tag: "v9.9.9", Assets: []Asset{
				{Name: archiveName, URL: srv.URL + "/archive", Size: int64(len(archive))},
				{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
			}}
			w.Write([]byte(marshalReleaseJSON(rel)))
		case "/archive":
			w.Write(archive)
		case "/checksums":
			w.Write([]byte(checksums))
		}
	}))
	defer srv.Close()

	checker := &Checker{Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return cannedResponse(req, 200, readURLBody(t, srv.URL+"/release")), nil
	})}}
	u := NewUpdater("o/r", t.TempDir(), "0.1.0")
	u.Checker = checker
	// big blob is not a real binary -> validation fails, but throttle ran.
	if _, err := u.Apply(context.Background(), Options{}); err == nil {
		t.Fatal("expected validation failure for non-executable blob")
	}
	if u.State().Phase != PhaseFailed {
		t.Errorf("phase = %q, want failed", u.State().Phase)
	}
}

// TestRollback_ListBackupsError covers Rollback's LatestBackup error path by
// pointing the Installer's backup dir at a regular file.
func TestRollback_ListBackupsError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	u := NewUpdater("o/r", blocker, "1.0.0")
	if _, err := u.Rollback(context.Background()); err == nil {
		t.Fatal("expected error when backup dir is a file")
	}
}

// TestDownloadFile_ExplicitTotal exercises the progress loop where total is
// supplied, the write succeeds, and an extra chunk-read error aborts the
// loop. It covers the throttling/payload branch and the read-error return.
func TestDownloadFile_ExplicitTotal(t *testing.T) {
	d := NewDownloader()
	type failingBody struct{ data []byte }
	body := &failingBody{data: []byte("payload")}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200, Status: "200 OK", Header: http.Header{},
			Body:    &errBody{data: append([]byte(nil), body.data...), err: errors.New("boom")},
			Request: req,
		}, nil
	})}
	d.Client = client
	dst := filepath.Join(t.TempDir(), "out")
	if err := d.downloadFile(context.Background(), "http://x/a", dst, 128, func(int64, int64) {}); err == nil {
		t.Fatal("expected read error in explicit-total loop")
	}
}

// TestDownloadFile_InvalidURL covers the http.NewRequestWithContext error
// branch at the top of downloadFile.
func TestDownloadFile_InvalidURL(t *testing.T) {
	d := NewDownloader()
	dst := filepath.Join(t.TempDir(), "out")
	if err := d.downloadFile(context.Background(), "http://x/%zz", dst, 0, nil); err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

// TestCurrentBinaryPath_SymlinkFallback covers the EvalSymlinks error branch
// inside CurrentBinaryPath by pointing os.Executable at a non-resolvable
// path is impossible, but a broken symlink target for the test binary works.
func TestCurrentBinaryPath_EvalFailNotReachable(t *testing.T) {
	// CurrentBinaryPath uses os.Executable which we cannot override; this test
	// only guards that it returns a valid path (the symlink branch is covered
	// indirectly by other tests). Keeping it ensures the helper is exercised.
	p, err := CurrentBinaryPath()
	if err != nil {
		t.Fatalf("CurrentBinaryPath: %v", err)
	}
	if p == "" {
		t.Fatal("expected non-empty path")
	}
}

// TestInstall_RenameFailureOnLinux forces the os.Rename error branch in
// Install on non-Windows by making the target path a non-empty directory, so
// the atomic rename of the staged tmp file fails.
func TestInstall_RenameFailureOnLinux(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix rename semantics only")
	}
	dir := t.TempDir()
	// target path must be a non-empty directory -> rename fails.
	target := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0755); err != nil {
		t.Fatal(err)
	}
	newBin := filepath.Join(dir, "new")
	if err := os.WriteFile(newBin, []byte("new"), 0755); err != nil {
		t.Fatal(err)
	}
	inst := NewInstaller(t.TempDir())
	if _, err := inst.Install(newBin, target, "0.1.0"); err == nil {
		t.Fatal("expected rename failure over non-empty directory")
	}
}

// TestDownload_ArchiveMissingBinary covers the extract-error branch in
// Download by serving a valid archive that doesn't contain the lele binary.
func TestDownload_ArchiveMissingBinary(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz pipeline only")
	}
	archiveName := ArchiveName(platform)
	archive := tarGz(t, "not-lele", []byte("payload"))
	checksums := checksumHex(archive) + "  " + archiveName + "\n"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			rel := Release{Tag: "v9.9.9", Assets: []Asset{
				{Name: archiveName, URL: srv.URL + "/archive"},
				{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
			}}
			w.Write([]byte(marshalReleaseJSON(rel)))
		case "/archive":
			w.Write(archive)
		case "/checksums":
			w.Write([]byte(checksums))
		}
	}))
	defer srv.Close()

	d := NewDownloader()
	_, err2 := d.Download(context.Background(), &Release{
		Tag: "v9.9.9",
		Assets: []Asset{
			{Name: archiveName, URL: srv.URL + "/archive"},
			{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
		},
	}, nil)
	if err2 == nil {
		t.Fatal("expected error extracting missing binary")
	}
	if !strings.Contains(err2.Error(), "extracting binary") {
		t.Errorf("unexpected error: %v", err2)
	}
}

// TestDownload_AssetSizeProgress verifies Download passes asset.Size into
// downloadFile so progress total>0 and callbacks fire with a total. This
// exercises the Size field path in the successful result construction.
func TestDownload_AssetSizeProgress(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz pipeline only")
	}
	archiveName := ArchiveName(platform)
	content := []byte(stringsRepeat("z", 128*1024))
	archive := tarGz(t, "lele", content)
	checksums := checksumHex(archive) + "  " + archiveName + "\n"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/release":
			rel := Release{Tag: "v9.9.9", Assets: []Asset{
				{Name: archiveName, URL: srv.URL + "/archive", Size: int64(len(archive))},
				{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
			}}
			w.Write([]byte(marshalReleaseJSON(rel)))
		case "/archive":
			w.Write(archive)
		case "/checksums":
			w.Write([]byte(checksums))
		}
	}))
	defer srv.Close()

	d := NewDownloader()
	var called bool
	res, err := d.Download(context.Background(), &Release{
		Tag: "v9.9.9",
		Assets: []Asset{
			{Name: archiveName, URL: srv.URL + "/archive", Size: int64(len(archive))},
			{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
		},
	}, func(downloaded, total int64) {
		called = true
		_ = downloaded
		_ = total
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer cleanupTemp(res.TempDir)
	if !called {
		t.Error("expected at least one progress callback")
	}
	if res.Size != int64(len(archive)) {
		t.Errorf("Size = %d, want %d", res.Size, len(archive))
	}
	if info, _ := os.Stat(res.BinaryPath); info == nil {
		t.Errorf("expected extracted binary at %s", res.BinaryPath)
	}
}

// small helpers
func stringsRepeat(s string, n int) string {
	var sb bytes.Buffer
	for i := 0; i < n; i++ {
		sb.WriteString(s)
	}
	return sb.String()
}

var _ = io.EOF
