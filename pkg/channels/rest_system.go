package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/config"
)

func (n *NativeChannel) handleTools(w http.ResponseWriter, r *http.Request) {
	sessionKey := getQueryParam(r, "session_key")
	if sessionKey == "" {
		sessionKey = getClientID(r)
	}

	supportsImages := n.agentLoop.GetSessionModelSupportsImages(sessionKey)

	tools := []ToolInfo{
		{Name: "read_file", Description: "Read file from workspace", Enabled: true},
		{Name: "write_file", Description: "Write file to workspace", Enabled: true},
		{Name: "list_dir", Description: "List directory contents", Enabled: true},
		{Name: "exec", Description: "Execute shell commands", Enabled: true},
		{Name: "web_search", Description: "Search the web", Enabled: true},
		{Name: "web_fetch", Description: "Fetch web content", Enabled: true},
		{Name: "spawn", Description: "Create subagent", Enabled: true},
	}

	if supportsImages {
		tools = append(tools, ToolInfo{Name: "read_image", Description: "Read and analyze images", Enabled: true})
	}

	writeJSON(w, http.StatusOK, ToolsResponse{Tools: tools})
}

func (n *NativeChannel) handleModels(w http.ResponseWriter, r *http.Request) {
	agentID := getQueryParam(r, "agent_id")
	if agentID == "" {
		agentID = n.agentLoop.GetSessionAgent(getClientID(r))
	}

	models := n.listAllModels()
	modelGroups := n.buildModelGroups(agentID, models)
	model := ""
	if sessionKey := getQueryParam(r, "session_key"); sessionKey != "" {
		model = n.agentLoop.GetSessionModel(sessionKey)
	}

	writeJSON(w, http.StatusOK, ModelsResponse{
		AgentID:     agentID,
		Model:       model,
		Models:      models,
		ModelGroups: modelGroups,
	})
}

func (n *NativeChannel) handleSkills(w http.ResponseWriter, r *http.Request) {
	skills := n.skillsLoader.ListSkills()

	skillInfos := make([]SkillInfo, 0, len(skills))
	for _, s := range skills {
		skillInfos = append(skillInfos, SkillInfo{
			ID:          s.Name,
			Name:        s.Name,
			Description: s.Description,
			Installed:   true,
			Enabled:     s.Enabled,
			Source:      s.Source,
		})
	}

	writeJSON(w, http.StatusOK, SkillsResponse{Skills: skillInfos})
}

func (n *NativeChannel) handleSkillInstall(w http.ResponseWriter, r *http.Request) {
	var req SkillInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}

	if req.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required", "missing_url")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := n.skillInstaller.InstallFromGitHub(ctx, req.URL); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to install skill: %v", err), "install_failed")
		return
	}

	skillName := filepath.Base(req.URL)
	writeJSON(w, http.StatusCreated, SkillInstallResponse{
		SkillID: skillName,
		Message: fmt.Sprintf("Skill '%s' installed successfully", skillName),
	})
}

func (n *NativeChannel) handleSkillsAvailable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	available, err := n.skillInstaller.ListAvailableSkills(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to fetch available skills: %v", err), "fetch_failed")
		return
	}

	type AvailableSkillInfo struct {
		Name        string   `json:"name"`
		Repository  string   `json:"repository"`
		Description string   `json:"description"`
		Author      string   `json:"author"`
		Tags        []string `json:"tags"`
	}

	result := make([]AvailableSkillInfo, 0, len(available))
	for _, s := range available {
		result = append(result, AvailableSkillInfo{
			Name:        s.Name,
			Repository:  s.Repository,
			Description: s.Description,
			Author:      s.Author,
			Tags:        s.Tags,
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"skills": result,
	})
}

func (n *NativeChannel) handleSkillRemove(w http.ResponseWriter, r *http.Request) {
	skillName := r.PathValue("name")
	if skillName == "" {
		writeError(w, http.StatusBadRequest, "skill name is required", "missing_name")
		return
	}

	if err := n.skillInstaller.Uninstall(skillName); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to remove skill: %v", err), "remove_failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message": fmt.Sprintf("Skill '%s' removed successfully", skillName),
	})
}

