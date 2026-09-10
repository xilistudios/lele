// Per-agent slash-command administration endpoints.
//
// Why this file exists: a custom command is a markdown file discovered at one of
// four levels (config.json < global < workspace < directory, see
// pkg/harness/manager.go). Before the agent pages, the only HTTP view of them
// was GET /api/v1/chat/commands, which flattens everything into one list for the
// palette: it answers "what can I type?" but not "which file defines this, which
// one hides it, and may I edit it?". An agent's Commands tab needs the latter,
// and needs to write the agent's OWN workspace — never the default one, which is
// the bug class the skills endpoints just fixed (see rest_agent_skills.go).
//
// Endpoints:
//
//	GET    /api/v1/agents/{agentID}/commands          merged catalog + built-ins
//	GET    /api/v1/agents/{agentID}/commands/{name}   raw markdown (403 if read-only)
//	POST   /api/v1/agents/{agentID}/commands          create            -> 201
//	PUT    /api/v1/agents/{agentID}/commands/{name}   update            -> 200
//	DELETE /api/v1/agents/{agentID}/commands/{name}   delete            -> 200
//
// Scope semantics (same shape as the skills API):
//   - "workspace" — <agent workspace>/commands. The level that decides where
//     @file references and !`cmd` execute, and the only one create may target.
//   - "global" — ~/.lele/commands, shared by every agent. Update and delete
//     accept it explicitly; create rejects it, because a command that suddenly
//     applies to all agents could not be explained to the user afterwards.
//
// config- and directory-source commands are always read-only: their source of
// truth is config.json or a checkout-local folder, not this API. The decision is
// made server-side and reported per row (`deletable`, and the 403 above) so the
// UI never has to recompute precedence.
//
// Writes go straight to disk and then drop the runtime cache of the affected
// command managers (see invalidateHarnessCommands): each agent loop keeps one
// manager per workspace behind a refresh TTL, and a command the user just
// created must be typeable on the very next message, not after the TTL expires.
// A global-scope write invalidates every cached workspace, since that level is
// merged into all of them.

package channels

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	agentcommands "github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/harness"
)

// agentCommandNamePattern is the command-name grammar, byte-identical to
// COMMAND_NAME_RE in web/src/lib/commandMarkdown.ts: the UI validates with it
// before sending, so a mismatch here would mean rejected saves the form cannot
// explain. Lowercase only because the harness lowercases the file stem anyway
// (pkg/harness ParseCommandMarkdown), and the first character must be a letter
// or digit so "." / ".." / "-x" can never be a name.
var agentCommandNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// maxAgentCommandNameLen bounds the name; it becomes a file name, and every
// filesystem has one.
const maxAgentCommandNameLen = 64

// agentCommandReservedExts are stems that would double up once ".md" is
// appended ("review.md" -> "review.md.md", which the loader reports as the
// command "review.md"). Rejected instead of silently normalised.
var agentCommandReservedExts = []string{".md", ".markdown"}

// agentCommandScopes are the file levels an explicit write may target.
var agentCommandScopes = []string{string(harness.SourceWorkspace), string(harness.SourceGlobal)}

// agentCommandsSubdir is the folder inside a workspace (and inside ~/.lele) that
// holds command markdown files. Mirrors harness.commandsSubdir, which is private.
const agentCommandsSubdir = "commands"

// maxAgentCommandSize caps the markdown a write may store. Commands are prompt
// templates, not documents; the bound keeps a stray paste from bloating every
// prompt that expands one.
const maxAgentCommandSize = 256 * 1024

// AgentCommandInfo is one row of the merged catalog. A name defined at several
// levels appears once per level: the winner carries shadowed_by == "" and the
// losers carry the source that hides them, so the UI can say "this file exists
// but the agent never runs it".
type AgentCommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Path        string `json:"path"`
	Agent       string `json:"agent"`
	Model       string `json:"model"`
	// AllowShell is the OR-merged result the harness applies (a command cannot
	// opt out of the global default), so it is a plain bool, never tri-state.
	AllowShell bool `json:"allow_shell"`
	// AllowAbsoluteFiles is tri-state: nil/absent means "inherit
	// harness.allow_absolute_files", an explicit value overrides it.
	AllowAbsoluteFiles *bool `json:"allow_absolute_files"`
	// Deletable is server-decided: true only for a workspace/global file this
	// agent may remove. The UI must not re-derive it.
	Deletable bool `json:"deletable"`
	// ShadowedBy is the source of the command that wins precedence over this
	// row; "" means this row IS the effective command.
	ShadowedBy string `json:"shadowed_by"`
}

