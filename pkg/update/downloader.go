package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	checksumsMaxBytes = 1 << 20
	signatureMaxBytes = 1 << 10
)

type DownloadResult struct {
	ArchivePath string
	BinaryPath  string
	TempDir     string
	Size        int64
}

type Downloader struct {
	Client *http.Client

	signingKey ed25519.PublicKey
}

func NewDownloader() *Downloader {
	return &Downloader{
		Client:     &http.Client{Timeout: 10 * time.Minute},
		signingKey: releaseSigningPublicKey,
	}
}

func (d *Downloader) releaseKey() (ed25519.PublicKey, error) {
	if len(d.signingKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf(
			"%w: the downloader has a %d byte key, expected %d",
			errReleaseSigningKeyNotConfigured, len(d.signingKey), ed25519.PublicKeySize,
		)
	}
	return d.signingKey, nil
}

type Platform struct {
	OS   string
	Arch string
}

func CurrentPlatform() (Platform, error) {
	var p Platform
	switch runtime.GOOS {
	case "linux":
		p.OS = "Linux"
	case "darwin":
		p.OS = "Darwin"
	case "windows":
		p.OS = "Windows"
	case "freebsd":
		p.OS = "Freebsd"
	default:
		return p, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}

	switch runtime.GOARCH {
	case "amd64":
		p.Arch = "x86_64"
	case "386":
		p.Arch = "i386"
	case "arm64":
		p.Arch = "arm64"
	case "arm":
		arm := os.Getenv("GOARM")
		if arm == "" {
			arm = "7"
		}
		p.Arch = "armv" + arm
	case "riscv64", "s390x", "mips64":
		p.Arch = runtime.GOARCH
	default:
		return p, fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
	return p, nil
}

func ArchiveName(p Platform) string {
	if p.OS == "Windows" {
		return fmt.Sprintf("lele_%s_%s.zip", p.OS, p.Arch)
	}
	return fmt.Sprintf("lele_%s_%s.tar.gz", p.OS, p.Arch)
}

func ChecksumsName(version string) string {
	return fmt.Sprintf("lele_%s_checksums.txt", strings.TrimPrefix(version, "v"))
}

func (d *Downloader) Download(ctx context.Context, rel *Release, progress func(downloaded, total int64)) (*DownloadResult, error) {
	signingKey, err := d.releaseKey()
	if err != nil {
		return nil, err
	}

	platform, err := CurrentPlatform()
	if err != nil {
		return nil, err
	}

	archiveName := ArchiveName(platform)
	asset := rel.FindAsset(archiveName)
	if asset == nil {
		return nil, fmt.Errorf("release %s has no asset for this platform (%s)", rel.Tag, archiveName)
	}
	checksumsName := ChecksumsName(rel.Version())
	checksumsAsset := rel.FindAsset(checksumsName)
	if checksumsAsset == nil {
		return nil, fmt.Errorf("release %s has no checksums file", rel.Tag)
	}
	signatureName := ChecksumsSignatureName(rel.Version())
	signatureAsset := rel.FindAsset(signatureName)
	if signatureAsset == nil {
		return nil, fmt.Errorf(
			"release %s is not signed: no %s asset (refusing to install an unsigned release)",
			rel.Tag, signatureName,
		)
	}

	tempDir, err := os.MkdirTemp("", "lele-update-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }

	checksumsPath := filepath.Join(tempDir, checksumsName)
	if err := d.downloadFile(ctx, checksumsAsset.URL, checksumsPath, 0, checksumsMaxBytes, nil); err != nil {
		cleanup()
		return nil, fmt.Errorf("downloading checksums: %w", err)
	}

	signaturePath := filepath.Join(tempDir, signatureName)
	if err := d.downloadFile(ctx, signatureAsset.URL, signaturePath, 0, signatureMaxBytes, nil); err != nil {
		cleanup()
		return nil, fmt.Errorf("downloading checksums signature: %w", err)
	}

	if err := verifyChecksumsSignature(signingKey, checksumsPath, signaturePath); err != nil {
		cleanup()
		return nil, err
	}

	archivePath := filepath.Join(tempDir, archiveName)
	if err := d.downloadFile(ctx, asset.URL, archivePath, asset.Size, 0, progress); err != nil {
		cleanup()
		return nil, fmt.Errorf("downloading archive: %w", err)
	}

	if err := verifyChecksum(archivePath, checksumsPath, archiveName); err != nil {
		cleanup()
		return nil, err
	}

	binaryPath, err := extractBinary(archivePath, tempDir, platform.OS == "Windows")
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("extracting binary: %w", err)
	}

	return &DownloadResult{
		ArchivePath: archivePath,
		BinaryPath:  binaryPath,
		TempDir:     tempDir,
		Size:        asset.Size,
	}, nil
}

func (d *Downloader) downloadFile(ctx context.Context, url, dst string, total, maxBytes int64, progress func(downloaded, total int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)

	resp, err := d.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	if total == 0 {
		total = resp.ContentLength
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	body := io.Reader(resp.Body)
	if maxBytes > 0 {
		body = io.LimitReader(resp.Body, maxBytes+1)
	}

	written, err := copyWithProgress(f, body, total, progress)
	if err != nil {
		return err
	}
	if maxBytes > 0 && written > maxBytes {
		return fmt.Errorf("%s returned %d bytes, more than the %d byte limit", url, written, maxBytes)
	}
	return nil
}

func copyWithProgress(dst io.Writer, src io.Reader, total int64, progress func(downloaded, total int64)) (int64, error) {
	if progress == nil {
		return io.Copy(dst, src)
	}

	var downloaded int64
	buf := make([]byte, 64*1024)
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return downloaded, writeErr
			}
			downloaded += int64(n)
			progress(downloaded, total)
		}
		if readErr == io.EOF {
			return downloaded, nil
		}
		if readErr != nil {
			return downloaded, readErr
		}
	}
}

