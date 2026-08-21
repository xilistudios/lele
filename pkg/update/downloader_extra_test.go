package update

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeZip builds a zip archive containing a single file.
func makeZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf strings.Builder
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(buf.String())
}

func checksumHex(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func TestExtractFromZip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "lele.zip")
	content := []byte("windows-binary")
	if err := os.WriteFile(archivePath, makeZip(t, "lele.exe", content), 0755); err != nil {
		t.Fatal(err)
	}

	dst, err := extractFromZip(archivePath, dir, "lele.exe")
	if err != nil {
		t.Fatalf("extractFromZip: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("extracted = %q, want %q", got, content)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0100 == 0 {
		t.Error("extracted zip binary should be executable")
	}
}

func TestExtractFromZip_BinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "lele.zip")
	if err := os.WriteFile(archivePath, makeZip(t, "other.exe", []byte("x")), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := extractFromZip(archivePath, dir, "lele.exe"); err == nil {
		t.Fatal("expected error for missing binary")
	}
}

func TestExtractFromZip_NotAZip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "lele.zip")
	if err := os.WriteFile(archivePath, []byte("not-a-zip"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := extractFromZip(archivePath, dir, "lele.exe"); err == nil {
		t.Fatal("expected error for corrupt zip")
	}
}

func TestDownload_MissingChecksums(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	archiveName := ArchiveName(platform)
	rel := &Release{
		Tag: "v9.9.9",
		Assets: []Asset{
			{Name: archiveName, URL: "http://dummy/archive"},
			// no checksums asset
		},
	}
	d := NewDownloader()
	if _, err := d.Download(context.Background(), rel, nil); err == nil {
		t.Fatal("expected error for missing checksums")
	}
}

func TestDownload_ArchiveHTTPError(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz test")
	}
	archiveName := ArchiveName(platform)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	rel := &Release{
		Tag: "v9.9.9",
		Assets: []Asset{
			{Name: archiveName, URL: srv.URL + "/archive"},
			{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
		},
	}
	d := NewDownloader()
	if _, err := d.Download(context.Background(), rel, nil); err == nil {
		t.Fatal("expected error on archive HTTP error")
	}
}

func TestDownload_ChecksumsHTTPError(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz test")
	}
	archiveName := ArchiveName(platform)
	archive := makeTarGz(t, "lele", []byte("binary"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			w.Write(archive)
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	rel := &Release{
		Tag: "v9.9.9",
		Assets: []Asset{
			{Name: archiveName, URL: srv.URL + "/archive"},
			{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
		},
	}
	// Point checksums at a 404 endpoint.
	rel.Assets[1].URL = srv.URL + "/missing"
	d := NewDownloader()
	if _, err := d.Download(context.Background(), rel, nil); err == nil {
		t.Fatal("expected error on checksums HTTP error")
	}
}

func TestDownload_ArchiveNotFoundInChecksums(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz test")
	}
	archiveName := ArchiveName(platform)
	archive := makeTarGz(t, "lele", []byte("binary"))
	checksums := "abcdef  someotherfile\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			w.Write(archive)
		case "/checksums":
			w.Write([]byte(checksums))
		}
	}))
	defer srv.Close()
	rel := &Release{
		Tag: "v9.9.9",
		Assets: []Asset{
			{Name: archiveName, URL: srv.URL + "/archive"},
			{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
		},
	}
	d := NewDownloader()
	if _, err := d.Download(context.Background(), rel, nil); err == nil {
		t.Fatal("expected error when archive missing from checksums")
	}
}

func TestDownload_ProgressThrottle(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz test")
	}
	archiveName := ArchiveName(platform)
	content := []byte(strings.Repeat("x", 512*1024))
	archive := makeTarGz(t, "lele", content)
	sum := checksumHex(archive)
	checksums := sum + "  " + archiveName + "\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			w.Write(archive)
		case "/checksums":
			w.Write([]byte(checksums))
		}
	}))
	defer srv.Close()
	rel := &Release{
		Tag: "v9.9.9",
		Assets: []Asset{
			{Name: archiveName, URL: srv.URL + "/archive"},
			{Name: ChecksumsName("9.9.9"), URL: srv.URL + "/checksums"},
		},
	}
	d := NewDownloader()
	var last int64
	res, err := d.Download(context.Background(), rel, func(downloaded, total int64) {
		last = downloaded
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer cleanupTemp(res.TempDir)
	if last <= 0 {
		t.Error("expected progress callbacks")
	}
}

func TestExtractFromTarGz_BinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archivePath, makeTarGz(t, "other", []byte("x")), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := extractFromTarGz(archivePath, dir, "lele"); err == nil {
		t.Fatal("expected error when binary not found")
	}
}

func TestExtractFromTarGz_InvalidGzip(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archivePath, []byte("not-gzip"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := extractFromTarGz(archivePath, dir, "lele"); err == nil {
		t.Fatal("expected error for invalid gzip")
	}
}

func TestArchiveName_Windows(t *testing.T) {
	if name := ArchiveName(Platform{OS: "Windows", Arch: "x86_64"}); name != "lele_Windows_x86_64.zip" {
		t.Errorf("windows name = %q", name)
	}
	if name := ArchiveName(Platform{OS: "Linux", Arch: "arm64"}); name != "lele_Linux_arm64.tar.gz" {
		t.Errorf("linux name = %q", name)
	}
}

func TestVerifyChecksum_ReadingError(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nonexistent-checksums.txt")
	if err := verifyChecksum(filepath.Join(dir, "a.tar.gz"), missing, "lele"); err == nil {
		t.Fatal("expected error reading missing checksums file")
	}
}

func TestVerifyChecksum_CaseInsensitiveMatch(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "lele.tar.gz")
	content := []byte("payload")
	if err := os.WriteFile(archivePath, content, 0755); err != nil {
		t.Fatal(err)
	}
	sum := strings.ToUpper(checksumHex(content))
	checksums := sum + "  lele.tar.gz\n"
	if err := verifyChecksum(archivePath, writeChecksums(t, dir, checksums), "lele.tar.gz"); err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}
}

func writeChecksums(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}