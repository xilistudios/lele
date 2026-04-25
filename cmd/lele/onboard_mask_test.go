package main

import "testing"

// TestMaskAPIKey tests the API key masking function
func TestMaskAPIKey(t *testing.T) {
	// maskAPIKey returns key[:4] + "..." + key[len(key)-4:] for len >= 8
	// and "***" for len < 8
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

// TestMaskAPIKey_EdgeCases tests edge cases for API key masking
func TestMaskAPIKey_EdgeCases(t *testing.T) {
	// Test with exactly 4 characters (len < 8)
	result := maskAPIKey("abcd")
	if result != "***" {
		t.Errorf("maskAPIKey('abcd') = %q, want '***'", result)
	}

	// Test with exactly 5 characters (len < 8)
	result = maskAPIKey("abcde")
	if result != "***" {
		t.Errorf("maskAPIKey('abcde') = %q, want '***'", result)
	}

	// Test with 7 characters (len < 8)
	result = maskAPIKey("abcdefg")
	if result != "***" {
		t.Errorf("maskAPIKey('abcdefg') = %q, want '***'", result)
	}

	// Test with 8 characters (boundary: len >= 8)
	result = maskAPIKey("abcdefgh")
	if result != "abcd...efgh" {
		t.Errorf("maskAPIKey('abcdefgh') = %q, want 'abcd...efgh'", result)
	}

	// Test with 9 characters
	result = maskAPIKey("abcdefghi")
	if result != "abcd...fghi" {
		t.Errorf("maskAPIKey('abcdefghi') = %q, want 'abcd...fghi'", result)
	}
}

// TestMaskAPIKey_Boundary tests exact boundary values
func TestMaskAPIKey_Boundary(t *testing.T) {
	// 4 chars: "***"
	if maskAPIKey("abcd") != "***" {
		t.Error("maskAPIKey('abcd') should be '***'")
	}

	// 5 chars: "***"
	if maskAPIKey("abcde") != "***" {
		t.Error("maskAPIKey('abcde') should be '***'")
	}

	// 7 chars: still "***" since len < 8
	if maskAPIKey("abcdefg") != "***" {
		t.Error("maskAPIKey('abcdefg') should be '***'")
	}
}
