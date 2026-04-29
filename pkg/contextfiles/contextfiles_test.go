package contextfiles

import (
	"os"
	"testing"
)

func TestFindTemplateWorkspaceDir(t *testing.T) {
	templateDir := t.TempDir()
	t.Setenv("LELE_TEMPLATE_WORKSPACE", templateDir)

	found := findTemplateWorkspaceDir()
	if found != templateDir {
		t.Errorf("findTemplateWorkspaceDir with env var set: got %q, want %q", found, templateDir)
	}

	t.Setenv("LELE_TEMPLATE_WORKSPACE", "")
	found = findTemplateWorkspaceDir()
	if found != "" {
		if info, err := os.Stat(found); err != nil || !info.IsDir() {
			t.Errorf("findTemplateWorkspaceDir returned %q which is not a valid directory", found)
		}
	}
}
