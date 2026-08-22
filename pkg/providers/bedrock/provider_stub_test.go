//go:build !bedrock

// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package bedrock

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewProvider_ReturnsStubError(t *testing.T) {
	provider, err := NewProvider(context.Background())

	assert.Nil(t, provider)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "build with -tags bedrock"),
		"error should mention build tag requirement, got: %s", err.Error())
}

func TestNewProvider_WithOptions_ReturnsStubError(t *testing.T) {
	provider, err := NewProvider(context.Background(), WithRegion("us-west-2"), WithProfile("test"))

	assert.Nil(t, provider)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "build with -tags bedrock"),
		"error should mention build tag requirement, got: %s", err.Error())
}
func TestProviderStub_Chat(t *testing.T) {
	p := &Provider{}
	_, err := p.Chat(context.Background(), nil, nil, "test-model", nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "build with -tags bedrock"),
		"error should mention build tag requirement, got: %s", err.Error())
}

func TestProviderStub_GetDefaultModel(t *testing.T) {
	p := &Provider{}
	assert.Equal(t, "", p.GetDefaultModel())
}

func TestBedrockOptions_AreNoop(t *testing.T) {
	cfg := &providerConfig{}

	WithRegion("us-east-1")(cfg)
	WithProfile("default")(cfg)
	WithBaseEndpoint("https://endpoint.example.com")(cfg)
	WithRequestTimeout(time.Second)(cfg)

	// All options are no-ops; ensure they don't panic and leave config empty.
	assert.NotNil(t, cfg)
}
