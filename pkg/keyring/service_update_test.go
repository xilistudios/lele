package keyring

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateFromUIKeepsValueWhenBlank(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("openai.api_key", "sk-secret", "OpenAI key",
		[]string{"provider"}, []string{"coder"}, "tui"))

	require.NoError(t, svc.UpdateFromUI("openai.api_key", "", "OpenAI key (rotated docs)",
		[]string{"provider", "llm"}, nil, "webui"))

	val, err := svc.GetRaw("openai.api_key")
	require.NoError(t, err)
	assert.Equal(t, "sk-secret", val, "a blank value must keep the stored plaintext")

	meta, err := svc.ListAll()
	require.NoError(t, err)
	require.Len(t, meta, 1)
	assert.Equal(t, "OpenAI key (rotated docs)", meta[0].Description)
	assert.Equal(t, []string{"provider", "llm"}, meta[0].Tags)
	assert.Empty(t, meta[0].Scope, "clearing the scope means all agents, not 'unchanged'")
}

func TestUpdateFromUIReplacesValueWhenProvided(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("github.token", "ghp_old", "", nil, nil, "tui"))

	require.NoError(t, svc.UpdateFromUI("github.token", "ghp_new", "", nil, nil, "webui"))

	val, err := svc.GetRaw("github.token")
	require.NoError(t, err)
	assert.Equal(t, "ghp_new", val)
}

func TestUpdateFromUIPreservesCreationStamps(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("slack.webhook", "https://hooks", "", nil, nil, "tui"))

	before, err := svc.ListAll()
	require.NoError(t, err)
	require.Len(t, before, 1)

	require.NoError(t, svc.UpdateFromUI("slack.webhook", "", "webhook", nil, nil, "webui"))

	after, err := svc.ListAll()
	require.NoError(t, err)
	require.Len(t, after, 1)
	assert.Equal(t, before[0].CreatedAt, after[0].CreatedAt)
	assert.Equal(t, "tui", after[0].CreatedBy)
	assert.False(t, after[0].UpdatedAt.Before(before[0].UpdatedAt))
}

func TestUpdateFromUIMissingSecretDoesNotCreate(t *testing.T) {
	svc := newTestService(t)

	err := svc.UpdateFromUI("ghost", "v", "", nil, nil, "webui")
	assert.ErrorIs(t, err, ErrNotFound)

	list, err := svc.ListAll()
	require.NoError(t, err)
	assert.Empty(t, list, "a failed edit must not leave a new secret behind")
}

func TestUpdateFromUIRequiresName(t *testing.T) {
	svc := newTestService(t)
	err := svc.UpdateFromUI("   ", "v", "", nil, nil, "webui")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestUpdateFromUITrimsName(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("token", "v1", "", nil, nil, "tui"))

	require.NoError(t, svc.UpdateFromUI("  token  ", "v2", "", nil, nil, "webui"))

	val, err := svc.GetRaw("token")
	require.NoError(t, err)
	assert.Equal(t, "v2", val)
}
