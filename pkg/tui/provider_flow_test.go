package tui

import (
	"strings"
	"testing"
)

func TestProviderPresetByType(t *testing.T) {
	tests := []struct {
		typ  string
		want string // expected preset typ, "" if not found
	}{
		{typ: "openai", want: "openai"},
		{typ: "OpenAI", want: "openai"},
		{typ: "  ANTHROPIC ", want: "anthropic"},
		{typ: "openrouter", want: "openrouter"},
		{typ: "gemini", want: "gemini"},
		{typ: "ollama", want: "ollama"},
		{typ: "ollama", want: "ollama"},
		{typ: "nonexistent", want: ""},
		{typ: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			p := providerPresetByType(tt.typ)
			if tt.want == "" {
				if p != nil {
					t.Fatalf("providerPresetByType(%q) = %+v, want nil", tt.typ, p)
				}
				return
			}
			if p == nil {
				t.Fatalf("providerPresetByType(%q) = nil, want %q", tt.typ, tt.want)
			}
			if p.typ != tt.want {
				t.Fatalf("providerPresetByType(%q).typ = %q, want %q", tt.typ, p.typ, tt.want)
			}
		})
	}
}

func TestIsKnownProviderType(t *testing.T) {
	if !isKnownProviderType("openai") {
		t.Fatal("isKnownProviderType(openai) = false, want true")
	}
	if !isKnownProviderType("Anthropic") {
		t.Fatal("isKnownProviderType(Anthropic) = false, want true")
	}
	if isKnownProviderType("mystery") {
		t.Fatal("isKnownProviderType(mystery) = true, want false")
	}
}

func TestProviderPresetsUniqueTypes(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range providerPresets {
		if p.typ == "" {
			t.Fatal("provider preset with empty typ")
		}
		if seen[p.typ] {
			t.Fatalf("duplicate provider preset type %q", p.typ)
		}
		seen[p.typ] = true
	}
	// Every preset should have an API base so the /connect flow can pre-fill it.
	for _, p := range providerPresets {
		if !strings.HasPrefix(p.apiBase, "http") {
			t.Fatalf("preset %q has invalid apiBase %q", p.typ, p.apiBase)
		}
	}
}

func TestProviderPresetLabels(t *testing.T) {
	for _, p := range providerPresets {
		if p.label == "" {
			t.Fatalf("preset %q has empty label", p.typ)
		}
	}
}
