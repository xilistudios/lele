package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/contextfiles"
)

func TestInitializeWorkspace(t *testing.T) {
	// Create a temporary template directory
	templateDir := t.TempDir()

	// Create template context files
	for _, filename := range ContextFiles {
		content := "# Test " + filename + "\nThis is a test file."
		if err := os.WriteFile(filepath.Join(templateDir, filename), []byte(content), 0644); err != nil {
			t.Fatalf("Failed to create template file %s: %v", filename, err)
		}
	}

	// Create template skills directory
	templateSkillsDir := filepath.Join(templateDir, "skills", "test-skill")
	if err := os.MkdirAll(templateSkillsDir, 0755); err != nil {
		t.Fatalf("Failed to create template skills directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(templateSkillsDir, "SKILL.md"), []byte("# Test Skill"), 0644); err != nil {
		t.Fatalf("Failed to create SKILL.md: %v", err)
	}

	// Create a temporary workspace to initialize
	workspace := t.TempDir()

	// Override findTemplateWorkspaceDir by setting environment variable
	os.Setenv("LELE_TEMPLATE_WORKSPACE", templateDir)
	defer os.Unsetenv("LELE_TEMPLATE_WORKSPACE")

	// Initialize workspace
	if err := InitializeWorkspace(workspace); err != nil {
		t.Fatalf("InitializeWorkspace failed: %v", err)
	}

	// Verify context files were copied
	for _, filename := range ContextFiles {
		dst := filepath.Join(workspace, filename)
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			t.Errorf("Context file %s was not copied", filename)
		}
	}

	// Verify memory directory was created
	memoryDir := filepath.Join(workspace, "memory")
	if _, err := os.Stat(memoryDir); os.IsNotExist(err) {
		t.Errorf("Memory directory was not created")
	}

	// Verify skills directory was copied
	skillFile := filepath.Join(workspace, "skills", "test-skill", "SKILL.md")
	if _, err := os.Stat(skillFile); os.IsNotExist(err) {
		t.Errorf("Skills directory was not copied")
	}
}

func TestInitializeWorkspaceDoesNotOverwrite(t *testing.T) {
	// Create a temporary template directory
	templateDir := t.TempDir()
	templateContent := "# Template Content"
	for _, filename := range ContextFiles {
		if err := os.WriteFile(filepath.Join(templateDir, filename), []byte(templateContent), 0644); err != nil {
			t.Fatalf("Failed to create template file %s: %v", filename, err)
		}
	}

	// Create a workspace that already has customized files
	workspace := t.TempDir()
	existingContent := "# My Custom Content - DO NOT OVERWRITE"
	for _, filename := range ContextFiles {
		if err := os.WriteFile(filepath.Join(workspace, filename), []byte(existingContent), 0644); err != nil {
			t.Fatalf("Failed to create existing file %s: %v", filename, err)
		}
	}

	// Set template directory
	os.Setenv("LELE_TEMPLATE_WORKSPACE", templateDir)
	defer os.Unsetenv("LELE_TEMPLATE_WORKSPACE")

	// Initialize workspace
	if err := InitializeWorkspace(workspace); err != nil {
		t.Fatalf("InitializeWorkspace failed: %v", err)
	}

	// Verify existing files were NOT overwritten
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
	// Create a workspace without template
	workspace := t.TempDir()

	// Clear template env var
	os.Unsetenv("LELE_TEMPLATE_WORKSPACE")

	// Initialize workspace - should succeed without errors
	if err := InitializeWorkspace(workspace); err != nil {
		t.Fatalf("InitializeWorkspace should not fail when template is missing: %v", err)
	}

	// Workspace directory should still be created
	if _, err := os.Stat(workspace); os.IsNotExist(err) {
		t.Errorf("Workspace directory was not created")
	}
}

func TestContextFiles_SyncWithChannels(t *testing.T) {
	// Verify that agent.ContextFiles matches contextfiles.ContextFiles,
	// the single source of truth used by both pkg/agent and pkg/channels.
	if len(ContextFiles) != len(contextfiles.ContextFiles) {
		t.Errorf("agent.ContextFiles length (%d) != contextfiles.ContextFiles length (%d)",
			len(ContextFiles), len(contextfiles.ContextFiles))
	}
	for i, f := range ContextFiles {
		if f != contextfiles.ContextFiles[i] {
			t.Errorf("agent.ContextFiles[%d] = %q, contextfiles.ContextFiles[%d] = %q",
				i, f, i, contextfiles.ContextFiles[i])
		}
	}
}
