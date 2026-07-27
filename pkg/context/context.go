// Package context provides the canonical list of agent context files
// and workspace initialization logic shared across the agent and channels packages.
//
// This package is intentionally minimal — it only depends on stdlib + pkg/logger
// to avoid import cycles between pkg/agent and pkg/channels.
package context

import (
	"embed"
	"io"
	"os"
	"path/filepath"

	"github.com/xilistudios/lele/pkg/logger"
)

//go:embed templates/*.md
var embeddedTemplates embed.FS

// templateWorkspaceDir is the default directory containing workspace template files.
// This is relative to the working directory where lele is run.
const templateWorkspaceDir = "workspace"

// ContextFiles are the core context files that should be initialized in every agent workspace.
// This is the single source of truth — both pkg/agent and pkg/channels reference this list.
var ContextFiles = []string{
	"AGENT.md",
	"SOUL.md",
	"USER.md",
	"IDENTITY.md",
	"MEMORY.md",
	"HEARTBEAT.md",
}

// IsContextFile returns true if the given filename is a known context file.
func IsContextFile(name string) bool {
	for _, f := range ContextFiles {
		if f == name {
			return true
		}
	}
	return false
}

// InitializeWorkspace copies template context files to a new agent's workspace.
// This ensures every agent has the essential context files on first creation.
// Files are only copied if they don't already exist in the destination.
func InitializeWorkspace(workspace string) error {
	// Always ensure workspace directory exists
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return err
	}

	// Always create memory directory
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		logger.WarnCF("agent", "Failed to create memory directory",
			map[string]interface{}{"error": err.Error()})
	}

	// Try disk-based templates first (allows customization)
	templateDir := findTemplateWorkspaceDir()
	if templateDir != "" {
		initializeFromDisk(workspace, templateDir)
		return nil
	}

	// Fallback: use embedded templates
	logger.DebugCF("agent", "Template workspace directory not found, using embedded templates", nil)
	initializeFromEmbedded(workspace)
	return nil
}

// initializeFromDisk copies context files and skills from a disk-based template directory.
func initializeFromDisk(workspace, templateDir string) {
	// Copy context files
	for _, filename := range ContextFiles {
		src := filepath.Join(templateDir, filename)
		dst := filepath.Join(workspace, filename)

		// Skip if destination already exists (user may have customized it)
		if _, err := os.Stat(dst); err == nil {
			logger.DebugCF("agent", "Context file already exists, skipping",
				map[string]interface{}{
					"file":      filename,
					"workspace": workspace,
				})
			continue
		}

		// Copy if source exists
		if _, err := os.Stat(src); err == nil {
			if err := copyFile(src, dst); err != nil {
				logger.WarnCF("agent", "Failed to copy context file",
					map[string]interface{}{
						"file":  filename,
						"error": err.Error(),
					})
				continue
			}
			logger.DebugCF("agent", "Copied context file",
				map[string]interface{}{
					"file":      filename,
					"workspace": workspace,
				})
		}
	}

	// Copy skills directory if template has it
	templateSkillsDir := filepath.Join(templateDir, "skills")
	if _, err := os.Stat(templateSkillsDir); err == nil {
		dstSkillsDir := filepath.Join(workspace, "skills")
		if err := copyDir(templateSkillsDir, dstSkillsDir); err != nil {
			logger.WarnCF("agent", "Failed to copy skills directory",
				map[string]interface{}{
					"error": err.Error(),
				})
		} else {
			logger.DebugCF("agent", "Copied skills directory",
				map[string]interface{}{
					"workspace": workspace,
				})
		}
	}
}

// initializeFromEmbedded writes embedded template files to the workspace.
func initializeFromEmbedded(workspace string) {
	for _, filename := range ContextFiles {
		dst := filepath.Join(workspace, filename)
		// Skip if destination already exists
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		// Read from embedded FS
		data, err := embeddedTemplates.ReadFile("templates/" + filename)
		if err != nil {
			logger.WarnCF("agent", "Embedded template not found",
				map[string]interface{}{"file": filename, "error": err.Error()})
			continue
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			logger.WarnCF("agent", "Failed to write embedded template",
				map[string]interface{}{"file": filename, "error": err.Error()})
		}
	}
}

// findTemplateWorkspaceDir locates the template workspace directory.
func findTemplateWorkspaceDir() string {
	if envDir := os.Getenv("LELE_TEMPLATE_WORKSPACE"); envDir != "" {
		if _, err := os.Stat(envDir); err == nil {
			return envDir
		}
	}

	wd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(wd, templateWorkspaceDir)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		candidate = filepath.Join(wd, "cmd", "lele", templateWorkspaceDir)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	execPath, err := os.Executable()
	if err == nil {
		execDir := filepath.Dir(execPath)
		candidate := filepath.Join(execDir, templateWorkspaceDir)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	info, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		if _, err := os.Stat(dstPath); err == nil {
			return nil
		}

		return copyFile(path, dstPath)
	})
}
