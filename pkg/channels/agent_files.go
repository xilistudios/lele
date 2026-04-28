package channels

import (
	"encoding/json"
	"io"
	"net/http"
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

// Context files that exist in every agent workspace.
var agentContextFiles = []string{
	"AGENT.md",
	"SOUL.md",
	"USER.md",
	"IDENTITY.md",
	"MEMORY.md",
	"HEARTBEAT.md",
}

func (n *NativeChannel) handleAgentDispatcher(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	// Check if path ends with /files by parsing the route properly
	prefix := "/api/v1/agents/"
	if strings.HasPrefix(path, prefix) {
		rest := strings.TrimPrefix(path, prefix)
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) == 2 && parts[1] == "files" {
			n.handleAgentFiles(w, r)
			return
		}
	}
	n.handleAgentInfo(w, r)
}

func (n *NativeChannel) handleAgentFiles(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	prefix := "/api/v1/agents/"
	if !strings.HasPrefix(path, prefix) {
		writeError(w, http.StatusBadRequest, "invalid path", "path_invalid")
		return
	}

	// Remove prefix and split to get agentID and trailing /files
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 || parts[1] != "files" {
		writeError(w, http.StatusBadRequest, "endpoint must be /api/v1/agents/{id}/files", "path_invalid")
		return
	}

	agentID := parts[0]
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	info, ok := n.agentLoop.GetAgentInfo(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}

	workspace := info.Workspace
	if workspace == "" {
		workspace = expandHomePath("~/.lele/workspace")
	} else {
		workspace = expandHomePath(workspace)
	}

	// Resolve to absolute path and validate the workspace lives under the home directory
	// (or is a well-known allowed path). This prevents accidental access to system dirs.
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve workspace path", "workspace_invalid")
		return
	}
	if !isAllowedWorkspacePath(absWorkspace) {
		writeError(w, http.StatusForbidden, "workspace path is outside allowed directories", "workspace_forbidden")
		return
	}

	// Bootstrap workspace: create directory and seed context files if missing.
	// This handles the case where an agent was just created via the UI but its
	// workspace directory hasn't been initialized yet.
	if err := bootstrapWorkspace(absWorkspace); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to bootstrap workspace: "+err.Error(), "workspace_create_failed")
		return
	}

	fileName := getQueryParam(r, "file")

	switch r.Method {
	case http.MethodGet:
		if fileName != "" {
			// Read specific file
			n.handleAgentFileRead(w, r, absWorkspace, fileName)
			return
		}

		// List available files
		n.handleAgentFileList(w, r, absWorkspace)

	case http.MethodPut:
		if fileName == "" {
			writeError(w, http.StatusBadRequest, "file parameter required", "file_required")
			return
		}
		n.handleAgentFileWrite(w, r, absWorkspace, fileName)

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
	}
}

func (n *NativeChannel) handleAgentFileList(w http.ResponseWriter, _ *http.Request, workspace string) {
	files := make([]AgentFileInfo, 0, len(agentContextFiles))

	for _, name := range agentContextFiles {
		filePath := filepath.Join(workspace, name)
		info, err := os.Stat(filePath)
		if err != nil {
			// File doesn't exist — still list it as creatable
			files = append(files, AgentFileInfo{
				Name:     name,
				Size:     0,
				Editable: true,
			})
			continue
		}
		files = append(files, AgentFileInfo{
			Name:     name,
			Size:     info.Size(),
			Editable: true,
		})
	}

	writeJSON(w, http.StatusOK, AgentFilesResponse{Files: files})
}

func (n *NativeChannel) handleAgentFileRead(w http.ResponseWriter, _ *http.Request, workspace, fileName string) {
	// Security: only allow known context files
	if !isAgentContextFile(fileName) {
		writeError(w, http.StatusForbidden, "file not allowed", "file_not_allowed")
		return
	}

	filePath := filepath.Join(workspace, fileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist yet — return empty content
			writeJSON(w, http.StatusOK, AgentFilesResponse{
				Content: "",
				Files: []AgentFileInfo{{
					Name:     fileName,
					Size:     0,
					Editable: true,
				}},
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to read file", "read_error")
		return
	}

	writeJSON(w, http.StatusOK, AgentFilesResponse{
		Content: string(data),
		Files: []AgentFileInfo{{
			Name:     fileName,
			Size:     int64(len(data)),
			Editable: true,
		}},
	})
}

func (n *NativeChannel) handleAgentFileWrite(w http.ResponseWriter, r *http.Request, workspace, fileName string) {
	// Security: only allow known context files
	if !isAgentContextFile(fileName) {
		writeError(w, http.StatusForbidden, "file not allowed", "file_not_allowed")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body", "body_invalid")
		return
	}

	var req AgentFilesRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	filePath := filepath.Join(workspace, fileName)

	if err := os.WriteFile(filePath, []byte(req.Content), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write file", "write_error")
		return
	}

	// Use known content length instead of re-stat-ing the file
	writeJSON(w, http.StatusOK, AgentFilesResponse{
		Files: []AgentFileInfo{{
			Name:     fileName,
			Size:     int64(len(req.Content)),
			Editable: true,
		}},
	})
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

func isAgentContextFile(name string) bool {
	for _, f := range agentContextFiles {
		if f == name {
			return true
		}
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

// bootstrapWorkspace creates the workspace directory and seeds empty context files
// if the workspace doesn't exist yet. Idempotent — safe to call on every request.
func bootstrapWorkspace(workspace string) error {
	// Create workspace directory if needed
	if err := os.MkdirAll(workspace, 0755); err != nil {
		return err
	}

	// Seed any missing context files as empty
	for _, name := range agentContextFiles {
		filePath := filepath.Join(workspace, name)
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if err := os.WriteFile(filePath, []byte{}, 0644); err != nil {
				return err
			}
		}
	}

	// Also ensure memory/ subdirectory exists (used by NewMemoryStore)
	memoryDir := filepath.Join(workspace, "memory")
	if err := os.MkdirAll(memoryDir, 0755); err != nil {
		return err
	}

	return nil
}
