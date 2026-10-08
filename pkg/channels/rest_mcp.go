// READ-only MCP management endpoints.
//
// Why this file exists: the MCP management UI needs, per agent, a merged view
// of its mcp.json layers (which copy wins, what is shadowed, what is
// invalid/disabled) plus the literal bytes of one layer file for inspection,
// and a surgical per-layer enable/disable toggle. The inventory/summary side
// is derived data — it must never trigger a reload, a write, or a parse that
// expands ${VAR} into secret values. The only mutation is the toggle
// endpoint, which writes ONE layer file through mcp.SetDisabled (byte
// splice, ${VAR} never expanded) and fires the reload seam exactly once,
// only after a successful write.
//
// Two rules shape this file:
//
//  1. Inventory/summary verdicts come from mcp.ReadInventory, which enumerates
//     RAW bytes (parseRawDoc), never mcp.ParseFile — so ${VAR} references stay
//     literal in every summary. Env and header VALUES never appear anywhere:
//     ServerSummary only carries key NAMES.
//  2. Paths are resolved through the OPTIONAL mcpPathsSource capability
//     (asserted at the call site, never added to AgentProvidable — same
//     reasoning as agentSkillsSource in rest_agent_skills.go: declaring it
//     there would break every channel fake).
//
// Endpoints (all behind withAuth; the toggle is the only mutation here):
//
//	GET  /api/v1/mcp?agent_id=                        merged inventory, one row per NAME
//	GET  /api/v1/mcp/{layer}/raw?agent_id=            literal bytes of one layer's file
//	PUT  /api/v1/mcp/{layer}/servers/{name}/toggle    enable/disable one server in one layer
//	                                                (?agent_id=[&force=true], body {"enabled":bool})
//
// layer ∈ global|agent|project on the raw endpoint, where "auto" is refused
// (invalid_layer): the raw endpoint answers "which physical file backs this
// layer", and auto-aliasing would make that answer depend on hidden
// precedence — callers pick a layer explicitly and see aliasing via
// AliasedWith instead. The toggle endpoint ADDS "auto" (resolve to the layer
// owning the winning copy) and never mutates outside one resolved layer file.

package channels

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xilistudios/lele/pkg/mcp"
)

// mcpRawFileMaxBytes caps how much of a layer file the raw endpoint will
// serve. Beyond this the response is 413 with code mcp_file_too_large —
// mcp.json is a config file, anything larger is not what this UI is for.
const mcpRawFileMaxBytes = 1 << 20 // 1 MiB

// mcpLayerAuto is the toggle endpoint's alias for "the layer that owns the
// winning copy"; the raw endpoint refuses it (see file header).
const mcpLayerAuto = "auto"

// mcpPathsSource is the OPTIONAL capability an agent loop may implement to
// expose the three mcp.json roots one agent reads. Asserted against
// n.agentLoop at the call site; *agent.AgentLoop satisfies it via
// MCPPathsFor, with the signature kept structurally identical so the real
// loop matches without pkg/channels importing pkg/agent.
type mcpPathsSource interface {
	MCPPathsFor(agentID string) (mcp.Paths, bool)
}

// mcpPathsFor resolves agent_id → mcp.Paths, writing the error response
// itself when resolution fails. ok=false means the caller must return.
//
// Errors: missing agent_id → 400 agent_id_missing; loop without the
// capability → 500 mcp_unavailable; MCPPathsFor reporting the agent unknown
// → 404 agent_not_found.
func (n *NativeChannel) mcpPathsFor(w http.ResponseWriter, agentID string) (mcp.Paths, bool) {
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id required", "agent_id_missing")
		return mcp.Paths{}, false
	}
	source, supported := n.agentLoop.(mcpPathsSource)
	if !supported {
		writeError(w, http.StatusInternalServerError, "mcp inventory not available", "mcp_unavailable")
		return mcp.Paths{}, false
	}
	paths, ok := source.MCPPathsFor(agentID)
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found", "agent_not_found")
		return mcp.Paths{}, false
	}
	return paths, true
}

// --- DTOs (feature-local, snake_case, no data wrapper) ----------------------

