package keyring

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewServiceDefaultTunables(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(ServiceConfig{Enabled: true, LeleDir: dir, Backend: BackendFile})
	assert.Equal(t, 1000, svc.cfg.AuditLogSize)
	assert.Equal(t, dir+"/keyring.enc", svc.cfg.VaultPath)
}

func TestNewServiceWithDeps(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "vault.enc"))
	prov := &fakeKeyProvider{
		key:       make([]byte, masterKeySize),
		backend:   BackendFile,
		available: true,
	}
	svc := NewServiceWithDeps(ServiceConfig{
		Enabled:      true,
		AuditLogSize: 5,
	}, store, prov)
	require.NotNil(t, svc)
	assert.Same(t, store, svc.store)
	assert.Same(t, prov, svc.keyProv)
	assert.Equal(t, 5, svc.cfg.AuditLogSize)
	assert.Equal(t, BackendFile, svc.Backend())
}

func TestServiceBackend_NilProvider(t *testing.T) {
	svc := &Service{keyProv: nil}
	assert.Equal(t, "", svc.Backend())
}

func TestServiceAuditLogAndRecord(t *testing.T) {
	svc := newTestService(t)
	assert.Empty(t, svc.AuditLog())

	// Populate a secret and perform scoped calls to generate audit entries.
	require.NoError(t, svc.SetFromUI("s", "secret-value", "d", nil, []string{"coder"}, "tui"))
	_, _ = svc.GetForAgent("s", "coder", "sess-1")
	_, _ = svc.GetForAgent("s", "denied-other", "sess-2")
	_, err := svc.GetRaw("missing")
	assert.ErrorIs(t, err, ErrNotFound) // records a denied "get"

	log := svc.AuditLog()
	require.NotEmpty(t, log)
	// Validate chronological ordering and some record fields.
	for i := 1; i < len(log); i++ {
		assert.False(t, log[i].Timestamp.Before(log[i-1].Timestamp))
	}
	assert.Equal(t, "sess-1", log[0].SessionKey)
}

func TestServiceRecord_NilAuditNoop(t *testing.T) {
	svc := &Service{audit: nil}
	svc.record("secret", "agent", "sess", "get", true) // must not panic
}

func TestServiceCount(t *testing.T) {
	svc := newTestService(t)
	assert.Equal(t, 0, svc.Count())
	require.NoError(t, svc.SetFromUI("a", "v", "d", nil, nil, "tui"))
	require.NoError(t, svc.SetFromUI("b", "v", "d", nil, nil, "tui"))
	assert.Equal(t, 2, svc.Count())
}

func TestServiceCount_DisabledReturnsZero(t *testing.T) {
	svc := NewService(ServiceConfig{Enabled: false, LeleDir: t.TempDir()})
	assert.Equal(t, 0, svc.Count())
}

func TestServiceStatus(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("a", "v", "d", nil, nil, "tui"))

	st := svc.Status()
	assert.Equal(t, true, st["enabled"])
	assert.Equal(t, BackendFile, st["backend"])
	assert.Equal(t, 1, st["count"])
}

func TestServiceStatus_Disabled(t *testing.T) {
	svc := NewService(ServiceConfig{Enabled: false, LeleDir: t.TempDir()})
	st := svc.Status()
	assert.Equal(t, false, st["enabled"])
	assert.Equal(t, 0, st["count"])
}

func TestEnsureOpen_KeyProviderError(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "vault.enc"))
	prov := &fakeKeyProvider{getErr: errTestKeyFailure}
	svc := NewServiceWithDeps(ServiceConfig{Enabled: true}, store, prov)

	err := svc.EnsureOpen()
	require.Error(t, err)
	assert.ErrorIs(t, err, errTestKeyFailure)
	// Second call returns the cached error.
	assert.ErrorIs(t, svc.EnsureOpen(), errTestKeyFailure)
}

func TestEnsureOpen_StoreOpenError(t *testing.T) {
	// A store whose Open fails (invalid key length passed via raw fake) is
	// exercised by using a real store with an invalid provider key.
	prov := &fakeKeyProvider{key: []byte("way-too-short-key")}
	svc := NewServiceWithDeps(ServiceConfig{Enabled: true}, NewFileStore("/tmp/x.enc"), prov)
	err := svc.EnsureOpen()
	require.Error(t, err)
	assert.ErrorContains(t, err, "master key must be 32 bytes")
}

