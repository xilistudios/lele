package keyring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openTestStore opens a FileStore over path with key, failing the test on error.
func openTestStore(t *testing.T, path string, key []byte) *FileStore {
	t.Helper()
	s := NewFileStore(path)
	require.NoError(t, s.Open(key))
	return s
}

// mustSet stores and flushes a single secret, i.e. it plays the part of a
// second process committing a change.
func mustStoreSet(t *testing.T, s *FileStore, name, value string) {
	t.Helper()
	require.NoError(t, s.Set(&Secret{Name: name, Value: value}))
	require.NoError(t, s.Flush())
}

// newStorePair returns two stores opened on the same vault path with the same
// master key, standing in for two lele processes (e.g. TUI + gateway). The key
// is returned so a test can open a third store over the same vault.
func newStorePair(t *testing.T) (a, b *FileStore, path string, key []byte) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "keyring.enc")
	key, err := generateMasterKey()
	require.NoError(t, err)
	return openTestStore(t, path, key), openTestStore(t, path, key), path, key
}

// TestFileStoreRefreshSeesExternalWrites covers the cross-process freshness
// contract: a store picks up another process's write without being reopened.
func TestFileStoreRefreshSeesExternalWrites(t *testing.T) {
	t.Run("external set is visible", func(t *testing.T) {
		a, b, _, _ := newStorePair(t)

		// B caches the empty vault before A writes anything.
		sec, err := b.Get("s")
		require.NoError(t, err)
		require.Nil(t, sec)

		mustStoreSet(t, a, "s", "v1")

		sec, err = b.Get("s")
		require.NoError(t, err)
		require.NotNil(t, sec)
		assert.Equal(t, "v1", sec.Value)

		metas, err := b.List()
		require.NoError(t, err)
		require.Len(t, metas, 1)
		assert.Equal(t, "s", metas[0].Name)
	})

	t.Run("external update is visible", func(t *testing.T) {
		a, b, _, _ := newStorePair(t)
		mustStoreSet(t, a, "s", "v1")
		first, err := b.Get("s")
		require.NoError(t, err)
		require.NotNil(t, first, "B must cache the first version")

		mustStoreSet(t, a, "s", "v2-longer")

		sec, err := b.Get("s")
		require.NoError(t, err)
		require.NotNil(t, sec)
		assert.Equal(t, "v2-longer", sec.Value)
	})

	t.Run("external delete is visible", func(t *testing.T) {
		a, b, _, _ := newStorePair(t)
		mustStoreSet(t, a, "s", "v1")
		mustStoreSet(t, a, "keep", "k")

		// B caches both.
		metas, err := b.List()
		require.NoError(t, err)
		require.Len(t, metas, 2)

		deleted, err := a.Delete("s")
		require.NoError(t, err)
		require.True(t, deleted)
		require.NoError(t, a.Flush())

		sec, err := b.Get("s")
		require.NoError(t, err)
		assert.Nil(t, sec, "a secret deleted by another process must disappear")

		sec, err = b.Get("keep")
		require.NoError(t, err)
		require.NotNil(t, sec)
		assert.Equal(t, "k", sec.Value)
	})

	t.Run("external file removal empties the vault", func(t *testing.T) {
		_, b, path, key := newStorePair(t)

		a := openTestStore(t, path, key)
		mustStoreSet(t, a, "s", "v1")

		sec, err := b.Get("s")
		require.NoError(t, err)
		require.NotNil(t, sec)

		require.NoError(t, os.Remove(path))

		// Deleted out from under us: treated as an empty vault, not an error.
		sec, err = b.Get("s")
		require.NoError(t, err)
		assert.Nil(t, sec)
		metas, err := b.List()
		require.NoError(t, err)
		assert.Empty(t, metas)

		// The next write recreates the file from the current state and must not
		// resurrect the secrets that were removed.
		mustStoreSet(t, b, "fresh", "f")

		c := openTestStore(t, path, key)
		metas, err = c.List()
		require.NoError(t, err)
		require.Len(t, metas, 1)
		assert.Equal(t, "fresh", metas[0].Name)
	})

	t.Run("unchanged file takes the fast path", func(t *testing.T) {
		a, b, _, _ := newStorePair(t)
		mustStoreSet(t, a, "s", "v1")

		first, err := b.Get("s")
		require.NoError(t, err)
		require.NotNil(t, first)

		// No external write: the cached object must be handed back as-is, which
		// proves no reload happened (a reload rebuilds every Secret).
		again, err := b.Get("s")
		require.NoError(t, err)
		assert.Same(t, first, again)

		// An external write does replace the object.
		mustStoreSet(t, a, "s", "v2-different-length")
		third, err := b.Get("s")
		require.NoError(t, err)
		require.NotNil(t, third)
		assert.NotSame(t, first, third)
		assert.Equal(t, "v2-different-length", third.Value)
	})

	t.Run("own writes do not force a reload", func(t *testing.T) {
		a, _, _, _ := newStorePair(t)
		mustStoreSet(t, a, "s", "v1")

		sec, err := a.Get("s")
		require.NoError(t, err)
		require.NotNil(t, sec)

		// Flush re-stamps the vault it just wrote, so the next read serves the
		// cached object instead of decrypting the file again.
		again, err := a.Get("s")
		require.NoError(t, err)
		assert.Same(t, sec, again)
	})
}

