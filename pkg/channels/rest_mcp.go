// MCP management endpoints (inventory, raw view, toggle, raw edit, validate).
//
// Why this file exists: the MCP management UI needs, per agent, a merged view
// of its mcp.json layers (which copy wins, what is shadowed, what is
// invalid/disabled) plus the literal bytes of one layer file for inspection,
// a surgical per-layer enable/disable toggle, a whole-file editor save and a
// dry-run validator. The inventory/summary side
// is derived data — it must never trigger a reload, a write, or a parse that
// expands ${VAR} into secret values. There are exactly TWO mutations: the
// toggle endpoint (writes ONE layer file through mcp.SetDisabled, byte
// splice, ${VAR} never expanded) and the raw PUT (replaces one layer file
// verbatim through mcp.SaveRawFile, after mcp.ValidateRawJSON has accepted
// the envelope). Each fires the reload seam exactly once, only after a
// successful write. The validate endpoint never writes and never reloads.
//
// Two rules shape this file:
//
//  1. Inventory/summary verdicts come from mcp.ReadInventory, which enumerates
//     RAW bytes (parseRawDoc), never mcp.ParseFile — so ${VAR} references stay
//     literal in every summary. Env and header VALUES never appear anywhere:
//     ServerSummary only carries key NAMES.
//  1b. The per-row auth report (MCPServerAuth) is the ONE thing built from a
//     resolved config, because only the merge knows whether an entry opted
//     into OAuth: it comes from the same mcp.Discover call ReadInventory
//     already performs, and projects structural facts only (declared auth
//     type, header KEY COUNT, url) plus the auth store's booleans.
//     client_id/client_secret/scopes/redirect_uri/tokens are never read, and
//     no value of any kind is ever placed in the response.
//  2. Paths are resolved through the OPTIONAL mcpPathsSource capability
//     (asserted at the call site, never added to AgentProvidable — same
//     reasoning as agentSkillsSource in rest_agent_skills.go: declaring it
//     there would break every channel fake).
//
// Endpoints (all behind withAuth). The toggle, the raw PUT and the OAuth
// writes mutate state; none of the OAuth routes ever touches a layer file —
// they act on the auth STORE and on live flows:
//
//	GET    /api/v1/mcp?agent_id=                        merged inventory, one row per NAME,
//	                                                  each with its secret-free auth report
//	GET    /api/v1/mcp/{layer}/raw?agent_id=            literal bytes of one layer's file
//	PUT    /api/v1/mcp/{layer}/raw?agent_id=            replace that file verbatim (body {"content":string})
//	PUT    /api/v1/mcp/{layer}/servers/{name}/toggle    enable/disable one server in one layer
//	                                                  (?agent_id=[&force=true], body {"enabled":bool})
//	POST   /api/v1/mcp/validate?agent_id=               dry-run validate (never writes/reloads;
//	                                                  body {"layer":string,"content":string})
//	POST   /api/v1/mcp/servers/{name}/auth?agent_id=    start an OAuth flow → MCPAuthStart
//	GET    /api/v1/mcp/auth/{flow_id}?agent_id=         poll it → MCPAuthStatus (fires the reload
//	                                                  exactly once, on pending → done)
//	POST   /api/v1/mcp/auth/{flow_id}/complete?agent_id= paste the code (body {"code","state"})
//	                                                  → MCPAuthStatus
//	DELETE /api/v1/mcp/auth/{flow_id}?agent_id=         cancel the flow, freeing its loopback
//	                                                  listener → 204
//	DELETE /api/v1/mcp/servers/{name}/auth?agent_id=    log out: forget token AND registration → 204
//
// The five OAuth routes (registry in mcp_auth_flows.go, handlers at the bottom
// of this file) are registered in the same block: they read the SAME winning
// config and never touch a layer file themselves. The raw READ route is
// registered through handleMCPRawDoc, a shim whose pattern uses a {doc}
// wildcard, because the literal "raw" pattern and the auth status route cannot
// coexist in one ServeMux (see native.go).
//
// layer ∈ global|agent|project on the raw endpoint, where "auto" is refused
// (invalid_layer): the raw endpoint answers "which physical file backs this
// layer", and auto-aliasing would make that answer depend on hidden
// precedence — callers pick a layer explicitly and see aliasing via
// AliasedWith instead. The raw PUT refuses auto for the same reason (it
// writes ONE physical file). The toggle endpoint ADDS "auto" (resolve to the
// layer owning the winning copy) and never mutates outside one resolved
// layer file. The validate endpoint accepts auto but does not resolve it
// (layer is checked for symmetry, see handleMCPValidate).

package channels

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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

	// Auth is the auth report of this row, absent when there is nothing to
	// report: no url, no header keys and no stored auth state. The pointer is
	// omitempty so an entry that has no auth story does not carry a
	// meaningless {"required":false,"authenticated":false,"method":""} member.
	Auth *MCPServerAuth `json:"auth,omitempty"`
}

// MCPServerAuth is the secret-free auth report of one inventory row: booleans
// and a fixed method string, never a value. It mirrors the project's rule that
// no credential MATERIAL crosses these routes.
//
// It is a 1:1 projection of mcp.AuthStatus (mcp.CheckAuth): Required is true
// when the entry DECLARES auth — an "auth": {"type": "oauth"} block — or when a
// 401 was RECORDED for (name, url) in the auth store. Header keys are NOT
// evidence: MCP headers are routinely non-credential (Accept, User-Agent,
// X-Client-Version), so they only report that a CARRIER exists to name — hence
// Method "headers" with Required still false until a challenge is recorded.
// Authenticated is true only for OAuth with a usable stored token, and Method
// is "oauth", "headers" or "".
// A header-carrying row therefore reports method:"headers" with
// authenticated:false (and required:false until a 401 is recorded): the values
// exist in mcp.json but are never read here, so no surface may claim they are
// valid.
type MCPServerAuth struct {
	Required      bool   `json:"required"`
	Authenticated bool   `json:"authenticated"`
	Method        string `json:"method"` // "oauth" | "headers" | ""
}

