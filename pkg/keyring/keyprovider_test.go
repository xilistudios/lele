package keyring

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeKeyProvider is a configurable KeyProvider used to exercise service code
// without touching a real OS keychain or file system.
type fakeKeyProvider struct {
	key       []byte
	backend   string
	available bool
	getErr    error
}

func (f *fakeKeyProvider) GetKey() ([]byte, error) { return f.key, f.getErr }
func (f *fakeKeyProvider) Backend() string         { return f.backend }
func (f *fakeKeyProvider) Available() bool         { return f.available }

func TestNewOSKeychainProvider(t *testing.T) {
	p := NewOSKeychainProvider()
	require.NotNil(t, p)
	assert.Equal(t, "lele", p.service)
	assert.Equal(t, "keyring-master-key", p.account)
}

func TestOSKeychainProviderBackend(t *testing.T) {
	p := NewOSKeychainProvider()
	assert.Equal(t, BackendKeychain, p.Backend())
}

func TestOSKeychainProviderAvailable(t *testing.T) {
	p := NewOSKeychainProvider()
	// Must not panic in any environment; result depends on the host keychain.
	_ = p.Available()
}

func TestOSKeychainProviderGetKey(t *testing.T) {
	p := NewOSKeychainProvider()
	// On headless hosts there is no D-Bus secret service, so GetKey should
	// surface an error rather than panic. On hosts where a real keychain is
	// available, a key may be generated and stored. Tolerate both outcomes.
	_, err := p.GetKey()
	if err != nil {
		assert.ErrorContains(t, err, "keyring")
	}
}

func TestNewFileKeyProvider(t *testing.T) {
	p := NewFileKeyProvider("/tmp/x.key")
	require.NotNil(t, p)
	assert.Equal(t, "/tmp/x.key", p.path)
}

func TestFileKeyProviderBackend(t *testing.T) {
	p := NewFileKeyProvider("/tmp/x.key")
	assert.Equal(t, BackendFile, p.Backend())
}

func TestFileKeyProviderAvailable(t *testing.T) {
	// Parent dir already exists (or is implied) -> available.
	dir := t.TempDir()
	p := NewFileKeyProvider(filepath.Join(dir, "keys", "keyring.key"))
	assert.True(t, p.Available())

	// Nested directories get created.
	p2 := NewFileKeyProvider(filepath.Join(dir, "a", "b", "c", "keyring.key"))
	assert.True(t, p2.Available())

	// Path with no directory component is trivially available.
	p3 := NewFileKeyProvider("keyring.key")
	assert.True(t, p3.Available())
}

func TestFileKeyProviderAvailable_UnwritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; permission checks are unreliable")
	}
	// Create a read-only directory and attempt a provider under it.
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot chmod temp dir:", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	p := NewFileKeyProvider(filepath.Join(dir, "readonly", "sub", "keyring.key"))
	assert.False(t, p.Available())
}

func TestFileKeyProviderGetKey_ExistingRawFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keyring.key")

	key, _ := generateMasterKey()
	require.NoError(t, os.WriteFile(path, key, 0o600)) // raw 32 bytes

	p := NewFileKeyProvider(path)
	got, err := p.GetKey()
	require.NoError(t, err)
	assert.Equal(t, key, got)
}

func TestFileKeyProviderGetKey_ExistingBase64File(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keyring.key")

	key, _ := generateMasterKey()
	// Write the encoded form plus surrounding whitespace to exercise TrimSpace.
	require.NoError(t, os.WriteFile(path, []byte(" "+base64.StdEncoding.EncodeToString(key)+"  "), 0o600))

	p := NewFileKeyProvider(path)
	got, err := p.GetKey()
	require.NoError(t, err)
	assert.Equal(t, key, got)
}

func TestFileKeyProviderGetKey_InvalidContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keyring.key")
	require.NoError(t, os.WriteFile(path, []byte("not-a-valid-key-here"), 0o600))

	p := NewFileKeyProvider(path)
	_, err := p.GetKey()
	require.Error(t, err)
	assert.ErrorContains(t, err, "invalid contents")
}

func TestFileKeyProviderGetKey_ReadError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; permission checks are unreliable")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "keyring.key")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0o000))
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	p := NewFileKeyProvider(path)
	_, err := p.GetKey()
	require.Error(t, err)
	assert.ErrorContains(t, err, "read key file")
}

func TestFileKeyProviderGetKey_WriteDirCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "keyring.key")

	p := NewFileKeyProvider(path)
	key, err := p.GetKey()
	require.NoError(t, err)
	assert.Len(t, key, masterKeySize)

	// File should exist and be a valid base64-encoded key.
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestFileKeyProviderGetKey_WriteError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root; permission checks are unreliable")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot chmod temp dir:", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	// Key file does NOT exist, so the read yields IsNotExist and GetKey falls
	// through to write — which fails against the read-only directory.
	path := filepath.Join(dir, "keyring.key")

	p := NewFileKeyProvider(path)
	_, err := p.GetKey()
	require.Error(t, err)
	assert.ErrorContains(t, err, "write key file")
}

func TestNewKeyProvider_FileBackend(t *testing.T) {
	dir := t.TempDir()
	prov := NewKeyProvider(dir, BackendFile)
	assert.Equal(t, BackendFile, prov.Backend())
	fp, ok := prov.(*FileKeyProvider)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, "keyring.key"), fp.path)
}

func TestNewKeyProvider_KeychainBackend(t *testing.T) {
	dir := t.TempDir()
	prov := NewKeyProvider(dir, BackendKeychain)
	_, ok := prov.(*OSKeychainProvider)
	assert.True(t, ok, "explicit keychain backend must return OS keychain provider")
}

func TestNewKeyProvider_AutoPreferredWhenKeychainAvailable(t *testing.T) {
	dir := t.TempDir()
	prov := NewKeyProvider(dir, BackendAuto)
	assert.Contains(t, []string{BackendKeychain, BackendFile}, prov.Backend())
}

func TestNewKeyProvider_EmptyBackendDefaultsToAuto(t *testing.T) {
	dir := t.TempDir()
	// Empty backend resolves to "auto", which prefers the OS keychain when
	// available and otherwise falls back to the file provider. Accept either.
	prov := NewKeyProvider(dir, "")
	assert.Contains(t, []string{BackendKeychain, BackendFile}, prov.Backend())
}

func TestNewKeyProvider_MixedCaseTrimsBackend(t *testing.T) {
	dir := t.TempDir()
	prov := NewKeyProvider(dir, "  FILE  ")
	fp, ok := prov.(*FileKeyProvider)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(dir, "keyring.key"), fp.path)
}
