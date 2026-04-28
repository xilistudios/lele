package agent

import (
	"io"
	"os"
	"path/filepath"

	"github.com/xilistudios/lele/pkg/logger"
)

// templateWorkspaceDir is the default directory containing workspace template files.
// This is relative to the working directory where lele is run.
const templateWorkspaceDir = "workspace"

// ContextFiles are the core context files that should be initialized in every agent workspace.
var ContextFiles = []string{
	"AGENT.md",
	"SOUL.md",
	"USER.md",
	"IDENTITY.md",
	"MEMORY.md",
	"HEARTBEAT.md",
}

// InitializeWorkspace copies template context files to a new agent's workspace.
// This ensures every agent has the essential context files on first creation.
// Files are only copied if they don't already exist in the destination.
func InitializeWorkspace(workspace string) error {
	// Find the template workspace directory
	templateDir := findTemplateWorkspaceDir()
	if templateDir == "" {
		logger.DebugCF("agent", "Template workspace directory not found, skipping initialization", nil)
		return nil // Not an error - template might not be available in some deployments
	}

	// Ensure workspace directory exists
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return err
	}

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

	// Create memory directory
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		logger.WarnCF("agent", "Failed to create memory directory",
			map[string]interface{}{
				"error": err.Error(),
			})
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

	logger.InfoCF("agent", "Workspace initialized with context files",
		map[string]interface{}{
			"workspace": workspace,
			"template":  templateDir,
		})

	return nil
}

// findTemplateWorkspaceDir locates the template workspace directory.
// It searches in:
// 1. Current working directory (for development/local runs)
// 2. Directory of the lele executable (for installed deployments)
// 3. LELE_TEMPLATE_WORKSPACE env variable (for custom setups)
func findTemplateWorkspaceDir() string {
	// Check environment variable first
	if envDir := os.Getenv("LELE_TEMPLATE_WORKSPACE"); envDir != "" {
		if _, err := os.Stat(envDir); err == nil {
			return envDir
		}
	}

	// Try current working directory
	wd, err := os.Getwd()
	if err == nil {
		candidate := filepath.Join(wd, templateWorkspaceDir)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		// Also try cmd/lele/workspace for development
		candidate = filepath.Join(wd, "cmd", "lele", templateWorkspaceDir)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Try executable directory
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

// copyFile copies a single file from src to dst, preserving permissions.
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

// copyDir recursively copies a directory from src to dst.
// Existing files in dst are not overwritten.
func copyDir(src, dst string) error {
	// Ensure destination directory exists
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

		// Skip if destination exists
		if _, err := os.Stat(dstPath); err == nil {
			return nil
		}

		return copyFile(path, dstPath)
	})
}
