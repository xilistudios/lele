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

const mcpRawFileMaxBytes = 1 << 20

const mcpLayerAuto = "auto"

type mcpPathsSource interface {
	MCPPathsFor(agentID string) (mcp.Paths, bool)
}

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

type MCPInventoryResponse struct {
	AgentID  string         `json:"agent_id"`
	Layers   []MCPLayer     `json:"layers,omitempty"`
	Servers  []MCPServerRow `json:"servers,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
}

type MCPLayer struct {
	Layer       string   `json:"layer"`
	Path        string   `json:"path,omitempty"`
	Exists      bool     `json:"exists"`
	AliasedWith []string `json:"aliased_with,omitempty"`
}

type MCPServerRow struct {
	Name      string           `json:"name"`
	Layer     string           `json:"layer"`
	Path      string           `json:"path,omitempty"`
	Effective string           `json:"effective"`
	Defines   bool             `json:"defines"`
	Shadowed  []MCPShadow      `json:"shadowed,omitempty"`
	Server    MCPServerSummary `json:"server"`
}

type MCPShadow struct {
	Layer  string `json:"layer"`
	Path   string `json:"path,omitempty"`
	Reason string `json:"reason"`
}

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

	Auth *MCPServerAuth `json:"auth,omitempty"`
}

type MCPServerAuth struct {
	Required      bool   `json:"required"`
	Authenticated bool   `json:"authenticated"`
	Method        string `json:"method"`
}

type MCPRawFileResponse struct {
	Layer       string   `json:"layer"`
	Path        string   `json:"path,omitempty"`
	Exists      bool     `json:"exists"`
	Content     string   `json:"content"`
	AliasedWith []string `json:"aliased_with,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

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

func mcpRowAuth(store *mcp.AuthStore, merged mcp.DiscoveryResult, name string, winner mcp.EntryState) *MCPServerAuth {
	srv, resolved := merged.Servers[name]
	if !resolved {
		var ok bool
		if srv, ok = mcpWinnerConfig(name, winner); !ok {
			return nil
		}
	}
	return mcpAuthFor(mcp.CheckAuth(store, name, srv))
}

func mcpWinnerConfig(name string, winner mcp.EntryState) (mcp.ServerConfig, bool) {
	file, err := mcp.ParseFile(winner.Path)
	if err != nil || file == nil {
		return mcp.ServerConfig{}, false
	}
	srv, ok := file.MCPServers[name]
	return srv, ok
}

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

func (n *NativeChannel) handleMCPInventory(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	inv := mcp.ReadInventory(paths)
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

func (n *NativeChannel) handleMCPRawDoc(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("doc") != "raw" {
		http.NotFound(w, r)
		return
	}
	n.handleMCPRawFile(w, r)
}

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

	if !mcpGuardLayerFile(w, file) {
		return
	}

	resp := MCPRawFileResponse{Layer: layer, Path: file}
	for _, other := range mcp.Layers() {
		if other != layer && mcp.LayerFile(paths, other) == file {
			resp.AliasedWith = append(resp.AliasedWith, other)
		}
	}

	info, err := os.Stat(file)
	switch {
	case os.IsNotExist(err):
		writeJSON(w, http.StatusOK, resp)
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
		writeJSON(w, http.StatusOK, resp)
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

type MCPToggleRequest struct {
	Enabled bool `json:"enabled"`
}

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

func (n *NativeChannel) fireReloadMCP() {
	n.mu.Lock()
	fn := n.reloadMCP
	n.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func mcpCopyInLayer(copies []mcp.EntryState, layer string) (mcp.EntryState, bool) {
	for _, c := range copies {
		if c.Layer == layer {
			return c, true
		}
	}
	return mcp.EntryState{}, false
}

func mcpLayerRank(layer string) int {
	for i, l := range mcp.Layers() {
		if l == layer {
			return i
		}
	}
	return -1
}

func mcpGuardLayerFile(w http.ResponseWriter, file string) bool {
	if filepath.Base(file) != "mcp.json" || !isAllowedWorkspacePath(filepath.Dir(file)) {
		writeError(w, http.StatusForbidden,
			fmt.Sprintf("layer file %q is outside the allowed workspace roots", file),
			"mcp_path_not_allowed")
		return false
	}
	return true
}

func (n *NativeChannel) handleMCPToggle(w http.ResponseWriter, r *http.Request) {
	layer := r.PathValue("layer")
	switch layer {
	case mcp.LayerGlobal, mcp.LayerAgent, mcp.LayerProject, mcpLayerAuto:
	default:
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid layer %q: use global, agent, project or auto", layer), "invalid_layer")
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid server name", "invalid_request")
		return
	}

	var req MCPToggleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid body: %v", err), "body_invalid")
		return
	}

	inv := mcp.ReadInventory(paths)
	copies, known := inv.ByName[name]
	if !known {
		writeError(w, http.StatusNotFound,
			fmt.Sprintf("mcp server %q not found", name), "mcp_server_not_found")
		return
	}

	if inv.Effective[name] == "invalid" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("mcp server %q has verdict %q and cannot be toggled; repair it in the raw editor (GET/PUT /api/v1/mcp/{layer}/raw)",
				name, inv.Effective[name]),
			"mcp_entry_invalid")
		return
	}

	if layer == mcpLayerAuto {
		layer = inv.Winner[name]
		if layer == "" {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("cannot resolve the owning layer of %q", name), "mcp_layer_required")
			return
		}
	}

	_, inLayer := mcpCopyInLayer(copies, layer)

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

	if !req.Enabled && !inLayer && mcpLayerRank(layer) < mcpLayerRank(inv.Winner[name]) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("cannot create a stub for %q in layer %q: it sits below the winning layer %q; disable it in the layer that owns it",
				name, layer, inv.Winner[name]),
			"mcp_stub_layer_not_allowed")
		return
	}

	file := mcp.LayerFile(paths, layer)
	if file == "" {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("layer %q has no root for this agent", layer), "mcp_layer_unavailable")
		return
	}

	if !mcpGuardLayerFile(w, file) {
		return
	}

	res, err := mcp.SetDisabled(file, name, !req.Enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to write %s: %v", file, err), "mcp_write_failed")
		return
	}

	n.fireReloadMCP()

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