// handleSkillScan scans a GitHub repo for skills.
func (n *NativeChannel) handleSkillScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Repo string `json:"repo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}

	if req.Repo == "" {
		writeError(w, http.StatusBadRequest, "repo is required", "missing_repo")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	scanned, err := n.skillInstaller.ScanGitHubRepo(ctx, req.Repo)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to scan repo: %v", err), "scan_failed")
		return
	}

	// Convert skills.ScannedSkill to channels.ScannedSkill
	result := make([]ScannedSkill, 0, len(scanned))
	for _, s := range scanned {
		result = append(result, ScannedSkill{
			Name:        s.Name,
			Description: s.Description,
			Path:        s.Path,
			HasSKILL:    s.HasSKILL,
		})
	}

	writeJSON(w, http.StatusOK, ScanSkillsResponse{
		Skills: result,
		Repo:   req.Repo,
	})
}

// handleSkillInstallBatch installs multiple skills from a scanned repo.
func (n *NativeChannel) handleSkillInstallBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Repo   string   `json:"repo"`
		Skills []string `json:"skills"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}

	if req.Repo == "" {
		writeError(w, http.StatusBadRequest, "repo is required", "missing_repo")
		return
	}

	if len(req.Skills) == 0 {
		writeError(w, http.StatusBadRequest, "skills list is required", "missing_skills")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	installed, err := n.skillInstaller.InstallMultiple(ctx, req.Repo, req.Skills)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to install skills: %v", err), "install_failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"installed": installed,
		"count":     len(installed),
		"message":   fmt.Sprintf("Installed %d skills successfully", len(installed)),
	})
}

// handleSkillToggle enables or disables a skill in workspace config.
func (n *NativeChannel) handleSkillToggle(w http.ResponseWriter, r *http.Request) {
	skillName := r.PathValue("name")
	if skillName == "" {
		writeError(w, http.StatusBadRequest, "skill name is required", "missing_name")
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}

	// Get workspace config manager from skills loader
	configMgr := n.skillsLoader.GetConfigManager()
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

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":    skillName,
		"enabled": req.Enabled,
		"message": fmt.Sprintf("Skill '%s' %s", skillName, map[bool]string{true: "enabled", false: "disabled"}[req.Enabled]),
	})
}

// handleSkillWorkspaceConfig returns the workspace skills config.
func (n *NativeChannel) handleSkillWorkspaceConfig(w http.ResponseWriter, r *http.Request) {
	configMgr := n.skillsLoader.GetConfigManager()
	if configMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"skills": map[string]interface{}{
				"enabled":  []string{},
				"disabled": []string{},
			},
		})
		return
	}

	cfg := configMgr.GetConfig()
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"skills": cfg,
	})
}

func (n *NativeChannel) handleStatus(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(n.startTime).String()

	agents := make([]map[string]interface{}, 0)
	for _, id := range n.agentLoop.ListAvailableAgentIDs() {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if ok {
			agents = append(agents, map[string]interface{}{
				"id":     info.ID,
				"name":   info.Name,
				"status": "running",
			})
		}
	}

	channels := make([]map[string]interface{}, 0)
	channels = append(channels, map[string]interface{}{
		"name":    "native",
		"enabled": true,
		"running": n.running,
	})

	writeJSON(w, http.StatusOK, SystemStatusResponse{
		Status:   "running",
		Uptime:   uptime,
		Agents:   agents,
		Channels: channels,
		Version:  "1.0.0",
	})
}

func (n *NativeChannel) handleChannels(w http.ResponseWriter, r *http.Request) {
	channels := []ChannelInfo{
		{Name: "native", Enabled: true, Running: n.running},
	}

	writeJSON(w, http.StatusOK, ChannelsResponse{Channels: channels})
}

func (n *NativeChannel) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	providerName, err := url.PathUnescape(r.PathValue("name"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid provider name", "name_invalid")
		return
	}
	if providerName == "" {
		writeError(w, http.StatusBadRequest, "provider name required", "name_missing")
		return
	}

	cfg := n.cfgSnapshot()
	named, ok := cfg.Providers.GetNamed(providerName)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("provider %q not found", providerName), "provider_not_found")
		return
	}

	apiKey := named.APIKey
	apiBase := strings.TrimRight(named.APIBase, "/")
	providerType := named.Type
	if providerType == "" {
		providerType = providerName
	}

	if apiBase == "" {
		apiBase = defaultAPIBaseByTypePublic(providerType)
	}
	if apiBase == "" {
		writeError(w, http.StatusBadRequest, "provider has no api_base configured", "no_api_base")
		return
	}

	if apiKey == "" {
		writeError(w, http.StatusBadRequest, "provider has no api_key configured", "no_api_key")
		return
	}

	// SSRF guard: validate the provider URL before making any outbound request.
	// Block requests to private/internal IPs and enforce HTTPS.
	if !isAllowedProviderURL(apiBase) {
		writeError(w, http.StatusBadRequest, "provider api_base is not allowed: must be a public HTTPS URL", "url_not_allowed")
		return
	}

	modelsURL := apiBase + "/models"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, modelsURL, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create request", "request_error")
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to fetch models: %v", err), "upstream_error")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		writeError(w, resp.StatusCode, fmt.Sprintf("upstream returned %d: %s", resp.StatusCode, string(body)), "upstream_error")
		return
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read response", "read_error")
		return
	}

	var modelsResp struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &modelsResp); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to parse models response", "parse_error")
		return
	}

	models := make([]ProviderModelInfo, 0, len(modelsResp.Data))
	for _, m := range modelsResp.Data {
		models = append(models, ProviderModelInfo{
			ID:      m.ID,
			Object:  m.Object,
			Created: m.Created,
			OwnedBy: m.OwnedBy,
		})
	}

	writeJSON(w, http.StatusOK, ProviderModelsResponse{
		Provider: providerName,
		Models:   models,
	})
}