// BuiltinCommandInfo is one built-in gateway command (help, new, model, ...).
// They are listed so the page can explain why a custom name is rejected.
type BuiltinCommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
}

// AgentCommandsHarnessConfig echoes the global permission defaults, so the
// editor can show what "inherit" resolves to without guessing. They are global
// by design: there is no per-agent harness field (plan D7/L2).
type AgentCommandsHarnessConfig struct {
	AllowShell         bool `json:"allow_shell"`
	AllowAbsoluteFiles bool `json:"allow_absolute_files"`
}

// AgentCommandsResponse is the payload of the list endpoint.
type AgentCommandsResponse struct {
	AgentID   string `json:"agent_id"`
	Workspace string `json:"workspace"`
	// CommandsDir is the workspace-level folder creates go to, whether or not
	// it exists yet.
	CommandsDir string `json:"commands_dir"`
	// CommandsDirExists distinguishes "no commands yet" from "wrong folder".
	CommandsDirExists bool `json:"commands_dir_exists"`
	// SharedBy counts the agents whose workspace is this one. > 1 means a write
	// here changes the commands of several agents.
	SharedBy int                        `json:"shared_by"`
	Harness  AgentCommandsHarnessConfig `json:"harness"`
	Commands []AgentCommandInfo         `json:"commands"`
	Builtin  []BuiltinCommandInfo       `json:"builtin"`
}

// AgentCommandDetail is the raw markdown of one file-backed command.
type AgentCommandDetail struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	Path      string `json:"path"`
	Source    string `json:"source"`
	Deletable bool   `json:"deletable"`
}

// AgentCommandWriteRequest is the POST body.
type AgentCommandWriteRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Scope   string `json:"scope,omitempty"`
}

// AgentCommandUpdateRequest is the PUT body.
type AgentCommandUpdateRequest struct {
	Content string `json:"content"`
}

// AgentCommandMutationResponse is the 200/201 payload of create/update.
type AgentCommandMutationResponse struct {
	OK      bool              `json:"ok"`
	Command *AgentCommandInfo `json:"command,omitempty"`
}

// AgentCommandDeleteResponse is the 200 payload of delete.
type AgentCommandDeleteResponse struct {
	OK bool `json:"ok"`
}

// --- resolution helpers ----------------------------------------------------

// agentCommandsConfig returns the live configuration snapshot. The snapshot is
// the authority (config hot-reload replaces it), so a config-defined command
// appears in the list as soon as the config is saved. Falls back to an empty
// config when no agent loop is wired: the file levels still answer, and the
// caller gets an honest "no config commands" instead of a 500.
func (n *NativeChannel) agentCommandsConfig() *config.Config {
	if n.agentLoop != nil {
		if cfg := n.agentLoop.GetConfigSnapshot(); cfg != nil {
			return cfg
		}
	}
	return &config.Config{}
}

// agentCommandScopeDir returns the directory backing a scope. ok is false for
// scopes without a folder of their own ("config", "directory").
func agentCommandScopeDir(scope, workspace string) (string, bool) {
	switch scope {
	case string(harness.SourceWorkspace):
		return filepath.Join(workspace, agentCommandsSubdir), true
	case string(harness.SourceGlobal):
		return filepath.Join(config.GetLeleDir(), agentCommandsSubdir), true
	default:
		return "", false
	}
}

