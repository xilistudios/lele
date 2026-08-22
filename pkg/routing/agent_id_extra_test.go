package routing

import (
	"strings"
	"testing"
)

// TestNormalizeAccountID_AllInvalid verifies that an account ID made only of
// invalid characters collapses to an empty result and returns DefaultAccountID.
func TestNormalizeAccountID_AllInvalid(t *testing.T) {
	if got := NormalizeAccountID("###"); got != DefaultAccountID {
		t.Errorf("NormalizeAccountID('###') = %q, want %q", got, DefaultAccountID)
	}
	if got := NormalizeAccountID("@!$"); got != DefaultAccountID {
		t.Errorf("NormalizeAccountID('@!$') = %q, want %q", got, DefaultAccountID)
	}
}

// TestNormalizeAccountID_Whitespace verifies whitespace-only collapses to default.
func TestNormalizeAccountID_Whitespace(t *testing.T) {
	if got := NormalizeAccountID("  \t "); got != DefaultAccountID {
		t.Errorf("NormalizeAccountID whitespace = %q, want %q", got, DefaultAccountID)
	}
}

// TestNormalizeAccountID_TruncatesAt64 verifies account IDs longer than
// MaxAgentIDLength are truncated and still valid.
func TestNormalizeAccountID_TruncatesAt64(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := NormalizeAccountID(long)
	if len(got) > MaxAgentIDLength {
		t.Errorf("length = %d, want <= %d", len(got), MaxAgentIDLength)
	}
	if got != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Errorf("NormalizeAccountID(long) = %q", got)
	}
}

// TestNormalizeAccountID_Table covers mixed valid/invalid patterns similar to
// those tested for agent IDs, ensuring parity between the two normalizers.
func TestNormalizeAccountID_Table(t *testing.T) {
	tests := []struct{ input, want string }{
		{"MyBot", "mybot"},
		{"BOT@home", "bot-home"},
		{"--lead", "lead"},
		{"trail--", "trail--"}, // valid ID: trailing dashes allowed by regex
		{"under_score", "under_score"},
		{"0abc", "0abc"},
		{"ABC-DEF", "abc-def"},
	}
	for _, tt := range tests {
		if got := NormalizeAccountID(tt.input); got != tt.want {
			t.Errorf("NormalizeAccountID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestNormalizeAgentID_JustOverLimit ensures truncation happens right at the
// boundary (64 is valid, 65 truncates to 64).
func TestNormalizeAgentID_LimitBoundary(t *testing.T) {
	valid := strings.Repeat("a", 64)
	if got := NormalizeAgentID(valid); got != valid || len(got) != 64 {
		t.Errorf("NormalizeAgentID 64 chars = %q (len %d), want 64 chars", got, len(got))
	}
	over := strings.Repeat("a", 65)
	got := NormalizeAgentID(over)
	if len(got) != 64 {
		t.Errorf("NormalizeAgentID 65 chars = %q (len %d), want exactly 64", got, len(got))
	}
}