// The Method values are mcp.CheckAuth's own ("oauth" | "headers" | ""): there
// is no local vocabulary to keep in step, because the engine's rule is the only
// producer of this DTO.

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
// inventory verdict for the name ("enabled" | "disabled" | "invalid") and auth
// is the already-resolved auth report (nil when the row has nothing to report).
func mcpSummaryRow(name string, winner mcp.EntryState, effective string, shadowed []MCPShadow, auth *MCPServerAuth) MCPServerRow {
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
			Auth:        auth,
		},
	}
}

// mcpRowAuth resolves the auth report of ONE inventory row.
//
// EVERY row is described by mcp.CheckAuth — the engine's own rule, so the
// report and the row's verdicts always agree and "authenticated" answers the
// question the client actually asks ("will my next call go through?"). The one
// thing that differs is where CheckAuth's config comes from: a row whose winner
// the merge RESOLVED (Discover reports it: enabled, valid) uses the merged
// config — the one the manager would dial — and any other row (disabled entry,
// invalid entry) uses the winner's own copy, re-read from its layer file.
//
// The re-read exists because a row's summary is value-free AND unexpanded: it
// carries the header KEY NAMES but not the ${VAR}-EXPANDED url, and the auth
// store binds a record to name AND url (auth_store.go), so asking the store
// with the raw summary url would MISS a token or a recorded 401 stored under
// the expanded one. mcp.ParseFile is the only exported way to re-apply the
// engine's own expansion (and its defaults), and it reads nothing secret: only
// the url and whether a carrier exists reach CheckAuth.
//
// store must be the per-request mcp.NewAuthStore(paths.LeleDir).
func mcpRowAuth(store *mcp.AuthStore, merged mcp.DiscoveryResult, name string, winner mcp.EntryState) *MCPServerAuth {
	srv, resolved := merged.Servers[name]
	if !resolved {
		var ok bool
		if srv, ok = mcpWinnerConfig(name, winner); !ok {
			// The winner's layer file no longer parses (it changed between
			// ReadInventory's read and this one, or it is gone): there is no
			// config left to describe, so the row reports nothing rather than
			// a guess.
			return nil
		}
	}
	return mcpAuthFor(mcp.CheckAuth(store, name, srv))
}

// mcpWinnerConfig returns the config of the winning copy of name for a row the
// merge did NOT resolve (a disabled entry, an invalid one), by re-reading the
// winner's own layer file with mcp.ParseFile: the same ${VAR} expansion and the
// same defaults the merge applies, so both branches of mcpRowAuth ask the engine
// the same question.
//
// ok is false when the file (or the name inside it) is no longer there — a race
// against an editor, never fatal: the row simply reports no auth.
//
// One parse per unresolved row: the inventory is a read-only UI poll, and
// ReadInventory and Discover already parse every layer file twice per request.
// The winner's Path is always set (ReadInventory emits rows per copy), and
// Discover's own read proves the file parsed; only an edit racing this request
// can turn the parse into a failure.
func mcpWinnerConfig(name string, winner mcp.EntryState) (mcp.ServerConfig, bool) {
	file, err := mcp.ParseFile(winner.Path)
	if err != nil || file == nil {
		return mcp.ServerConfig{}, false
	}
	srv, ok := file.MCPServers[name]
	return srv, ok
}

// mcpAuthFor projects one row's resolved mcp.AuthStatus into its secret-free
// report, or returns nil when the row has no auth story to tell — the "auth"
// member is omitempty and only Required, Authenticated or a Method earns a
// slot.
//
// There is exactly ONE rule ("which method is this row?"): the engine's
// mcp.CheckAuth, which both branches of mcpRowAuth call. No path here ever
// reads a header, env, scope or token VALUE.
func mcpAuthFor(st mcp.AuthStatus) *MCPServerAuth {
	if !st.Required && !st.Authenticated && st.Method == "" {
		return nil
	}
	return &MCPServerAuth{
		Required:      st.Required,
		Authenticated: st.Authenticated,
		Method:        st.Method,
	}
}