func verifyChecksumsSignature(publicKey ed25519.PublicKey, checksumsPath, signaturePath string) error {
	checksums, err := os.ReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}
	signature, err := os.ReadFile(signaturePath)
	if err != nil {
		return fmt.Errorf("reading checksums signature: %w", err)
	}
	if err := VerifyChecksumsSignature(publicKey, checksums, signature); err != nil {
		return fmt.Errorf("verifying release signature: %w", err)
	}
	return nil
}

func verifyChecksum(archivePath, checksumsPath, archiveName string) error {
	data, err := os.ReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("reading checksums: %w", err)
	}

	expected := ""
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == archiveName {
			expected = fields[0]
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("archive %q not found in checksums file", archiveName)
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))

	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expected, actual)
	}
	return nil
}

func extractBinary(archivePath, outDir string, isZip bool) (string, error) {
	binaryName := "lele"
	if isZip {
		binaryName = "lele.exe"
	}

	if isZip {
		return extractFromZip(archivePath, outDir, binaryName)
	}
	return extractFromTarGz(archivePath, outDir, binaryName)
}

func extractFromTarGz(archivePath, outDir, binaryName string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(hdr.Name) != binaryName || hdr.Typeflag != tar.TypeReg {
			continue
		}
		dst := filepath.Join(outDir, binaryName)
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", fmt.Errorf("closing extracted binary: %w", err)
		}
		return dst, nil
	}
	return "", fmt.Errorf("binary %q not found in archive", binaryName)
}

func extractFromZip(archivePath, outDir, binaryName string) (string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	for _, zf := range r.File {
		if filepath.Base(zf.Name) != binaryName {
			continue
		}
		src, err := zf.Open()
		if err != nil {
			return "", err
		}
		dst := filepath.Join(outDir, binaryName)
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			src.Close()
			return "", err
		}
		_, err = io.Copy(out, src)
		src.Close()
		if err != nil {
			out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", fmt.Errorf("closing extracted binary: %w", err)
		}
		return dst, nil
	}
	return "", fmt.Errorf("binary %q not found in archive", binaryName)
}
