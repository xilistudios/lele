package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	installShPath = "../../install.sh"

	pinnedKeyAssignment = "RELEASE_SIGNING_PUBKEY_PEM='"
	pemEndMarker        = "-----END PUBLIC KEY-----'"

	testReleaseTag = "v9.9.9"
	testReleaseVer = "9.9.9"
)

type installOptions struct {
	hashTool   bool
	openSSL    bool
	signature  string
	pinTestKey bool
	tamper     bool
}

const (
	signatureValid   = "valid"
	signatureInvalid = "invalid"
	signatureAbsent  = "absent"
)

type installHarness struct {
	script    string
	binDir    string
	assetsDir string
	prefixDir string
	env       []string
}

func newInstallHarness(t *testing.T, opts installOptions) *installHarness {
	t.Helper()

	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar is required for the installer harness")
	}

	dir := t.TempDir()
	harness := &installHarness{
		script:    filepath.Join(dir, "install.sh"),
		binDir:    filepath.Join(dir, "bin"),
		assetsDir: filepath.Join(dir, "assets"),
		prefixDir: filepath.Join(dir, "prefix"),
	}
	home := filepath.Join(dir, "home")
	for _, path := range []string{harness.binDir, harness.assetsDir, home} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("creating %s: %v", path, err)
		}
	}

	publicKey, privateKey := testSigningKeys(t)
	writeInstallerAssets(t, harness.assetsDir, privateKey, opts)

	writeScript(t, harness.script, publicKey, opts.pinTestKey)
	writeStubTool(t, harness.binDir, "uname", stubUname)
	writeStubTool(t, harness.binDir, "curl", stubCurl)
	for _, tool := range []string{"grep", "awk", "sed", "head", "cat", "gzip", "mktemp", "rm", "mkdir", "cp", "chmod", "ls", "tar"} {
		linkTool(t, harness.binDir, tool)
	}
	if opts.openSSL {
		linkTool(t, harness.binDir, "openssl")
	}
	if opts.hashTool {
		linkTool(t, harness.binDir, firstAvailable(t, "sha256sum", "shasum"))
	}

	harness.env = []string{
		"PATH=" + harness.binDir,
		"HOME=" + home,
		"LELE_TEST_ASSETS=" + harness.assetsDir,
	}
	return harness
}

func writeInstallerAssets(t *testing.T, assetsDir string, privateKey ed25519.PrivateKey, opts installOptions) {
	t.Helper()

	archive := makeTarGz(t, "lele", []byte("#!/bin/sh\necho lele 9.9.9\n"))
	sum := sha256.Sum256(archive)
	archiveName := "lele_Linux_x86_64.tar.gz"
	checksums := fmt.Appendf(nil, "%s  %s\n", hex.EncodeToString(sum[:]), archiveName)

	if opts.tamper {
		archive = makeTarGz(t, "lele", []byte("#!/bin/sh\necho pwned\n"))
	}

	writeAsset(t, assetsDir, archiveName, archive)
	writeAsset(t, assetsDir, "lele_"+testReleaseVer+"_checksums.txt", checksums)

	switch opts.signature {
	case signatureValid:
		writeAsset(t, assetsDir, checksumSignatureName(), ed25519.Sign(privateKey, checksums))
	case signatureInvalid:
		writeAsset(t, assetsDir, checksumSignatureName(), ed25519.Sign(privateKey, []byte("other\n")))
	case signatureAbsent:
	default:
		t.Fatalf("unknown signature mode %q", opts.signature)
	}
}

func checksumSignatureName() string {
	return "lele_" + testReleaseVer + "_checksums.txt.sig"
}

func writeAsset(t *testing.T, dir, name string, content []byte) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatalf("writing asset %s: %v", name, err)
	}
}

func writeScript(t *testing.T, path string, publicKey ed25519.PublicKey, pinTestKey bool) {
	t.Helper()

	source, err := os.ReadFile(installShPath)
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	script := string(source)
	if pinTestKey {
		script = replacePinnedKey(t, script, publicKey)
	}

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing install.sh copy: %v", err)
	}
}

func replacePinnedKey(t *testing.T, script string, publicKey ed25519.PublicKey) string {
	t.Helper()

	pemBytes, err := pemForPublicKey(publicKey)
	if err != nil {
		t.Fatalf("encoding test public key: %v", err)
	}

	start := strings.Index(script, pinnedKeyAssignment)
	if start < 0 {
		t.Fatalf("install.sh has no %q assignment", pinnedKeyAssignment)
	}
	end := strings.Index(script[start:], pemEndMarker)
	if end < 0 {
		t.Fatalf("install.sh has no %q marker", pemEndMarker)
	}

	return script[:start] + pinnedKeyAssignment + string(pemBytes) + script[start+end:]
}