// TestFileStoreRefreshWrongKeyFails proves that a vault rewritten under a
// different master key surfaces a decrypt error instead of silently emptying
// (data loss) or serving stale values.
func TestFileStoreRefreshWrongKeyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.enc")
	keyB, err := generateMasterKey()
	require.NoError(t, err)
	keyA, err := generateMasterKey()
	require.NoError(t, err)

	// B opens first, while the vault does not exist yet.
	b := openTestStore(t, path, keyB)

	// A writes the vault with a different key.
	a := openTestStore(t, path, keyA)
	mustStoreSet(t, a, "s", "v1")

	_, err = b.Get("s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt")

	// The failure is sticky: the stamp was not advanced, so B never pretends to
	// have valid data.
	_, err = b.Get("s")
	require.Error(t, err)
	_, err = b.List()
	require.Error(t, err)
	_, err = b.Search("s")
	require.Error(t, err)

	// A mutation is refused too, so B cannot overwrite a vault it cannot read.
	require.Error(t, b.Set(&Secret{Name: "y", Value: "2"}))
}

// TestFileStoreMutationMergesExternalChange covers the ordering guarantee: the
// refresh runs BEFORE a mutation, so a flush never drops another process's
// write.
func TestFileStoreMutationMergesExternalChange(t *testing.T) {
	a, b, path, key := newStorePair(t)

	mustStoreSet(t, a, "x", "1")
	// B has not seen "x" yet when it writes "y".
	mustStoreSet(t, b, "y", "2")

	c := openTestStore(t, path, key)
	metas, err := c.List()
	require.NoError(t, err)
	require.Len(t, metas, 2, "B's flush must keep A's secret")
	assert.Equal(t, "x", metas[0].Name)
	assert.Equal(t, "y", metas[1].Name)

	// And B can see both without reopening.
	sec, err := b.Get("x")
	require.NoError(t, err)
	require.NotNil(t, sec)
	assert.Equal(t, "1", sec.Value)
}

// TestFileStoreRequiresOpen pins the existing "vault is not open" behaviour now
// that every operation can return an error.
func TestFileStoreRequiresOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.enc")

	t.Run("never opened", func(t *testing.T) {
		s := NewFileStore(path)
		_, err := s.Get("x")
		assert.ErrorIs(t, err, errVaultNotOpen)
		_, err = s.List()
		assert.ErrorIs(t, err, errVaultNotOpen)
		_, err = s.Search("")
		assert.ErrorIs(t, err, errVaultNotOpen)
		_, err = s.Delete("x")
		assert.ErrorIs(t, err, errVaultNotOpen)
		assert.ErrorIs(t, s.Set(&Secret{Name: "x", Value: "v"}), errVaultNotOpen)
		assert.ErrorIs(t, s.Flush(), errVaultNotOpen)
	})

	t.Run("closed", func(t *testing.T) {
		key, err := generateMasterKey()
		require.NoError(t, err)
		s := openTestStore(t, path, key)
		mustStoreSet(t, s, "x", "v")
		require.NoError(t, s.Close())

		_, err = s.Get("x")
		assert.ErrorIs(t, err, errVaultNotOpen)
		assert.ErrorIs(t, s.Flush(), errVaultNotOpen)
	})
}

// TestServiceSeesExternalVaultWrites is the end-to-end version of the freshness
// contract at the Service layer: the TUI writes, the gateway reads the new value
// without being restarted.
func TestServiceSeesExternalVaultWrites(t *testing.T) {
	dir := t.TempDir()
	cfg := ServiceConfig{
		Enabled:   true,
		VaultPath: filepath.Join(dir, "keyring.enc"),
		Backend:   BackendFile,
		LeleDir:   dir,
	}

	tui := NewService(cfg)
	gateway := NewService(cfg)

	// The gateway caches the empty vault first.
	_, err := gateway.GetRaw("mcp.oauth.token")
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, tui.SetFromUI("mcp.oauth.token", "tok-1", "token", nil, nil, "tui"))

	val, err := gateway.GetRaw("mcp.oauth.token")
	require.NoError(t, err)
	assert.Equal(t, "tok-1", val)

	// Deletion is visible as well.
	require.NoError(t, tui.DeleteFromUI("mcp.oauth.token", "tui"))
	_, err = gateway.GetRaw("mcp.oauth.token")
	assert.ErrorIs(t, err, ErrNotFound)
}