// agentCommandName validates a command name coming from a path segment or a
// request body and returns it trimmed. The name doubles as the file stem, so it
// must be one safe path segment: the grammar excludes separators entirely.
func agentCommandName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	switch {
	case name == "":
		return "", errors.New("command name is required")
	case len(name) > maxAgentCommandNameLen:
		return "", fmt.Errorf("command name too long (max %d characters)", maxAgentCommandNameLen)
	case !agentCommandNamePattern.MatchString(name):
		return "", errors.New("invalid command name: must start with a lowercase letter or digit and contain only lowercase letters, digits, dots, hyphens and underscores")
	}
	for _, ext := range agentCommandReservedExts {
		if strings.HasSuffix(name, ext) {
			return "", fmt.Errorf("command name must not end in %s", ext)
		}
	}
	return name, nil
}

// agentCommandScope validates the requested write scope. An empty scope means
// "workspace", the agent-private level.
func agentCommandScope(raw string) (string, error) {
	scope := strings.TrimSpace(raw)
	if scope == "" {
		return string(harness.SourceWorkspace), nil
	}
	if !slices.Contains(agentCommandScopes, scope) {
		return "", fmt.Errorf("invalid scope %q (must be workspace or global)", scope)
	}
	return scope, nil
}

// agentCommandsProjectDir is the ".lele/commands" directory level, resolved
// against the process working directory — the same rule pkg/agent applies when
// it builds the manager. Included in the listing because it really does affect
// what the agent runs; it is read-only here because it belongs to a checkout,
// not to this agent.
func agentCommandsProjectDir() string {
	dir := filepath.Join(".lele", agentCommandsSubdir)
	if wd, err := os.Getwd(); err == nil {
		dir = filepath.Join(wd, dir)
	}
	return dir
}

