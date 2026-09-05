// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

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

// harnessRefreshTTL bounds how stale the file-backed command levels may get
// before the next lookup triggers a reload. Config-defined commands react
// immediately (a config reload changes the fingerprint and rebuilds the
// manager); markdown files are polled at this granularity.
const harnessRefreshTTL = 30 * time.Second

// harnessCommandsDir is the project-local command folder, relative to the
// process working directory (the fourth, highest-precedence discovery level).
var harnessCommandsDir = filepath.Join(".lele", "commands")

// harnessManager returns the loop's command manager, (re)building it when the
// parts of the configuration it depends on changed. The manager is lazy on
// purpose: building it touches the filesystem (four load levels), so it should
// not run for every process — but it must follow config hot-reloads, hence the
// fingerprint instead of a sync.Once.
func (al *AgentLoop) harnessManager() *harness.Manager {
	cfg := al.cfg()

	leleDir := config.GetLeleDir()
	workspace := cfg.WorkspacePath()
	dir := harnessCommandsDir
	if wd, err := os.Getwd(); err == nil {
		dir = filepath.Join(wd, harnessCommandsDir)
	}
	defs := harnessCommandDefsFromConfig(cfg.Commands)

	fp := harnessFingerprint(cfg.Harness.AllowShell, defs, workspace, leleDir, dir)

	al.harnessMu.Lock()
	defer al.harnessMu.Unlock()
	if al.harnessMgr == nil || al.harnessCfgFP != fp {
		mgr := harness.NewManager(harness.ManagerConfig{
			LeleDir:           leleDir,
			Workspace:         workspace,
			Dir:               dir,
			Commands:          defs,
			AllowShellDefault: cfg.Harness.AllowShell,
		})
		al.harnessMgr = mgr
		al.harnessCfgFP = fp
	}
	// File-backed levels (global/workspace/.lele commands) are re-scanned at most
	// once per TTL; EnsureFresh is a no-op while the last load is recent.
	al.harnessMgr.EnsureFresh(harnessRefreshTTL)
	return al.harnessMgr
}

// harnessFingerprint builds the change detector for the manager: it covers every
// config input that requires a rebuild (paths, the shell default and a hash of
// the declared commands, so an in-place template edit is picked up too).
// Markdown edits on disk are handled by EnsureFresh instead.
func harnessFingerprint(allowShell bool, defs map[string]harness.CommandDef, workspace, leleDir, dir string) string {
	h := fnv.New64a()
	names := slices.Sorted(maps.Keys(defs))
	for _, name := range names {
		d := defs[name]
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%t\n", name, d.Description, d.Agent, d.Model, d.Template, d.AllowShell)
	}
	return fmt.Sprintf("%t|%x|%s|%s|%s", allowShell, h.Sum64(), workspace, leleDir, dir)
}

// HarnessCommands returns the currently available custom commands (all four
// discovery levels merged, precedence applied), sorted by name. It refreshes
// the file-backed levels when they are older than harnessRefreshTTL.
func (al *AgentLoop) HarnessCommands() []*harness.Command {
	return al.harnessManager().Registry().All()
}

// HarnessCommands delegates to the owning loop.
//
// The channels package receives this object (AgentLoop.GetProvidable), not the
// loop itself, and it discovers custom commands through an OPTIONAL interface
// assertion (channels.customCommandProvider) rather than a method added to
// AgentProvidable — adding one there would break every channel fake and mock.
// Without this delegation the assertion would silently fail in the real binary
// and the WebUI palette would never show harness commands, even though the TUI
// (which holds *AgentLoop directly) does.
func (ap *agentProvidableImpl) HarnessCommands() []*harness.Command {
	return ap.al.HarnessCommands()
}

// harnessCommandDefsFromConfig converts the config.json command map into the
// harness shape. pkg/config does not import pkg/harness (dependency direction),
// so the mapping lives here.
func harnessCommandDefsFromConfig(m map[string]config.CommandDefinition) map[string]harness.CommandDef {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]harness.CommandDef, len(m))
	for name, def := range m {
		out[name] = harness.CommandDef{
			Description: def.Description,
			Agent:       def.Agent,
			Model:       def.Model,
			Template:    def.Template,
			AllowShell:  def.AllowShell,
		}
	}
	return out
}

// applyHarnessCommand expands a user-defined slash command into msg.Content.
// It runs *after* the built-in command dispatcher declined, so built-ins always
// win on name collisions (the harness registry is never consulted for them).
//
// workDir is the directory used for @file references and !`cmd` execution; the
// caller passes the default agent workspace because per-agent routing happens
// later in processMessage. Agents that override the workspace are a rare
// exception and only affect where relative template references resolve.
//
// It returns true when msg.Content was rewritten. On any miss or expansion
// error the message is left untouched and dispatched to the LLM as plain text.
func (mp *messageProcessorImpl) applyHarnessCommand(_ context.Context, msg *bus.InboundMessage, workDir string) bool {
	if msg == nil {
		return false
	}
	// harness_* keys are outputs of expansion, never inputs: a channel (or a
	// crafted REST/WebUI payload) must not be able to switch the agent or model
	// of a turn without a custom command actually matching. The clear runs on
	// every path, including the early returns, because processMessage reads
	// those keys unconditionally right after this call.
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
	mgr := al.harnessManager()
	cmd, ok := mgr.Registry().Get(name)
	if !ok {
		return false
	}

	rawArgs := strings.Join(fields[1:], " ")
	expanded, err := harness.Expand(cmd, rawArgs, harness.ExpandOptions{
		WorkDir:    workDir,
		AllowShell: mgr.AllowShell(cmd),
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
	if cmd.Agent != "" {
		msg.Metadata["harness_agent"] = cmd.Agent
	}
	if cmd.Model != "" {
		msg.Metadata["harness_model"] = cmd.Model
	}

	mp.publishCommandApplied(msg, cmd, rawArgs)
	return true
}

// harnessMetadataKeys are the message-metadata keys the harness owns: they are
// written by applyHarnessCommand and read by processMessage to apply the
// per-turn agent/model overrides.
var harnessMetadataKeys = []string{
	"harness_command",
	"harness_args",
	"harness_source",
	"harness_agent",
	"harness_model",
}

// clearHarnessMetadata removes every harness-owned metadata key from msg.
func clearHarnessMetadata(msg *bus.InboundMessage) {
	if msg.Metadata == nil {
		return
	}
	for _, k := range harnessMetadataKeys {
		delete(msg.Metadata, k)
	}
}

// publishCommandApplied notifies channels that a custom command was applied, so
// UIs can render the command chip without parsing the prompt. ChatID carries the
// session key (fallback to the raw chat id) so clients can match the event to
// the conversation that produced it.
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
