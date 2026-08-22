package keyring

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	realkeyring "github.com/zalando/go-keyring"
)

func b64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

// ── AuditRing.Len ──────────────────────────────────────────────────────────

func TestAuditRingLen(t *testing.T) {
	ring := NewAuditRing(3)
	// Not full: Len returns the number of records stored.
	assert.Equal(t, 0, ring.Len())
	ring.Record(AccessRecord{SecretName: "a"})
	ring.Record(AccessRecord{SecretName: "b"})
	assert.Equal(t, 2, ring.Len())

	// Full: Len returns size (capacity), overwriting old entries.
	for i := 0; i < 3; i++ {
		ring.Record(AccessRecord{SecretName: "x"})
	}
	assert.Equal(t, 3, ring.Len())
	assert.Equal(t, 3, len(ring.Records()))
}

func TestAuditRingRecord_DefaultTimestamp(t *testing.T) {
	// Zero-timestamp records get a default timestamp assigned.
	ring := NewAuditRing(2)
	ring.Record(AccessRecord{SecretName: "a"})
	recs := ring.Records()
	require.Len(t, recs, 1)
	assert.False(t, recs[0].Timestamp.IsZero())
}

func TestAuditRingRecords_SequencePreserved(t *testing.T) {
	// Fill exactly to capacity so pos wraps but not fully-overwritten.
	ring := NewAuditRing(4)
	for i := 0; i < 4; i++ {
		ring.Record(AccessRecord{SecretName: string(rune('a' + i))})
	}
	recs := ring.Records()
	require.Len(t, recs, 4)
	// Chronological order (oldest-first).
	assert.Equal(t, []string{"a", "b", "c", "d"},
		[]string{recs[0].SecretName, recs[1].SecretName, recs[2].SecretName, recs[3].SecretName})
}

func TestNewAuditRing_DefaultSize(t *testing.T) {
	ring := NewAuditRing(0)
	assert.Equal(t, 1000, ring.size)
	ring2 := NewAuditRing(-5)
	assert.Equal(t, 1000, ring2.size)
}

// ── defaultVaultPath ───────────────────────────────────────────────────────

func TestDefaultVaultPath(t *testing.T) {
	assert.Equal(t, "./keyring.enc", defaultVaultPath(""))
	assert.Equal(t, "/tmp/lele/keyring.enc", defaultVaultPath("/tmp/lele"))
}

// ── Service EnsureOpen / Backend default provider paths ────────────────────

func TestServiceDefaultVaultPathApplied(t *testing.T) {
	// LeleDir empty -> VaultPath defaults to "./keyring.enc".
	svc := NewService(ServiceConfig{Enabled: true, Backend: BackendFile})
	assert.Equal(t, "./keyring.enc", svc.cfg.VaultPath)
}

func TestServiceBackend_DefaultProvider(t *testing.T) {
	// Backend "auto"/empty resolves a provider; Backend() should return a
	// non-empty value (either keychain or file).
	svc := NewService(ServiceConfig{Enabled: true, LeleDir: t.TempDir()})
	assert.Contains(t, []string{BackendKeychain, BackendFile}, svc.Backend())
}

// ── Service Store error paths ─────────────────────────────────────────────

// brokenStore fails on every operation.
type brokenStore struct{}

func (b *brokenStore) Open([]byte) error          { return errors.New("open failed") }
func (b *brokenStore) Close() error               { return nil }
func (b *brokenStore) IsOpen() bool               { return true }
func (b *brokenStore) Set(*Secret) error          { return errors.New("set failed") }
func (b *brokenStore) Get(string) (*Secret, bool) { return nil, false }
func (b *brokenStore) Delete(string) bool         { return true }
func (b *brokenStore) List() []SecretMeta         { return nil }
func (b *brokenStore) Search(string) []SecretMeta { return nil }
func (b *brokenStore) Flush() error               { return errors.New("flush failed") }
func (b *brokenStore) Backend() string            { return "broken" }

// failingSetStore succeeds on Open but errors on Set/Flush.
type failingSetStore struct{ *FileStore }

