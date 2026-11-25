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
	"runtime"
	"strings"
	"testing"
)

const (
	installShPath = "../../install.sh"

	pinnedKeysAssignment = "RELEASE_SIGNING_PUBKEYS_PEM='"
	pemEndMarker         = "-----END PUBLIC KEY-----'"

	testReleaseTag = "v9.9.9"
	testReleaseVer = "9.9.9"

	archiveAssetName = "lele_Linux_x86_64.tar.gz"

	openSSLReal      = "real"
	openSSLIncapable = "incapable"

	signatureValid   = "valid"
	signatureInvalid = "invalid"
	signatureAbsent  = "absent"
)

type installOptions struct {
	hashTool bool

	openSSL string

	signature string

	signPrivate ed25519.PrivateKey
	pinKeys     []ed25519.PublicKey

	tamper bool

	transportFailure string
}

type installHarness struct {
	script    string
	binDir    string
	assetsDir string
	prefixDir string
	logPath   string
	env       []string
}

func newInstallHarness(t *testing.T, opts installOptions) *installHarness {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("install.sh is a POSIX script; the harness needs sh, tar and symlinks")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar is required for the installer harness")
	}

	dir := t.TempDir()
	harness := &installHarness{
		script:    filepath.Join(dir, "install.sh"),
		binDir:    filepath.Join(dir, "bin"),
		assetsDir: filepath.Join(dir, "assets"),
		prefixDir: filepath.Join(dir, "prefix"),
		logPath:   filepath.Join(dir, "curl.log"),
	}
	home := filepath.Join(dir, "home")
	for _, path := range []string{harness.binDir, harness.assetsDir, home} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("creating %s: %v", path, err)
		}
	}

	privateKey := opts.signPrivate
	if privateKey == nil {
		_, privateKey = testSigningKeys(t)
	}
	pinKeys := opts.pinKeys
	if pinKeys == nil {
		pinKeys = []ed25519.PublicKey{publicKeyOf(t, privateKey)}
	}

	writeInstallerAssets(t, harness.assetsDir, privateKey, opts)
	writeScript(t, harness.script, pinKeys)

	writeStubTool(t, harness.binDir, "uname", stubUname)
	writeStubTool(t, harness.binDir, "curl", stubCurl)
	for _, tool := range []string{
		"grep", "awk", "sed", "head", "cat", "gzip", "mktemp", "rm", "mkdir",
		"cp", "chmod", "ls", "tar", "sleep",
	} {
		linkTool(t, harness.binDir, tool)
	}

	if opts.hashTool {
		linkTool(t, harness.binDir, firstAvailable(t, "sha256sum", "shasum"))
	}

	switch opts.openSSL {
	case "":
	case openSSLReal:
		path, ok := linkOptionalTool(t, harness.binDir, "openssl")
		if !ok {
			t.Skip("openssl is required for this case")
		}
		if !opensslCanVerifyRawin(path) {
			t.Skip("the available openssl cannot verify oneshot ed25519 signatures (needs OpenSSL >= 3.0)")
		}
	case openSSLIncapable:
		writeStubTool(t, harness.binDir, "openssl", stubIncapableOpenSSL)
	default:
		t.Fatalf("unknown openssl mode %q", opts.openSSL)
	}

	harness.env = []string{
		"PATH=" + harness.binDir,
		"HOME=" + home,
		"LELE_TEST_ASSETS=" + harness.assetsDir,
		"LELE_TEST_CURL_LOG=" + harness.logPath,
		"LELE_TEST_CURL_TRANSPORT_FAIL=" + opts.transportFailure,
	}
	return harness
}

func publicKeyOf(t *testing.T, privateKey ed25519.PrivateKey) ed25519.PublicKey {
	t.Helper()

	publicKey, ok := privateKey.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatalf("private key exposes %T, want ed25519.PublicKey", privateKey.Public())
	}
	return publicKey
}

