package keyring

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileStoreIsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	s := NewFileStore(path)
	require.False(t, s.IsOpen()) // not open initially

	key, _ := generateMasterKey()
	require.NoError(t, s.Open(key))
	assert.True(t, s.IsOpen())

	require.NoError(t, s.Close())
	assert.False(t, s.IsOpen())
}

func TestFileStoreBackend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	s := NewFileStore(path)
	assert.Equal(t, path, s.Backend())
}

func TestFileStoreOpen_InvalidKeyLength(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "vault.enc"))
	err := s.Open([]byte("short"))
	require.Error(t, err)
	assert.ErrorContains(t, err, "master key must be 32 bytes")
}

func TestFileStoreOpen_ReadError(t *testing.T) {
	// Place a directory at the vault path so os.ReadFile fails with a
	// non-IsNotExist error (EISDIR) — exercising the "read vault" error path.
	vaultPath := filepath.Join(t.TempDir(), "vault.enc")
	require.NoError(t, os.MkdirAll(filepath.Join(vaultPath, "child"), 0o700))

	s := NewFileStore(vaultPath)
	key, _ := generateMasterKey()
	err := s.Open(key)
	require.Error(t, err)
	assert.ErrorContains(t, err, "read vault")
	// Key must have been zeroed on failure.
	assert.Nil(t, s.key)
}

func TestFileStoreOpen_NotExistCreatesEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	s := NewFileStore(path)
	key, _ := generateMasterKey()
	require.NoError(t, s.Open(key))
	assert.True(t, s.IsOpen())

	sec, ok := s.Get("nope")
	assert.False(t, ok)
	assert.Nil(t, sec)
}

func TestFileStoreOpen_CorruptVaultUnmarshal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.enc")
	key, _ := generateMasterKey()

	// Write a valid-looking encrypted blob that is NOT valid JSON.
	badJSON := []byte("{ this is not: valid json ]")
	// Round-trip anyway: encrypt a random payload that unmarshals to an error.
	ct, err := encrypt(key, []byte("garbage-not-json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, ct, 0o600))

	s := NewFileStore(path)
	err = s.Open(key)
	require.Error(t, err)
	assert.ErrorContains(t, err, "corrupt vault")
	_ = badJSON
}

func TestFileStoreOpen_NilSecretSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.enc")
	key, _ := generateMasterKey()

	v := vault{
		Version: vaultVersion,
		Secrets: []*Secret{
			nil,
			{Name: "ok", Value: "v"},
			{Name: "", Value: "empty-name"},
		},
	}
	pt, err := json.Marshal(&v)
	require.NoError(t, err)
	ct, err := encrypt(key, pt)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, ct, 0o600))

	s := NewFileStore(path)
	require.NoError(t, s.Open(key))
	_, ok := s.Get("ok")
	assert.True(t, ok)
	_, ok = s.Get("")
	assert.False(t, ok)
}

func TestFileStoreClose_ZeroesSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	s := NewFileStore(path)
	key, _ := generateMasterKey()
	require.NoError(t, s.Open(key))
	require.NoError(t, s.Set(&Secret{Name: "s", Value: "super-secret-value"}))
	assert.True(t, s.IsOpen())

	require.NoError(t, s.Close())
	assert.False(t, s.IsOpen())

	// After close, Get reports not found.
	_, ok := s.Get("s")
	assert.False(t, ok)
}

func TestFileStoreSet_Errors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	s := NewFileStore(path)

	// Not open.
	require.False(t, s.IsOpen())
	err := s.Set(&Secret{Name: "s", Value: "v"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "vault is not open")

	// Open, then test nil / empty-name.
	key, _ := generateMasterKey()
	require.NoError(t, s.Open(key))
	require.Error(t, s.Set(nil))
	require.Error(t, s.Set(&Secret{Name: "", Value: "v"}))
}

func TestFileStoreDelete_ReturnsFalseWhenClosedOrMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	s := NewFileStore(path)
	assert.False(t, s.Delete("x")) // closed

	key, _ := generateMasterKey()
	require.NoError(t, s.Open(key))
	assert.False(t, s.Delete("missing"))
	require.NoError(t, s.Set(&Secret{Name: "present", Value: "v"}))
	assert.True(t, s.Delete("present"))
	assert.False(t, s.Delete("present"))
}

func TestFileStoreFlush_NotOpen(t *testing.T) {
	s := NewFileStore(filepath.Join(t.TempDir(), "vault.enc"))
	err := s.Flush()
	require.Error(t, err)
	assert.ErrorContains(t, err, "vault is not open")
}

func TestFileStoreFlush_WritesFileAndReloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.enc")
	key, _ := generateMasterKey()

	s := NewFileStore(path)
	require.NoError(t, s.Open(key))
	require.NoError(t, s.Set(&Secret{Name: "b", Value: "vab"}))
	require.NoError(t, s.Set(&Secret{Name: "a", Value: "vaa"}))
	require.NoError(t, s.Flush())

	// A fresh store over the same path decrypts and reads the secrets back.
	s2 := NewFileStore(path)
	require.NoError(t, s2.Open(key))
	v, ok := s2.Get("a")
	require.True(t, ok)
	assert.Equal(t, "vaa", v.Value)
}

func TestDecrypt_ShortCiphertext(t *testing.T) {
	key, _ := generateMasterKey()
	_, err := decrypt(key, []byte{0x01, 0x02}) // shorter than nonce size
	require.Error(t, err)
	assert.ErrorContains(t, err, "ciphertext too short")
}

func TestFileStoreOpen_DecryptFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.enc")
	key1, _ := generateMasterKey()
	key2, _ := generateMasterKey()

	s := NewFileStore(path)
	require.NoError(t, s.Open(key1))
	require.NoError(t, s.Set(&Secret{Name: "s", Value: "v"}))
	require.NoError(t, s.Flush())

	// A different key fails authentication.
	s2 := NewFileStore(path)
	err := s2.Open(key2)
	require.Error(t, err)
	assert.ErrorContains(t, err, "decrypt vault")
	assert.Nil(t, s2.key)
	assert.False(t, s2.IsOpen())
}
