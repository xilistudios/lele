package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func testPlatform(t *testing.T) Platform {
	t.Helper()

	platform, err := CurrentPlatform()
	if err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	if platform.OS == "Windows" {
		t.Skip("tar.gz fixture only")
	}
	return platform
}

type releaseFixture struct {
	Tag             string
	Platform        Platform
	Archive         []byte
	Checksums       []byte
	Signature       []byte
	IncludeSigAsset bool
	ServeSigFile    bool
}

func newReleaseFixture(t *testing.T) *releaseFixture {
	t.Helper()

	platform := testPlatform(t)
	archive := makeTarGz(t, "lele", []byte("#!/bin/sh\necho lele 9.9.9\n"))
	sum := sha256.Sum256(archive)

	return &releaseFixture{
		Tag:             "v9.9.9",
		Platform:        platform,
		Archive:         archive,
		Checksums:       fmt.Appendf(nil, "%s  %s\n", hex.EncodeToString(sum[:]), ArchiveName(platform)),
		IncludeSigAsset: true,
		ServeSigFile:    true,
	}
}

func (f *releaseFixture) sign(privateKey ed25519.PrivateKey) {
	f.Signature = ed25519.Sign(privateKey, f.Checksums)
}

func (f *releaseFixture) serve(t *testing.T) *httptest.Server {
	t.Helper()

	files := map[string][]byte{
		ArchiveName(f.Platform): f.Archive,
		ChecksumsName(f.Tag):    f.Checksums,
	}
	if f.ServeSigFile {
		files[ChecksumsSignatureName(f.Tag)] = f.Signature
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func (f *releaseFixture) release(srv *httptest.Server) *Release {
	names := []string{ArchiveName(f.Platform), ChecksumsName(f.Tag)}
	if f.IncludeSigAsset {
		names = append(names, ChecksumsSignatureName(f.Tag))
	}

	rel := &Release{Tag: f.Tag}
	for _, name := range names {
		rel.Assets = append(rel.Assets, Asset{Name: name, URL: srv.URL + "/" + name})
	}
	return rel
}

func downloadFixture(t *testing.T, f *releaseFixture, publicKey ed25519.PublicKey) (*DownloadResult, error) {
	t.Helper()

	srv := f.serve(t)
	result, err := newTestDownloader(publicKey).Download(context.Background(), f.release(srv), nil)
	if err == nil {
		t.Cleanup(func() { _ = os.RemoveAll(result.TempDir) })
	}
	return result, err
}

func newTestDownloader(publicKey ed25519.PublicKey) *Downloader {
	d := NewDownloader()
	d.signingKey = publicKey
	return d
}

func TestDownloadVerifyExtract(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.sign(privateKey)

	srv := f.serve(t)
	d := newTestDownloader(publicKey)

	var lastPct float64
	res, err := d.Download(context.Background(), f.release(srv), func(downloaded, total int64) {
		if total > 0 {
			lastPct = float64(downloaded) / float64(total) * 100
		}
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(res.TempDir) })

	got, err := os.ReadFile(res.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("#!/bin/sh\necho lele 9.9.9\n")) {
		t.Error("extracted binary content mismatch")
	}
	if lastPct < 99.9 {
		t.Errorf("progress should reach ~100%%, got %.1f", lastPct)
	}
}

func TestDownloadChecksumMismatch(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.Checksums = fmt.Appendf(nil, "%s  %s\n", strings.Repeat("0", 64), ArchiveName(f.Platform))
	f.sign(privateKey)

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error = %v, want a checksum mismatch", err)
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

func TestDownload_RejectsUnsignedRelease(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.IncludeSigAsset = false
	f.sign(privateKey)

	result, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected Download to reject a release without a signature asset")
	}
	if result != nil {
		t.Errorf("Download returned a result alongside the error: %+v", result)
	}
	want := ChecksumsSignatureName(f.Tag)
	if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "not signed") {
		t.Errorf("error = %v, want it to name the missing %s asset", err, want)
	}
}

func TestDownload_RejectsInvalidSignature(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.sign(privateKey)
	f.Signature = ed25519.Sign(privateKey, []byte("a different checksums file\n"))

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected Download to reject an invalid signature")
	}
	if !strings.Contains(err.Error(), "invalid release signature") {
		t.Errorf("error = %v, want an invalid release signature error", err)
	}
}