// isAllowedProviderURL validates that a provider API base URL is safe to connect to.
// It blocks SSRF attacks by rejecting private/internal IPs and non-HTTPS schemes.
func isAllowedProviderURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}

	// Only allow HTTPS to prevent man-in-the-middle and credential leakage.
	if u.Scheme != "https" {
		return false
	}

	host := u.Hostname()
	// Block hostnames that resolve to localhost.
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return false
	}

	// Block private, loopback, and link-local IP addresses.
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return false
	}

	return true
}

func parsePagination(r *http.Request) (offset, limit int) {
	offsetStr := getQueryParam(r, "offset")
	limitStr := getQueryParam(r, "limit")

	offset, _ = strconv.Atoi(offsetStr)
	if offset < 0 {
		offset = 0
	}

	limit, _ = strconv.Atoi(limitStr)
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	return offset, limit
}

func (n *NativeChannel) buildModelGroups(_ string, _ []string) []ModelGroup {
	cfg := n.cfgSnapshot()
	if cfg == nil {
		return nil
	}

	providers := cfg.Providers.ListNamed()
	providerNames := make([]string, 0, len(providers))
	for name := range providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	groups := make([]ModelGroup, 0, len(providerNames))
	for _, providerName := range providerNames {
		provider := providers[providerName]
		aliases := make([]string, 0, len(provider.Models))
		for alias := range provider.Models {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		if len(aliases) == 0 {
			continue
		}

		group := ModelGroup{
			Provider: providerName,
			Models:   make([]ModelOption, 0, len(aliases)),
		}
		for _, alias := range aliases {
			modelCfg := provider.Models[alias]
			resolved := strings.TrimSpace(modelCfg.Model)
			var value string
			if resolved != "" {
				value = providerName + ":" + resolved
			} else {
				value = providerName + ":" + alias
			}
			group.Models = append(group.Models, ModelOption{
				Value:     value,
				Label:     alias,
				Reasoning: modelCfg.Reasoning,
			})
		}
		groups = append(groups, group)
	}

	if len(groups) == 0 {
		return nil
	}
	return groups
}

func (n *NativeChannel) listAllModels() []string {
	cfg := n.cfgSnapshot()
	if cfg == nil {
		return nil
	}

	providers := cfg.Providers.ListNamed()
	providerNames := make([]string, 0, len(providers))
	for name := range providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)

	models := make([]string, 0)
	seen := make(map[string]bool)
	for _, providerName := range providerNames {
		provider := providers[providerName]
		aliases := make([]string, 0, len(provider.Models))
		for alias := range provider.Models {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		for _, alias := range aliases {
			key := providerName + ":" + alias
			if seen[key] {
				continue
			}
			models = append(models, key)
			seen[key] = true
		}
	}

	return models
}

func (n *NativeChannel) cfgSnapshot() *config.Config {
	if n.agentLoop != nil {
		if cfg := n.agentLoop.GetConfigSnapshot(); cfg != nil {
			return cfg
		}
	}

	cfg := config.DefaultConfig()
	if n.cfg != nil {
		cfg.Channels.Native = *n.cfg
	}
	return cfg
}

func defaultAPIBaseByTypePublic(providerType string) string {
	switch providerType {
	case "groq":
		return "https://api.groq.com/openai/v1"
	case "openai", "gpt":
		return "https://api.openai.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "nanogpt":
		return "https://nano-gpt.com/api/v1"
	case "chutes":
		return "https://llm.chutes.ai/v1"
	case "alibaba", "alibaba_coding_plan":
		return "https://coding-intl.dashscope.aliyuncs.com/v1"
	case "zhipu":
		return "https://open.bigmodel.cn/api/paas/v4"
	case "gemini", "google":
		return "https://generativelanguage.googleapis.com/v1beta"
	case "shengsuanyun":
		return "https://router.shengsuanyun.com/api/v1"
	case "nvidia":
		return "https://integrate.api.nvidia.com/v1"
	case "moonshot":
		return "https://api.moonshot.cn/v1"
	case "ollama":
		return "http://localhost:11434/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "vllm":
		return ""
	default:
		return ""
	}
}
