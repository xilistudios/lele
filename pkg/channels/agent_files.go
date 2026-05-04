package channels

import (
	"os"
	"path/filepath"
	"strings"
)

// AgentFilesRequest is the request body for saving an agent context file.
type AgentFilesRequest struct {
	Content string `json:"content"`
}

// AgentFilesResponse returns file content for the agent context files.
type AgentFilesResponse struct {
	Files   []AgentFileInfo `json:"files"`
	Content string          `json:"content,omitempty"`
}

// AgentFileInfo describes a single context file.
type AgentFileInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Editable bool   `json:"editable"`
}

// isAllowedWorkspacePath returns true if the absolute workspace path is within
// an allowed directory tree (user home, /tmp, or /var/folders for macOS).
func isAllowedWorkspacePath(absPath string) bool {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(absPath, home+string(filepath.Separator)) {
		return true
	}
	if home != "" && absPath == home {
		return true
	}
	// Allow common sandbox locations
	for _, allowed := range []string{"/tmp/", "/var/folders/"} {
		if strings.HasPrefix(absPath, allowed) {
			return true
		}
	}
	// In development / testing, allow relative paths that resolve under the current dir
	cwd, err := os.Getwd()
	if err == nil && strings.HasPrefix(absPath, cwd+string(filepath.Separator)) {
		return true
	}
	return false
}

func expandHomePath(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path // Return original path if home dir unavailable
		}
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}

// normalizePath removes a trailing slash from a URL path (except for root "/").
func normalizePath(path string) string {
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/")
	}
	return path
}
