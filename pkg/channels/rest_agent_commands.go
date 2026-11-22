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

var agentCommandNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

const maxAgentCommandNameLen = 64

var agentCommandReservedExts = []string{".md", ".markdown"}

var agentCommandScopes = []string{string(harness.SourceWorkspace), string(harness.SourceGlobal)}

const agentCommandsSubdir = "commands"

const maxAgentCommandSize = 256 * 1024

type AgentCommandInfo struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	Source             string `json:"source"`
	Path               string `json:"path"`
	Agent              string `json:"agent"`
	Model              string `json:"model"`
	AllowShell         bool   `json:"allow_shell"`
	AllowAbsoluteFiles *bool  `json:"allow_absolute_files"`
	Deletable          bool   `json:"deletable"`
	ShadowedBy         string `json:"shadowed_by"`
}

type BuiltinCommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Usage       string `json:"usage"`
}

type AgentCommandsHarnessConfig struct {
	AllowShell         bool `json:"allow_shell"`
	AllowAbsoluteFiles bool `json:"allow_absolute_files"`
}

type AgentCommandsResponse struct {
	AgentID           string                     `json:"agent_id"`
	Workspace         string                     `json:"workspace"`
	CommandsDir       string                     `json:"commands_dir"`
	CommandsDirExists bool                       `json:"commands_dir_exists"`
	SharedBy          int                        `json:"shared_by"`
	Harness           AgentCommandsHarnessConfig `json:"harness"`
	Commands          []AgentCommandInfo         `json:"commands"`
	Builtin           []BuiltinCommandInfo       `json:"builtin"`
}

type AgentCommandDetail struct {
	Name      string `json:"name"`
	Content   string `json:"content"`
	Path      string `json:"path"`
	Source    string `json:"source"`
	Deletable bool   `json:"deletable"`
}

type AgentCommandWriteRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Scope   string `json:"scope,omitempty"`
}

type AgentCommandUpdateRequest struct {
	Content string `json:"content"`
}

type AgentCommandMutationResponse struct {
	OK      bool              `json:"ok"`
	Command *AgentCommandInfo `json:"command,omitempty"`
}

type AgentCommandDeleteResponse struct {
	OK bool `json:"ok"`
}

func (n *NativeChannel) agentCommandsConfig() *config.Config {
	if n.agentLoop != nil {
		if cfg := n.agentLoop.GetConfigSnapshot(); cfg != nil {
			return cfg
		}
	}
	return &config.Config{}
}

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

func agentCommandsProjectDir() string {
	dir := filepath.Join(".lele", agentCommandsSubdir)
	if wd, err := os.Getwd(); err == nil {
		dir = filepath.Join(wd, dir)
	}
	return dir
}

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

func agentCommandRows(mgr *harness.Manager) []AgentCommandInfo {
	winner := map[string]harness.Source{}
	for _, cmd := range mgr.Registry().All() {
		if cmd != nil {
			winner[cmd.Name] = cmd.Source
		}
	}

	levels, _ := mgr.Levels()
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

func agentCommandScopeDeletable(source harness.Source) bool {
	return source == harness.SourceWorkspace || source == harness.SourceGlobal
}

type agentCommandState int

const (
	agentCommandMissing agentCommandState = iota
	agentCommandWritable
	agentCommandBlocked
)

type agentCommandLookup struct {
	row   AgentCommandInfo
	state agentCommandState
}

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

func agentCommandWritePath(scope, name, workspace string) (string, error) {
	dir, ok := agentCommandScopeDir(scope, workspace)
	if !ok {
		return "", fmt.Errorf("invalid scope %q", scope)
	}
	return filepath.Join(dir, name+".md"), nil
}

func agentCommandBlockedReason(row AgentCommandInfo) string {
	return fmt.Sprintf("%s is defined at the %q level, which this API does not write to",
		row.Name, row.Source)
}

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

func (n *NativeChannel) agentsSharingWorkspace(workspace string) int {
	shared := 0
	for _, id := range n.agentLoop.ListAvailableAgentIDs() {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if !ok {
			continue
		}
		other, err := agentWorkspaceDir(info.Workspace)
		if err != nil {
			continue
		}
		if other == workspace {
			shared++
		}
	}
	if shared == 0 {
		return 1
	}
	return shared
}

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
		writeError(w, http.StatusForbidden,
			"command is not managed here: "+agentCommandBlockedReason(lookup.row),
			blockedCode)
	default:
		writeError(w, http.StatusNotFound, fmt.Sprintf("command not found: %s", name), "command_not_found")
	}
	return AgentCommandInfo{}, false
}

func (n *NativeChannel) agentCommandRequestContext(w http.ResponseWriter, r *http.Request, agentID string) (string, bool) {
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent id required", "agent_id_missing")
		return "", false
	}
	return n.agentCommandWorkspace(w, agentID)
}

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

	row, _ := findAgentCommandRow(n.agentCommandCatalogRows(workspace), name, scope)
	writeJSON(w, http.StatusCreated, AgentCommandMutationResponse{OK: true, Command: row})
}

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

	updated, _ := findAgentCommandRow(n.agentCommandCatalogRows(workspace), row.Name, row.Source)
	writeJSON(w, http.StatusOK, AgentCommandMutationResponse{OK: true, Command: updated})
}

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

func (n *NativeChannel) agentCommandCatalogRows(workspace string) []AgentCommandInfo {
	return agentCommandRows(agentCommandsManager(n.agentCommandsConfig(), workspace))
}

func agentCommandBuiltinCollision(name string) string {
	if reservedCommandNames()[strings.ToLower(name)] {
		return fmt.Sprintf("/%s is already answered by the backend and always wins; choose another name", name)
	}
	return ""
}

func validateAgentCommandContent(name, content string) (string, string) {
	if len(content) > maxAgentCommandSize {
		return fmt.Sprintf("command content too large (max %d KB)", maxAgentCommandSize/1024), "too_large"
	}
	cmd, err := harness.ParseCommandMarkdown(name, name+".md", []byte(content))
	if err != nil {
		return strings.TrimPrefix(err.Error(), "harness: "), "command_invalid"
	}
	if strings.TrimSpace(cmd.Template) == "" {
		return "command template is empty: add the prompt body after the front matter", "command_invalid"
	}
	if strings.TrimSpace(cmd.Description) == "" {
		return `command description is empty: add description: "..." to the front matter`, "command_invalid"
	}
	return "", ""
}

func commandWriteStatus(code string) int {
	if code == "too_large" {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

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
	defer os.Remove(tmpName)

	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write command file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("failed to write command file: %w", err)
	}
	return nil
}

func findAgentCommandRow(rows []AgentCommandInfo, name, source string) (*AgentCommandInfo, bool) {
	for i := range rows {
		if rows[i].Name == name && rows[i].Source == source {
			row := rows[i]
			return &row, true
		}
	}
	return nil, false
}

type harnessCommandInvalidator interface {
	InvalidateHarnessWorkspace(workspace string)
}

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

func (n *NativeChannel) workspacesAffectedBy(scope, workspace string) []string {
	if scope != string(harness.SourceGlobal) {
		return []string{workspace}
	}

	out := []string{"", workspace}
	for _, id := range n.agentLoop.ListAvailableAgentIDs() {
		info, ok := n.agentLoop.GetAgentInfo(id)
		if !ok || info.Workspace == "" {
			continue
		}
		ws, err := agentWorkspaceDir(info.Workspace)
		if err != nil {
			continue
		}
		out = append(out, ws)
	}
	return dedupeAgentCommandPaths(out)
}

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