// agentCommandDefs converts the config.json command map into the harness shape.
// pkg/config does not import pkg/harness (dependency direction), so the mapping
// lives in the caller; it mirrors agent.harnessCommandDefsFromConfig exactly.
func agentCommandDefs(m map[string]config.CommandDefinition) map[string]harness.CommandDef {
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

// agentCommandsManager builds the harness manager for one agent workspace.
//
// It is constructed on demand instead of being taken from the AgentLoop: the
// loop caches one manager per workspace behind a 30 s refresh TTL, and a page
// that just wrote a file must show it immediately. The cost is four directory
// scans per request, which is the same cost the agent pays on its own reload.
func agentCommandsManager(cfg *config.Config, workspace string) *harness.Manager {
	return harness.NewManager(harness.ManagerConfig{
		LeleDir:                   config.GetLeleDir(),
		Workspace:                 workspace,
		Dir:                       agentCommandsProjectDir(),
		Commands:                  agentCommandDefs(cfg.Commands),
		AllowShellDefault:         cfg.Harness.AllowShell,
		AllowAbsoluteFilesDefault: cfg.Harness.AllowAbsoluteFiles,
	})
}

// agentCommandRows flattens the four discovery levels into catalog rows: every
// command of every level, each tagged with the source that shadows it ("" for
// the effective winner). Precedence comes from the manager, never from a local
// reimplementation.
func agentCommandRows(mgr *harness.Manager) []AgentCommandInfo {
	winner := map[string]harness.Source{}
	for _, cmd := range mgr.Registry().All() {
		if cmd != nil {
			winner[cmd.Name] = cmd.Source
		}
	}

	levels, _ := mgr.Levels() // per-level errors are already logged by the manager
	rows := make([]AgentCommandInfo, 0)
	for _, source := range []harness.Source{
		harness.SourceConfig,
		harness.SourceGlobal,
		harness.SourceWorkspace,
		harness.SourceDirectory,
	} {
		for _, cmd := range levels[source] {
			if cmd == nil {
				continue
			}
			row := AgentCommandInfo{
				Name:               cmd.Name,
				Description:        cmd.Description,
				Source:             string(cmd.Source),
				Path:               cmd.Path,
				Agent:              cmd.Agent,
				Model:              cmd.Model,
				AllowShell:         mgr.AllowShell(cmd),
				AllowAbsoluteFiles: cmd.AllowAbsoluteFiles,
				Deletable:          agentCommandScopeDeletable(cmd.Source),
			}
			if w, ok := winner[cmd.Name]; ok && w != cmd.Source {
				row.ShadowedBy = string(w)
			}
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Source < rows[j].Source
	})
	return rows
}

// agentCommandScopeDeletable reports whether a level is a file this API may
// remove: the agent's workspace and the shared global folder. config entries
// live in config.json and directory entries in a checkout-local folder.
func agentCommandScopeDeletable(source harness.Source) bool {
	return source == harness.SourceWorkspace || source == harness.SourceGlobal
}

// agentCommandState classifies a name against the catalog of one agent, which
// is what lets the handlers answer 403 (defined, but outside the writable
// levels) instead of a misleading 404.
type agentCommandState int

const (
	agentCommandMissing agentCommandState = iota
	agentCommandWritable
	agentCommandBlocked
)

// agentCommandLookup is the answer of resolveAgentCommand: the effective row of
// a name, where its file lives, and whether this API may touch it.
type agentCommandLookup struct {
	row   AgentCommandInfo
	state agentCommandState
}

// resolveAgentCommand classifies one name from the catalog rows of an agent.
//
// It reads the catalog instead of guessing "<name>.md" inside a level on
// purpose: the loader accepts .markdown too and lowercases the stem, so only
// the catalog knows the real path of the command the agent would run. And the
// decision is taken on the EFFECTIVE row (shadowed_by == "") — when a
// checkout-local or config command wins precedence, editing the agent's own
// file would change nothing, and saying so is more useful than writing a dead
// file.
func resolveAgentCommand(rows []AgentCommandInfo, name string) agentCommandLookup {
	for _, row := range rows {
		if row.Name != name || row.ShadowedBy != "" {
			continue
		}
		if row.Deletable && row.Path != "" {
			return agentCommandLookup{row: row, state: agentCommandWritable}
		}
		return agentCommandLookup{row: row, state: agentCommandBlocked}
	}
	return agentCommandLookup{state: agentCommandMissing}
}

// agentCommandWritePath returns the file a create would write for a scope.
func agentCommandWritePath(scope, name, workspace string) (string, error) {
	dir, ok := agentCommandScopeDir(scope, workspace)
	if !ok {
		return "", fmt.Errorf("invalid scope %q", scope)
	}
	return filepath.Join(dir, name+".md"), nil
}

// agentCommandBlockedReason explains a non-writable name for an error message:
// either the read-only level that defines it, or the level that shadows it.
func agentCommandBlockedReason(row AgentCommandInfo) string {
	return fmt.Sprintf("%s is defined at the %q level, which this API does not write to",
		row.Name, row.Source)
}

// reservedCommandNames are the names a custom command may never take: the
// dispatcher (or a channel, for /help and friends) always answers them first,
// so writing the file would create a command that silently never runs. The list
// is the registry's, not a local copy — pkg/agent keeps it synced to the real
// switch with a source-level test (TestDispatcherReserved_MatchesSource).
func reservedCommandNames() map[string]bool {
	names := map[string]bool{}
	for _, name := range agentcommands.DispatcherReserved() {
		names[name] = true
	}
	for _, c := range agentcommands.WebUICommands() {
		names[strings.ToLower(strings.TrimLeft(c.Name, "/"))] = true
	}
	return names
}

// --- handlers --------------------------------------------------------------

// handleAgentCommands serves GET /api/v1/agents/{agentID}/commands.
//
// The response is the merged catalog (all four levels, shadowing tagged), the
// workspace folder creates go to, how many agents share that workspace, the
// global harness defaults and the built-in commands. It answers 404 for an
// unknown agent and 403 for a workspace outside the allowed roots, like every
// other /agents/{id} endpoint.
func (n *NativeChannel) handleAgentCommands(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return
	}
	workspace, ok := n.agentCommandWorkspace(w, agentID)
	if !ok {
		return
	}

	cfg := n.agentCommandsConfig()
	mgr := agentCommandsManager(cfg, workspace)

	commandsDir := filepath.Join(workspace, agentCommandsSubdir)
	_, dirErr := os.Stat(commandsDir)

	writeJSON(w, http.StatusOK, AgentCommandsResponse{
		AgentID:           agentID,
		Workspace:         workspace,
		CommandsDir:       commandsDir,
		CommandsDirExists: dirErr == nil,
		SharedBy:          n.agentsSharingWorkspace(workspace),
		Harness: AgentCommandsHarnessConfig{
			AllowShell:         cfg.Harness.AllowShell,
			AllowAbsoluteFiles: cfg.Harness.AllowAbsoluteFiles,
		},
		Commands: agentCommandRows(mgr),
		Builtin:  builtinCommands(),
	})
}

