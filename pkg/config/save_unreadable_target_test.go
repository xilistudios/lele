package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveConfigRefusesUnreadableConfig(t *testing.T) {
	assertUnreadableTargetRefused(t, func(path string) error {
		return SaveConfig(path, newTestSaveConfig())
	})
}

func TestSaveEditableDocumentRefusesUnreadableConfig(t *testing.T) {
	assertUnreadableTargetRefused(t, func(path string) error {
		return SaveEditableDocument(path, defaultEditableDocument())
	})
}

func assertUnreadableTargetRefused(t *testing.T, save func(path string) error) {
	t.Helper()

	cases := []struct {
		name  string
		setup func(t *testing.T, root, path string)
	}{
		{
			name: "target is a directory",
			setup: func(t *testing.T, root, path string) {
				if err := os.Mkdir(path, 0755); err != nil {
					t.Fatalf("mkdir target: %v", err)
				}
				if err := os.WriteFile(filepath.Join(path, "inside"), []byte("keep"), 0600); err != nil {
					t.Fatalf("write inside: %v", err)
				}
			},
		},
		{
			name: "target is a symlink to a directory",
			setup: func(t *testing.T, root, path string) {
				linked := filepath.Join(root, "linked")
				if err := os.Mkdir(linked, 0755); err != nil {
					t.Fatalf("mkdir symlink target: %v", err)
				}
				if err := os.WriteFile(filepath.Join(linked, "inside"), []byte("keep"), 0600); err != nil {
					t.Fatalf("write inside: %v", err)
				}
				if err := os.Symlink(linked, path); err != nil {
					t.Fatalf("symlink: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config.json")
			tc.setup(t, root, path)

			if err := save(path); err == nil {
				t.Fatal("save over an entry that exists but cannot be read succeeded; want an error: refuse rather than clobber")
			}

			assertUnreadableEntryIntact(t, path)
		})
	}
}

func assertUnreadableEntryIntact(t *testing.T, path string) {
	t.Helper()

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat %s: %v", path, err)
	}
	if info.Mode().IsRegular() {
		t.Fatalf("%s was replaced by a regular file: the unreadable config was clobbered", path)
	}

	inside, err := os.ReadFile(filepath.Join(path, "inside"))
	if err != nil {
		t.Fatalf("read inside %s: %v", path, err)
	}
	if string(inside) != "keep" {
		t.Errorf("contents behind %s changed to %q, want %q", path, inside, "keep")
	}
}
