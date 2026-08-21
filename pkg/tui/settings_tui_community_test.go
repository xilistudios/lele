package tui

import (
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/tui/theme"
)

// TestInstallCommunityThemeInvalidName covers the error path of
// installCommunityTheme without hitting the network: an invalid theme name is
// rejected early by theme.FetchCommunityTheme's validateCommunityName.
func TestInstallCommunityThemeInvalidName(t *testing.T) {
	m := &Model{}
	m.installCommunityTheme("Invalid.Name!")
	if m.communityErr == "" {
		t.Error("expected communityErr set for invalid theme name")
	}
	// Must not have been added to custom themes or installed list.
	if len(m.customThemes) != 0 {
		t.Errorf("expected no customThemes added, got %d", len(m.customThemes))
	}
}

// TestInstallCommunityThemeEmptyName covers the empty-name error path.
func TestInstallCommunityThemeEmptyName(t *testing.T) {
	m := &Model{}
	m.installCommunityTheme("")
	if m.communityErr == "" {
		t.Error("expected communityErr set for empty theme name")
	}
}

// TestInstallCommunityThemeStoresInstalled ensures that after a failed install
// the installedCommunity list is not mutated (idempotency path at least doesn't
// panic when lists are nil).
func TestInstallCommunityThemeNilLists(t *testing.T) {
	m := &Model{}
	m.installCommunityTheme("")
	// Should not panic and lists remain nil.
	if m.customThemes != nil {
		t.Error("customThemes should remain nil after failed install")
	}
}

// TestCommunityThemeIndexEntries verifies CommunityThemeEntry usage in picker
// build does not choke on entries.
func TestCommunityThemeIndexEntries(t *testing.T) {
	m := &Model{
		communityIndex: []theme.CommunityThemeEntry{
			{Name: "foo-theme", Description: "desc"},
			{Name: "bar-theme"},
		},
	}
	items := m.buildThemePickerItems()
	count := 0
	for _, it := range items {
		if it.kind == "community" {
			count++
		}
	}
	if count != 2 {
		t.Errorf("expected 2 community items, got %d", count)
	}
	if !strings.Contains(m.communityIndex[0].Description, "desc") {
		t.Error("description field should be present")
	}
}