// handleMCPInventory serves GET /api/v1/mcp?agent_id= — one row per NAME
// (the winner), sorted by name; layers stay in ReadInventory's low → high
// order. Read-only: no reload, no mutation.
//
// Each row also carries its auth report (MCPServerAuth), which needs the
// WINNING CONFIG — not just the summary — to see whether the entry opted into
// OAuth. That config comes from mcp.Discover, the merge rule ReadInventory
// already delegates its verdicts to, so the auth report always agrees with the
// row's layer/effective verdicts. The auth store is built ONCE per request
// and its reads re-validate <LeleDir>/mcp-auth.json with an os.Stat, so a
// login performed by ANOTHER process (the TUI, while the gateway serves this
// request) shows up on the next request without a restart.
func (n *NativeChannel) handleMCPInventory(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	inv := mcp.ReadInventory(paths)
	// The error is the same warning set ReadInventory already reports in
	// resp.Warnings: a broken layer never aborts the merge, so it is ignored here.
	merged, _ := mcp.Discover(paths)
	store := mcp.NewAuthStore(paths.LeleDir)
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

		row := mcpSummaryRow(name, winner, inv.Effective[name], shadowed,
			mcpRowAuth(store, merged, name, winner))
		resp.Servers = append(resp.Servers, row)
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleMCPRawDoc is the registration shim for the raw-file READ (see the
// route table in native.go): its pattern is "GET /api/v1/mcp/{layer}/{doc}"
// because "GET /api/v1/mcp/{layer}/raw" cannot be registered next to the OAuth
// status route "GET /api/v1/mcp/auth/{flow_id}" — both match
// GET /api/v1/mcp/auth/raw and neither is more specific, so ServeMux PANICS at
// registration time. Only the doc segment's VALUE differs from the old route:
// "raw" is served exactly as before (same handler, same layer validation, same
// status codes) and anything else is the 404 the ServeMux would have produced
// for an unregistered path.
//
// THE doc CHECK BELOW IS A SECURITY GATE, NOT A CONVENIENCE. Widening the
// pattern from the literal "raw" to this wildcard also widened WHAT REACHES
// handleMCPRawFile: that handler serves the layer's mcp.json BYTES — header and
// env values included — to any authenticated caller. Without the check every
// GET /api/v1/mcp/{global|agent|project}/<anything> would be a 200 file read.
// Do NOT "simplify" the shim away either: re-registering the literal
// "{layer}/raw" next to "auth/{flow_id}" PANICS at registration (above), and a
// per-layer literal ("global/raw", …) would turn the pinned 400 invalid_layer
// for GET /api/v1/mcp/bogus/raw into a 404 because no pattern would match the
// unknown layer at all. The 404s this gate produces are pinned by
// TestMCPRawDoc_RejectsAnyDocOtherThanRaw.
func (n *NativeChannel) handleMCPRawDoc(w http.ResponseWriter, r *http.Request) {
	// The gate. See the doc comment: deleting it exposes the layer file.
	if r.PathValue("doc") != "raw" {
		http.NotFound(w, r)
		return
	}
	n.handleMCPRawFile(w, r)
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

	// path guard — the SAME rule the write handlers apply to the same layer
	// file, checked BEFORE anything is read (read/write guard agreement):
	// a path the writes refuse must not be served by this read.
	if !mcpGuardLayerFile(w, file) {
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

// --- toggle (PUT .../servers/{name}/toggle) -------------------------------

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

// mcpLayerRank returns the position of layer in mcp.Layers() — the canonical
// low → high merge order the inventory already defines — so the stub guard
// below can compare layers without a second index table in pkg/channels.
// Unknown layers rank -1; unreachable at the call site (step 1 validates the
// layer and step 7 resolves auto before this runs).
func mcpLayerRank(layer string) int {
	for i, l := range mcp.Layers() {
		if l == layer {
			return i
		}
	}
	return -1
}

// mcpGuardLayerFile writes 403 mcp_path_not_allowed and returns false when
// the resolved layer file is not an mcp.json under an allowed workspace
// root. Shared by EVERY handler that touches one layer file — the raw GET
// (before any read), the raw PUT (before decode) and the toggle (before the
// write) — so a path refused to one client is refused to all of them with
// the same code (read/write guard agreement).
func mcpGuardLayerFile(w http.ResponseWriter, file string) bool {
	if filepath.Base(file) != "mcp.json" || !isAllowedWorkspacePath(filepath.Dir(file)) {
		writeError(w, http.StatusForbidden,
			fmt.Sprintf("layer file %q is outside the allowed workspace roots", file),
			"mcp_path_not_allowed")
		return false
	}
	return true
}

// handleMCPToggle serves
// PUT /api/v1/mcp/{layer}/servers/{name}/toggle?agent_id=[&force=true]
// with body {"enabled":bool} — enables/disables one server in ONE layer.
//
// layer ∈ global|agent|project|auto (auto = the layer that currently owns
// the winning copy). Writing a name into a layer that does not hold it is an
// allowed cross-layer create, EXCEPT a disable that would create a stub
// BELOW the winning layer (the winner keeps serving, so the write is inert —
// refused as mcp_stub_layer_not_allowed), and except when a losing copy in
// the addressed layer would be overwritten without ?force=true → 409
// mcp_entry_shadowed. A name whose EFFECTIVE verdict is invalid is not
// toggleable in EITHER direction (400 mcp_entry_invalid): a write would
// mask the invalid verdict behind a legit-looking enabled/disabled state
// (the verdict derivation prefers Disabled over Invalid), and the only
// repair is the raw editor — the same rule the TUI and WebUI clients
// enforce; the API is the invariant's single enforcer.
//
// Validation order (each failure returns immediately, reload fires exactly
// once and only after a successful write): invalid_layer → mcpPathsFor →
// invalid_request → body_invalid → mcp_server_not_found → mcp_entry_invalid
// → mcp_layer_required → mcp_entry_shadowed → mcp_stub_layer_not_allowed →
// mcp_layer_unavailable → mcp_path_not_allowed → mcp_write_failed.
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

	// 6. an INVALID entry is not toggleable in EITHER direction (the rule
	//    both clients already enforce; the API is the enforcer). A write
	//    would mask the invalid verdict behind a legit-looking
	//    enabled/disabled state (the verdict derivation prefers Disabled
	//    over Invalid), and the only repair is the raw editor. Checked
	//    against the EFFECTIVE verdict — the same map the response row
	//    uses — for the resolved target AND the winner alike, before auto
	//    resolution, the shadow/force logic and any write.
	if inv.Effective[name] == "invalid" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("mcp server %q has verdict %q and cannot be toggled; repair it in the raw editor (GET/PUT /api/v1/mcp/{layer}/raw)",
				name, inv.Effective[name]),
			"mcp_entry_invalid")
		return
	}

	// 7. auto resolves to the owning (winning) layer.
	if layer == mcpLayerAuto {
		layer = inv.Winner[name]
		if layer == "" {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("cannot resolve the owning layer of %q", name), "mcp_layer_required")
			return
		}
	}

	_, inLayer := mcpCopyInLayer(copies, layer)

	// 8. overwriting a LOSING copy in the addressed layer needs ?force=true;
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

	// 9. a DISABLE creating a stub BELOW the winning layer is inert — the
	//    winner keeps serving — whatever the layer (the old global-only guard
	//    was exactly this rule spelled for the lowest layer). Ranks come from
	//    mcp.Layers() (low → high); the refusal message names the winning
	//    layer. Winner is always set for a name that reached step 5.
	if !req.Enabled && !inLayer && mcpLayerRank(layer) < mcpLayerRank(inv.Winner[name]) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("cannot create a stub for %q in layer %q: it sits below the winning layer %q; disable it in the layer that owns it",
				name, layer, inv.Winner[name]),
			"mcp_stub_layer_not_allowed")
		return
	}

	// 10. resolve the physical file for the layer.
	file := mcp.LayerFile(paths, layer)
	if file == "" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("layer %q has no root for this agent", layer), "mcp_layer_unavailable")
		return
	}

	// 11. path guard — checked BEFORE any write (shared with the raw
	//    endpoints so read and write verdicts always agree).
	if !mcpGuardLayerFile(w, file) {
		return
	}

	// 12. write. The file stores "disabled", the API speaks "enabled".
	res, err := mcp.SetDisabled(file, name, !req.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to write %s: %v", file, err), "mcp_write_failed")
		return
	}

	// 13. reload, exactly once, only after a successful write.
	n.fireReloadMCP()

	// 14. fresh inventory: effective state AFTER the write + reload.
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

