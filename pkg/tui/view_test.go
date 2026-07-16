package tui

import "testing"

func TestCalculateViewportHeight(t *testing.T) {
	tests := []struct {
		name          string
		contentHeight int
		statusHeight  int
		autocomplete  int
		inputHeight   int
		bottomHeight  int
		want          int
	}{
		{
			name:          "normal layout",
			contentHeight: 24,
			statusHeight:  3,
			inputHeight:   3,
			bottomHeight:  1,
			want:          14,
		},
		{
			name:          "autocomplete consumes its own lines",
			contentHeight: 24,
			statusHeight:  3,
			autocomplete:  5,
			inputHeight:   3,
			bottomHeight:  1,
			want:          9,
		},
		{
			name:          "small terminal keeps minimum viewport",
			contentHeight: 8,
			statusHeight:  3,
			inputHeight:   3,
			bottomHeight:  1,
			want:          3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateViewportHeight(tt.contentHeight, tt.statusHeight, tt.autocomplete, tt.inputHeight, tt.bottomHeight)
			if got != tt.want {
				t.Fatalf("calculateViewportHeight() = %d, want %d", got, tt.want)
			}
		})
	}
}
