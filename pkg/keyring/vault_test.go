package keyring

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpaqueVaultAbsentIsNotAnError pins the contract downstream subsystems
// rely on: a secret that was never stored reads as an empty string.
func TestOpaqueVaultAbsentIsNotAnError(t *testing.T) {
	v := NewOpaqueVault(newTestService(t), "mcp")

	val, err := v.GetSecret("mcp.oauth.token")
	require.NoError(t, err)
	assert.Equal(t, "", val)
}

func TestOpaqueVaultRoundTrip(t *testing.T) {
	v := NewOpaqueVault(newTestService(t), "mcp")

	require.NoError(t, v.SetSecret("mcp.oauth.token", "access-1"))

	val, err := v.GetSecret("mcp.oauth.token")
	require.NoError(t, err)
	assert.Equal(t, "access-1", val)
}

// SetSecret is an upsert: a second call replaces the value and bumps UpdatedAt
// while keeping the creation lineage.
func TestOpaqueVaultSetIsUpsert(t *testing.T) {
	svc := newTestService(t)
	v := NewOpaqueVault(svc, "mcp")

	require.NoError(t, v.SetSecret("mcp.oauth.token", "access-1"))
	before, err := svc.ListAll()
	require.NoError(t, err)
	require.Len(t, before, 1)

	require.NoError(t, v.SetSecret("mcp.oauth.token", "access-2"))

	val, err := v.GetSecret("mcp.oauth.token")
	require.NoError(t, err)
	assert.Equal(t, "access-2", val)

	after, err := svc.ListAll()
	require.NoError(t, err)
	require.Len(t, after, 1, "an upsert must not create a second entry")
	assert.Equal(t, before[0].CreatedAt, after[0].CreatedAt)
	assert.False(t, after[0].UpdatedAt.Before(before[0].UpdatedAt))
}

// The metadata SetSecret writes is part of the contract: the UI shows who owns
// the secret, and the ["system"] scope keeps it away from agents.
func TestOpaqueVaultStoresOriginMetadata(t *testing.T) {
	svc := newTestService(t)
	v := NewOpaqueVault(svc, "mcp")

	require.NoError(t, v.SetSecret("mcp.oauth.token", "access-1"))

	metas, err := svc.ListAll()
	require.NoError(t, err)
	require.Len(t, metas, 1)
	assert.Equal(t, "mcp.oauth.token", metas[0].Name)
	assert.Equal(t, "Stored by mcp", metas[0].Description)
	assert.Equal(t, "mcp", metas[0].CreatedBy)
	assert.Equal(t, []string{systemScope}, metas[0].Scope)
}

func TestOpaqueVaultDeleteIsIdempotent(t *testing.T) {
	v := NewOpaqueVault(newTestService(t), "mcp")

	require.NoError(t, v.SetSecret("mcp.oauth.token", "access-1"))
	require.NoError(t, v.DeleteSecret("mcp.oauth.token"))

	val, err := v.GetSecret("mcp.oauth.token")
	require.NoError(t, err)
	assert.Equal(t, "", val)

	// Deleting again (and deleting a name that never existed) succeeds.
	require.NoError(t, v.DeleteSecret("mcp.oauth.token"))
	require.NoError(t, v.DeleteSecret("never.stored"))
}

// The ["system"] scope is what hides internal secrets from agents: agent-facing
// access is denied and listing omits them, while raw/UI access still works.
func TestOpaqueVaultIsHiddenFromAgents(t *testing.T) {
	svc := newTestService(t)
	v := NewOpaqueVault(svc, "mcp")
	require.NoError(t, v.SetSecret("mcp.oauth.token", "access-1"))

	_, err := svc.GetForAgent("mcp.oauth.token", "coder", "sess")
	assert.ErrorIs(t, err, ErrAccessDenied)

	agentList, err := svc.ListForAgent("coder")
	require.NoError(t, err)
	assert.Empty(t, agentList)

	// Only an agent literally named "system" would see it — which is why the
	// scope value is a reserved name rather than a real agent ID.
	internalList, err := svc.ListForAgent(systemScope)
	require.NoError(t, err)
	assert.Len(t, internalList, 1)

	// Raw and UI access are unaffected.
	raw, err := svc.GetRaw("mcp.oauth.token")
	require.NoError(t, err)
	assert.Equal(t, "access-1", raw)

	all, err := svc.ListAll()
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestOpaqueVaultDisabledService(t *testing.T) {
	svc := NewService(ServiceConfig{Enabled: false, Backend: BackendFile, LeleDir: t.TempDir()})
	v := NewOpaqueVault(svc, "mcp")

	_, err := v.GetSecret("mcp.oauth.token")
	assert.ErrorIs(t, err, ErrDisabled)
	assert.ErrorIs(t, v.SetSecret("mcp.oauth.token", "access-1"), ErrDisabled)
	assert.ErrorIs(t, v.DeleteSecret("mcp.oauth.token"), ErrDisabled)
}

func TestOpaqueVaultNilService(t *testing.T) {
	for name, vault := range map[string]*OpaqueVault{
		"nil receiver": nil,
		"nil service":  NewOpaqueVault(nil, "mcp"),
		"zero value":   {},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := vault.GetSecret("x")
			assert.ErrorIs(t, err, errNilService)
			assert.ErrorIs(t, vault.SetSecret("x", "v"), errNilService)
			assert.ErrorIs(t, vault.DeleteSecret("x"), errNilService)
		})
	}
}