// agentsSharingWorkspace counts the configured agents whose resolved workspace
// is the given directory. Writes to a shared workspace affect all of them, so
// the UI warns when the count is above one. An unresolvable agent is skipped
// rather than failing the listing: the number is informational.
func (n *NativeChannel) agentsSharingWorkspace(workspace string) int {
	shared := 0
	for _, id := range n.agentLoop.ListAvailableAgentIDs() {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if !ok {
			continue
		}
		// Deliberately not resolveAgentWorkspace: counting must not create the
		// workspace of every agent it looks at, only read the configured path.
		other, err := agentWorkspaceDir(info.Workspace)
		if err != nil {
			continue
		}
		if other == workspace {
			shared++
		}
	}
	// An agent that exists but is not listed (ListAvailableAgentIDs is runtime
	// state) must still count as one, otherwise the field reads "0" for the very
	// workspace the request is about.
	if shared == 0 {
		return 1
	}
	return shared
}

// builtinCommands maps the dispatched built-ins onto this API's wire shape.
// The registry stores slashed names ("/clear"); the UI prefixes the slash
// itself, so it is trimmed here to match the custom rows.
//
// This is the PALETTE list (commands a user may type, with a description and a
// usage line), deliberately narrower than the set of names a create may not
// take: reservedCommandNames() also covers everything the dispatcher or a
// channel answers, whether or not anyone documented it. A rejected name is
// explained by its own error message, so the two lists do not have to agree.
func builtinCommands() []BuiltinCommandInfo {
	base := agentcommands.WebUICommands()
	out := make([]BuiltinCommandInfo, 0, len(base))
	for _, c := range base {
		out = append(out, BuiltinCommandInfo{
			Name:        strings.TrimLeft(c.Name, "/"),
			Description: c.Description,
			Usage:       c.Usage,
		})
	}
	return out
}

// handleAgentCommandDetail serves GET /api/v1/agents/{agentID}/commands/{name}.
//
// It returns the raw markdown of a workspace/global file. A name that exists at
// a read-only level answers 403 not_editable (the UI shows a read-only dialog
// instead of an empty form), and an unknown name answers 404.
func (n *NativeChannel) handleAgentCommandDetail(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	workspace, ok := n.agentCommandRequestContext(w, r, agentID)
	if !ok {
		return
	}
	row, ok := n.agentCommandWritableTarget(w, r, workspace, r.PathValue("name"), "not_editable")
	if !ok {
		return
	}

	raw, err := os.ReadFile(row.Path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read command file", "read_failed")
		return
	}

	writeJSON(w, http.StatusOK, AgentCommandDetail{
		Name:      row.Name,
		Content:   string(raw),
		Path:      row.Path,
		Source:    row.Source,
		Deletable: row.Deletable,
	})
}

// agentCommandWritableTarget resolves the {name} of a request to the catalog row
// of the command this API may actually write, and answers the request itself when
// it cannot: 400 on a malformed name, 403 not_editable when the effective
// command lives at a read-only level (or the agent's own file is shadowed, where
// writing would change nothing), 404 when nothing defines the name.
func (n *NativeChannel) agentCommandWritableTarget(w http.ResponseWriter, r *http.Request, workspace, rawName, blockedCode string) (AgentCommandInfo, bool) {
	name, err := agentCommandName(rawName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_name")
		return AgentCommandInfo{}, false
	}

	lookup := resolveAgentCommand(n.agentCommandCatalogRows(workspace), name)
	switch lookup.state {
	case agentCommandWritable:
		return lookup.row, true
	case agentCommandBlocked:
		// Two codes for one condition, because the UI branches on them
		// differently: a read-only row can still be OPENED (GET/PUT report
		// not_editable and the dialog shows it in view mode), while DELETE has
		// no read-only meaning to fall back to and answers not_deletable.
		writeError(w, http.StatusForbidden,
			"command is not managed here: "+agentCommandBlockedReason(lookup.row),
			blockedCode)
	default:
		writeError(w, http.StatusNotFound, fmt.Sprintf("command not found: %s", name), "command_not_found")
	}
	return AgentCommandInfo{}, false
}

