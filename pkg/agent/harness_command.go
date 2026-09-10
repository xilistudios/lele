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

// harnessEntry is one cached command manager plus the config fingerprint it was
// built from. Entries live in AgentLoop.harnessMgrs keyed by the absolute,
// cleaned workspace they were built for, so every agent sees the commands of
// ITS workspace: the workspace level of ManagerConfig is the only discovery
// level that differs between agents, and it is also the level that decides
// where @file references and !`cmd` execute.
type harnessEntry struct {
	mgr *harness.Manager
	fp  string // fingerprint of this entry (harnessFingerprint fed with the entry's workspace)
}

// harnessManager returns the command manager of the agents.defaults workspace.
// It is the ""-workspace shorthand of harnessManagerFor and the source behind
// HarnessCommands(); callers that know which agent will handle the message must
// use harnessManagerFor with that agent's workspace instead.
func (al *AgentLoop) harnessManager() *harness.Manager {
	return al.harnessManagerFor("")
}

// harnessManagerFor returns the command manager for one workspace, (re)building
// it when the parts of the configuration it depends on changed. workspace == ""
// selects the agents.defaults workspace (cfg.WorkspacePath), which keeps the
// historical behaviour for callers without an agent context.
//
// Managers are lazy on purpose: building one touches the filesystem (four load
// levels), so it should not run for every process — but they must follow config
// hot-reloads, hence the per-entry fingerprint instead of a sync.Once. Because
// the fingerprint is recomputed on every access, a global config change (the
// harness permission defaults or the config.json command map) invalidates ALL
// entries lazily: each one is rebuilt the next time it is accessed, and an
// entry nobody touches is never rebuilt at all.
//
// The map is bounded by the number of distinct workspaces in the config (one
// per agent, plus the defaults), so entries are never evicted.
func (al *AgentLoop) harnessManagerFor(workspace string) *harness.Manager {
	cfg := al.cfg()

	leleDir := config.GetLeleDir()
	dir := harnessCommandsDir
	if wd, err := os.Getwd(); err == nil {
		dir = filepath.Join(wd, harnessCommandsDir)
	}
	defs := harnessCommandDefsFromConfig(cfg.Commands)

	// The map key is the workspace the manager is built for, normalised so the
	// same directory reached by two spellings ("/x/", "/x") shares one entry.
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

	// File-backed levels (global/workspace/.lele commands) are re-scanned at
	// most once per TTL; EnsureFresh is a no-op while the last load is recent.
	// It runs outside harnessMu on purpose: Manager is internally synchronised,
	// so a slow disk rescan for one workspace must not block access to another.
	mgr.EnsureFresh(harnessRefreshTTL)
	return mgr
}

// harnessFingerprint builds the change detector for the manager: it covers every
// config input that requires a rebuild (paths, the permission defaults and a
// hash of the declared commands, so an in-place template or flag edit is
// picked up too). Markdown edits on disk are handled by EnsureFresh instead.
func harnessFingerprint(allowShell, allowAbs bool, defs map[string]harness.CommandDef, workspace, leleDir, dir string) string {
	h := fnv.New64a()
	names := slices.Sorted(maps.Keys(defs))
	for _, name := range names {
		d := defs[name]
		// triState() renders the *bool by VALUE: %v on a *bool prints the
		// pointer address, which changes every time the config is re-parsed
		// and would trigger spurious rebuilds.
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%t\x00%s\n", name, d.Description, d.Agent, d.Model, d.Template, d.AllowShell, triState(d.AllowAbsoluteFiles))
	}
	return fmt.Sprintf("%t|%t|%x|%s|%s|%s", allowShell, allowAbs, h.Sum64(), workspace, leleDir, dir)
}

// triState renders an optional bool as "nil", "true" or "false" so hashes
// depend on the value, not on where the pointer lives.
func triState(p *bool) string {
	if p == nil {
		return "nil"
	}
	if *p {
		return "true"
	}
	return "false"
}

// harnessWorkspaceKey maps a caller-supplied workspace to the key its manager is
// cached under: "" selects the agents.defaults workspace and every path is
// cleaned, so two spellings of the same directory share one entry. Callers that
// must reach the same cache entry from outside harnessManagerFor (invalidation)
// have to go through here rather than re-implementing the rule.
func (al *AgentLoop) harnessWorkspaceKey(workspace string) string {
	key := workspace
	if key == "" {
		key = al.cfg().WorkspacePath()
	}
	return filepath.Clean(key)
}

// InvalidateHarnessWorkspace forces the cached manager of one workspace to be
// rebuilt on next access, bypassing both the config fingerprint and the file
// rescan TTL. It is the hook for writers that change <workspace>/commands/*.md
// behind the manager's back (the REST command endpoints): without it a client
// could POST a command and still get the stale registry for up to
// harnessRefreshTTL. An unknown workspace is a no-op — nothing was cached, so
// nothing can be stale.
func (al *AgentLoop) InvalidateHarnessWorkspace(workspace string) {
	key := al.harnessWorkspaceKey(workspace)
	al.harnessMu.Lock()
	delete(al.harnessMgrs, key)
	al.harnessMu.Unlock()
}

// HarnessCommands returns the custom commands of the agents.defaults workspace
// (all four discovery levels merged, precedence applied), sorted by name. It
// refreshes the file-backed levels when they are older than
// harnessRefreshTTL.
//
// The signature is load-bearing: pkg/channels (customCommandProvider) and
// pkg/tui (harnessCommandSource) discover this method through structural
// interface assertions, so renaming it or changing its shape would fail
// silently — the palette would just stop showing custom commands. Per-agent
// lookup goes through HarnessCommandsFor instead.
func (al *AgentLoop) HarnessCommands() []*harness.Command {
	return al.HarnessCommandsFor("")
}

// HarnessCommandsFor returns the custom commands visible to an agent whose
// resolved workspace is `workspace` ("" = the defaults workspace, identical to
// HarnessCommands()). Each workspace gets its own cached manager, so the
// <workspace>/commands level — and with it the agent's own commands — is never
// confused with another agent's.
func (al *AgentLoop) HarnessCommandsFor(workspace string) []*harness.Command {
	return al.harnessManagerFor(workspace).Registry().All()
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
//
// The "" workspace is deliberate: this is the palette of the whole gateway, not
// of one agent's turn. Per-agent command lookup stays inside pkg/agent, where
// the routed agent's workspace is known.
func (ap *agentProvidableImpl) HarnessCommands() []*harness.Command {
	return ap.al.HarnessCommandsFor("")
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

// applyHarnessCommand expands a user-defined slash command into msg.Content.
// It runs *after* the built-in command dispatcher declined, so built-ins always
// win on name collisions (the harness registry is never consulted for them).
//
// workDir is the resolved workspace of the agent that will handle this message
// (routing and the session's agent override are computed before this call in
// processMessage). It selects BOTH the command manager — so the
// <workDir>/commands discovery level, and therefore the commands an agent can
// actually invoke, are its own and never another agent's — and the directory
// @file references and !`cmd` execute in.
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
	mgr := al.harnessManagerFor(workDir)
	cmd, ok := mgr.Registry().Get(name)
	if !ok {
		return false
	}

	rawArgs := strings.Join(fields[1:], " ")
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
