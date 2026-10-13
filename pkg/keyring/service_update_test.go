package keyring

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// UpdateFromUI is the WebUI/TUI edit path: it edits a stored secret and never
// creates one, and an empty value means "keep the plaintext the editor did not
// touch" so metadata can be changed without a round trip of the secret value.
func TestUpdateFromUIKeepsValueWhenBlank(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("openai.api_key", "sk-secret", "OpenAI key",
		[]string{"provider"}, []string{"coder"}, "tui"))

	// Edit the metadata only: no value in the request.
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

// Editing is not creating: the original creation stamps survive a metadata-only
// edit, so "Created by" keeps naming who created the secret.
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

// A missing name is an error, not an upsert: a stale WebUI card must not be able
// to (re)create a secret that was deleted from another surface.
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

// The edit path trims the name like every other write, so a lookup by the
// stored key hits.
func TestUpdateFromUITrimsName(t *testing.T) {
	svc := newTestService(t)
	require.NoError(t, svc.SetFromUI("token", "v1", "", nil, nil, "tui"))

	require.NoError(t, svc.UpdateFromUI("  token  ", "v2", "", nil, nil, "webui"))

	val, err := svc.GetRaw("token")
	require.NoError(t, err)
	assert.Equal(t, "v2", val)
}
