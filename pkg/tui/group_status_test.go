package tui

import "testing"

// TestRecordGroupCompleteStatusPreservesTerminal guards the TUI side of the
// single-terminal-signal contract (B1): the backend now emits group.complete on
// every terminal path (done | stopped | error), right after the matching
// group.status. The TUI must not overwrite a failed/stopped group with "done".
func TestRecordGroupCompleteStatusPreservesTerminal(t *testing.T) {
	tests := []struct {
		name    string
		initial map[string]string
		groupID string
		want    string
	}{
		{
			name:    "error status survives group.complete",
			initial: map[string]string{"g1": "error"},
			groupID: "g1",
			want:    "error",
		},
		{
			name:    "stopped status survives group.complete",
			initial: map[string]string{"g1": "stopped"},
			groupID: "g1",
			want:    "stopped",
		},
		{
			name:    "done status unchanged",
			initial: map[string]string{"g1": "done"},
			groupID: "g1",
			want:    "done",
		},
		{
			name:    "started falls back to done",
			initial: map[string]string{"g1": "started"},
			groupID: "g1",
			want:    "done",
		},
		{
			name:    "unknown group falls back to done",
			initial: nil,
			groupID: "g2",
			want:    "done",
		},
		{
			name:    "other groups untouched",
			initial: map[string]string{"g1": "started", "g2": "error"},
			groupID: "g1",
			want:    "done",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{groupStatus: tt.initial}
			// Snapshot the unrelated group's status before the call, since
			// m.groupStatus aliases tt.initial.
			wantOther, hadOther := "", false
			if v, ok := tt.initial["g2"]; ok && tt.groupID != "g2" {
				wantOther, hadOther = v, true
			}
			m.recordGroupCompleteStatus(tt.groupID)
			if got := m.groupStatus[tt.groupID]; got != tt.want {
				t.Fatalf("groupStatus[%s] = %q, want %q", tt.groupID, got, tt.want)
			}
			if hadOther && m.groupStatus["g2"] != wantOther {
				t.Fatalf("unrelated group g2 mutated: %q -> %q", wantOther, m.groupStatus["g2"])
			}
		})
	}
}
