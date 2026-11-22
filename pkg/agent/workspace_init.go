package agent

import "github.com/xilistudios/lele/pkg/context"

var ContextFiles = context.ContextFiles

func InitializeWorkspace(workspace string) error {
	return context.InitializeWorkspace(workspace)
}
