# Plan: pkg/harness — Custom slash commands (opencode-style)

Branch: feat/harness-custom-commands. Repo root = this worktree.
Reference: https://opencode.ai/docs/commands/

## Goal
Custom commands defined as markdown files (frontmatter + prompt template) or in
lele config.json, discovered at 4 levels, expanded with args/shell/@file, and
executed by the backend: template replaces the user message sent to the LLM,
optionally overriding agent and model FOR THAT TURN ONLY, and a
`command.applied` event makes WebUI + TUI show a tool-like card.

## Precedence (later wins on name collision)
1. config.json `"commands"` map (root level)
2. global: `<GetLeleDir()>/commands/*.md` (i.e. ~/.lele/commands)
3. workspace: `<WorkspacePath()>/commands/*.md`
4. directory: `./.lele/commands/*.md` (relative to process cwd)

Only `*.md` files, non-recursive. Command name = filename without extension,
lowercased. Built-in backend commands (/clear, /model, ...) ALWAYS win over
custom ones (dispatch switch is authority; harness only handles names the
switch does not claim).

## pkg/harness public API (no imports of pkg/agent, pkg/channels; pkg/config may import it)

```go
type Source string
const (SourceConfig Source = "config"; SourceGlobal = "global"; SourceWorkspace = "workspace"; SourceDirectory = "directory")

type CommandDef struct { // config.json shape
    Description string `json:"description,omitempty"`
    Agent       string `json:"agent,omitempty"`
    Model       string `json:"model,omitempty"`
    Template    string `json:"template"`
    AllowShell  bool   `json:"allow_shell,omitempty"`
}

type Command struct {
    Name string; Description string; Template string
    Agent string; Model string; AllowShell bool
    Source Source; Path string // source file, "" for config-defined
}

func LoadMarkdownFile(path string, source Source) (*Command, error)
func LoadDir(dir string, source Source) ([]*Command, error) // missing dir => nil, nil

type Registry struct{ ... } // thread-safe
func NewRegistry() *Registry
func (r *Registry) Register(c *Command)          // overwrite same name
func (r *Registry) Get(name string) (*Command, bool)
func (r *Registry) All() []*Command              // sorted by name
func (r *Registry) Replace(cmds []*Command)

type Manager struct{ ... }
type ManagerConfig struct {
    LeleDir, Workspace, Dir string          // the 3 dirs ("" disables level)
    Commands map[string]CommandDef          // from config.json
    AllowShellDefault bool                   // config harness-level default, false
}
func NewManager(mc ManagerConfig) *Manager
func (m *Manager) Reload() error             // rebuild registry, all 4 levels
func (m *Manager) EnsureFresh(ttl time.Duration) // reload if stale (mtime check or TTL)
func (m *Manager) Registry() *Registry

type ExpandOptions struct {
    WorkDir string; AllowShell bool
    MaxShellOutput int // default 32<<10
    MaxFileOutput int  // default 64<<10
}
func Expand(cmd *Command, rawArgs string, opts ExpandOptions) (string, error)
```

## Markdown format
```
---
description: Run tests with coverage
agent: coder
model: openai/gpt-5
---
Run the full test suite for $ARGUMENTS...
!`git log --oneline -5`
See @src/main.go
```
Frontmatter regex precedent: pkg/skills/loader.go:384. Parse simple `key: value`
(description, agent, model, allow_shell). No frontmatter => whole file is template.
Body (after frontmatter, trimmed) = Template.

## Expand semantics
- Tokenize rawArgs respecting single/double quotes (hand-rolled shlex-lite).
- `$ARGUMENTS` -> raw args verbatim; `$1..$9` -> positional tokens (missing => replaced with "").
- `` !`cmd` `` -> run cmd via `sh -c` in WorkDir, capture combined output, replace.
  Only when opts.AllowShell (per-command AllowShell OR ManagerConfig.AllowShellDefault).
  Disabled => placeholder replaced with "[shell disabled]" AND Expand returns the text (no error).
  Output trimmed, capped MaxShellOutput (truncate + "...[truncated]").
- `@relative/path` or `@/abs/path` -> file content inline. Resolve against WorkDir;
  refuse escaping WorkDir for relative paths (filepath.Clean + prefix check);
  cap MaxFileOutput. Missing file => "[missing: @path]" placeholder, no error.