// --- raw PUT (whole-file editor save) --------------------------------------

// MCPRawPutRequest is the body of PUT /api/v1/mcp/{layer}/raw?agent_id=.
// Content is the literal file to write — no ${VAR} expansion, no reformat.
type MCPRawPutRequest struct {
	Content string `json:"content"`
}

// handleMCPRawPut serves PUT /api/v1/mcp/{layer}/raw?agent_id= with body
// {"content":string} — the MCP editor's save path: it replaces ONE layer
// file with the given bytes, byte for byte.
//
// layer ∈ global|agent|project (auto refused, same rule as GET raw: the
// editor saves a PHYSICAL file and auto-aliasing would make the target
// depend on hidden precedence — see file header).
//
// Validation order (each failure returns immediately; the reload fires
// exactly once, only after a successful write):
// invalid_layer → mcpPathsFor → mcp_layer_unavailable → mcp_path_not_allowed
// → body_invalid → mcp_file_too_large → config_invalid → mcp_write_failed.
//
// 422 config_invalid vs 500 mcp_write_failed: pkg/mcp's SaveRawFile writes
// bytes atomically but does NOT validate, so the envelope verdict is taken
// HERE — mcp.ValidateRawJSON runs BEFORE the write and a fatal envelope
// result (not JSON / no mcpServers object / wrong type) is 422 with nothing
// written; every SaveRawFile failure is therefore by construction an I/O
// error → 500. Per-entry problems are warnings carried in the response —
// never a hard error, because fixing a broken entry is exactly what this
// editor is for.
//
// After a successful SaveRawFile there is NO failure path: the response
// echoes req.Content (the bytes the atomic temp+rename just persisted)
// instead of re-reading the file, so a landed, reloaded write can never be
// reported as 500 and a second writer winning the race after our rename can
// never have its bytes echoed as this client's save result.
func (n *NativeChannel) handleMCPRawPut(w http.ResponseWriter, r *http.Request) {
	// 1. layer ∈ global|agent|project (auto refused).
	layer := r.PathValue("layer")
	switch layer {
	case mcp.LayerGlobal, mcp.LayerAgent, mcp.LayerProject:
	default:
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid layer %q: use global, agent or project", layer), "invalid_layer")
		return
	}

	// 2. agent_id → paths (writes its own error responses).
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	// 3. resolve the physical file for the layer.
	file := mcp.LayerFile(paths, layer)
	if file == "" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("layer %q has no root for this agent", layer), "mcp_layer_unavailable")
		return
	}

	// 4. path guard — checked BEFORE anything is decoded or written, so a
	//    rejected path never sees the payload (nothing written). Shared with
	//    the read endpoint: same file, same verdict.
	if !mcpGuardLayerFile(w, file) {
		return
	}

	// 5. decode the body.
	var req MCPRawPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid body: %v", err), "body_invalid")
		return
	}

	// 6. size cap (mcp.json is a config file; beyond 1 MiB this UI is the
	//    wrong tool). The route IS body-limited: applyBodyLimit (1 MiB
	//    http.MaxBytesReader) wraps it, so an over-limit request fails at
	//    decode above with 400 body_invalid and never gets here. This check
	//    stays as the FIELD-level guard — a body just under 1 MiB can still
	//    carry a `content` string just under 1 MiB (plus envelope), so only
	//    len(content) proves the file itself is within the cap. Defence in
	//    depth if the route limit is ever raised: unreachable today.
	if len(req.Content) > mcpRawFileMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			"content exceeds 1 MiB", "mcp_file_too_large")
		return
	}

	// 7. envelope validation BEFORE the write: a fatal result is 422
	//    config_invalid and the file is untouched. Warnings (per-entry
	//    problems) do NOT block the save — they ride along in the response.
	warnings, verr := mcp.ValidateRawJSON([]byte(req.Content))
	if verr != nil {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("invalid mcp.json: %v", verr), "config_invalid")
		return
	}

	// 8. write the bytes verbatim (atomic temp+rename in mcp.SaveRawFile).
	if err := mcp.SaveRawFile(file, []byte(req.Content)); err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to write %s: %v", file, err), "mcp_write_failed")
		return
	}

	// 9. reload, exactly once, only after a successful write.
	n.fireReloadMCP()

	// 10. respond with the bytes we JUST wrote — no read-back. SaveRawFile is
	//     atomic (temp+rename), so at this point the file holds exactly
	//     req.Content. Re-reading could only lie: it can fail after a landed,
	//     reloaded write (turning a success into a 500) or lose a race with a
	//     second writer and echo bytes this client never sent. On-disk state
	//     that is legitimately observable (aliased roots) is still computed
	//     from the path resolution.
	resp := MCPRawFileResponse{Layer: layer, Path: file, Exists: true, Content: req.Content}
	for _, other := range mcp.Layers() {
		if other != layer && mcp.LayerFile(paths, other) == file {
			resp.AliasedWith = append(resp.AliasedWith, other)
		}
	}
	for _, warn := range warnings {
		resp.Warnings = append(resp.Warnings, warn.Error())
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- validate (POST .../validate, dry run) ---------------------------------

// MCPValidateRequest is the body of POST /api/v1/mcp/validate?agent_id=.
type MCPValidateRequest struct {
	Layer   string `json:"layer"`
	Content string `json:"content"`
}

// MCPValidateResponse is the validate endpoint's verdict. Valid reflects
// the FATAL envelope result only; Warnings carry the per-entry problems
// (never fatal by design — see ValidateRawJSON); Error is set iff Valid is
// false. The response is always 200.
type MCPValidateResponse struct {
	Valid    bool     `json:"valid"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// handleMCPValidate serves POST /api/v1/mcp/validate?agent_id= with body
// {"layer":string,"content":string} — a DRY RUN for the editor. It never
// writes, never creates a file and never calls fireReloadMCP; paths are
// resolved only to pin the agent (agent_id / availability errors), not to
// touch disk.
//
// layer ∈ global|agent|project|auto is accepted for symmetry with the raw
// endpoints; beyond validating the string its value is UNUSED here (auto is
// deliberately NOT resolved — there is nothing to address until a write is
// requested), reserved for future cross-checks against the addressed file.
//
// Always 200: Valid = (mcp.ValidateRawJSON returned no fatal error), Error
// carries that fatal error, Warnings carry the per-entry problems — which
// must NOT fail the request, because an invalid entry is fixable only
// through this editor.
func (n *NativeChannel) handleMCPValidate(w http.ResponseWriter, r *http.Request) {
	// 1. agent_id → paths (writes its own error responses; no file access).
	agentID := r.URL.Query().Get("agent_id")
	if _, ok := n.mcpPathsFor(w, agentID); !ok {
		return
	}

	// 2. decode the body.
	var req MCPValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid body: %v", err), "body_invalid")
		return
	}

	// 3. layer ∈ global|agent|project|auto (string check only — see comment).
	switch req.Layer {
	case mcp.LayerGlobal, mcp.LayerAgent, mcp.LayerProject, mcpLayerAuto:
	default:
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid layer %q: use global, agent, project or auto", req.Layer), "invalid_layer")
		return
	}

	// 4. verdict — ALWAYS 200 from here on.
	resp := MCPValidateResponse{}
	warnings, err := mcp.ValidateRawJSON([]byte(req.Content))
	for _, warn := range warnings {
		resp.Warnings = append(resp.Warnings, warn.Error())
	}
	if err != nil {
		resp.Error = err.Error()
	} else {
		resp.Valid = true
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- OAuth flow (REST routes 2-6 of the frozen contract) -------------------
//
// The five auth routes are the REST face of the interactive OAuth 2.1 flow
// (pkg/mcp/auth.go). They differ from the routes above in one structural way:
// the unit of work is not a FILE but a LIVE FLOW that outlives the request
// which created it, so the flow's state lives in the per-channel registry
// (mcp_auth_flows.go) and every handler is a thin projection of it.
//
// Routes (all withAuth, all backwards-compatible with the five above; see the
// file header for the endpoint table):
//
//	POST   /api/v1/mcp/servers/{name}/auth?agent_id=      start a flow → MCPAuthStart
//	GET    /api/v1/mcp/auth/{flow_id}?agent_id=           poll it   → MCPAuthStatus
//	POST   /api/v1/mcp/auth/{flow_id}/complete?agent_id=  paste the code → MCPAuthStatus
//	DELETE /api/v1/mcp/auth/{flow_id}?agent_id=           cancel it  → 204
//	DELETE /api/v1/mcp/servers/{name}/auth?agent_id=      log out    → 204
//
// Three rules hold across all five:
//
//  1. No value crosses the API. The responses carry a flow id (128-bit
//     crypto/rand hex), an auth URL, a redirect URI, a deadline, three state
//     words, a rendered error message and ONE boolean. Never a token, a code,
//     a client id/secret or a PKCE verifier — see mcpAuthStatusFor.
//  2. The reload seam (fireReloadMCP) fires at most ONCE per flow, only when a
//     poll observes "done" (and once after a successful logout). The claim is
//     atomic in the registry (MarkReloaded), so concurrent polls of the same
//     completed flow reload once, not N times.
//  3. A flow id is NOT storage: a flow that is never polled and never
//     cancelled is dropped (and its loopback listener closed) by the next
//     lookup after its deadline. There is no reaper goroutine — the lazy sweep
//     in the registry is the only reaper.

// mcpAuthStateDone is the engine's terminal success state, spelled once so the
// reload trigger and the tests cannot drift from pkg/mcp's string.
const mcpAuthStateDone = "done"

// MCPAuthStart is the response to POST /api/v1/mcp/servers/{name}/auth.
//
// ExpiresAt is the FLOW REGISTRY's own deadline (start + 5m, matching
// pkg/mcp.defaultAuthTimeout): AuthFlow exposes no deadline, and the deadline
// that matters to the client is the one after which this gateway stops
// resolving the id.
type MCPAuthStart struct {
	FlowID      string    `json:"flow_id"`
	AuthURL     string    `json:"auth_url"`
	RedirectURI string    `json:"redirect_uri"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// MCPAuthCompleteRequest is the body of
// POST /api/v1/mcp/auth/{flow_id}/complete: the code the user's browser was
// redirected with, plus the state it carried.
//
// state is optional but should be sent: the engine rejects a non-empty state
// that does not match the flow's own CSRF state BEFORE attempting the token
// exchange, and a rejected attempt leaves the flow open and retryable.
type MCPAuthCompleteRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// MCPAuthStatus is the poll response and, identically, the answer to a
// successful complete.
//
// State ∈ pending|done|error. "error" is deliberately ambiguous (the engine's
// own contract): a failed token exchange reports "error" while leaving the
// flow OPEN and retryable, so a client must keep its paste box available
// whenever state == "error" — the flow id stops resolving only once the
// registry reaps it.
//
// Error is a value-free rendered message: it may name an OAuth error code
// (invalid_grant) or one of the engine's fixed sentinels, never a token, a
// code, a verifier or a secret. A cancelled flow reports the literal "canceled".
type MCPAuthStatus struct {
	FlowID        string `json:"flow_id"`
	Server        string `json:"server"`
	State         string `json:"state"`
	Error         string `json:"error,omitempty"`
	Authenticated bool   `json:"authenticated"`
}

// mcpAuthErrorCode maps a flow-engine failure to the contract's (status, code)
// pair. The mapping is by SENTINEL, never by message text: "this server needs
// no login" (409), "this server cannot register a client" (501) and "the store
// is off" (503) are three different problems with three different fixes, and
// collapsing them into one line is what the sentinels exist to prevent.
//
// Anything else is 502 mcp_auth_failed carrying the engine's own value-free
// message: pkg/mcp guarantees no token, code, verifier or secret ever reaches
// an error string (it sanitizes token-endpoint failures through
// valueFreeError), so echoing it is safe and is the only way to report a
// transport failure whose cause is outside the sentinel set.
func mcpAuthErrorCode(err error) (int, string) {
	switch {
	case errors.Is(err, mcp.ErrAuthNotSupported):
		return http.StatusNotImplemented, "mcp_auth_not_supported"
	case errors.Is(err, mcp.ErrAuthStoreDisabled):
		return http.StatusServiceUnavailable, "mcp_auth_disabled"
	case errors.Is(err, mcp.ErrAuthNotRequired):
		return http.StatusConflict, "mcp_auth_not_required"
	case errors.Is(err, errMCPAuthFlowNotFound):
		return http.StatusNotFound, "mcp_auth_flow_not_found"
	default:
		return http.StatusBadGateway, "mcp_auth_failed"
	}
}

// writeMCPAuthError renders a flow failure, prefixing the engine's message with
// what the gateway was doing. The prefix names a SERVER or a flow, both of
// which the caller already knows.
func writeMCPAuthError(w http.ResponseWriter, err error, what string) {
	status, code := mcpAuthErrorCode(err)
	writeError(w, status, fmt.Sprintf("%s: %v", what, err), code)
}

// mcpAuthFlowError renders a flow's error for the status DTO. Value-free by
// construction, with one fixed exception the contract pins: a cancelled flow
// reports the literal "canceled" (not the engine's phrasing), because the
// WebUI shows that string as-is and no surface should have to parse it.
func mcpAuthFlowError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, mcp.ErrAuthCanceled):
		return "canceled"
	default:
		return err.Error()
	}
}