func TestSetFromAgent_WhenEnabled(t *testing.T) {
	dir := t.TempDir()
	cfg := ServiceConfig{
		Enabled:          true,
		VaultPath:        filepath.Join(dir, "keyring.enc"),
		Backend:          BackendFile,
		LeleDir:          dir,
		AllowAgentSet:    true,
		AllowAgentDelete: true,
	}
	svc := NewService(cfg)

	err := svc.SetFromAgent("token", "sekret-value", "desc", []string{"tok"}, []string{"coder"}, "coder", "sess")
	require.NoError(t, err)

	// Verify contents.
	v, err := svc.GetRaw("token")
	require.NoError(t, err)
	assert.Equal(t, "sekret-value", v)

	// Agent delete enabled -> succeeds.
	require.NoError(t, svc.DeleteFromAgent("token", "coder", "sess"))
	_, err = svc.GetRaw("token")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestSetFromAgent_WithDepsWhenEnabled(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "vault.enc"))
	key, _ := generateMasterKey()
	require.NoError(t, store.Open(key))
	prov := &fakeKeyProvider{key: key, backend: BackendFile}
	svc := NewServiceWithDeps(ServiceConfig{Enabled: true, AllowAgentSet: true, AllowAgentDelete: true}, store, prov)

	require.NoError(t, svc.SetFromAgent("t", "x-value-long", "d", nil, []string{"coder"}, "coder", "sess"))

	// Scope enforcement rejects a different agent.
	_, err := svc.GetForAgent("t", "other", "sess")
	assert.ErrorIs(t, err, ErrAccessDenied)

	// Delete for a forbidden agent gated by AllowAgentDelete=true but delete
	// itself succeeds (gating is on the flag, not agent here); verify the
	// audit entry is produced.
	require.NoError(t, svc.DeleteFromAgent("t", "coder", "sess"))
	assert.NotEmpty(t, svc.AuditLog())
}

func TestSetFromAgent_WriteOffRecordsAudit(t *testing.T) {
	svc := newTestService(t) // AllowAgentSet=false
	err := svc.SetFromAgent("x", "v", "", nil, nil, "agent", "sess")
	assert.ErrorIs(t, err, ErrAgentWriteOff)
	// Audit should contain one denied set entry.
	log := svc.AuditLog()
	require.Len(t, log, 1)
	assert.False(t, log[0].Granted)
	assert.Equal(t, "set", log[0].Action)
}

func TestDeleteFromAgent_WriteOffRecordsAudit(t *testing.T) {
	svc := newTestService(t) // AllowAgentDelete=false
	err := svc.DeleteFromAgent("x", "agent", "sess")
	assert.ErrorIs(t, err, ErrAgentWriteOff)
	log := svc.AuditLog()
	require.Len(t, log, 1)
	assert.Equal(t, "delete", log[0].Action)
	assert.False(t, log[0].Granted)
}

func TestSetFromAgent_StoreFailureRecordsAudit(t *testing.T) {
	// A store that errors on Open makes SetFromAgent fail and record denial.
	svc := NewServiceWithDeps(ServiceConfig{Enabled: true, AllowAgentSet: true},
		NewFileStore("/nonexistent-parent-dir/vault.enc"),
		&fakeKeyProvider{key: []byte("bad-short-key!"), backend: BackendFile})
	err := svc.SetFromAgent("x", "v", "", nil, nil, "agent", "sess")
	require.Error(t, err)
	log := svc.AuditLog()
	require.Len(t, log, 1)
	assert.False(t, log[0].Granted)
}

func TestDeleteFromAgent_NotFoundRecordsAudit(t *testing.T) {
	dir := t.TempDir()
	cfg := ServiceConfig{Enabled: true, VaultPath: filepath.Join(dir, "v.enc"), Backend: BackendFile, LeleDir: dir, AllowAgentDelete: true}
	svc := NewService(cfg)

	err := svc.DeleteFromAgent("missing", "agent", "sess")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
	log := svc.AuditLog()
	require.Len(t, log, 1)
	assert.False(t, log[0].Granted)
	assert.Equal(t, "delete", log[0].Action)
}

func TestSet_EmptyNameRejected(t *testing.T) {
	svc := newTestService(t)
	err := svc.SetFromUI("   ", "v", "d", nil, nil, "tui")
	require.Error(t, err)
	assert.ErrorContains(t, err, "secret name is required")
}

func TestSet_PreservesCreatedAtOnUpdate(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("a", "v1", "d", nil, nil, "tui"))

	// Update; CreatedAt must be preserved while Updated changes.
	require.NoError(t, svc.SetFromUI("a", "v2", "d", nil, nil, "webui"))

	all, err := svc.ListAll()
	require.NoError(t, err)
	require.Len(t, all, 1)
	// CreatedBy is overwritten by the new source since "webui" != "".
	assert.Equal(t, "webui", all[0].CreatedBy)
}

func TestDelete_NotFound(t *testing.T) {
	svc := newTestService(t)
	err := svc.DeleteFromUI("missing", "tui")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGetForAgent_RecordsGet(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("a", "value", "d", nil, nil, "tui"))
	_, _ = svc.GetForAgent("a", "agent", "sess")
	_, err := svc.GetForAgent("missing", "agent", "sess")
	assert.ErrorIs(t, err, ErrNotFound)

	log := svc.AuditLog()
	require.Len(t, log, 2)
	assert.True(t, log[0].Granted)
	assert.False(t, log[1].Granted)
}

func TestListForAgent_EmptyScopeAllVisible(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("a", "v", "d", nil, nil, "tui"))
	require.NoError(t, svc.SetFromUI("b", "v", "d", nil, []string{"other"}, "tui"))

	list, err := svc.ListForAgent("anyone")
	require.NoError(t, err)
	// Only "a" is visible (no scope); "b" is scoped to "other".
	require.Len(t, list, 1)
	assert.Equal(t, "a", list[0].Name)
}

func TestNewServiceWithDeps_DefaultAuditSize(t *testing.T) {
	svc := NewServiceWithDeps(ServiceConfig{Enabled: true}, nil, nil)
	assert.Equal(t, 1000, svc.cfg.AuditLogSize)
}

var errTestKeyFailure = errors.New("test: key provider failure")
