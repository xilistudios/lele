package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/skills"
)

const (
	workspaceSkillScope = "workspace"
	globalSkillScope    = "global"
)

type agentSkillsSource interface {
	AgentSkills(agentID string) (loader *skills.SkillsLoader, invalidate func(), ok bool)
}

func (n *NativeChannel) skillLoaderFor(agentID string) (loader *skills.SkillsLoader, invalidate func(), ok bool) {
	if n.agentLoop != nil {
		if source, supported := n.agentLoop.(agentSkillsSource); supported {
			if agentLoader, agentInvalidate, found := source.AgentSkills(agentID); found && agentLoader != nil {
				return agentLoader, agentInvalidate, true
			}
		}
	}
	if n.skillsLoader == nil {
		return nil, nil, false
	}
	return n.skillsLoader, func() {}, true
}

func toggleVerb(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func (n *NativeChannel) handleAgentSkillToggle(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	skillName := r.PathValue("name")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}
	if skillName == "" {
		writeError(w, http.StatusBadRequest, "skill name is required", "missing_name")
		return
	}

	var req SkillToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}

	if _, ok := n.agentLoop.GetAgentInfo(agentID); !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}
	loader, invalidate, ok := n.skillLoaderFor(agentID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "skills not available", "skills_unavailable")
		return
	}
	configMgr := loader.GetConfigManager()
	if configMgr == nil {
		writeError(w, http.StatusInternalServerError, "workspace config not available", "config_unavailable")
		return
	}

	var err error
	if req.Enabled {
		err = configMgr.SetEnabled(skillName)
	} else {
		err = configMgr.SetDisabled(skillName)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to toggle skill: %v", err), "toggle_failed")
		return
	}
	invalidate()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agent_id": agentID,
		"name":     skillName,
		"enabled":  req.Enabled,
		"message":  fmt.Sprintf("Skill '%s' %s", skillName, toggleVerb(req.Enabled)),
	})
}

func (n *NativeChannel) handleAgentSkillRemove(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	skillName := r.PathValue("name")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}
	if skillName == "" {
		writeError(w, http.StatusBadRequest, "skill name is required", "missing_name")
		return
	}

	if _, ok := n.agentLoop.GetAgentInfo(agentID); !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}
	loader, invalidate, ok := n.skillLoaderFor(agentID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "skills not available", "skills_unavailable")
		return
	}

	var found *skills.SkillInfo
	loaded := loader.ListSkills()
	for i := range loaded {
		if loaded[i].Name == skillName {
			found = &loaded[i]
			break
		}
	}
	if found == nil {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("skill '%s' is not installed for agent '%s'", skillName, agentID), "not_found")
		return
	}
	if found.Source != "workspace" {
		writeError(w, http.StatusForbidden,
			fmt.Sprintf("skill '%s' is %s-scoped and shared with other agents; remove it from the Skills page", skillName, found.Source),
			"not_workspace_skill")
		return
	}

	installer := skills.NewSkillInstaller(loader.WorkspaceDir())
	if err := installer.Uninstall(skillName); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to remove skill: %v", err), "remove_failed")
		return
	}
	invalidate()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": fmt.Sprintf("Skill '%s' removed from agent '%s'", skillName, agentID),
	})
}

type AgentSkillInstallRequest struct {
	URL    string   `json:"url"`
	Repo   string   `json:"repo"`
	Skills []string `json:"skills"`
	Scope  string   `json:"scope"`
}

func installTarget(loader *skills.SkillsLoader, scope string) (installer *skills.SkillInstaller, resolved string, badRequest string) {
	switch scope {
	case "", workspaceSkillScope:
		workspace := loader.WorkspaceDir()
		if workspace == "" {
			return nil, "", "agent has no workspace to install into"
		}
		return skills.NewSkillInstaller(workspace), workspaceSkillScope, ""
	case globalSkillScope:
		globalDir := loader.GlobalSkillsDir()
		if globalDir == "" {
			return nil, "", "global skills directory not configured"
		}
		return skills.NewSkillInstaller(filepath.Dir(globalDir)), globalSkillScope, ""
	default:
		return nil, "", fmt.Sprintf("unknown scope %q: use %q or %q", scope, workspaceSkillScope, globalSkillScope)
	}
}

func (n *NativeChannel) handleAgentSkillInstall(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	var req AgentSkillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}
	if strings.TrimSpace(req.URL) == "" {
		writeError(w, http.StatusBadRequest, "url is required", "missing_url")
		return
	}

	if _, ok := n.agentLoop.GetAgentInfo(agentID); !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}
	loader, invalidate, ok := n.skillLoaderFor(agentID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "skills not available", "skills_unavailable")
		return
	}
	installer, scope, badRequest := installTarget(loader, req.Scope)
	if badRequest != "" {
		writeError(w, http.StatusBadRequest, badRequest, "invalid_scope")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := installer.InstallFromGitHub(ctx, req.URL); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to install skill: %v", err), "install_failed")
		return
	}
	invalidate()

	skillName := filepath.Base(req.URL)
	writeJSON(w, http.StatusCreated, SkillInstallResponse{
		SkillID: skillName,
		Message: fmt.Sprintf("Skill '%s' installed (%s)", skillName, scope),
	})
}

func (n *NativeChannel) handleAgentSkillInstallBatch(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}

	var req AgentSkillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeError(w, http.StatusBadRequest, "repo is required", "missing_repo")
		return
	}
	if len(req.Skills) == 0 {
		writeError(w, http.StatusBadRequest, "skills list is required", "missing_skills")
		return
	}

	if _, ok := n.agentLoop.GetAgentInfo(agentID); !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return
	}
	loader, invalidate, ok := n.skillLoaderFor(agentID)
	if !ok {
		writeError(w, http.StatusInternalServerError, "skills not available", "skills_unavailable")
		return
	}
	installer, scope, badRequest := installTarget(loader, req.Scope)
	if badRequest != "" {
		writeError(w, http.StatusBadRequest, badRequest, "invalid_scope")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	installed, err := installer.InstallMultiple(ctx, req.Repo, req.Skills)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to install skills: %v", err), "install_failed")
		return
	}
	invalidate()

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"agent_id":  agentID,
		"scope":     scope,
		"installed": installed,
		"count":     len(installed),
		"message":   fmt.Sprintf("Installed %d skills (%s)", len(installed), scope),
	})
}