// agentCommandRequestContext validates the agent of a command request and
// returns its workspace. It writes the error response and reports false when
// the agent id is missing, unknown, or its workspace cannot be resolved.
func (n *NativeChannel) agentCommandRequestContext(w http.ResponseWriter, r *http.Request, agentID string) (string, bool) {
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return "", false
	}
	return n.agentCommandWorkspace(w, agentID)
}

// agentCommandWorkspace resolves an agent id to its absolute workspace and
// answers the request when it cannot. The two failures are distinguished on
// purpose: an unknown agent is the client's mistake (404), while a workspace
// pointing outside the allowed roots is a policy refusal (403) — the same split
// handleAgentFiles makes, so every /agents/{id} endpoint answers alike.
func (n *NativeChannel) agentCommandWorkspace(w http.ResponseWriter, agentID string) (string, bool) {
	workspace, err := n.resolveAgentWorkspace(agentID)
	if err != nil {
		if strings.Contains(err.Error(), "outside allowed") {
			writeError(w, http.StatusForbidden, err.Error(), "workspace_forbidden")
		} else {
			writeError(w, http.StatusNotFound, err.Error(), "agent_not_found")
		}
		return "", false
	}
	return workspace, true
}

// handleAgentCommandCreate serves POST /api/v1/agents/{agentID}/commands.
//
// The body carries the FULL markdown (the UI serializes the front matter), so
// the server validates it with the very parser the loader uses: a 201 response
// can never mean "a file the agent will refuse to load".
func (n *NativeChannel) handleAgentCommandCreate(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	workspace, ok := n.agentCommandRequestContext(w, r, agentID)
	if !ok {
		return
	}

	var req AgentCommandWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}
	name, err := agentCommandName(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_name")
		return
	}
	scope, err := agentCommandScope(req.Scope)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_scope")
		return
	}
	// Creating in the shared global level would silently add a command to every
	// agent; there is no global-commands UI, so it is managed from the
	// filesystem. Updates and deletes stay
	// allowed there because the caller already sees the file.
	if scope != string(harness.SourceWorkspace) {
		writeError(w, http.StatusBadRequest,
			"new commands must be created in the workspace scope; the shared ~/.lele/commands level is managed from the filesystem",
			"invalid_scope")
		return
	}

	path, err := agentCommandWritePath(scope, name, workspace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_scope")
		return
	}
	if builtin := agentCommandBuiltinCollision(name); builtin != "" {
		writeError(w, http.StatusConflict, builtin, "reserved_name")
		return
	}
	if msg, code := validateAgentCommandContent(name, req.Content); msg != "" {
		writeError(w, commandWriteStatus(code), msg, code)
		return
	}

	// "already exists" is answered from the catalog, not from os.Stat(path):
	// the loader discovers .markdown as well as .md, so a stat on the single
	// name we are about to write would miss review.markdown and drop a second
	// file on the same command, where last-read wins and the user cannot tell
	// which body ran.
	if existing, taken := findAgentCommandRow(n.agentCommandCatalogRows(workspace), name, scope); taken {
		writeError(w, http.StatusConflict,
			fmt.Sprintf("command already exists: %s (%s)", name, existing.Path), "exists")
		return
	}

	if err := writeAgentCommandFile(path, req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "write_failed")
		return
	}
	n.invalidateHarnessCommands(scope, workspace)

	// Re-read after the write: the row the client gets back must carry what the
	// disk says now (merged flags, shadowing), not what the pre-write catalog
	// said. Two scans per create is fine — this is an admin action, not a hot
	// path, and the alternative is deriving the row by hand and drifting from
	// the loader.
	row, _ := findAgentCommandRow(n.agentCommandCatalogRows(workspace), name, scope)
	writeJSON(w, http.StatusCreated, AgentCommandMutationResponse{OK: true, Command: row})
}

