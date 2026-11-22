package agent

import (
	"sort"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/routing"
)

func AgentWorkspacePath(cfg *config.Config, agentID string) (string, bool) {
	id := routing.NormalizeAgentID(agentID)
	if len(cfg.Agents.List) == 0 {
		if id != "main" {
			return "", false
		}
		implicit := &config.AgentConfig{ID: "main", Default: true}
		return resolveAgentWorkspace(implicit, &cfg.Agents.Defaults), true
	}
	workspace, found := "", false
	for i := range cfg.Agents.List {
		ac := &cfg.Agents.List[i]
		if routing.NormalizeAgentID(ac.ID) != id {
			continue
		}
		workspace, found = resolveAgentWorkspace(ac, &cfg.Agents.Defaults), true
	}
	return workspace, found
}

func AgentIDs(cfg *config.Config) []string {
	if len(cfg.Agents.List) == 0 {
		return []string{"main"}
	}
	seen := make(map[string]bool, len(cfg.Agents.List))
	ids := make([]string, 0, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		id := routing.NormalizeAgentID(cfg.Agents.List[i].ID)
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