// mcpAuthStatusFor projects ONE live entry into the DTO. The auth report is
// rebuilt through mcp.CheckAuth from the SAME resolved config the flow was
// started with, so "authenticated" answers the question a client is asking
// ("can this gateway use the server now?") and not "is there a record on
// disk?" — an entry rejected by the winning config reports false either way.
func mcpAuthStatusFor(store *mcp.AuthStore, entry *mcpAuthEntry) MCPAuthStatus {
	state, ferr := entry.flow.Status()
	srv := entry.ServerConfig()
	return MCPAuthStatus{
		FlowID:        entry.flowID,
		Server:        entry.name,
		State:         state,
		Error:         mcpAuthFlowError(ferr),
		Authenticated: mcp.CheckAuth(store, entry.name, srv).Authenticated,
	}
}

// mcpAuthFlowID validates the flow id SHAPE before any lookup: a malformed id
// is a client error (400 invalid_request), not a missing resource, and the
// check keeps the registry from being asked about ids it could never hold.
func mcpAuthFlowID(w http.ResponseWriter, id string) (string, bool) {
	if !isValidMCPFlowID(id) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid flow_id %q", id), "invalid_request")
		return "", false
	}
	return id, true
}

// handleMCPAuthStart serves POST /api/v1/mcp/servers/{name}/auth?agent_id=.
//
// The server is resolved from the WINNING config (mcp.Discover — the same
// merge T8a's rows report), so every write path that changes which copy wins
// changes what this route authorizes: there is exactly one resolution rule in
// the feature.
//
// Validation order (each failure returns immediately): invalid_request (bad
// name) → agent_id → mcp_server_not_found → mcp_auth_not_supported (stdio) →
// engine sentinels (501/503/409/502).
//
// The flow is started with context.Background(): it outlives this request (see
// the registry's Start). No body is read — the route is body-limited for
// symmetry with the other write routes, and a body, if sent, is ignored.
func (n *NativeChannel) handleMCPAuthStart(w http.ResponseWriter, r *http.Request) {
	// 1. name sanity — the same rule the toggle route applies to the same
	//    path variable.
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid server name", "invalid_request")
		return
	}

	// 2. agent_id → paths (writes its own error responses).
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	// 3. resolve the entry in the winning config.
	//
	// The Discover error is deliberately DISCARDED, and that is a decision, not
	// an oversight. Discover never fails fatally: its error is the whole-merge
	// WARNING aggregate (a layer file that did not parse, plus one wrapped
	// error per invalid entry ANYWHERE in any layer), and the result it returns
	// alongside it is always usable. Threading it into this route would
	// therefore be wrong twice over:
	//
	//   - it is not attributable to `name`. A name missing here because a layer
	//     file is corrupt is a different problem from one that is merely absent
	//     (or defined only in a layer that is not readable), and Discover cannot
	//     tell us which; the sibling routes that DO report mcp_read_failed /
	//     config_invalid (raw GET/PUT, validate) each read exactly ONE file, so
	//     their error is about that file by construction;
	//   - the common case would break. A single unrelated broken entry in any
	//     layer makes the aggregate non-nil (the fixture in
	//     rest_mcp_auth_test.go keeps one on purpose), so reporting it here
	//     would turn every start on a healthy server into an error about a
	//     server the caller never mentioned.
	//
	// The surface for these warnings is route 1: mcp.ReadInventory returns them
	// unmangled in MCPInventoryResponse.Warnings, so the UI already shows the
	// broken layer next to the row that it affects, and the "known but
	// unresolved" branch below still distinguishes a typo from an entry that
	// exists but cannot be authorized.
	merged, _ := mcp.Discover(paths)
	srv, resolved := merged.Servers[name]
	if !resolved {
		// Distinguish "no such name" from "known but not usable": a disabled
		// or invalid entry has no resolved config to authorize, and saying so
		// is the difference between a typo and an operator's next step.
		inv := mcp.ReadInventory(paths)
		if verdict, known := inv.Effective[name]; known {
			writeError(w, http.StatusNotFound,
				fmt.Sprintf("mcp server %q has no resolved configuration (verdict %q); repair it in the raw editor before authorizing",
					name, verdict), "mcp_server_not_found")
			return
		}
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("mcp server %q not found", name), "mcp_server_not_found")
		return
	}

	// 4. a local (stdio) entry has no URL and can never be an OAuth resource.
	//    The engine would answer its own unexported "no url" sentinel, which
	//    this layer cannot classify and would report as a generic 502, so the
	//    verdict is taken here: 501 mcp_auth_not_supported, the code T11 of
	//    .lele/tasks/mcp-oauth-plan.md pins for a stdio entry. The contract's
	//    mapping of the same code onto mcp.ErrAuthNotSupported (a remote entry
	//    that advertises no registration endpoint) is unchanged: both mean
	//    "this entry cannot perform the OAuth flow".
	//
	//    The test is NARROWER than the engine's errAuthNoURL on purpose: that
	//    sentinel fires for `URL == "" || Command != ""`, so it also rejects a
	//    MIXED entry that carries both a command and a url. Do NOT "fix" the
	//    condition below by widening it to match: mcp.ServerConfig.validate
	//    rejects exactly that mix ("mixes stdio and remote fields") on the READ
	//    path, so a mixed entry can never appear in merged.Servers — it is
	//    excluded from the merge (it shows up as effective:"invalid" in the
	//    inventory) and never reaches this line. Only `URL == ""` is reachable
	//    here, and it is reachable for EXACTLY the stdio entries.
	if srv.URL == "" {
		writeError(w, http.StatusNotImplemented,
			fmt.Sprintf("mcp server %q is a local (stdio) server: OAuth is not supported for entries without a url", name),
			"mcp_auth_not_supported")
		return
	}

	// 5. start the flow (OpenURL nil: the gateway never spawns a browser).
	flow, flowID, deadline, err := n.mcpAuthFlowRegistry().Start(context.Background(), agentID, name, srv)
	if err != nil {
		writeMCPAuthError(w, err, fmt.Sprintf("failed to start authorization for %q", name))
		return
	}

	// 6. project the flow: id, the URL the CLIENT must show, the redirect URI
	//    it registered (loopback: the real ephemeral port) and the registry
	//    deadline. No credential, ever.
	writeJSON(w, http.StatusOK, MCPAuthStart{
		FlowID:      flowID,
		AuthURL:     flow.URL(),
		RedirectURI: flow.RedirectURI(),
		ExpiresAt:   deadline,
	})
}