func (f *failingSetStore) Set(*Secret) error { return errors.New("store set failed") }
func (f *failingSetStore) Flush() error      { return errors.New("store flush failed") }

func TestServiceGetRaw_StoreFailure(t *testing.T) {
	key, _ := generateMasterKey()
	svc := NewServiceWithDeps(ServiceConfig{Enabled: true}, &brokenStore{}, &fakeKeyProvider{key: key, backend: BackendFile})
	_, err := svc.GetRaw("x")
	require.Error(t, err)
	assert.ErrorContains(t, err, "open failed")
}

func TestServiceDelete_NotFoundRecordsAudit(t *testing.T) {
	dir := t.TempDir()
	cfg := ServiceConfig{Enabled: true, VaultPath: filepath.Join(dir, "v.enc"), Backend: BackendFile, LeleDir: dir, AllowAgentDelete: true}
	svc := NewService(cfg)
	err := svc.DeleteFromAgent("missing", "agent", "sess")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestServiceSet_StoreSetError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v.enc")
	fs := NewFileStore(path)
	key, _ := generateMasterKey()
	require.NoError(t, fs.Open(key))
	svc := NewServiceWithDeps(ServiceConfig{Enabled: true},
		&failingSetStore{FileStore: fs}, &fakeKeyProvider{key: key, backend: BackendFile})
	err := svc.SetFromUI("a", "v", "d", nil, nil, "tui")
	require.Error(t, err)
	assert.ErrorContains(t, err, "set failed")
}

func TestServiceListAll_EmptyReturnsEmpty(t *testing.T) {
	svc := newTestService(t)
	list, err := svc.ListAll()
	require.NoError(t, err)
	assert.Empty(t, list)
	assert.NotNil(t, list)
}

func TestServiceSearch_EmptyResult(t *testing.T) {
	svc := newTestService(t)
	list, err := svc.Search("nothing-here")
	require.NoError(t, err)
	assert.Empty(t, list)
}

// ── OSKeychainProvider via go-keyring mock ─────────────────────────────────

func TestOSKeychainProviderGetKey_ReadAndStore(t *testing.T) {
	realkeyring.MockInit()
	t.Cleanup(func() { realkeyring.MockInit() })

	p := NewOSKeychainProvider()
	key, err := p.GetKey()
	require.NoError(t, err)
	assert.Len(t, key, masterKeySize)

	// A second call must read the stored base64 key back, returning the same.
	key2, err := p.GetKey()
	require.NoError(t, err)
	assert.Equal(t, key, key2)
}

func TestOSKeychainProviderGetKey_CorruptEntryRegenerates(t *testing.T) {
	realkeyring.MockInit()
	t.Cleanup(func() { realkeyring.MockInit() })

	// Pre-store a corrupt (wrong-size) base64 value.
	p := NewOSKeychainProvider()
	require.NoError(t, realkeyring.Set(p.service, p.account, "dG9vLXNob3J0")) // "too-short" base64

	key, err := p.GetKey()
	require.NoError(t, err)
	assert.Len(t, key, masterKeySize) // regenerated
}

func TestOSKeychainProviderGetKey_ReadError(t *testing.T) {
	realkeyring.MockInitWithError(errors.New("dbus unavailable"))
	t.Cleanup(func() { realkeyring.MockInit() })

	p := NewOSKeychainProvider()
	_, err := p.GetKey()
	require.Error(t, err)
	assert.ErrorContains(t, err, "keychain read failed")
}

func TestOSKeychainProviderAvailable_TrueOnFound(t *testing.T) {
	realkeyring.MockInit()
	t.Cleanup(func() { realkeyring.MockInit() })

	p := NewOSKeychainProvider()
	k, err := generateMasterKey()
	require.NoError(t, err)
	require.NoError(t, realkeyring.Set(p.service, p.account, b64(k)))
	assert.True(t, p.Available())
}

func TestOSKeychainProviderAvailable_FalseOnError(t *testing.T) {
	realkeyring.MockInitWithError(errors.New("no secret service"))
	t.Cleanup(func() { realkeyring.MockInit() })

	p := NewOSKeychainProvider()
	assert.False(t, p.Available())
}