- Order: @file and !`cmd` substitution first, then $ARGUMENTS/$N (so args can't smuggle shell).

## Backend integration (pkg/agent)
- AgentLoop owns `harnessMgr *harness.Manager`, built in NewAgentLoop from cfg
  (LeleDir=config.GetLeleDir(), Workspace=cfg.WorkspacePath(), Dir="./.lele/commands",
  Commands=cfg.Commands mapped, AllowShellDefault from cfg). Expose
  `func (al *AgentLoop) HarnessCommands() []*harness.Command` (after EnsureFresh).
- messageProcessor.processMessage: after handleCommand returns handled=false, call new
  `applyHarnessCommand(ctx, msg *bus.InboundMessage) *harness.Command` (new file
  pkg/agent/harness_command.go):
  1. first token of trimmed content starts with "/", look up registry (strip "/").
  2. no match => nil.
  3. Expand template (WorkDir = agent workspace dir; AllowShell from command/manager).
  4. msg.Content = expanded text; set msg.Metadata["harness_command"]=name,
     ["harness_agent"], ["harness_model"], ["harness_args"], ["harness_source"].
  5. Publish bus event: PublishOutbound(OutboundMessage{Event:"command.applied",
     Channel: msg.Channel, ChatID: msg.SessionKey (fallback ChatID), Metadata: {...}}).
- processMessage routing: after agent resolution, if metadata harness_agent set and
  registry.GetAgent ok => override agent for this turn ONLY (do NOT persist session agent).
- Model: add `ModelOverride string` to processOptions (pkg/agent/loop.go:335).
  processMessage sets it from metadata harness_model (resolve alias via
  cfg.Providers.ResolveModelAlias). llm_runner.go: model selection at ~line 512 and
  vision sync at ~162 must prefer opts.ModelOverride over ModelForSession. Turn-only,
  never stored into sessionModels/Sessions.
- pkg/agent/commands/command_registry.go: add
  `func WithCustom(base []CommandInfo, custom []CustomCommandInfo) []CommandInfo` where
  CustomCommandInfo{Name,Description,Usage} — merges, dedupes (built-in wins), sorted.
  Keep static list + guard test untouched.

## WS payload (pkg/channels)
types.go: `WSCommandAppliedPayload{SessionKey, Command, Description, Args, Agent, Model, Source string}`
(json: session_key, command, description, args, agent, model, source).
native.go Send(): case "command.applied" => emitNativeEvent(sessionKey, "command.applied", payload).
rest_commands.go: response = builtins + custom via agent.Loop().HarnessCommands()
(AgentProvidable already exposes loop; check existing accessor). CommandInfo gains
`Source string json:"source,omitempty"` ("builtin"|"config"|"global"|"workspace"|"directory")
and Usage "/name [args]" for custom.

## WebUI (web/src)
- lib/types.ts: add 'command.applied' WS event union + payload type.
- hooks/event-handlers/index.ts: 'command.applied' -> handleCommandApplied (new or in tools.ts):
  push ChatMessage role 'tool' with tool:'command', toolStatus 'completed',
  arguments {command, args, agent, model, source}.
- ToolCallDisplay.tsx: TOOL_ICONS entry for 'command' (⚡ bolt), label "Command applied";
  args summary shows `/name args` + badges for agent/model when present.
- SlashCommandMenu/commandPalette: already driven by /api/v1/chat/commands; ensure custom
  entries render (description + usage). Selection inserts "/name " (with trailing space for args).

## TUI (pkg/tui)
- types.go allCommands stays builtins. helpers.go filterAutocomplete: additionally append
  custom commands from m.agentLoop.HarnessCommands() (name "/"+Name, description Description)
  when agentLoop != nil. Dedup by name (builtins win).
- commands.go executeCommand default branch: if first token matches a custom harness command
  => publishUserMessage(raw input) and return nil (let backend expand). Unknown => keep current nil.
- handlers.go event switch (~1814): case "command.applied" => append assistant-style system
  card message: "⚡ /name applied" + " agent: X · model: Y" when set (reuse tool call rendering
  pattern used for tool.executing).

## Config (pkg/config)
- config.go Config struct: add `Commands map[string]harness.CommandDef json:"commands,omitempty"`
  and `Harness HarnessConfig json:"harness,omitempty"` where HarnessConfig{AllowShell bool json:"allow_shell,omitempty"}.
- document_types.go EditableDocument + document_convert.go: mirror Commands + Harness both ways
  (unknown keys are NOT preserved on settings-save otherwise). Check document_parse_* pattern.
- DefaultConfig untouched (empty map).

## Coder tasks (atomic, ordered)
- T1 pkg/harness core: types.go, loader.go, registry.go, expand.go + *_test.go (table tests; t.TempDir fixtures). NO other package touched.
- T2 pkg/harness manager.go + manager_test.go (precedence across levels incl. config defs, EnsureFresh TTL) + pkg/config integration (Config fields, editable doc round-trip test).
- T3 pkg/agent integration: harness_command.go, processMessage hooks, processOptions.ModelOverride, llm_runner prefer override, AgentLoop manager + HarnessCommands(), commands.WithCustom + tests.
- T4 pkg/channels: WS type + native.go case + rest_commands merge + tests.
- T5 WebUI: types, event handler, ToolCallDisplay, palette tweaks + unit tests (vitest patterns from existing *.test.tsx).
- T6 TUI: autocomplete merge, pass-through execution, event render + go tests where testable.
- T7 verify: go build ./... , go vet ./..., go test ./pkg/harness/... ./pkg/agent/... ./pkg/channels/... ./pkg/tui/... ./pkg/config/..., cd web && npm run build (+ npm test if fast). Manual: create ./.lele/commands/hola.md, run harness against real binary if feasible.

## Acceptance (user-facing)
- /hola shows in WebUI palette with description; sending "/hola mundo" expands $ARGUMENTS,
  emits command.applied; WebUI shows ⚡ card; agent/model switch honored for that turn only
  (session model unchanged afterwards); TUI autocompletes and executes it the same way.
- No regression: built-in /clear etc. unaffected; unknown /foo still falls through to LLM.