func writeInstallerAssets(t *testing.T, assetsDir string, privateKey ed25519.PrivateKey, opts installOptions) {
	t.Helper()

	archive := makeTarGz(t, "lele", []byte("#!/bin/sh\necho lele 9.9.9\n"))
	sum := sha256.Sum256(archive)
	checksums := fmt.Appendf(nil, "%s  %s\n", hex.EncodeToString(sum[:]), archiveAssetName)

	if opts.tamper {
		archive = makeTarGz(t, "lele", []byte("#!/bin/sh\necho pwned\n"))
	}

	writeAsset(t, assetsDir, archiveAssetName, archive)
	writeAsset(t, assetsDir, checksumAssetName(), checksums)

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

func checksumAssetName() string {
	return "lele_" + testReleaseVer + "_checksums.txt"
}

func writeAsset(t *testing.T, dir, name string, content []byte) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatalf("writing asset %s: %v", name, err)
	}
}

func writeScript(t *testing.T, path string, pinKeys []ed25519.PublicKey) {
	t.Helper()

	source, err := os.ReadFile(installShPath)
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	script := replacePinnedKeys(t, string(source), pinKeys)

	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("writing install.sh copy: %v", err)
	}
}

func replacePinnedKeys(t *testing.T, script string, keys []ed25519.PublicKey) string {
	t.Helper()

	start := strings.Index(script, pinnedKeysAssignment)
	if start < 0 {
		t.Fatalf("install.sh has no %q assignment", pinnedKeysAssignment)
	}
	end := strings.Index(script[start:], pemEndMarker)
	if end < 0 {
		t.Fatalf("install.sh has no %q marker", pemEndMarker)
	}

	list := strings.Join(pemBlocks(t, keys), "\n")
	tail := script[start+end+len(pemEndMarker):]
	return script[:start] + pinnedKeysAssignment + list + "'" + tail
}

func pemBlocks(t *testing.T, keys []ed25519.PublicKey) []string {
	t.Helper()

	blocks := make([]string, 0, len(keys))
	for _, key := range keys {
		der, err := x509.MarshalPKIXPublicKey(key)
		if err != nil {
			t.Fatalf("marshalling public key: %v", err)
		}
		encoded := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
		blocks = append(blocks, strings.TrimRight(string(encoded), "\n"))
	}
	return blocks
}

func installShPinnedKeys(t *testing.T) []ed25519.PublicKey {
	t.Helper()

	source, err := os.ReadFile(installShPath)
	if err != nil {
		t.Fatalf("reading install.sh: %v", err)
	}
	script := string(source)

	start := strings.Index(script, pinnedKeysAssignment)
	if start < 0 {
		t.Fatalf("install.sh has no %q assignment", pinnedKeysAssignment)
	}
	rest := script[start+len(pinnedKeysAssignment):]
	end := strings.Index(rest, pemEndMarker)
	if end < 0 {
		t.Fatalf("install.sh has no %q marker", pemEndMarker)
	}

	remaining := []byte(rest[:end] + "-----END PUBLIC KEY-----\n")
	var keys []ed25519.PublicKey
	for {
		block, tail := pem.Decode(remaining)
		if block == nil {
			break
		}
		remaining = tail
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			t.Fatalf("parsing install.sh pinned key: %v", err)
		}
		key, ok := parsed.(ed25519.PublicKey)
		if !ok {
			t.Fatalf("install.sh pinned key is %T, want ed25519.PublicKey", parsed)
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		t.Fatal("install.sh pins no release signing key")
	}
	return keys
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

func linkOptionalTool(t *testing.T, binDir, name string) (string, bool) {
	t.Helper()

	path, err := exec.LookPath(name)
	if err != nil {
		return "", false
	}
	if err := os.Symlink(path, filepath.Join(binDir, name)); err != nil {
		t.Fatalf("linking %s: %v", name, err)
	}
	return path, true
}

func opensslCanVerifyRawin(path string) bool {
	output, _ := exec.Command(path, "pkeyutl", "-help").CombinedOutput()
	return bytes.Contains(output, []byte("-rawin"))
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
want_code=0
while [ $# -gt 0 ]; do
  case "$1" in
    -o) dst="$2"; shift 2 ;;
    -w) want_code=1; shift 2 ;;
    --fail|-fsSL|-sSL|-s|-S|-L|-f) shift ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
