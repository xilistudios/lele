package keyring

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestStore(t *testing.T, path string, key []byte) *FileStore {
	t.Helper()
	s := NewFileStore(path)
	require.NoError(t, s.Open(key))
	return s
}

func mustStoreSet(t *testing.T, s *FileStore, name, value string) {
	t.Helper()
	require.NoError(t, s.Set(&Secret{Name: name, Value: value}))
	require.NoError(t, s.Flush())
}

func newStorePair(t *testing.T) (a, b *FileStore, path string, key []byte) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "keyring.enc")
	key, err := generateMasterKey()
	require.NoError(t, err)
	return openTestStore(t, path, key), openTestStore(t, path, key), path, key
}

func TestFileStoreRefreshSeesExternalWrites(t *testing.T) {
	t.Run("external set is visible", func(t *testing.T) {
		a, b, _, _ := newStorePair(t)

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

		sec, err = b.Get("s")
		require.NoError(t, err)
		assert.Nil(t, sec)
		metas, err := b.List()
		require.NoError(t, err)
		assert.Empty(t, metas)

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

		again, err := b.Get("s")
		require.NoError(t, err)
		assert.Same(t, first, again)

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

		again, err := a.Get("s")
		require.NoError(t, err)
		assert.Same(t, sec, again)
	})
}

func TestFileStoreRefreshWrongKeyFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.enc")
	keyB, err := generateMasterKey()
	require.NoError(t, err)
	keyA, err := generateMasterKey()
	require.NoError(t, err)

	b := openTestStore(t, path, keyB)

	a := openTestStore(t, path, keyA)
	mustStoreSet(t, a, "s", "v1")

	_, err = b.Get("s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decrypt")

	_, err = b.Get("s")
	require.Error(t, err)
	_, err = b.List()
	require.Error(t, err)
	_, err = b.Search("s")
	require.Error(t, err)

	require.Error(t, b.Set(&Secret{Name: "y", Value: "2"}))
}

func TestFileStoreMutationMergesExternalChange(t *testing.T) {
	a, b, path, key := newStorePair(t)

	mustStoreSet(t, a, "x", "1")
	mustStoreSet(t, b, "y", "2")

	c := openTestStore(t, path, key)
	metas, err := c.List()
	require.NoError(t, err)
	require.Len(t, metas, 2, "B's flush must keep A's secret")
	assert.Equal(t, "x", metas[0].Name)
	assert.Equal(t, "y", metas[1].Name)

	sec, err := b.Get("x")
	require.NoError(t, err)
	require.NotNil(t, sec)
	assert.Equal(t, "1", sec.Value)
}

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

	_, err := gateway.GetRaw("mcp.oauth.token")
	require.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, tui.SetFromUI("mcp.oauth.token", "tok-1", "token", nil, nil, "tui"))

	val, err := gateway.GetRaw("mcp.oauth.token")
	require.NoError(t, err)
	assert.Equal(t, "tok-1", val)

	require.NoError(t, tui.DeleteFromUI("mcp.oauth.token", "tui"))
	_, err = gateway.GetRaw("mcp.oauth.token")
	assert.ErrorIs(t, err, ErrNotFound)
}
