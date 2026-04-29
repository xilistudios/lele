package agent

import "github.com/xilistudios/lele/pkg/contextfiles"

// ContextFiles are the core context files that should be initialized in every agent workspace.
// Re-exported from pkg/contextfiles, the single source of truth.
var ContextFiles = contextfiles.ContextFiles

// InitializeWorkspace copies template context files to a new agent's workspace.
// Re-exported from pkg/contextfiles, the single source of truth.
func InitializeWorkspace(workspace string) error {
	return contextfiles.InitializeWorkspace(workspace)
}
