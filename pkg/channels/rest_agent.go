package channels

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
	lelectx "github.com/xilistudios/lele/pkg/context"
)

func (n *NativeChannel) handleAgents(w http.ResponseWriter, r *http.Request) {
	agentIDs := n.agentLoop.ListAvailableAgentIDs()
	agents := make([]NativeAgentInfo, 0, len(agentIDs))
	defaultID := n.agentLoop.GetDefaultAgentID()

	for _, id := range agentIDs {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if ok {
			agents = append(agents, NativeAgentInfo{
				ID:        info.ID,
				Name:      info.Name,
				Workspace: info.Workspace,
				Model:     info.Model,
				Default:   info.ID == defaultID,
				Reasoning: info.Reasoning,
			})
		}
	}

	writeJSON(w, http.StatusOK, AgentsResponse{Agents: agents})
}

func (n *NativeChannel) handleAgentInfo(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	info, ok := n.agentLoop.GetAgentInfo(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}

	writeJSON(w, http.StatusOK, NativeAgentInfo{
		ID:        info.ID,
		Name:      info.Name,
		Workspace: info.Workspace,
		Model:     info.Model,
		Default:   info.ID == n.agentLoop.GetDefaultAgentID(),
	})
}

func (n *NativeChannel) handleAgentStatus(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	_, ok := n.agentLoop.GetAgentInfo(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}

	status := n.agentLoop.GetStatus(getClientID(r))
	writeJSON(w, http.StatusOK, AgentStatusResponse{
		ID:             agentID,
		Status:         status,
		ActiveSessions: 0,
	})
}

func (n *NativeChannel) resolveAgentWorkspace(agentID string) (string, error) {
	info, ok := n.agentLoop.GetAgentInfo(agentID)
	if !ok {
		return "", fmt.Errorf("agent not found: %s", agentID)
	}

	workspace := info.Workspace
	if workspace == "" {
		workspace = filepath.Join(config.GetLeleDir(), "workspace")
	} else {
		workspace = expandHomePath(workspace)
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}
	if !isAllowedWorkspacePath(absWorkspace) {
		return "", fmt.Errorf("workspace path is outside allowed directories")
	}

	// Initialize workspace if needed
	if err := lelectx.InitializeWorkspace(absWorkspace); err != nil {
		return "", fmt.Errorf("failed to initialize workspace: %w", err)
	}

	return absWorkspace, nil
}

func (n *NativeChannel) handleAgentFiles(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	absWorkspace, err := n.resolveAgentWorkspace(agentID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "agent not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "outside allowed") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error(), "workspace_error")
		return
	}

	n.handleAgentFileList(w, r, absWorkspace)
}

func (n *NativeChannel) handleAgentFileRead(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	fileName := r.PathValue("fileName")

	if agentID == "" || fileName == "" {
		writeError(w, http.StatusBadRequest, "agent id and file name required", "params_missing")
		return
	}

	absWorkspace, err := n.resolveAgentWorkspace(agentID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "agent not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "outside allowed") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error(), "workspace_error")
		return
	}

	if !lelectx.IsContextFile(fileName) {
		writeError(w, http.StatusForbidden, "file not allowed", "file_not_allowed")
		return
	}

	filePath := filepath.Join(absWorkspace, fileName)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
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

func (n *NativeChannel) handleAgentFileSave(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	fileName := r.PathValue("fileName")

	if agentID == "" || fileName == "" {
		writeError(w, http.StatusBadRequest, "agent id and file name required", "params_missing")
		return
	}

	absWorkspace, err := n.resolveAgentWorkspace(agentID)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "agent not found") {
			status = http.StatusNotFound
		} else if strings.Contains(err.Error(), "outside allowed") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error(), "workspace_error")
		return
	}

	if !lelectx.IsContextFile(fileName) {
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

	filePath := filepath.Join(absWorkspace, fileName)
	if err := os.WriteFile(filePath, []byte(req.Content), 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to write file", "write_error")
		return
	}

	writeJSON(w, http.StatusOK, AgentFilesResponse{
		Files: []AgentFileInfo{{
			Name:     fileName,
			Size:     int64(len(req.Content)),
			Editable: true,
		}},
	})
}

func (n *NativeChannel) handleAgentFileList(w http.ResponseWriter, _ *http.Request, workspace string) {
	files := make([]AgentFileInfo, 0, len(lelectx.ContextFiles))

	for _, name := range lelectx.ContextFiles {
		absFilePath := filepath.Join(workspace, name)
		info, err := os.Stat(absFilePath)
		if err != nil {
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
