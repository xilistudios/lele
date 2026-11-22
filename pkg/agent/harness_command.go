package agent

import (
	"context"
	"fmt"
	"hash/fnv"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/harness"
)

const harnessRefreshTTL = 30 * time.Second

var harnessCommandsDir = filepath.Join(".lele", "commands")

type harnessEntry struct {
	mgr *harness.Manager
	fp  string
}

func (al *AgentLoop) harnessManager() *harness.Manager {
	return al.harnessManagerFor("")
}

func (al *AgentLoop) harnessManagerFor(workspace string) *harness.Manager {
	cfg := al.cfg()

	leleDir := config.GetLeleDir()
	dir := harnessCommandsDir
	if wd, err := os.Getwd(); err == nil {
		dir = filepath.Join(wd, harnessCommandsDir)
	}
	defs := harnessCommandDefsFromConfig(cfg.Commands)

	key := al.harnessWorkspaceKey(workspace)

	fp := harnessFingerprint(cfg.Harness.AllowShell, cfg.Harness.AllowAbsoluteFiles, defs, key, leleDir, dir)

	al.harnessMu.Lock()
	if al.harnessMgrs == nil {
		al.harnessMgrs = make(map[string]*harnessEntry)
	}
	entry := al.harnessMgrs[key]
	if entry == nil || entry.fp != fp {
		entry = &harnessEntry{
			fp: fp,
			mgr: harness.NewManager(harness.ManagerConfig{
				LeleDir:                   leleDir,
				Workspace:                 key,
				Dir:                       dir,
				Commands:                  defs,
				AllowShellDefault:         cfg.Harness.AllowShell,
				AllowAbsoluteFilesDefault: cfg.Harness.AllowAbsoluteFiles,
			}),
		}
		al.harnessMgrs[key] = entry
	}
	mgr := entry.mgr
	al.harnessMu.Unlock()

	mgr.EnsureFresh(harnessRefreshTTL)
	return mgr
}

func harnessFingerprint(allowShell, allowAbs bool, defs map[string]harness.CommandDef, workspace, leleDir, dir string) string {
	h := fnv.New64a()
	names := slices.Sorted(maps.Keys(defs))
	for _, name := range names {
		d := defs[name]
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%s\n", name, d.Description, d.Agent, d.Model, d.Template, d.AllowShell, triState(d.AllowAbsoluteFiles))
	}
	return fmt.Sprintf("%t|%t|%x|%s|%s|%s", allowShell, allowAbs, h.Sum64(), workspace, leleDir, dir)
}

func triState(p *bool) string {
	if p == nil {
		return "nil"
	}
	if *p {
		return "true"
	}
	return "false"
}

func (al *AgentLoop) harnessWorkspaceKey(workspace string) string {
	key := workspace
	if key == "" {
		key = al.cfg().WorkspacePath()
	}
	return filepath.Clean(key)
}

func (al *AgentLoop) InvalidateHarnessWorkspace(workspace string) {
	key := al.harnessWorkspaceKey(workspace)
	al.harnessMu.Lock()
	delete(al.harnessMgrs, key)
	al.harnessMu.Unlock()
}

func (al *AgentLoop) InvalidateAllHarnessWorkspaces() {
	al.harnessMu.Lock()
	al.harnessMgrs = nil
	al.harnessMu.Unlock()
}

func (al *AgentLoop) HarnessCommands() []*harness.Command {
	return al.HarnessCommandsFor("")
}

func (al *AgentLoop) HarnessCommandsFor(workspace string) []*harness.Command {
	return al.harnessManagerFor(workspace).Registry().All()
}

func (al *AgentLoop) HarnessManagerFor(workspace string) *harness.Manager {
	return al.harnessManagerFor(workspace)
}

func (ap *agentProvidableImpl) HarnessCommands() []*harness.Command {
	return ap.al.HarnessCommandsFor("")
}

func harnessCommandDefsFromConfig(m map[string]config.CommandDefinition) map[string]harness.CommandDef {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]harness.CommandDef, len(m))
	for name, def := range m {
		out[name] = harness.CommandDef{
			Description:        def.Description,
			Agent:              def.Agent,
			Model:              def.Model,
			Template:           def.Template,
			AllowShell:         def.AllowShell,
			AllowAbsoluteFiles: def.AllowAbsoluteFiles,
		}
	}
	return out
}

func (mp *messageProcessorImpl) applyHarnessCommand(_ context.Context, msg *bus.InboundMessage, workDir string) bool {
	if msg == nil {
		return false
	}
	clearHarnessMetadata(msg)

	content := strings.TrimSpace(msg.Content)
	if !strings.HasPrefix(content, "/") {
		return false
	}
	fields := strings.Fields(content)
	name := strings.ToLower(strings.TrimPrefix(fields[0], "/"))
	if name == "" {
		return false
	}

	al := mp.al
	mgr := al.harnessManagerFor(workDir)
	cmd, ok := mgr.Registry().Get(name)
	if !ok {
		return false
	}

	rawArgs := strings.TrimSpace(content[len(fields[0]):])
	expanded, err := harness.Expand(cmd, rawArgs, harness.ExpandOptions{
		WorkDir:            workDir,
		AllowShell:         mgr.AllowShell(cmd),
		AllowAbsoluteFiles: mgr.AllowAbsoluteFiles(cmd),
	})
	if err != nil {
		slog.Warn("harness: command expansion failed", "command", name, "error", err)
		return false
	}
	if strings.TrimSpace(expanded) == "" {
		slog.Warn("harness: command expanded to empty content", "command", name)
		return false
	}

	msg.Content = expanded
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]string, 6)
	}
	msg.Metadata["harness_command"] = cmd.Name
	msg.Metadata["harness_args"] = rawArgs
	msg.Metadata["harness_source"] = string(cmd.Source)
	msg.Metadata["harness_description"] = cmd.Description
	if cmd.Agent != "" {
		msg.Metadata["harness_agent"] = cmd.Agent
	}
	if cmd.Model != "" {
		msg.Metadata["harness_model"] = cmd.Model
	}

	mp.publishCommandApplied(msg, cmd, rawArgs)
	return true
}

var harnessMetadataKeys = []string{
	"harness_command",
	"harness_args",
	"harness_source",
	"harness_description",
	"harness_agent",
	"harness_model",
}

func clearHarnessMetadata(msg *bus.InboundMessage) {
	if msg.Metadata == nil {
		return
	}
	for _, k := range harnessMetadataKeys {
		delete(msg.Metadata, k)
	}
}

func (mp *messageProcessorImpl) publishCommandApplied(msg *bus.InboundMessage, cmd *harness.Command, rawArgs string) {
	if mp.al == nil || mp.al.bus == nil {
		return
	}
	chatID := msg.ChatID
	if msg.SessionKey != "" {
		chatID = msg.SessionKey
	}
	mp.al.bus.PublishOutbound(bus.OutboundMessage{
		Event:   "command.applied",
		Channel: msg.Channel,
		ChatID:  chatID,
		Metadata: map[string]string{
			"command":     cmd.Name,
			"description": cmd.Description,
			"args":        rawArgs,
			"agent":       cmd.Agent,
			"model":       cmd.Model,
			"source":      string(cmd.Source),
		},
	})
}
