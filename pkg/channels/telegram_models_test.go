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