name=${url##*/}
src="${LELE_TEST_ASSETS}/${name}"
if [ -n "${LELE_TEST_CURL_LOG:-}" ]; then
  printf '%s\n' "$name" >> "${LELE_TEST_CURL_LOG}"
fi
if [ -n "${LELE_TEST_CURL_TRANSPORT_FAIL:-}" ] && [ "${LELE_TEST_CURL_TRANSPORT_FAIL}" = "$name" ]; then
  printf 'curl: (7) Failed to connect to github.com port 443: Connection refused\n' >&2
  exit 7
fi
if [ ! -f "$src" ]; then
  if [ "$want_code" = 1 ]; then
    printf '404'
    exit 0
  fi
  printf 'curl: (22) The requested URL returned error: 404\n' >&2
  exit 22
fi
if [ -n "$dst" ]; then
  cp "$src" "$dst"
else
  cat "$src"
fi
if [ "$want_code" = 1 ]; then
  printf '200'
fi
`

const stubIncapableOpenSSL = `#!/bin/sh
case "${1:-}" in
  pkeyutl)
    case "${2:-}" in
      -help)
        printf 'Usage: pkeyutl [options]\n'
        printf ' -in file        input file\n'
        printf ' -sigfile file   signature file to verify\n'
        exit 0
        ;;
    esac
    printf 'pkeyutl: Error initializing context: operation not supported for this keytype\n' >&2
    exit 1
    ;;
esac
printf 'openssl: unsupported command %s\n' "${1:-}" >&2
exit 1
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

func (h *installHarness) requests(t *testing.T) []string {
	t.Helper()

	log, err := os.ReadFile(h.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading the curl log: %v", err)
	}
	return strings.Fields(string(log))
}

func indexOf(items []string, want string) int {
	for index, item := range items {
		if item == want {
			return index
		}
	}
	return -1
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

func TestInstallSh_RejectsForgedSignatureWithCapableOpenSSL(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:  true,
		openSSL:   openSSLReal,
		signature: signatureInvalid,
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
		hashTool:  true,
		openSSL:   openSSLReal,
		signature: signatureValid,
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
		hashTool:  true,
		signature: signatureValid,
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

func TestInstallSh_InstallsWithWarningWhenOpenSSLIsIncapable(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:  true,
		openSSL:   openSSLIncapable,
		signature: signatureValid,
	})

	stdout, stderr, err := harness.run(t)
	if err != nil {
		t.Fatalf(
			"install.sh aborted on a VALID signature because its openssl cannot do oneshot ed25519: %v\nstdout: %s\nstderr: %s",
			err, stdout, stderr,
		)
	}
	if !strings.Contains(stdout, "cannot verify ed25519 signatures") {
		t.Errorf("stdout = %q, want a loud warning about the incapable openssl", stdout)
	}
	if !strings.Contains(stdout, "OpenSSL >= 3.0") {
		t.Errorf("stdout = %q, want the OpenSSL >= 3.0 requirement named", stdout)
	}
	if strings.Contains(stdout+stderr, "tampered") {
		t.Errorf("output = %q, must not accuse the download of tampering", stdout+stderr)
	}
	if !strings.Contains(stdout, "Checksum OK") {
		t.Errorf("stdout = %q, want the checksum to still be enforced", stdout)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr != nil {
		t.Errorf("binary was not installed: %v", statErr)
	}
}

func TestInstallSh_DowngradesUnverifiableSignatureWhenOpenSSLIsIncapable(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:  true,
		openSSL:   openSSLIncapable,
		signature: signatureInvalid,
	})

	stdout, stderr, err := harness.run(t)
	if err != nil {
		t.Fatalf(
			"install.sh treated an unverifiable signature as tampering: %v\nstdout: %s\nstderr: %s",
			err, stdout, stderr,
		)
	}
	if !strings.Contains(stdout, "cannot verify ed25519 signatures") {
		t.Errorf("stdout = %q, want the incapable-openssl warning", stdout)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr != nil {
		t.Errorf("binary was not installed: %v", statErr)
	}
}