// handleAgentCommandUpdate serves PUT /api/v1/agents/{agentID}/commands/{name}.
// It replaces the content of the file that wins precedence for the name, which
// is exactly the file the detail endpoint handed back.
func (n *NativeChannel) handleAgentCommandUpdate(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	workspace, ok := n.agentCommandRequestContext(w, r, agentID)
	if !ok {
		return
	}
	row, ok := n.agentCommandWritableTarget(w, r, workspace, r.PathValue("name"), "not_editable")
	if !ok {
		return
	}

	var req AgentCommandUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "invalid_request")
		return
	}
	if msg, code := validateAgentCommandContent(row.Name, req.Content); msg != "" {
		writeError(w, commandWriteStatus(code), msg, code)
		return
	}
	if err := writeAgentCommandFile(row.Path, req.Content); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "write_failed")
		return
	}
	n.invalidateHarnessCommands(row.Source, workspace)

	// Re-read the catalog: the row carries the merged flags the file now has, so
	// the client can update its cache from the response instead of refetching.
	updated, _ := findAgentCommandRow(n.agentCommandCatalogRows(workspace), row.Name, row.Source)
	writeJSON(w, http.StatusOK, AgentCommandMutationResponse{OK: true, Command: updated})
}

// handleAgentCommandDelete serves DELETE /api/v1/agents/{agentID}/commands/{name}.
// It removes the winning file only; a command shadowing it (if any) reappears
// on the next list, which is the correct outcome of deleting a level.
func (n *NativeChannel) handleAgentCommandDelete(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("agentID")
	workspace, ok := n.agentCommandRequestContext(w, r, agentID)
	if !ok {
		return
	}
	row, ok := n.agentCommandWritableTarget(w, r, workspace, r.PathValue("name"), "not_deletable")
	if !ok {
		return
	}

	if err := os.Remove(row.Path); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete command file: %v", err), "delete_failed")
		return
	}
	n.invalidateHarnessCommands(row.Source, workspace)
	writeJSON(w, http.StatusOK, AgentCommandDeleteResponse{OK: true})
}

// --- write helpers ---------------------------------------------------------

// agentCommandCatalogRows lists the catalog of an agent workspace. It exists
// because three handlers need the same read-only answer (is this name defined
// at a level we cannot touch?) and building the manager is the only way to ask.
func (n *NativeChannel) agentCommandCatalogRows(workspace string) []AgentCommandInfo {
	return agentCommandRows(agentCommandsManager(n.agentCommandsConfig(), workspace))
}

// agentCommandBuiltinCollision returns a message when the name would be hidden
// by a built-in the backend answers first, "" when it is free.
func agentCommandBuiltinCollision(name string) string {
	if reservedCommandNames()[strings.ToLower(name)] {
		return fmt.Sprintf("/%s is already answered by the backend and always wins; choose another name", name)
	}
	return ""
}

// validateAgentCommandContent checks the markdown before it reaches the disk.
// It runs the same parser the loader uses, so a stored file can never be one
// the agent would silently skip, and it enforces the size bound that
// applyBodyLimit cannot express per field. Returns ("", "") when the content is
// good; otherwise a human-readable message and a stable error code.
func validateAgentCommandContent(name, content string) (string, string) {
	if len(content) > maxAgentCommandSize {
		return fmt.Sprintf("command content too large (max %d KB)", maxAgentCommandSize/1024), "too_large"
	}
	// The parser lowercases the stem itself, so it agrees with the loader on a
	// name typed as "Review".
	cmd, err := harness.ParseCommandMarkdown(name, name+".md", []byte(content))
	if err != nil {
		return strings.TrimPrefix(err.Error(), "harness: "), "command_invalid"
	}
	// Two rules the parser does not enforce because the loader tolerates them,
	// but that make a stored command useless: an empty template expands to
	// nothing (and the config level skips such entries outright), and an empty
	// description leaves the palette and the catalog rows blank.
	if strings.TrimSpace(cmd.Template) == "" {
		return "command template is empty: add the prompt body after the front matter", "command_invalid"
	}
	if strings.TrimSpace(cmd.Description) == "" {
		return `command description is empty: add description: "..." to the front matter`, "command_invalid"
	}
	return "", ""
}

