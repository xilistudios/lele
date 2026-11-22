package channels

import (
	"os"
	"path/filepath"
	"strings"
)

type AgentFilesRequest struct {
	Content string `json:"content"`
}

type AgentFilesResponse struct {
	Files   []AgentFileInfo `json:"files"`
	Content string          `json:"content,omitempty"`
}

type AgentFileInfo struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Editable bool   `json:"editable"`
}

func isAllowedWorkspacePath(absPath string) bool {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(absPath, home+string(filepath.Separator)) {
		return true
	}
	if home != "" && absPath == home {
		return true
	}
	for _, allowed := range []string{"/tmp/", "/var/folders/"} {
		if strings.HasPrefix(absPath, allowed) {
			return true
		}
	}
	cwd, err := os.Getwd()
	if err == nil && strings.HasPrefix(absPath, cwd+string(filepath.Separator)) {
		return true
	}
	return false
}

func IsAllowedWorkspacePath(absPath string) bool {
	return isAllowedWorkspacePath(absPath)
}

func expandHomePath(path string) string {
	if path == "" {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if len(path) > 1 && path[1] == '/' {
			return home + path[1:]
		}
		return home
	}
	return path
}

func normalizePath(path string) string {
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/")
	}
	return path
}