func TestInstallSh_InstallsUnsignedReleaseWithWarning(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:  true,
		openSSL:   openSSLReal,
		signature: signatureAbsent,
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
		hashTool:  true,
		openSSL:   openSSLReal,
		signature: signatureValid,
		tamper:    true,
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

func TestInstallSh_VerifiesAgainstSecondPinnedKey(t *testing.T) {
	_, signingKey := testSigningKeys(t)
	otherPublicKey, _ := testSigningKeys(t)

	harness := newInstallHarness(t, installOptions{
		hashTool:    true,
		openSSL:     openSSLReal,
		signature:   signatureValid,
		signPrivate: signingKey,
		pinKeys:     []ed25519.PublicKey{otherPublicKey, publicKeyOf(t, signingKey)},
	})

	stdout, stderr, err := harness.run(t)
	if err != nil {
		t.Fatalf(
			"install.sh failed on a release signed by the previous key during a rotation overlap: %v\nstdout: %s\nstderr: %s",
			err, stdout, stderr,
		)
	}
	if !strings.Contains(stdout, "Release signature OK") {
		t.Errorf("stdout = %q, want the second pinned key to verify the signature", stdout)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr != nil {
		t.Errorf("binary was not installed: %v", statErr)
	}
}

func TestInstallSh_RejectsSignatureWhenNoPinnedKeyMatches(t *testing.T) {
	_, signingKey := testSigningKeys(t)
	otherPublicKey, _ := testSigningKeys(t)

	harness := newInstallHarness(t, installOptions{
		hashTool:    true,
		openSSL:     openSSLReal,
		signature:   signatureValid,
		signPrivate: signingKey,
		pinKeys:     []ed25519.PublicKey{otherPublicKey},
	})

	stdout, stderr, err := harness.run(t)
	if err == nil {
		t.Fatalf("install.sh accepted a release signed by an unpinned key\nstdout: %s", stdout)
	}
	if !strings.Contains(stderr, "Release signature verification failed") {
		t.Errorf("stderr = %q, want a signature failure message", stderr)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr == nil {
		t.Error("install.sh installed a release signed by an unpinned key")
	}
}

func TestInstallSh_FailsOnSignatureTransportFailure(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:         true,
		openSSL:          openSSLReal,
		signature:        signatureValid,
		transportFailure: checksumSignatureName(),
	})

	stdout, stderr, err := harness.run(t)
	if err == nil {
		t.Fatalf("install.sh installed a release whose signature could not be downloaded\nstdout: %s", stdout)
	}
	if !strings.Contains(stderr, "Could not download the release signature") {
		t.Errorf("stderr = %q, want a transport failure message", stderr)
	}
	if strings.Contains(stdout, "not signed") {
		t.Errorf("stdout = %q, a transport failure must not be reported as an unsigned release", stdout)
	}
	if _, statErr := os.Stat(harness.installedBinary(t)); statErr == nil {
		t.Error("install.sh installed a release whose signature could not be downloaded")
	}
	if got := strings.Count(strings.Join(harness.requests(t), "\n"), checksumSignatureName()); got != 3 {
		t.Errorf("the signature was requested %d times, want 3 attempts", got)
	}
}

func TestInstallSh_VerifiesAuthenticityBeforeDownloadingArchive(t *testing.T) {
	harness := newInstallHarness(t, installOptions{
		hashTool:  true,
		openSSL:   openSSLReal,
		signature: signatureValid,
	})

	stdout, stderr, err := harness.run(t)
	if err != nil {
		t.Fatalf("install.sh failed: %v\nstdout: %s\nstderr: %s", err, stdout, stderr)
	}

	requests := harness.requests(t)
	checksums := indexOf(requests, checksumAssetName())
	signature := indexOf(requests, checksumSignatureName())
	archive := indexOf(requests, archiveAssetName)

	if checksums < 0 || signature < 0 || archive < 0 {
		t.Fatalf("requests = %v, want the checksums, the signature and the archive", requests)
	}
	if checksums > archive {
		t.Errorf("requests = %v, the archive was downloaded before the checksums", requests)
	}
	if signature > archive {
		t.Errorf("requests = %v, the archive was downloaded before the signature", requests)
	}
}

func TestInstallSh_PinnedKeyMatchesPackage(t *testing.T) {
	pinned := ReleaseSigningPublicKey()
	if len(pinned) != ed25519.PublicKeySize {
		t.Fatalf("package pinned key is %d bytes, want %d", len(pinned), ed25519.PublicKeySize)
	}

	keys := installShPinnedKeys(t)
	for _, key := range keys {
		if bytes.Equal(key, pinned) {
			return
		}
	}
	t.Errorf(
		"install.sh pins %d release signing key(s) and none of them is the key pinned in pkg/update",
		len(keys),
	)
}
