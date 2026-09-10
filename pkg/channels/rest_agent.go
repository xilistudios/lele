package channels

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/config"
	lelectx "github.com/xilistudios/lele/pkg/context"
	"github.com/xilistudios/lele/pkg/skills"
)

func (n *NativeChannel) handleAgents(w http.ResponseWriter, r *http.Request) {
	agentIDs := n.agentLoop.ListAvailableAgentIDs()
	agents := make([]NativeAgentInfo, 0, len(agentIDs))
	defaultID := n.agentLoop.GetDefaultAgentID()

	for _, id := range agentIDs {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if ok {
			agents = append(agents, NativeAgentInfo{
				ID:          info.ID,
				Name:        info.Name,
				Description: info.Description,
				Workspace:   info.Workspace,
				Model:       info.Model,
				Default:     info.ID == defaultID,
				Reasoning:   info.Reasoning,
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
		ID:          info.ID,
		Name:        info.Name,
		Description: info.Description,
		Workspace:   info.Workspace,
		Model:       info.Model,
		Default:     info.ID == n.agentLoop.GetDefaultAgentID(),
	})
}

// handleAgentCatalog serves GET /api/v1/agents/{agentID}/catalog.
//
// It reports what the agent CAN actually use right now, derived from live
// runtime state (never hardcoded):
//   - tools:  the agent instance's real ToolRegistry via agentLoop.ListAgentTools
//   - skills: the agent's OWN skills loader (see skillLoaderFor), so the list
//     matches the <skills> block of this agent's system prompt.
//
// Returns 404 when the agent does not exist (same pattern as handleAgentInfo).
func (n *NativeChannel) handleAgentCatalog(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	// 404 if the agent doesn't exist. GetAgentInfo is the existence check used
	// by every other /agents/{id} endpoint.
	if _, ok := n.agentLoop.GetAgentInfo(agentID); !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}

	// Resolve the catalog from the agent's live tool registry. The existence
	// check above passed, so a missing/false result here means the agent loop
	// implementation cannot expose tools; treat it as 404 rather than 500 so
	// clients see a stable "unknown agent" answer.
	agentTools, ok := n.agentLoop.ListAgentTools(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}

	tools := make([]AgentCatalogTool, 0, len(agentTools))
	for _, t := range agentTools {
		tools = append(tools, AgentCatalogTool{Name: t.Name, Description: t.Description})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	// Skills come from THIS agent's loader — the same instance that renders its
	// <skills> prompt block — so the catalog can never disagree with what the
	// agent actually sees. Before this, every agent's catalog was answered from
	// the channel's default-workspace loader, which hid each agent's own
	// <workspace>/skills entirely (the bug this feature fixes). skillLoaderFor
	// falls back to that channel loader when no per-agent instance exists, so
	// the answer degrades to the old behaviour instead of failing.
	catalogSkills := []AgentCatalogSkill{}
	workspace := ""
	if loader, _, ok := n.skillLoaderFor(agentID); ok {
		workspace = loader.WorkspaceDir()
		catalogSkills = buildAgentCatalogSkills(loader.ListSkills(), workspace)
	}

	writeJSON(w, http.StatusOK, AgentCatalogResponse{
		AgentID:   agentID,
		Workspace: workspace,
		Tools:     tools,
		Skills:    catalogSkills,
	})
}

// buildAgentCatalogSkills converts a loader's view into catalog entries,
// marking a skill deletable only when it comes from the agent's own workspace
// directory (global and built-in skills are shared with other agents, so they
// must not be removable "through" one). Sorted by name; the frontend renders
// without re-sorting.
func buildAgentCatalogSkills(loaded []skills.SkillInfo, workspace string) []AgentCatalogSkill {
	out := make([]AgentCatalogSkill, 0, len(loaded))
	for _, s := range loaded {
		out = append(out, AgentCatalogSkill{
			Name:        s.Name,
			Description: s.Description,
			Source:      s.Source,
			Enabled:     s.Enabled,
			Deletable:   s.Source == "workspace" && workspace != "",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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

	absWorkspace, err := agentWorkspaceDir(info.Workspace)
	if err != nil {
		return "", err
	}

	// Initialize workspace if needed
	if err := lelectx.InitializeWorkspace(absWorkspace); err != nil {
		return "", fmt.Errorf("failed to initialize workspace: %w", err)
	}

	return absWorkspace, nil
}

// agentWorkspaceDir turns the raw workspace of an agent config into the absolute
// path the agent works in: empty means the shared default, "~" is expanded, and
// the result must stay inside the allowed roots. Split out of
// resolveAgentWorkspace so read-only callers (listing, counting the agents that
// share a workspace) can resolve the path WITHOUT creating the folder.
func agentWorkspaceDir(raw string) (string, error) {
	workspace := raw
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
