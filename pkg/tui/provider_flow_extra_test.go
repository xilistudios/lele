package tui

import (
	"testing"
)

func TestProviderPresetDefaultsEnsuresOpenAIPresent(t *testing.T) {
	p := providerPresetByType("openai")
	if p == nil {
		t.Fatal("openai preset should exist")
	}
	if p.apiBase != "https://api.openai.com/v1" {
		t.Errorf("openai apiBase = %q", p.apiBase)
	}
	if p.defaultModel != "gpt-4o" {
		t.Errorf("openai defaultModel = %q", p.defaultModel)
	}
}