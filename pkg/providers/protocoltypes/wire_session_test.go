// Lele - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package protocoltypes

import "testing"

func TestWireSessionFromOptions(t *testing.T) {
	tests := []struct {
		name    string
		options map[string]interface{}
		want    string
	}{
		{
			name:    "string value",
			options: map[string]interface{}{OptWireSessionID: "abc123"},
			want:    "abc123",
		},
		{
			name:    "whitespace trimmed",
			options: map[string]interface{}{OptWireSessionID: "  abc123  "},
			want:    "abc123",
		},
		{
			name:    "absent key",
			options: map[string]interface{}{"other": "abc123"},
			want:    "",
		},
		{
			name:    "wrong type",
			options: map[string]interface{}{OptWireSessionID: 42},
			want:    "",
		},
		{
			name:    "nil options",
			options: nil,
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WireSessionFromOptions(tt.options); got != tt.want {
				t.Fatalf("WireSessionFromOptions() = %q, want %q", got, tt.want)
			}
		})
	}
}