// MCPInventoryResponse is GET /api/v1/mcp.
type MCPInventoryResponse struct {
	AgentID  string         `json:"agent_id"`
	Layers   []MCPLayer     `json:"layers,omitempty"`
	Servers  []MCPServerRow `json:"servers,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
}

// MCPLayer is one mcp.json layer, low → high, aliases folded (the row is
// owned by the highest layer sharing the file, as ReadInventory reports).
type MCPLayer struct {
	Layer       string   `json:"layer"`
	Path        string   `json:"path,omitempty"`
	Exists      bool     `json:"exists"`
	AliasedWith []string `json:"aliased_with,omitempty"`
}

// MCPServerRow is one server NAME — the winning copy only; shadowed copies
// are summarised in Shadowed, so a name always appears exactly once.
type MCPServerRow struct {
	Name      string           `json:"name"`
	Layer     string           `json:"layer"` // layer owning the winning copy
	Path      string           `json:"path,omitempty"`
	Effective string           `json:"effective"` // "enabled" | "disabled" | "invalid"
	Defines   bool             `json:"defines"`
	Shadowed  []MCPShadow      `json:"shadowed,omitempty"`
	Server    MCPServerSummary `json:"server"`
}

// MCPShadow describes one inert copy of a name: where it sits (Layer, Path)
// and why it is not served (Reason names the winning layer).
type MCPShadow struct {
	Layer  string `json:"layer"`
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"` // "shadowed by <winning layer>"
}

// MCPServerSummary is the display-safe slice of mcp.ServerSummary the UI
// shows: raw strings (no ${VAR} expansion) and env/header key NAMES only.
type MCPServerSummary struct {
	Kind        string   `json:"kind,omitempty"`
	Command     string   `json:"command,omitempty"`
	URL         string   `json:"url,omitempty"`
	Type        string   `json:"type,omitempty"`
	Description string   `json:"description,omitempty"`
	Invalid     string   `json:"invalid,omitempty"`
	Args        int      `json:"args,omitempty"`
	Disabled    bool     `json:"disabled,omitempty"`
	EnvKeys     []string `json:"env_keys,omitempty"`
	HeaderKeys  []string `json:"header_keys,omitempty"`
}

