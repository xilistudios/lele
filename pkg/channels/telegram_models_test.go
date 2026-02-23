package channels

import "testing"

func TestModelPageBounds(t *testing.T) {
	start, end, page, total := modelPageBounds(14, 0, 6)
	if start != 0 || end != 6 || page != 0 || total != 3 {
		t.Fatalf("unexpected page 0 bounds: %d,%d,%d,%d", start, end, page, total)
	}

	start, end, page, total = modelPageBounds(14, 1, 6)
	if start != 6 || end != 12 || page != 1 || total != 3 {
		t.Fatalf("unexpected page 1 bounds: %d,%d,%d,%d", start, end, page, total)
	}

	start, end, page, total = modelPageBounds(14, 9, 6)
	if start != 12 || end != 14 || page != 2 || total != 3 {
		t.Fatalf("unexpected last page bounds: %d,%d,%d,%d", start, end, page, total)
	}
}

func TestSelectedModelCommand(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		model    string
		want     string
	}{
		{
			name:     "provider and plain model",
			provider: "nanogpt",
			model:    "minimax-m2.5",
			want:     "/model nanogpt/minimax-m2.5",
		},
		{
			name:     "already prefixed model",
			provider: "nanogpt",
			model:    "minimax/minimax-m2.5",
			want:     "/model minimax/minimax-m2.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := selectedModelCommand(tt.provider, tt.model); got != tt.want {
				t.Fatalf("selectedModelCommand(%q, %q) = %q, want %q", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}
