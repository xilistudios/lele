package keyring

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	cfg := ServiceConfig{
		Enabled:      true,
		VaultPath:    filepath.Join(dir, "keyring.enc"),
		Backend:      BackendFile,
		AuditLogSize: 100,
		LeleDir:      dir,
	}
	return NewService(cfg)
}

func TestFileProviderGeneratesAndPersistsKey(t *testing.T) {
	dir := t.TempDir()
	p := NewFileKeyProvider(filepath.Join(dir, "keyring.key"))

	key1, err := p.GetKey()
	require.NoError(t, err)
	assert.Len(t, key1, masterKeySize)

	p2 := NewFileKeyProvider(filepath.Join(dir, "keyring.key"))
	key2, err := p2.GetKey()
	require.NoError(t, err)
	assert.Equal(t, key1, key2)
}

func TestServiceCRUD(t *testing.T) {
	svc := newTestService(t)

	err := svc.SetFromUI("openai.api_key", "sk-secret", "OpenAI key", []string{"provider"}, nil, "tui")
	require.NoError(t, err)

	val, err := svc.GetRaw("openai.api_key")
	require.NoError(t, err)
	assert.Equal(t, "sk-secret", val)

	list, err := svc.ListAll()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "openai.api_key", list[0].Name)
	assert.Equal(t, "OpenAI key", list[0].Description)

	err = svc.SetFromUI("openai.api_key", "sk-new", "Updated", []string{"provider"}, nil, "webui")
	require.NoError(t, err)
	val, _ = svc.GetRaw("openai.api_key")
	assert.Equal(t, "sk-new", val)

	err = svc.DeleteFromUI("openai.api_key", "tui")
	require.NoError(t, err)
	_, err = svc.GetRaw("openai.api_key")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestServiceScopeEnforcement(t *testing.T) {
	svc := newTestService(t)

	err := svc.SetFromUI("github.token", "ghp_x", "token", nil, []string{"coder"}, "tui")
	require.NoError(t, err)

	val, err := svc.GetForAgent("github.token", "coder", "sess")
	require.NoError(t, err)
	assert.Equal(t, "ghp_x", val)

	_, err = svc.GetForAgent("github.token", "main", "sess")
	assert.ErrorIs(t, err, ErrAccessDenied)

	list, err := svc.ListForAgent("main")
	require.NoError(t, err)
	assert.Empty(t, list)

	list, err = svc.ListForAgent("coder")
	require.NoError(t, err)
	assert.Len(t, list, 1)
}

func TestServiceAgentWriteGating(t *testing.T) {
	svc := newTestService(t)

	err := svc.SetFromAgent("x", "v", "", nil, nil, "coder", "sess")
	assert.ErrorIs(t, err, ErrAgentWriteOff)

	err = svc.DeleteFromAgent("x", "coder", "sess")
	assert.ErrorIs(t, err, ErrAgentWriteOff)
}

func TestServiceDisabled(t *testing.T) {
	dir := t.TempDir()
	svc := NewService(ServiceConfig{Enabled: false, LeleDir: dir, Backend: BackendFile})
	_, err := svc.GetRaw("x")
	assert.ErrorIs(t, err, ErrDisabled)
}

func TestServiceEnabledAndLeleDir(t *testing.T) {
	dir := t.TempDir()

	on := NewService(ServiceConfig{Enabled: true, LeleDir: dir, Backend: BackendFile})
	assert.True(t, on.Enabled())
	assert.Equal(t, dir, on.LeleDir())

	off := NewService(ServiceConfig{Enabled: false, LeleDir: dir, Backend: BackendFile})
	assert.False(t, off.Enabled())

	var nilSvc *Service
	assert.False(t, nilSvc.Enabled(), "a nil Service is not an enabled one")
	assert.Equal(t, "", nilSvc.LeleDir())
}

func TestVaultPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := ServiceConfig{
		Enabled:   true,
		VaultPath: filepath.Join(dir, "keyring.enc"),
		Backend:   BackendFile,
		LeleDir:   dir,
	}

	svc1 := NewService(cfg)
	require.NoError(t, svc1.SetFromUI("a.b", "value-1", "desc", nil, nil, "tui"))

	svc2 := NewService(cfg)
	val, err := svc2.GetRaw("a.b")
	require.NoError(t, err)
	assert.Equal(t, "value-1", val)
}

func TestVaultWrongKeyFails(t *testing.T) {
	dir := t.TempDir()
	vaultPath := filepath.Join(dir, "keyring.enc")

	store1 := NewFileStore(vaultPath)
	key1, _ := generateMasterKey()
	require.NoError(t, store1.Open(key1))
	require.NoError(t, store1.Set(&Secret{Name: "s", Value: "v"}))
	require.NoError(t, store1.Flush())

	store2 := NewFileStore(vaultPath)
	key2, _ := generateMasterKey()
	err := store2.Open(key2)
	assert.Error(t, err)
}

func TestSearch(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("openai.api_key", "v", "LLM provider", []string{"ai"}, nil, "tui"))
	require.NoError(t, svc.SetFromUI("github.token", "v", "VCS", []string{"devops"}, nil, "tui"))

	byName, _ := svc.Search("openai")
	require.Len(t, byName, 1)
	assert.Equal(t, "openai.api_key", byName[0].Name)

	byTag, _ := svc.Search("devops")
	require.Len(t, byTag, 1)
	assert.Equal(t, "github.token", byTag[0].Name)

	byDesc, _ := svc.Search("llm")
	require.Len(t, byDesc, 1)

	all, _ := svc.Search("")
	assert.Len(t, all, 2)
}

func TestAuditRingWraps(t *testing.T) {
	ring := NewAuditRing(3)
	for i := 0; i < 5; i++ {
		ring.Record(AccessRecord{SecretName: string(rune('a' + i))})
	}
	recs := ring.Records()
	require.Len(t, recs, 3)
	assert.Equal(t, "c", recs[0].SecretName)
	assert.Equal(t, "e", recs[2].SecretName)
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, _ := generateMasterKey()
	plaintext := []byte("hello secret world")
	ct, err := encrypt(key, plaintext)
	require.NoError(t, err)
	assert.NotEqual(t, plaintext, ct)

	pt, err := decrypt(key, ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, pt)

	ct[len(ct)-1] ^= 0xff
	_, err = decrypt(key, ct)
	assert.Error(t, err)
}
