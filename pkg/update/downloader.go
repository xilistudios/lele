package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
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

// DownloadResult holds the outcome of a successful download+verify+extract.
type DownloadResult struct {
	// ArchivePath is the downloaded archive (in a temp dir).
	ArchivePath string
	// BinaryPath is the extracted lele binary (in a temp dir).
	BinaryPath string
	// TempDir owns both files; caller must RemoveAll when done.
	TempDir string
	// Size is the archive size in bytes.
	Size int64
}

// Downloader downloads and verifies release assets.
type Downloader struct {
	Client *http.Client
}

// NewDownloader creates a Downloader.
func NewDownloader() *Downloader {
	return &Downloader{Client: &http.Client{Timeout: 10 * time.Minute}}
}

// Platform describes the target platform for asset selection.
type Platform struct {
	OS   string // Linux, Darwin, Windows, FreeBSD (title case, goreleaser style)
	Arch string // x86_64, arm64, armv7, ...
}

// CurrentPlatform detects the platform using the same naming as goreleaser
// archives (matching install.sh).
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
			arm = "7" // conservative default, matches most distros
		}
		p.Arch = "armv" + arm
	case "riscv64", "s390x", "mips64":
		p.Arch = runtime.GOARCH
	default:
		return p, fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}
	return p, nil
}

// ArchiveName returns the expected archive name for a platform.
func ArchiveName(p Platform) string {
	if p.OS == "Windows" {
		return fmt.Sprintf("lele_%s_%s.zip", p.OS, p.Arch)
	}
	return fmt.Sprintf("lele_%s_%s.tar.gz", p.OS, p.Arch)
}

// ChecksumsName returns the checksums file name for a release version
// (without leading v), e.g. "lele_0.9.0_checksums.txt".
func ChecksumsName(version string) string {
	return fmt.Sprintf("lele_%s_checksums.txt", strings.TrimPrefix(version, "v"))
}

// Download fetches the release archive for the current platform, verifies
// its SHA256 against the release checksums file, and extracts the binary.
// progress (optional) receives (downloaded, total) byte counts.
func (d *Downloader) Download(ctx context.Context, rel *Release, progress func(downloaded, total int64)) (*DownloadResult, error) {
	platform, err := CurrentPlatform()
	if err != nil {
		return nil, err
	}

	archiveName := ArchiveName(platform)
	asset := rel.FindAsset(archiveName)
	if asset == nil {
		return nil, fmt.Errorf("release %s has no asset for this platform (%s)", rel.Tag, archiveName)
	}
	checksumsAsset := rel.FindAsset(ChecksumsName(rel.Version()))
	if checksumsAsset == nil {
		return nil, fmt.Errorf("release %s has no checksums file", rel.Tag)
	}

	tempDir, err := os.MkdirTemp("", "lele-update-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }

	archivePath := filepath.Join(tempDir, archiveName)
	if err := d.downloadFile(ctx, asset.URL, archivePath, asset.Size, progress); err != nil {
		cleanup()
		return nil, fmt.Errorf("downloading archive: %w", err)
	}

	checksumsPath := filepath.Join(tempDir, "checksums.txt")
	if err := d.downloadFile(ctx, checksumsAsset.URL, checksumsPath, 0, nil); err != nil {
		cleanup()
		return nil, fmt.Errorf("downloading checksums: %w", err)
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

func (d *Downloader) downloadFile(ctx context.Context, url, dst string, total int64, progress func(downloaded, total int64)) error {
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

	if progress == nil {
		_, err = io.Copy(f, resp.Body)
		return err
	}

	var downloaded int64
	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				return wErr
			}
			downloaded += int64(n)
			progress(downloaded, total)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}

// verifyChecksum checks the archive's SHA256 against the checksums file.
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

// extractBinary extracts the lele binary from the archive into outDir.
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
		out.Close()
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
		out.Close()
		if err != nil {
			return "", err
		}
		return dst, nil
	}
	return "", fmt.Errorf("binary %q not found in archive", binaryName)
}