// commandWriteStatus maps an error code onto the HTTP status the UI branches on.
func commandWriteStatus(code string) int {
	if code == "too_large" {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// writeAgentCommandFile stores the markdown, creating the level's folder on
// first use. The write is atomic (temp file + rename) because the agent polls
// these directories: a half-written file would be parsed and skipped.
func writeAgentCommandFile(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create commands directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".command-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeded

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write command file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	// CreateTemp is 0600; command files are plain prompts read by the gateway
	// process, and 0644 keeps them editable by hand from a shell.
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	return nil
}

// findAgentCommandRow returns the catalog row of a name at a given source, so a
// mutation can answer with the row the list will show instead of a guess.
func findAgentCommandRow(rows []AgentCommandInfo, name, source string) (*AgentCommandInfo, bool) {
	for i := range rows {
		if rows[i].Name == name && rows[i].Source == source {
			row := rows[i]
			return &row, true
		}
	}
	return nil, false
}

// --- runtime cache invalidation ---------------------------------------------

// harnessCommandInvalidator is the OPTIONAL capability an agent loop exposes to
// drop its cached command manager for one workspace. It is asserted against
// n.agentLoop and never declared on AgentProvidable: adding a method there would
// break every channel fake and mock, while an assertion degrades gracefully (a
// loop without the hook simply reloads on its own TTL, the pre-existing
// behaviour). The signature matches *agent.AgentLoop structurally so
// pkg/channels does not have to import pkg/agent for it.
type harnessCommandInvalidator interface {
	InvalidateHarnessWorkspace(workspace string)
}

// invalidateHarnessCommands tells the runtime to rebuild the command managers of
// the given workspaces on next access ("" = the defaults workspace).
//
// Why it is needed: each agent loop caches one harness.Manager per workspace and
// re-scans the file-backed levels at most once per harnessRefreshTTL, so a file
// this API just wrote would stay invisible to the dispatcher for up to 30
// seconds — the user could create a command here and be told it does not exist
// when they type it in chat.
//
// A write to the shared global level is merged into EVERY workspace, so it
// invalidates all of them; a workspace write only affects the agents that resolve
// to that directory. Missing or duplicated workspaces are handled here, which
// keeps the callers one line long.
func (n *NativeChannel) invalidateHarnessCommands(scope, workspace string) {
	if n.agentLoop == nil {
		return
	}
	invalidator, ok := n.agentLoop.(harnessCommandInvalidator)
	if !ok {
		return
	}
	for _, ws := range n.workspacesAffectedBy(scope, workspace) {
		invalidator.InvalidateHarnessWorkspace(ws)
	}
}

// workspacesAffectedBy returns the workspaces whose command view a write at
// `scope` changed: the agent's own for a workspace write, the defaults plus every
// configured agent's for a global one. The agent's workspace is always included,
// even for global writes, because that agent reads the global level too.
func (n *NativeChannel) workspacesAffectedBy(scope, workspace string) []string {
	if scope != string(harness.SourceGlobal) {
		return []string{workspace}
	}

	// "" selects the defaults workspace inside the loop's cache key, so the
	// listing has to use the same sentinel instead of resolving it here.
	out := []string{"", workspace}
	for _, id := range n.agentLoop.ListAvailableAgentIDs() {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if !ok || info.Workspace == "" {
			continue // "" == the defaults workspace, already queued
		}
		ws, err := agentWorkspaceDir(info.Workspace)
		if err != nil {
			continue // unresolvable path: nothing cached under it
		}
		out = append(out, ws)
	}
	return dedupeAgentCommandPaths(out)
}

// dedupeAgentCommandPaths removes repeated workspaces while keeping order, so a
// pair of agents sharing one directory invalidates its cache once.
func dedupeAgentCommandPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
