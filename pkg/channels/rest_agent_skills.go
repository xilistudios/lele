// Per-agent skill administration endpoints.
//
// Why this file exists: every skill endpoint used to go through the single
// loader/installer the native channel builds at startup, anchored on
// cfg.WorkspacePath() -- the DEFAULT agent's workspace. That made the WebUI
// wrong in two ways when editing an agent:
//
//  1. The agent's Skills tab listed the default workspace's skills, never the
//     ones in the edited agent's own <workspace>/skills (agents have had
//     per-agent workspaces since resolveAgentWorkspace).
//  2. "Install" always wrote to the default workspace, which every agent
//     without its own workspace shares -- i.e. effectively "global" -- with no
//     way to install into the agent being edited.
//
// The fix: resolve skills state from the LIVE agent instance
// (agentSkillsSource below), which is the same *skills.SkillsLoader that
// renders that agent's <skills> prompt block. Toggling therefore writes the
// workspace.json the agent actually reads, and installing with
// scope=workspace writes into the agent's own skills directory.
//
// Endpoints (all under the agent's own path, so the UI needs no global state):
//
// Reading is GET /api/v1/agents/{agentID}/catalog (rest_agent.go), which now
// resolves skills through the same loader as everything here.
//
//	PUT    /api/v1/agents/{agentID}/skills/{name}/toggle  enable/disable in ITS workspace
//	DELETE /api/v1/agents/{agentID}/skills/{name}         remove from ITS workspace
//	POST   /api/v1/agents/{agentID}/skills/install        install {url, scope}
//	POST   /api/v1/agents/{agentID}/skills/install-batch  install {repo, skills[], scope}
//
// scope is "workspace" (default: the agent's own skills dir) or "global"
// (leleDir/skills, shared by every agent). Deleting is deliberately restricted
// to workspace-scoped skills: removing a global or built-in skill "through one
// agent" would silently affect all the others.

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

// Skill install scopes accepted by the API. workspaceSkillScope installs into
// the agent's own workspace skills dir; globalSkillScope installs into the
// shared <leleDir>/skills dir every agent reads.
const (
	workspaceSkillScope = "workspace"
	globalSkillScope    = "global"
)

// agentSkillsSource is the OPTIONAL capability an agent loop may implement to
// expose per-agent skills state. It is asserted against n.agentLoop and never
// declared on AgentProvidable: adding a method to that interface would break
// every channel fake and mock, while an assertion degrades gracefully -- a loop
// that cannot expose per-agent skills falls back to the channel's own loader,
// which is exactly the pre-existing behaviour.
//
// *agent.agentProvidableImpl satisfies it via AgentSkills; the signature is
// kept structurally identical so the real loop matches without pkg/channels
// importing pkg/agent.
type agentSkillsSource interface {
	AgentSkills(agentID string) (loader *skills.SkillsLoader, invalidate func(), ok bool)
}

// skillLoaderFor returns the loader backing an agent plus the callback that
// drops the agent's cached system prompt (call it after any mutation so the
// next turn re-reads skills from disk). ok=false means no skills view is
// available at all.
//
// Resolution order: live agent instance first (the correct per-agent answer),
// channel default loader second (loop without the capability, or an agent with
// no context builder yet).
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

// toggleVerb names the result of a toggle for the human-readable message.
func toggleVerb(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

// handleAgentSkillToggle serves PUT /api/v1/agents/{agentID}/skills/{name}/toggle.
// It writes the enable/disable state into the AGENT's workspace config -- the
// file that agent's loader actually consults -- then invalidates the agent's
// cached prompt so the next turn reflects it.
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

// handleAgentSkillRemove serves DELETE /api/v1/agents/{agentID}/skills/{name}.
// Only skills installed in the agent's own workspace can be removed here;
// global/built-in skills answer 403 rather than being deleted behind the backs
// of every other agent that shares them.
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

	// Authorise against the agent's real view: the name must exist AND come
	// from its workspace dir. Unknown name -> 404, wrong scope -> 403.
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

// AgentSkillInstallRequest is the body of the per-agent install endpoints.
// Scope defaults to "workspace" (the agent being edited); the previous
// behaviour -- installing into the shared default workspace -- is now explicit
// as scope "global".
type AgentSkillInstallRequest struct {
	URL    string   `json:"url"`    // single-skill install: "owner/repo" or "owner/repo/skill"
	Repo   string   `json:"repo"`   // batch install: scanned repo
	Skills []string `json:"skills"` // batch install: skill paths inside the repo
	Scope  string   `json:"scope"`  // "workspace" (default) | "global"
}

// installTarget resolves where an install with the given scope writes.
// badRequest is non-empty when the caller must answer 400 with that message.
func installTarget(loader *skills.SkillsLoader, scope string) (installer *skills.SkillInstaller, resolved string, badRequest string) {
	switch scope {
	case "", workspaceSkillScope:
		workspace := loader.WorkspaceDir()
		if workspace == "" {
			return nil, "", "agent has no workspace to install into"
		}
		return skills.NewSkillInstaller(workspace), workspaceSkillScope, ""
	case globalSkillScope:
		// The loader's global dir is <leleDir>/skills; the installer appends
		// "skills" itself, so hand it the parent directory.
		globalDir := loader.GlobalSkillsDir()
		if globalDir == "" {
			return nil, "", "global skills directory not configured"
		}
		return skills.NewSkillInstaller(filepath.Dir(globalDir)), globalSkillScope, ""
	default:
		return nil, "", fmt.Sprintf("unknown scope %q: use %q or %q", scope, workspaceSkillScope, globalSkillScope)
	}
}

// handleAgentSkillInstall serves POST /api/v1/agents/{agentID}/skills/install.
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

// handleAgentSkillInstallBatch serves POST /api/v1/agents/{agentID}/skills/install-batch.
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
