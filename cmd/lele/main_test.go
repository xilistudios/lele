package main

import (
	"reflect"
	"testing"
)

func TestParseSessionFlag(t *testing.T) {
	tests := []struct {
		name          string
		args          []string
		wantSessionID string
		wantRemaining []string
	}{
		{
			name:          "no session flag",
			args:          []string{"tui"},
			wantSessionID: "",
			wantRemaining: []string{"tui"},
		},
		{
			name:          "session flag long equals",
			args:          []string{"--session=574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"},
			wantSessionID: "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36",
			wantRemaining: []string{},
		},
		{
			name:          "session flag long space",
			args:          []string{"--session", "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"},
			wantSessionID: "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36",
			wantRemaining: []string{},
		},
		{
			name:          "session flag short equals",
			args:          []string{"-s=574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"},
			wantSessionID: "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36",
			wantRemaining: []string{},
		},
		{
			name:          "session flag short space",
			args:          []string{"-s", "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36"},
			wantSessionID: "574f2fc5-3e50-4415-9e7d-aa70e4d4ab36",
			wantRemaining: []string{},
		},
		{
			name:          "session flag after subcommand",
			args:          []string{"tui", "-s", "abc-123"},
			wantSessionID: "abc-123",
			wantRemaining: []string{"tui"},
		},
		{
			name:          "session flag equals after subcommand",
			args:          []string{"tui", "--session=abc-123"},
			wantSessionID: "abc-123",
			wantRemaining: []string{"tui"},
		},
		{
			name:          "session flag before subcommand",
			args:          []string{"--session=abc-123", "tui"},
			wantSessionID: "abc-123",
			wantRemaining: []string{"tui"},
		},
		{
			name:          "session flag missing value",
			args:          []string{"-s"},
			wantSessionID: "",
			wantRemaining: []string{"-s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSessionID, gotRemaining := parseSessionFlag(tt.args)
			if gotSessionID != tt.wantSessionID {
				t.Errorf("parseSessionFlag() gotSessionID = %q, want %q", gotSessionID, tt.wantSessionID)
			}
			if len(gotRemaining) == 0 && len(tt.wantRemaining) == 0 {
				return
			}
			if !reflect.DeepEqual(gotRemaining, tt.wantRemaining) {
				t.Errorf("parseSessionFlag() gotRemaining = %v, want %v", gotRemaining, tt.wantRemaining)
			}
		})
	}
}
