package group

import (
	"strings"
	"testing"
)

func TestNewGroupID(t *testing.T) {
	const count = 1000
	seen := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		id := NewGroupID("adhoc")
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate group ID generated: %s", id)
		}
		seen[id] = struct{}{}
	}

	id := NewGroupID("adhoc")
	if !strings.HasPrefix(id, "group:adhoc-") {
		t.Errorf("expected prefix %q, got %q", "group:adhoc-", id)
	}
	suffix := strings.TrimPrefix(id, "group:adhoc-")
	if len(suffix) != 16 {
		t.Errorf("expected 16-char hex suffix, got %d chars: %q", len(suffix), suffix)
	}

	id2 := NewGroupID("myprofile")
	if !strings.HasPrefix(id2, "group:myprofile-") {
		t.Errorf("expected prefix %q, got %q", "group:myprofile-", id2)
	}
	suffix2 := strings.TrimPrefix(id2, "group:myprofile-")
	if len(suffix2) != 16 {
		t.Errorf("expected 16-char hex suffix, got %d chars: %q", len(suffix2), suffix2)
	}
}
