package channels

import (
	"net/http"
	"strings"

	agentcommands "github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/harness"
)

type ChatCommandsResponse struct {
	Commands []agentcommands.CommandInfo `json:"commands"`
}

type customCommandProvider interface {
	HarnessCommands() []*harness.Command
}

func (n *NativeChannel) handleChatCommands(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed", "method_invalid")
		return
	}

	writeJSON(w, http.StatusOK, ChatCommandsResponse{
		Commands: n.chatCommands(r.URL.Query().Get("agent_id")),
	})
}

func (n *NativeChannel) chatCommands(agentID string) []agentcommands.CommandInfo {
	base := agentcommands.WebUICommands()

	if n.agentLoop == nil {
		return base
	}

	custom := n.harnessCommandsFor(agentID)
	if len(custom) == 0 {
		return base
	}
	return agentcommands.WithCustom(base, harnessCommandsAsCustom(custom))
}

func (n *NativeChannel) harnessCommandsFor(agentID string) []*harness.Command {
	provider, ok := n.agentLoop.(customCommandProvider)
	if !ok || provider == nil {
		return nil
	}
	if agentID == "" {
		return provider.HarnessCommands()
	}
	info, found := n.agentLoop.GetAgentInfo(agentID)
	if !found {
		return provider.HarnessCommands()
	}
	workspace, err := agentWorkspaceDir(info.Workspace)
	if err != nil {
		return provider.HarnessCommands()
	}
	mgr := agentCommandsManager(n.agentCommandsConfig(), workspace)
	return mgr.Registry().All()
}

func harnessCommandsAsCustom(cmds []*harness.Command) []agentcommands.CustomCommandInfo {
	out := make([]agentcommands.CustomCommandInfo, 0, len(cmds))
	for _, c := range cmds {
		if c == nil || c.Name == "" {
			continue
		}
		name := "/" + strings.ToLower(strings.TrimLeft(c.Name, "/"))
		out = append(out, agentcommands.CustomCommandInfo{
			Name:        name,
			Description: c.Description,
			Usage:       name + " [args]",
			Source:      string(c.Source),
		})
	}
	return out
}
