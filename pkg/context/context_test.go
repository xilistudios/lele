package context

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

// TestContextFilesAreCreatedButDeprecatedOnesAreNot is the deprecation
// contract, split in two lists: every ContextFiles entry must ship as an
// embedded template (a new workspace gets it), and no DeprecatedContextFiles
// entry may ship one (a new workspace must NOT get it — that is the whole
// point of the deprecation).
func TestContextFilesAreCreatedButDeprecatedOnesAreNot(t *testing.T) {
	for _, name := range ContextFiles {
		if _, err := embeddedTemplates.ReadFile("templates/" + name); err != nil {
			t.Errorf("ContextFiles entry %s has no embedded template: %v", name, err)
		}
	}
	for _, name := range DeprecatedContextFiles {
		if _, err := embeddedTemplates.ReadFile("templates/" + name); err == nil {
			t.Errorf("deprecated context file %s must not ship a template any more", name)
		}
	}
}

// TestContextFileListsAreDisjoint: a file is either created or deprecated,
// never both, so "is it created?" and "is it deprecated?" can never disagree.
func TestContextFileListsAreDisjoint(t *testing.T) {
	seen := make(map[string]bool, len(ContextFiles))
	for _, name := range ContextFiles {
		if seen[name] {
			t.Errorf("%s appears twice in ContextFiles", name)
		}
		seen[name] = true
	}
	for _, name := range DeprecatedContextFiles {
		if seen[name] {
			t.Errorf("%s is both current and deprecated", name)
		}
		seen[name] = true
	}
	if got, want := len(AllContextFiles()), len(ContextFiles)+len(DeprecatedContextFiles); got != want {
		t.Errorf("AllContextFiles() has %d entries, want %d", got, want)
	}
}

// TestIsContextFileAcceptsDeprecated: an existing workspace still has
// SOUL.md/USER.md/IDENTITY.md on disk and the REST/WebUI editors must keep
// serving them, so deprecation must not mean "unknown file".
func TestIsContextFileAcceptsDeprecated(t *testing.T) {
	for _, name := range AllContextFiles() {
		if !IsContextFile(name) {
			t.Errorf("IsContextFile(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"MEMORY.md.bak", "memory/MEMORY.md", "", "README.md"} {
		if IsContextFile(name) {
			t.Errorf("IsContextFile(%q) = true, want false", name)
		}
	}
}

// TestBootstrapFilesStillInlineDeprecatedPersona pins the compat half of the
// deprecation: the persona files stay in the bootstrap order (they are read
// only if the workspace has them), and HEARTBEAT.md is not a bootstrap file.
func TestBootstrapFilesStillInlineDeprecatedPersona(t *testing.T) {
	inBootstrap := make(map[string]bool, len(BootstrapFiles))
	for _, name := range BootstrapFiles {
		inBootstrap[name] = true
	}
	if !inBootstrap["AGENT.md"] || !inBootstrap["MEMORY.md"] {
		t.Errorf("BootstrapFiles must keep AGENT.md and MEMORY.md, have %v", BootstrapFiles)
	}
	for _, name := range DeprecatedContextFiles {
		if !inBootstrap[name] {
			t.Errorf("%s must still be inlined when present (existing workspaces), have %v", name, BootstrapFiles)
		}
	}
	if inBootstrap["HEARTBEAT.md"] {
		t.Error("HEARTBEAT.md is read by the heartbeat service, not inlined into every prompt")
	}
}
