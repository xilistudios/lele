package constants

import "testing"

func TestIsInternalChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		want    bool
	}{
		{"cli is internal", "cli", true},
		{"system is internal", "system", true},
		{"subagent is internal", "subagent", true},
		{"telegram is external", "telegram", true}, // placeholder, overridden below
		{"empty channel", "", false},
		{"unknown channel", "slack", false},
		{"case-sensitive", "CLI", false},
		{"whitespace not stripped", " cli", false},
	}

	// Correct the placeholder expectation for a known external channel.
	for i := range tests {
		if tests[i].channel == "telegram" {
			tests[i].want = false
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsInternalChannel(tt.channel); got != tt.want {
				t.Errorf("IsInternalChannel(%q) = %v, want %v", tt.channel, got, tt.want)
			}
		})
	}
}
