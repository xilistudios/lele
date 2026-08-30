package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// makeTarGz builds a tar.gz archive containing a single file.
func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestDownloadVerifyExtract(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz path test only")
	}

	archiveName := ArchiveName(platform)
	binaryContent := []byte("#!/bin/sh\necho lele 9.9.9\n")
	archive := makeTarGz(t, "lele", binaryContent)

	sum := sha256.Sum256(archive)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), archiveName)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/archive":
			w.Write(archive)
		case "/checksums":
			w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
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
	var lastPct float64
	res, err := d.Download(context.Background(), rel, func(downloaded, total int64) {
		if total > 0 {
			lastPct = float64(downloaded) / float64(total) * 100
		}
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer cleanupTemp(res.TempDir)

	got, err := os.ReadFile(res.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, binaryContent) {
		t.Error("extracted binary content mismatch")
	}
	if lastPct < 99.9 {
		t.Errorf("progress should reach ~100%%, got %.1f", lastPct)
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	platform, err := CurrentPlatform()
	if err != nil || platform.OS == "Windows" {
		t.Skip("tar.gz path test only")
	}

	archiveName := ArchiveName(platform)
	archive := makeTarGz(t, "lele", []byte("binary"))
	checksums := "0000000000000000000000000000000000000000000000000000000000000000  " + archiveName + "\n"

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
	_, err = d.Download(context.Background(), rel, nil)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
}

func TestDownloadMissingAsset(t *testing.T) {
	rel := &Release{Tag: "v1.0.0", Assets: nil}
	d := NewDownloader()
	_, err := d.Download(context.Background(), rel, nil)
	if err == nil {
		t.Fatal("expected error for missing asset")
	}
}

func cleanupTemp(dir string) { _ = os.RemoveAll(dir) }

// TestExtractFromTarGz verifies that extractFromTarGz extracts the binary to
// dst without error and with the original content.
func TestExtractFromTarGz(t *testing.T) {
	outDir := t.TempDir()
	binaryContent := []byte("#!/bin/sh\necho lele test\n")
	archivePath := filepath.Join(outDir, "lele_Linux_x86_64.tar.gz")
	if err := os.WriteFile(archivePath, makeTarGz(t, "lele", binaryContent), 0644); err != nil {
		t.Fatal(err)
	}

	extractDir := t.TempDir()
	dst, err := extractFromTarGz(archivePath, extractDir, "lele")
	if err != nil {
		t.Fatalf("extractFromTarGz returned error: %v", err)
	}

	if want := filepath.Join(extractDir, "lele"); dst != want {
		t.Errorf("dst = %q, want %q", dst, want)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, binaryContent) {
		t.Errorf("extracted content mismatch: got %q, want %q", got, binaryContent)
	}

	// File must be executable (0755).
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("extracted binary is not executable: %v", info.Mode())
	}
}

// TestExtractFromTarGz_MissingBinary verifies the error when the archive does
// not contain the expected binary.
func TestExtractFromTarGz_MissingBinary(t *testing.T) {
	outDir := t.TempDir()
	archivePath := filepath.Join(outDir, "empty.tar.gz")
	if err := os.WriteFile(archivePath, makeTarGz(t, "other-file", []byte("x")), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := extractFromTarGz(archivePath, t.TempDir(), "lele"); err == nil {
		t.Error("expected error for archive without the binary, got nil")
	}
}