func TestDownload_RejectsTamperedChecksums(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.sign(privateKey)
	f.Checksums = bytes.Replace(f.Checksums, []byte("lele_"), []byte("evil_"), 1)

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected Download to reject tampered checksums")
	}
	if !strings.Contains(err.Error(), "invalid release signature") {
		t.Errorf("error = %v, want an invalid release signature error", err)
	}
}

func TestDownload_VerifiesSignatureBeforeChecksum(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.Checksums = fmt.Appendf(nil, "%s  %s\n", strings.Repeat("0", 64), ArchiveName(f.Platform))
	f.sign(privateKey)
	f.Signature = ed25519.Sign(privateKey, []byte("unrelated\n"))

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "invalid release signature") {
		t.Errorf("error = %v, want the signature check to run before the checksum check", err)
	}
	if strings.Contains(err.Error(), "checksum mismatch") {
		t.Error("checksum was trusted before the release signature was verified")
	}
}

func TestDownload_RejectsSignatureFromAnotherKey(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, _ := testSigningKeys(t)
	_, otherPrivateKey := testSigningKeys(t)
	f.sign(otherPrivateKey)

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected Download to reject a signature from an unrelated key")
	}
	if !strings.Contains(err.Error(), "invalid release signature") {
		t.Errorf("error = %v, want an invalid release signature error", err)
	}
}

func TestDownload_RejectsTruncatedSignature(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.sign(privateKey)
	f.Signature = f.Signature[:16]

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected Download to reject a truncated signature")
	}
	if !strings.Contains(err.Error(), "invalid release signature") {
		t.Errorf("error = %v, want an invalid release signature error", err)
	}
}

func TestDownload_MissingSignatureFile(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.sign(privateKey)
	f.ServeSigFile = false

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected Download to fail when the signature asset cannot be fetched")
	}
	if !strings.Contains(err.Error(), "downloading checksums signature") {
		t.Errorf("error = %v, want a signature download error", err)
	}
}

func TestNewDownloader_PinsReleaseSigningKey(t *testing.T) {
	d := NewDownloader()
	key, err := d.releaseKey()
	if err != nil {
		t.Fatalf("releaseKey: %v", err)
	}
	if !bytes.Equal(key, ReleaseSigningPublicKey()) {
		t.Error("NewDownloader does not default to the pinned release signing key")
	}

	custom, _ := testSigningKeys(t)
	d.signingKey = custom
	key, err = d.releaseKey()
	if err != nil {
		t.Fatalf("releaseKey: %v", err)
	}
	if !bytes.Equal(key, custom) {
		t.Error("releaseKey ignores an explicitly configured signing key")
	}
}

func TestDownload_FailsClosedWithoutSigningKey(t *testing.T) {
	d := NewDownloader()
	d.signingKey = nil

	result, err := d.Download(context.Background(), &Release{Tag: "v9.9.9"}, nil)
	if err == nil {
		t.Fatal("expected Download to refuse an unconfigured signing key")
	}
	if result != nil {
		t.Errorf("Download returned a result alongside the error: %+v", result)
	}
	if !errors.Is(err, errReleaseSigningKeyNotConfigured) {
		t.Errorf("error = %v, want errReleaseSigningKeyNotConfigured", err)
	}
}

func TestDownload_RejectsOversizedChecksums(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.Checksums = bytes.Repeat([]byte("a"), checksumsMaxBytes+1)
	f.sign(privateKey)

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected Download to reject an oversized checksums file")
	}
	if !strings.Contains(err.Error(), "downloading checksums") || !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want an oversized checksums download error", err)
	}
}

func TestDownload_RejectsOversizedSignature(t *testing.T) {
	f := newReleaseFixture(t)
	publicKey, privateKey := testSigningKeys(t)
	f.sign(privateKey)
	f.Signature = bytes.Repeat([]byte{0}, signatureMaxBytes+1)

	_, err := downloadFixture(t, f, publicKey)
	if err == nil {
		t.Fatal("expected Download to reject an oversized signature")
	}
	if !strings.Contains(err.Error(), "downloading checksums signature") || !strings.Contains(err.Error(), "limit") {
		t.Errorf("error = %v, want an oversized signature download error", err)
	}
}

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

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("extracted binary is not executable: %v", info.Mode())
	}
}

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
