package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/context"
)

func TestInitializeWorkspace(t *testing.T) {
	templateDir := t.TempDir()

	for _, filename := range ContextFiles {
		content := "# Test " + filename + "\nThis is a test file."
		if err := os.WriteFile(filepath.Join(templateDir, filename), []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create template file %s: %v", filename, err)
		}
	}

	templateSkillsDir := filepath.Join(templateDir, "skills", "test-skill")
	if err := os.MkdirAll(templateSkillsDir, 0755); err != nil {
		t.Fatalf("Failed to create template skills directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templateSkillsDir, "SKILL.md"), []byte("# Test Skill"), 0644); err != nil {
		t.Fatalf("Failed to create SKILL.md: %v", err)
	}

	workspace := t.TempDir()

	os.Setenv("LELE_TEMPLATE_WORKSPACE", templateDir)
	defer os.Unsetenv("LELE_TEMPLATE_WORKSPACE")

	if err := InitializeWorkspace(workspace); err != nil {
		t.Fatalf("InitializeWorkspace failed: %v", err)
	}

	for _, filename := range ContextFiles {
		dst := filepath.Join(workspace, filename)
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			t.Errorf("Context file %s was not copied", filename)
		}
	}

	memoryDir := filepath.Join(workspace, "memory")
	if _, err := os.Stat(memoryDir); os.IsNotExist(err) {
		t.Errorf("Memory directory was not created")
	}

	skillFile := filepath.Join(workspace, "skills", "test-skill", "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		t.Errorf("Skills directory was not copied")
	}
}

func TestInitializeWorkspaceDoesNotOverwrite(t *testing.T) {
	templateDir := t.TempDir()
	templateContent := "# Template Content"
	for _, filename := range ContextFiles {
		if err := os.WriteFile(filepath.Join(templateDir, filename), []byte(templateContent), 0644); err != nil {
			t.Fatalf("Failed to create template file %s: %v", filename, err)
		}
	}

	workspace := t.TempDir()
	existingContent := "# My Custom Content - DO NOT OVERWRITE"
	for _, filename := range ContextFiles {
		if err := os.WriteFile(filepath.Join(workspace, filename), []byte(existingContent), 0644); err != nil {
			t.Fatalf("Failed to create existing file %s: %v", filename, err)
		}
	}

	os.Setenv("LELE_TEMPLATE_WORKSPACE", templateDir)
	defer os.Unsetenv("LELE_TEMPLATE_WORKSPACE")

	if err := InitializeWorkspace(workspace); err != nil {
		t.Fatalf("InitializeWorkspace failed: %v", err)
	}

	for _, filename := range ContextFiles {
		dst := filepath.Join(workspace, filename)
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Errorf("Failed to read %s: %v", filename, err)
			continue
		}
		if string(data) != existingContent {
			t.Errorf("File %s was overwritten with template content", filename)
		}
	}
}

func TestInitializeWorkspaceNoTemplate(t *testing.T) {
	workspace := t.TempDir()
	os.Unsetenv("LELE_TEMPLATE_WORKSPACE")

	if err := InitializeWorkspace(workspace); err != nil {
		t.Fatalf("InitializeWorkspace should not fail when template is missing: %v", err)
	}

	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		t.Errorf("Workspace directory was not created")
	}

	for _, filename := range ContextFiles {
		dst := filepath.Join(workspace, filename)
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			t.Errorf("Context file %s was not created from embedded template", filename)
		}
	}

	for _, filename := range context.DeprecatedContextFiles {
		dst := filepath.Join(workspace, filename)
		if _, err := os.Stat(dst); err == nil {
			t.Errorf("deprecated context file %s must not be created for a new workspace", filename)
		}
	}

	memoryDir := filepath.Join(workspace, "memory")
	if _, err := os.Stat(memoryDir); os.IsNotExist(err) {
		t.Errorf("Memory directory was not created")
	}
}

func TestInitializeWorkspaceKeepsDeprecatedFilesOfAnExistingWorkspace(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("LELE_TEMPLATE_WORKSPACE", "")

	const persona = "# SOUL\nmy own persona"
	if err := os.WriteFile(filepath.Join(workspace, "SOUL.md"), []byte(persona), 0o644); err != nil {
		t.Fatalf("seed SOUL.md: %v", err)
	}

	if err := InitializeWorkspace(workspace); err != nil {
		t.Fatalf("InitializeWorkspace failed: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(workspace, "SOUL.md"))
	if err != nil {
		t.Fatalf("an existing SOUL.md must survive initialization: %v", err)
	}
	if string(got) != persona {
		t.Errorf("SOUL.md content = %q, want it untouched (%q)", got, persona)
	}
	if prompt := NewContextBuilder(workspace).LoadBootstrapFiles(); !strings.Contains(prompt, "my own persona") {
		t.Errorf("a deprecated context file must still reach the bootstrap prompt:\n%s", prompt)
	}
}

func TestContextFiles_SyncWithChannels(t *testing.T) {
	if len(ContextFiles) != len(context.ContextFiles) {
		t.Errorf("agent.ContextFiles length (%d) != context.ContextFiles length (%d)",
			len(ContextFiles), len(context.ContextFiles))
	}
	for i, f := range ContextFiles {
		if f != context.ContextFiles[i] {
			t.Errorf("agent.ContextFiles[%d] = %q, context.ContextFiles[%d] = %q",
				i, f, i, context.ContextFiles[i])
		}
	}
}
