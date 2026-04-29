package agent

import "github.com/xilistudios/lele/pkg/context"

// ContextFiles are the core context files that should be initialized in every agent workspace.
// Re-exported from pkg/context, the single source of truth.
var ContextFiles = context.ContextFiles

// InitializeWorkspace copies template context files to a new agent's workspace.
// Re-exported from pkg/context, the single source of truth.
func InitializeWorkspace(workspace string) error {
	return context.InitializeWorkspace(workspace)
}