// handleMCPAuthStatus serves GET /api/v1/mcp/auth/{flow_id}?agent_id= — the
// poll. It is the ONE place the reload seam fires for a flow (exactly once, on
// the pending → done transition; see MarkReloaded).
//
// Validation order: agent_id → invalid_request (flow id shape) →
// mcp_auth_flow_not_found (unknown/expired/foreign id — indistinguishable by
// design).
func (n *NativeChannel) handleMCPAuthStatus(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}
	flowID, ok := mcpAuthFlowID(w, r.PathValue("flow_id"))
	if !ok {
		return
	}

	entry, ok := n.mcpAuthFlowRegistry().Get(agentID, flowID)
	if !ok {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("no live authorization flow %q for this agent", flowID), "mcp_auth_flow_not_found")
		return
	}
	n.writeMCPAuthStatus(w, agentID, paths, entry)
}

// handleMCPAuthComplete serves POST /api/v1/mcp/auth/{flow_id}/complete
// with body {"code":"…","state":"…"} — the manual half of the flow: the WebUI
// shows auth_url, the user logs in, the authorization server redirects to a
// URI this gateway does not serve, and the client pastes the code back.
//
// Validation order: agent_id → invalid_request (flow id shape) → body_invalid
// → invalid_request (blank code) → mcp_auth_flow_not_found → engine sentinels.
//
// Submit runs with r.Context(): the token exchange IS this request's work, and
// a client that abandons the request abandons the exchange. That is safe
// because a failed exchange leaves the flow open and retryable — the documented
// retry path, not a terminal failure.
func (n *NativeChannel) handleMCPAuthComplete(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}
	flowID, ok := mcpAuthFlowID(w, r.PathValue("flow_id"))
	if !ok {
		return
	}

	var req MCPAuthCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid body: %v", err), "body_invalid")
		return
	}
	// A blank code is a client error the engine would also refuse (its own
	// "empty code" sentinel is unexported): refusing it here names the missing
	// field instead of reporting a generic flow failure.
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code required", "invalid_request")
		return
	}

	if err := n.mcpAuthFlowRegistry().Complete(r.Context(), agentID, flowID, req.Code, req.State); err != nil {
		writeMCPAuthError(w, err, "authorization failed")
		return
	}

	// Success: the flow is terminal ("done") and stays resolvable until its
	// deadline, so the answer is the SAME status DTO a poll would return — the
	// client needs no second request to learn the outcome.
	entry, ok := n.mcpAuthFlowRegistry().Get(agentID, flowID)
	if !ok {
		// Unreachable: Complete just resolved it and only the sweep removes
		// entries. Answered as a missing flow rather than a nil dereference.
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("no live authorization flow %q for this agent", flowID), "mcp_auth_flow_not_found")
		return
	}
	n.writeMCPAuthStatus(w, agentID, paths, entry)
}