func pemForPublicKey(publicKey ed25519.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("marshalling public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

func linkTool(t *testing.T, binDir, name string) {
	t.Helper()

	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("%s is required for the installer harness: %v", name, err)
	}
	if err := os.Symlink(path, filepath.Join(binDir, name)); err != nil {
		t.Fatalf("linking %s: %v", name, err)
	}
}

func firstAvailable(t *testing.T, names ...string) string {
	t.Helper()

	for _, name := range names {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	t.Skipf("none of %s is available", strings.Join(names, ", "))
	return ""
}

func writeStubTool(t *testing.T, binDir, name, content string) {
	t.Helper()

	path := filepath.Join(binDir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("writing stub %s: %v", name, err)
	}
}

const stubUname = `#!/bin/sh
case "${1:-}" in
  -m) echo x86_64 ;;
  *) echo Linux ;;
esac
`

const stubCurl = `#!/bin/sh
set -u
dst=""
url=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) dst="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
name=${url##*/}
src="${LELE_TEST_ASSETS}/${name}"
if [ ! -f "$src" ]; then
  printf 'curl: (22) 404 %s\n' "$url" >&2
  exit 22
fi
if [ -n "$dst" ]; then
  cp "$src" "$dst"
else
  printf '%s' "$(cat "$LELE_TEST_ASSETS/$name")"
fi
`

func (h *installHarness) run(t *testing.T) (stdout, stderr string, err error) {
	t.Helper()

	cmd := exec.Command(h.script, "--version", testReleaseTag, "--prefix", h.prefixDir)
	cmd.Env = h.env
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func (h *installHarness) installedBinary(t *testing.T) string {
	t.Helper()

	return filepath.Join(h.prefixDir, "bin", "lele")
}

func TestInstallSh_FailClosedWithoutHashTool(t *testing.T) {
	harness := newInstallHarness(t, installOptions{signature: signatureInvalid})

	stdout, stderr, err := harness.run(t)
	if err == nil {
		t.Fatalf("install.sh exited 0 without a sha256 tool\nstdout: %s", stdout)
	}
	if !strings.Contains(stderr, "sha256sum or shasum is required") {
		t.Errorf("stderr = %q, want the fail-closed checksum message", stderr)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr == nil {
		t.Error("install.sh installed a binary it could not verify")
	}
}

func TestInstallSh_RejectsInvalidSignature(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:   true,
		openSSL:    true,
		signature:  signatureInvalid,
		pinTestKey: true,
	})

	stdout, stderr, err := harness.run(t)
	if err == nil {
		t.Fatalf("install.sh installed a release with a bad signature\nstdout: %s", stdout)
	}
	if !strings.Contains(stderr, "Release signature verification failed") {
		t.Errorf("stderr = %q, want a signature failure message", stderr)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr == nil {
		t.Error("install.sh installed a release whose signature did not verify")
	}
}

func TestInstallSh_VerifiesSignatureAndChecksum(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:   true,
		openSSL:    true,
		signature:  signatureValid,
		pinTestKey: true,
	})

	stdout, stderr, err := harness.run(t)
	if err != nil {
		t.Fatalf("install.sh failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "Release signature OK") {
		t.Errorf("stdout = %q, want a signature OK message", stdout)
	}
	if !strings.Contains(stdout, "Checksum OK") {
		t.Errorf("stdout = %q, want a checksum OK message", stdout)
	}

	info, statErr := os.Stat(harness.installedBinary(t))
	if statErr != nil {
		t.Fatalf("binary was not installed: %v", statErr)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: %v", info.Mode())
	}
}

func TestInstallSh_WarnsWhenOpenSSLIsMissing(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:   true,
		signature:  signatureValid,
		pinTestKey: true,
	})

	stdout, stderr, err := harness.run(t)
	if err != nil {
		t.Fatalf("install.sh failed without openssl: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "openssl not found") {
		t.Errorf("stdout = %q, want a loud warning about the missing openssl", stdout)
	}
	if !strings.Contains(stdout, "Checksum OK") {
		t.Errorf("stdout = %q, want the checksum to still be enforced", stdout)
	}
}

func TestInstallSh_InstallsUnsignedReleaseWithWarning(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:   true,
		openSSL:    true,
		signature:  signatureAbsent,
		pinTestKey: true,
	})

	stdout, stderr, err := harness.run(t)
	if err != nil {
		t.Fatalf("install.sh rejected an unsigned legacy release: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}
	if !strings.Contains(stdout, "not signed") {
		t.Errorf("stdout = %q, want a warning that the release is not signed", stdout)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr != nil {
		t.Errorf("binary was not installed: %v", statErr)
	}
}

func TestInstallSh_RejectsChecksumMismatch(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:   true,
		openSSL:    true,
		signature:  signatureValid,
		pinTestKey: true,
		tamper:     true,
	})

	stdout, stderr, err := harness.run(t)
	if err == nil {
		t.Fatalf("install.sh installed a tampered archive\nstdout: %s", stdout)
	}
	if !strings.Contains(stdout+stderr, "Checksum mismatch") {
		t.Errorf("output = %q, want a checksum mismatch error", stdout+stderr)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr == nil {
		t.Error("install.sh installed a tampered archive")
	}
}

func TestInstallSh_PinnedKeyMatchesPackage(t *testing.T) {
	source, err := os.ReadFile(installShPath)
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}

	script := string(source)
	start := strings.Index(script, pinnedKeyAssignment)
	if start < 0 {
		t.Fatalf("install.sh has no %q assignment", pinnedKeyAssignment)
	}
	rest := script[start+len(pinnedKeyAssignment):]
	end := strings.Index(rest, pemEndMarker)
	if end < 0 {
		t.Fatalf("install.sh has no %q marker", pemEndMarker)
	}

	block, _ := pem.Decode([]byte(rest[:end] + "-----END PUBLIC KEY-----\n"))
	if block == nil {
		t.Fatal("install.sh pinned key is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatalf("parsing install.sh pinned key: %v", err)
	}
	installShKey, ok := parsed.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("install.sh pinned key is %T, want ed25519.PublicKey", parsed)
	}

	pinned := ReleaseSigningPublicKey()
	if len(pinned) != ed25519.PublicKeySize {
		t.Fatalf("package pinned key is %d bytes, want %d", len(pinned), ed25519.PublicKeySize)
	}
	if !bytes.Equal(installShKey, pinned) {
		t.Error("install.sh and pkg/update pin different release signing keys")
	}
}