// MCPRawFileResponse is GET /api/v1/mcp/{layer}/raw. Content holds the
// literal file bytes — ${VAR} is never expanded.
type MCPRawFileResponse struct {
	Layer       string   `json:"layer"`
	Path        string   `json:"path,omitempty"`
	Exists      bool     `json:"exists"`
	Content     string   `json:"content"` // "" when the file does not exist
	AliasedWith []string `json:"aliased_with,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

// mcpSummaryRow converts a winning EntryState into its row; effective is the
// inventory verdict for the name ("enabled" | "disabled" | "invalid").
func mcpSummaryRow(name string, winner mcp.EntryState, effective string, shadowed []MCPShadow) MCPServerRow {
	return MCPServerRow{
		Name:      name,
		Layer:     winner.Layer,
		Path:      winner.Path,
		Effective: effective,
		Defines:   winner.Defines,
		Shadowed:  shadowed,
		Server: MCPServerSummary{
			Kind:        winner.Summary.Kind,
			Command:     winner.Summary.Command,
			URL:         winner.Summary.URL,
			Type:        winner.Summary.Type,
			Description: winner.Summary.Description,
			Invalid:     winner.Summary.Invalid,
			Args:        winner.Summary.Args,
			Disabled:    winner.Summary.Disabled,
			EnvKeys:     winner.Summary.EnvKeys,
			HeaderKeys:  winner.Summary.HeaderKeys,
		},
	}
}

// handleMCPInventory serves GET /api/v1/mcp?agent_id= — one row per NAME
// (the winner), sorted by name; layers stay in ReadInventory's low → high
// order. Read-only: no reload, no mutation.
func (n *NativeChannel) handleMCPInventory(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	inv := mcp.ReadInventory(paths)
	resp := MCPInventoryResponse{AgentID: agentID}

	for _, li := range inv.Layers {
		resp.Layers = append(resp.Layers, MCPLayer{
			Layer:       li.Layer,
			Path:        li.Path,
			Exists:      li.Exists,
			AliasedWith: li.AliasedWith,
		})
	}
	for _, warn := range inv.Warnings {
		resp.Warnings = append(resp.Warnings, warn.Error())
	}

	names := make([]string, 0, len(inv.ByName))
	for name := range inv.ByName {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		copies := inv.ByName[name]
		// Rows are low → high; the last copy is the winner (ReadInventory's
		// own rule), and it carries the layer/path/verdict the row reports.
		winner := copies[len(copies)-1]

		var shadowed []MCPShadow
		for _, layer := range inv.Shadowed[name] {
			entry := MCPShadow{Layer: layer, Reason: "shadowed by " + winner.Layer}
			for _, c := range copies {
				if c.Layer == layer {
					entry.Path = c.Path
					break
				}
			}
			shadowed = append(shadowed, entry)
		}

		row := mcpSummaryRow(name, winner, inv.Effective[name], shadowed)
		resp.Servers = append(resp.Servers, row)
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleMCPRawFile serves GET /api/v1/mcp/{layer}/raw?agent_id= — the
// literal bytes of that layer's mcp.json, ${VAR} unexpanded.
//
// Missing file → 200 exists:false (layers are optional). An empty root /
// disabled layer resolves to "" → 400 mcp_layer_unavailable, and NOTHING is
// read or created (an empty path must never become a process-relative path).
func (n *NativeChannel) handleMCPRawFile(w http.ResponseWriter, r *http.Request) {
	layer := r.PathValue("layer")
	switch layer {
	case mcp.LayerGlobal, mcp.LayerAgent, mcp.LayerProject:
	default:
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid layer %q: use global, agent or project", layer), "invalid_layer")
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	file := mcp.LayerFile(paths, layer)
	if file == "" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("layer %q has no root for this agent", layer), "mcp_layer_unavailable")
		return
	}

	resp := MCPRawFileResponse{Layer: layer, Path: file}
	// Other layers resolving to the same file (aliased roots), low → high.
	for _, other := range mcp.Layers() {
		if other != layer && mcp.LayerFile(paths, other) == file {
			resp.AliasedWith = append(resp.AliasedWith, other)
		}
	}

	// Stat first: the size cap must reject a huge file without reading it.
	info, err := os.Stat(file)
	switch {
	case os.IsNotExist(err):
		writeJSON(w, http.StatusOK, resp) // exists:false, content:""
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to stat layer file: %v", err), "mcp_read_failed")
		return
	case info.Size() > mcpRawFileMaxBytes:
		writeError(w, http.StatusRequestEntityTooLarge,
			"layer file exceeds 1 MiB", "mcp_file_too_large")
		return
	}

	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		writeJSON(w, http.StatusOK, resp) // vanished between stat and read
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to read layer file: %v", err), "mcp_read_failed")
		return
	}
	if len(data) > mcpRawFileMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			"layer file exceeds 1 MiB", "mcp_file_too_large")
		return
	}

	resp.Exists = true
	resp.Content = string(data)
	writeJSON(w, http.StatusOK, resp)
}

// --- toggle (the only mutating endpoint in this file) -----------------------

// MCPToggleRequest is the body of
// PUT /api/v1/mcp/{layer}/servers/{name}/toggle?agent_id=[&force=true].
// The API speaks "enabled"; the mcp.json file stores "disabled", so the
// handler inverts at the SetDisabled call site.
type MCPToggleRequest struct {
	Enabled bool `json:"enabled"`
}

// MCPToggleResponse is the post-write view: the flags reported by
// mcp.SetDisabled plus a fresh inventory read (Effective/EffectiveLayer come
// from AFTER the write and reload, so they reflect the new state).
type MCPToggleResponse struct {
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	Changed        bool   `json:"changed"`
	Removed        bool   `json:"removed"`
	Created        bool   `json:"created"`
	Layer          string `json:"layer"`
	Path           string `json:"path,omitempty"`
	Effective      string `json:"effective"`
	EffectiveLayer string `json:"effective_layer,omitempty"`
}

// fireReloadMCP runs the optional reload seam AFTER a successful write,
// never while holding n.mu (the callback may re-enter the channel).
func (n *NativeChannel) fireReloadMCP() {
	n.mu.Lock()
	fn := n.reloadMCP
	n.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// mcpCopyInLayer reports whether name has a copy in the given layer.
func mcpCopyInLayer(copies []mcp.EntryState, layer string) (mcp.EntryState, bool) {
	for _, c := range copies {
		if c.Layer == layer {
			return c, true
		}
	}
	return mcp.EntryState{}, false
}

// handleMCPToggle serves
// PUT /api/v1/mcp/{layer}/servers/{name}/toggle?agent_id=[&force=true]
// with body {"enabled":bool} — enables/disables one server in ONE layer.
//
// layer ∈ global|agent|project|auto (auto = the layer that currently owns
// the winning copy). Writing a name into a layer that does not hold it is an
// allowed cross-layer create, EXCEPT a disable into global (a global stub
// can never shadow anything — refused as mcp_stub_layer_not_allowed), and
// except when a losing copy in the addressed layer would be overwritten
// without ?force=true → 409 mcp_entry_shadowed.
//
// Validation order (each failure returns immediately, reload fires exactly
// once and only after a successful write): invalid_layer → mcpPathsFor →
// invalid_request → body_invalid → mcp_server_not_found → mcp_layer_required
// → mcp_entry_shadowed → mcp_stub_layer_not_allowed → mcp_layer_unavailable
// → mcp_path_not_allowed → mcp_write_failed.
func (n *NativeChannel) handleMCPToggle(w http.ResponseWriter, r *http.Request) {
	// 1. layer ∈ global|agent|project|auto.
	layer := r.PathValue("layer")
	switch layer {
	case mcp.LayerGlobal, mcp.LayerAgent, mcp.LayerProject, mcpLayerAuto:
	default:
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid layer %q: use global, agent, project or auto", layer), "invalid_layer")
		return
	}

	// 2. agent_id → paths (writes its own error responses).
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	// 3. name sanity.
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid server name", "invalid_request")
		return
	}

	// 4. decode the body BEFORE touching disk.
	var req MCPToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid body: %v", err), "body_invalid")
		return
	}

	// 5. the name must exist SOMEWHERE (otherwise there is nothing to toggle).
	inv := mcp.ReadInventory(paths)
	copies, known := inv.ByName[name]
	if !known {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("mcp server %q not found", name), "mcp_server_not_found")
		return
	}

	// 6. auto resolves to the owning (winning) layer.
	if layer == mcpLayerAuto {
		layer = inv.Winner[name]
		if layer == "" {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("cannot resolve the owning layer of %q", name), "mcp_layer_required")
			return
		}
	}

	_, inLayer := mcpCopyInLayer(copies, layer)

	// 7. overwriting a LOSING copy in the addressed layer needs ?force=true;
	//    writing where the name is absent is an allowed cross-layer create.
	force := r.URL.Query().Get("force") == "true"
	if layer != inv.Winner[name] && !force && inLayer {
		owner := inv.Winner[name]
		ownerPath := ""
		if c, ok := mcpCopyInLayer(copies, owner); ok {
			ownerPath = c.Path
		}
		msg := fmt.Sprintf("mcp server %q in layer %q is shadowed by layer %q; retry with force=true to overwrite it",
			name, layer, owner)
		writeJSON(w, http.StatusConflict, APIError{
			Code:    "mcp_entry_shadowed",
			Message: msg,
			Error:   msg,
			Details: map[string]any{"owner_layer": owner, "owner_path": ownerPath},
		})
		return
	}

	// 8. a DISABLE into global when global does not hold the name would
	//    create a stub that can never shadow anything — refuse it.
	if !req.Enabled && layer == mcp.LayerGlobal && !inLayer {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("cannot create a global stub for %q: disable it in the layer that owns it", name),
			"mcp_stub_layer_not_allowed")
		return
	}

	// 9. resolve the physical file for the layer.
	file := mcp.LayerFile(paths, layer)
	if file == "" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("layer %q has no root for this agent", layer), "mcp_layer_unavailable")
		return
	}

	// 10. path guard — checked BEFORE any write.
	if filepath.Base(file) != "mcp.json" || !isAllowedWorkspacePath(filepath.Dir(file)) {
		writeError(w, http.StatusForbidden,
			fmt.Sprintf("layer file %q is outside the allowed workspace roots", file),
			"mcp_path_not_allowed")
		return
	}

	// 11. write. The file stores "disabled", the API speaks "enabled".
	res, err := mcp.SetDisabled(file, name, !req.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to write %s: %v", file, err), "mcp_write_failed")
		return
	}

	// 12. reload, exactly once, only after a successful write.
	n.fireReloadMCP()

	// 13. fresh inventory: effective state AFTER the write + reload.
	inv2 := mcp.ReadInventory(paths)
	writeJSON(w, http.StatusOK, MCPToggleResponse{
		Name:           name,
		Enabled:        req.Enabled,
		Changed:        res.Changed,
		Removed:        res.Removed,
		Created:        res.Created,
		Layer:          layer,
		Path:           file,
		Effective:      inv2.Effective[name],
		EffectiveLayer: inv2.Winner[name],
	})
}
