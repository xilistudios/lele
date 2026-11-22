package main

import "testing"

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Long key", "sk-abc123def456ghi789", "sk-a...i789"},
		{"Short key (5 chars)", "abcde", "***"},
		{"Short key (4 chars)", "ab", "***"},
		{"Short key (1 char)", "a", "***"},
		{"Exact 8 chars", "abcdefgh", "abcd...efgh"},
		{"Empty key", "", "***"},
		{"9 chars", "abcdefghi", "abcd...fghi"},
		{"10 chars", "abcdefghij", "abcd...ghij"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := maskAPIKey(tc.input)
			if result != tc.expected {
				t.Errorf("maskAPIKey(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestMaskAPIKey_EdgeCases(t *testing.T) {
	result := maskAPIKey("abcd")
	if result != "***" {
		t.Errorf("maskAPIKey('abcd') = %q, want '***'", result)
	}

	result = maskAPIKey("abcde")
	if result != "***" {
		t.Errorf("maskAPIKey('abcde') = %q, want '***'", result)
	}

	result = maskAPIKey("abcdefg")
	if result != "***" {
		t.Errorf("maskAPIKey('abcdefg') = %q, want '***'", result)
	}

	result = maskAPIKey("abcdefgh")
	if result != "abcd...efgh" {
		t.Errorf("maskAPIKey('abcdefgh') = %q, want 'abcd...efgh'", result)
	}

	result = maskAPIKey("abcdefghi")
	if result != "abcd...fghi" {
		t.Errorf("maskAPIKey('abcdefghi') = %q, want 'abcd...fghi'", result)
	}
}

func TestMaskAPIKey_Boundary(t *testing.T) {
	if maskAPIKey("abcd") != "***" {
		t.Error("maskAPIKey('abcd') should be '***'")
	}

	if maskAPIKey("abcde") != "***" {
		t.Error("maskAPIKey('abcde') should be '***'")
	}

	if maskAPIKey("abcdefg") != "***" {
		t.Error("maskAPIKey('abcdefg') should be '***'")
	}
}