// writeMCPAuthStatus writes one entry as MCPAuthStatus and fires the reload
// seam if — and only if — this call is the one that observes the completed
// flow (MarkReloaded is the atomic claim, so concurrent polls reload once).
//
// Order matters: the claim is taken BEFORE the response is written, so a client
// that immediately re-reads the inventory cannot race the reload it just
// triggered.
func (n *NativeChannel) writeMCPAuthStatus(w http.ResponseWriter, agentID string, paths mcp.Paths, entry *mcpAuthEntry) {
	store := mcp.NewAuthStore(paths.LeleDir)
	resp := mcpAuthStatusFor(store, entry)
	if resp.State == mcpAuthStateDone &&
		n.mcpAuthFlowRegistry().MarkReloaded(agentID, entry.flowID) {
		n.fireReloadMCP()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleMCPAuthCancel serves DELETE /api/v1/mcp/auth/{flow_id}?agent_id= —
// stop a flow and release its loopback listener.
//
// Best-effort-idempotent (invariant 7): 204 for an already-terminal flow that is
// still in the registry, 404 only once the id no longer resolves (unknown,
// expired, swept, or another agent's). Cancelling is what frees the port, so a
// client that abandons a login must call this rather than wait for the TTL.
func (n *NativeChannel) handleMCPAuthCancel(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if _, ok := n.mcpPathsFor(w, agentID); !ok {
		return
	}
	flowID, ok := mcpAuthFlowID(w, r.PathValue("flow_id"))
	if !ok {
		return
	}

	if err := n.mcpAuthFlowRegistry().Cancel(agentID, flowID); err != nil {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("no live authorization flow %q for this agent", flowID), "mcp_auth_flow_not_found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMCPAuthLogout serves DELETE /api/v1/mcp/servers/{name}/auth?agent_id= —
// forget everything stored about one server (token AND dynamic registration)
// through mcp.Logout, the same primitive `lele mcp logout` and the TUI use.
//
// It also cancels any live flow for that pair first: a login that completes
// after the logout would otherwise re-store the credential the user just
// removed.
//
// A name that exists nowhere AND has nothing stored is 404 mcp_server_not_found
// (a typo must be visible); a known name with no stored record is a successful
// no-op 204, so the route is idempotent from the client's point of view.
// The reload fires exactly once on the 204 path.
func (n *NativeChannel) handleMCPAuthLogout(w http.ResponseWriter, r *http.Request) {
	// 1. name sanity (same rule as every other {name} route).
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid server name", "invalid_request")
		return
	}

	// 2. agent_id → paths.
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	store := mcp.NewAuthStore(paths.LeleDir)

	// 3. refuse only what can be neither a server nor a stored record.
	inv := mcp.ReadInventory(paths)
	if _, known := inv.Effective[name]; !known {
		if _, stored := store.Record(name); !stored {
			writeError(w, http.StatusNotFound,
				fmt.Sprintf("mcp server %q not found", name), "mcp_server_not_found")
			return
		}
	}

	// 4. no flow may outlive the logout for this pair.
	_ = n.mcpAuthFlowRegistry().CancelPair(agentID, name)

	// 5. forget (idempotent: a missing record is a no-op).
	if err := mcp.Logout(store, name); err != nil {
		writeMCPAuthError(w, err, fmt.Sprintf("failed to log out %q", name))
		return
	}

	// 6. reload, exactly once, only after a successful logout.
	n.fireReloadMCP()
	w.WriteHeader(http.StatusNoContent)
}