type MCPRawPutRequest struct {
	Content string `json:"content"`
}

func (n *NativeChannel) handleMCPRawPut(w http.ResponseWriter, r *http.Request) {
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

	if !mcpGuardLayerFile(w, file) {
		return
	}

	var req MCPRawPutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid body: %v", err), "body_invalid")
		return
	}

	if len(req.Content) > mcpRawFileMaxBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			"content exceeds 1 MiB", "mcp_file_too_large")
		return
	}

	warnings, verr := mcp.ValidateRawJSON([]byte(req.Content))
	if verr != nil {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("invalid mcp.json: %v", verr), "config_invalid")
		return
	}

	if err := mcp.SaveRawFile(file, []byte(req.Content)); err != nil {
		writeError(w, http.StatusInternalServerError,
			fmt.Sprintf("failed to write %s: %v", file, err), "mcp_write_failed")
		return
	}

	n.fireReloadMCP()

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

type MCPValidateRequest struct {
	Layer   string `json:"layer"`
	Content string `json:"content"`
}

type MCPValidateResponse struct {
	Valid    bool     `json:"valid"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func (n *NativeChannel) handleMCPValidate(w http.ResponseWriter, r *http.Request) {
	agentID := r.URL.Query().Get("agent_id")
	if _, ok := n.mcpPathsFor(w, agentID); !ok {
		return
	}

	var req MCPValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid body: %v", err), "body_invalid")
		return
	}

	switch req.Layer {
	case mcp.LayerGlobal, mcp.LayerAgent, mcp.LayerProject, mcpLayerAuto:
	default:
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid layer %q: use global, agent, project or auto", req.Layer), "invalid_layer")
		return
	}

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

const mcpAuthStateDone = "done"

type MCPAuthStart struct {
	FlowID      string    `json:"flow_id"`
	AuthURL     string    `json:"auth_url"`
	RedirectURI string    `json:"redirect_uri"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type MCPAuthCompleteRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type MCPAuthStatus struct {
	FlowID        string `json:"flow_id"`
	Server        string `json:"server"`
	State         string `json:"state"`
	Error         string `json:"error,omitempty"`
	Authenticated bool   `json:"authenticated"`
}

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

func writeMCPAuthError(w http.ResponseWriter, err error, what string) {
	status, code := mcpAuthErrorCode(err)
	writeError(w, status, fmt.Sprintf("%s: %v", what, err), code)
}

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

func mcpAuthFlowID(w http.ResponseWriter, id string) (string, bool) {
	if !isValidMCPFlowID(id) {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid flow_id %q", id), "invalid_request")
		return "", false
	}
	return id, true
}

func (n *NativeChannel) handleMCPAuthStart(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid server name", "invalid_request")
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	merged, _ := mcp.Discover(paths)
	srv, resolved := merged.Servers[name]
	if !resolved {
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

	if srv.URL == "" {
		writeError(w, http.StatusNotImplemented,
			fmt.Sprintf("mcp server %q is a local (stdio) server: OAuth is not supported for entries without a url", name),
			"mcp_auth_not_supported")
		return
	}

	flow, flowID, deadline, err := n.mcpAuthFlowRegistry().Start(context.Background(), agentID, name, srv)
	if err != nil {
		writeMCPAuthError(w, err, fmt.Sprintf("failed to start authorization for %q", name))
		return
	}

	writeJSON(w, http.StatusOK, MCPAuthStart{
		FlowID:      flowID,
		AuthURL:     flow.URL(),
		RedirectURI: flow.RedirectURI(),
		ExpiresAt:   deadline,
	})
}

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
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code required", "invalid_request")
		return
	}

	if err := n.mcpAuthFlowRegistry().Complete(r.Context(), agentID, flowID, req.Code, req.State); err != nil {
		writeMCPAuthError(w, err, "authorization failed")
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

func (n *NativeChannel) writeMCPAuthStatus(w http.ResponseWriter, agentID string, paths mcp.Paths, entry *mcpAuthEntry) {
	store := mcp.NewAuthStore(paths.LeleDir)
	resp := mcpAuthStatusFor(store, entry)
	if resp.State == mcpAuthStateDone &&
		n.mcpAuthFlowRegistry().MarkReloaded(agentID, entry.flowID) {
		n.fireReloadMCP()
	}
	writeJSON(w, http.StatusOK, resp)
}

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

func (n *NativeChannel) handleMCPAuthLogout(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid server name", "invalid_request")
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	paths, ok := n.mcpPathsFor(w, agentID)
	if !ok {
		return
	}

	store := mcp.NewAuthStore(paths.LeleDir)

	inv := mcp.ReadInventory(paths)
	if _, known := inv.Effective[name]; !known {
		if _, stored := store.Record(name); !stored {
			writeError(w, http.StatusNotFound,
				fmt.Sprintf("mcp server %q not found", name), "mcp_server_not_found")
			return
		}
	}

	_ = n.mcpAuthFlowRegistry().CancelPair(agentID, name)

	if err := mcp.Logout(store, name); err != nil {
		writeMCPAuthError(w, err, fmt.Sprintf("failed to log out %q", name))
		return
	}

	n.fireReloadMCP()
	w.WriteHeader(http.StatusNoContent)
}
