package commands

import (
	"sort"
	"strings"
)

type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
	Source      string `json:"source,omitempty"`
}

var webUICommands = []CommandInfo{
	{
		Name:        "/clear",
		Description: "Clear the conversation history for this session.",
		Usage:       "/clear",
	},
	{
		Name:        "/compact",
		Description: "Summarize and compact the conversation history (needs 5+ messages).",
		Usage:       "/compact",
	},
	{
		Name:        "/goal",
		Description: "Set a persistent goal the agent works toward autonomously, or check/pause/resume/clear it (/goal status|pause|resume|clear).",
		Usage:       "/goal <text>",
	},
}

func WebUICommands() []CommandInfo {
	out := make([]CommandInfo, len(webUICommands))
	copy(out, webUICommands)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type CustomCommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
	Source      string `json:"source"`
}

func WithCustom(base []CommandInfo, custom []CustomCommandInfo) []CommandInfo {
	out := make([]CommandInfo, 0, len(base)+len(custom))
	seen := make(map[string]struct{}, len(base)+len(custom))

	for _, c := range base {
		out = append(out, c)
		seen[normalizeCommandName(c.Name)] = struct{}{}
	}

	for _, c := range custom {
		key := normalizeCommandName(c.Name)
		if key == "" || key == "/" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, CommandInfo{
			Name:        key,
			Description: c.Description,
			Usage:       c.Usage,
			Source:      c.Source,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func normalizeCommandName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimLeft(n, "/")
	if n == "" {
		return ""
	}
	return "/" + n
}

func DispatcherReserved() []string {
	out := make([]string, 0, len(dispatcherReserved)+len(channelIntercepted))
	out = append(out, dispatcherReserved...)
	out = append(out, channelIntercepted...)
	sort.Strings(out)
	return out
}

var dispatcherReserved = []string{
	"agent", "clear", "compact", "goal", "group", "list", "model", "new",
	"show", "status", "stop", "subagents", "switch", "think", "toggle", "verbose",
}

var channelIntercepted = []string{"help", "models", "start"}
