package agent

import (
	"github.com/xilistudios/lele/pkg/agent/commands"
)

type CommandInfo = commands.CommandInfo

func WebUICommands() []CommandInfo {
	return commands.WebUICommands()
}

type CustomCommandInfo = commands.CustomCommandInfo

func WithCustom(base []CommandInfo, custom []CustomCommandInfo) []CommandInfo {
	return commands.WithCustom(base, custom)
}
