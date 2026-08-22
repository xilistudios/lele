package keyring

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSecretHasScope(t *testing.T) {
	assert.False(t, (&Secret{}).HasScope())
	assert.False(t, (&Secret{Scope: []string{}}).HasScope())
	assert.True(t, (&Secret{Scope: []string{"coder"}}).HasScope())
}

func TestSecretMeta_OmitsValue(t *testing.T) {
	now := time.Now()
	sec := &Secret{
		Name:        "n",
		Description: "d",
		Value:       "super-secret-value",
		Tags:        []string{"t1"},
		Scope:       []string{"a"},
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   "tui",
	}
	m := sec.Meta()
	assert.Equal(t, "n", m.Name)
	assert.Equal(t, "d", m.Description)
	assert.Equal(t, []string{"t1"}, m.Tags)
	assert.Equal(t, []string{"a"}, m.Scope)
	assert.Equal(t, now, m.CreatedAt)
	assert.Equal(t, now, m.UpdatedAt)
	assert.Equal(t, "tui", m.CreatedBy)
}

func TestSecretAllowsAgent(t *testing.T) {
	sec := &Secret{}
	assert.True(t, sec.AllowsAgent("anyone")) // empty scope -> all

	sec = &Secret{Scope: []string{"a", "b"}}
	assert.True(t, sec.AllowsAgent("a"))
	assert.True(t, sec.AllowsAgent("b"))
	assert.False(t, sec.AllowsAgent("c"))
	assert.False(t, sec.AllowsAgent(""))
}
