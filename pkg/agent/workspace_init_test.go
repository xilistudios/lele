package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestFindTemplateWorkspaceDir(t *testing.T) {
	// Test with environment variable set
	templateDir := t.TempDir()
	t.Setenv("LELE_TEMPLATE_WORKSPACE", templateDir)

	found := findTemplateWorkspaceDir()
	if found != templateDir {
		t.Errorf("findTemplateWorkspaceDir with env var set: got %q, want %q", found, templateDir)
	}

	// Test without env var — behavior depends on working directory.
	// If running from project root, it may find cmd/lele/workspace/.
	// We just verify it doesn't panic and returns a valid-looking path
	// (either empty or a real directory).
	t.Setenv("LELE_TEMPLATE_WORKSPACE", "")
	found = findTemplateWorkspaceDir()
	if found != "" {
		if info, err := os.Stat(found); err != nil || !info.IsDir() {
			t.Errorf("findTemplateWorkspaceDir returned %q which is not a valid directory", found)
		}
	}
	// found == "" is also an acceptable result
}

func TestContextFiles_SyncWithChannels(t *testing.T) {
	// This test ensures that agent.ContextFiles and channels.agentContextFiles
	// stay in sync. Since there's an import cycle between the packages, we
	// verify the channels list by reading the source file directly.
	//
	// If you add a new context file, update BOTH:
	//   - pkg/agent/workspace_init.go: ContextFiles
	//   - pkg/channels/agent_files.go: agentContextFiles

	thisDir := filepath.Dir("workspace_init.go")
	channelsFile := filepath.Join(thisDir, "..", "channels", "agent_files.go")
	data, err := os.ReadFile(channelsFile)
	if err != nil {
		t.Skipf("Cannot read channels/agent_files.go: %v", err)
	}

	content := string(data)
	for _, filename := range ContextFiles {
		if !strings.Contains(content, `"`+filename+`"`) {
			t.Errorf("ContextFiles entry %q not found in pkg/channels/agent_files.go — update agentContextFiles!", filename)
		}
	}
}
