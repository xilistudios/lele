package channels

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/xilistudios/lele/pkg/locales"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// LocalesListResponse is the payload for GET /api/v1/locales.
type LocalesListResponse struct {
	Languages []locales.Status `json:"languages"`
	Current   string           `json:"current"`
	// CacheDir is the on-disk pack directory (informational).
	CacheDir string `json:"cache_dir"`
}

// LocalePackResponse is the payload for GET /api/v1/locales/{code}.
type LocalePackResponse struct {
	Code   string          `json:"code"`
	Web    json.RawMessage `json:"web"`
	Source string          `json:"source"` // "builtin" | "cache"
}

// LocaleInstallResponse is the payload for POST /api/v1/locales/{code}/install.
type LocaleInstallResponse struct {
	Code      string `json:"code"`
	Installed bool   `json:"installed"`
}

// getLocalesManager returns the channel's locales manager, creating one lazily.
func (n *NativeChannel) getLocalesManager() *locales.Manager {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.localesMgr == nil {
		n.localesMgr = locales.NewManagerWithCacheDir(locales.JoinCacheDir(n.leleDir))
	}
	return n.localesMgr
}

// handleLocalesList lists available languages with install flags.
// GET /api/v1/locales
func (n *NativeChannel) handleLocalesList(w http.ResponseWriter, r *http.Request) {
	mgr := n.getLocalesManager()
	list := mgr.List(r.Context())
	current := "es"
	if n.agentLoop != nil {
		if cfg := n.agentLoop.GetConfigSnapshot(); cfg != nil {
			current = cfg.GetLanguage()
		}
	}
	writeJSON(w, http.StatusOK, LocalesListResponse{
		Languages: list,
		Current:   current,
		CacheDir:  mgr.CacheDir(),
	})
}

// handleLocaleGet returns the WebUI translation pack for a language.
// Builtins are served from the binary-bundled JSON that the SPA already has;
// for them we still return a minimal marker so clients can detect builtin.
// Downloaded packs return the cached nested JSON.
// GET /api/v1/locales/{code}
func (n *NativeChannel) handleLocaleGet(w http.ResponseWriter, r *http.Request) {
	code := locales.NormalizeCode(r.PathValue("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "language code required", "code_required")
		return
	}

	mgr := n.getLocalesManager()

	// Builtins: the SPA bundles them; return source=builtin with empty web
	// so the client keeps using its bundled resources.
	if code == "es" || code == "en" || code == "pt" {
		writeJSON(w, http.StatusOK, LocalePackResponse{
			Code:   code,
			Web:    json.RawMessage(`{}`),
			Source: "builtin",
		})
		return
	}

	if !mgr.IsInstalled(code) {
		// Try a just-in-time install so WebUI can switch without a prior POST.
		if err := mgr.Install(r.Context(), code); err != nil {
			writeError(w, http.StatusNotFound, "language pack not installed: "+err.Error(), "locale_not_installed")
			return
		}
		// Reload TUI packs so a running TUI process (same host) can pick it up.
		i18n.LoadExternalPacks()
	}

	web, err := mgr.GetWeb(code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "locale_read_failed")
		return
	}
	writeJSON(w, http.StatusOK, LocalePackResponse{
		Code:   code,
		Web:    web,
		Source: "cache",
	})
}

// handleLocaleInstall downloads a language pack from GitHub into the cache.
// POST /api/v1/locales/{code}/install
func (n *NativeChannel) handleLocaleInstall(w http.ResponseWriter, r *http.Request) {
	code := locales.NormalizeCode(r.PathValue("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "language code required", "code_required")
		return
	}

	mgr := n.getLocalesManager()
	if code == "es" || code == "en" || code == "pt" {
		writeJSON(w, http.StatusOK, LocaleInstallResponse{Code: code, Installed: true})
		return
	}

	if err := mgr.Install(r.Context(), code); err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "404") {
			status = http.StatusNotFound
		}
		logger.WarnCF("native", "locale install failed", map[string]interface{}{
			"code":  code,
			"error": err.Error(),
		})
		writeError(w, status, err.Error(), "locale_install_failed")
		return
	}

	loaded := i18n.LoadExternalPacks()
	logger.InfoCF("native", "locale pack installed", map[string]interface{}{
		"code":   code,
		"loaded": loaded,
	})

	writeJSON(w, http.StatusOK, LocaleInstallResponse{Code: code, Installed: true})
}

// handleLocaleUninstall removes a cached pack.
// DELETE /api/v1/locales/{code}
func (n *NativeChannel) handleLocaleUninstall(w http.ResponseWriter, r *http.Request) {
	code := locales.NormalizeCode(r.PathValue("code"))
	if code == "es" || code == "en" || code == "pt" {
		writeError(w, http.StatusBadRequest, "cannot remove builtin language", "locale_builtin")
		return
	}
	mgr := n.getLocalesManager()
	if err := mgr.Uninstall(code); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "locale_uninstall_failed")
		return
	}
	i18n.LoadExternalPacks()
	writeJSON(w, http.StatusOK, map[string]any{"code": code, "installed": false})
}

// handleLocalesRefresh forces a catalog refresh from GitHub.
// POST /api/v1/locales/refresh
func (n *NativeChannel) handleLocalesRefresh(w http.ResponseWriter, r *http.Request) {
	mgr := n.getLocalesManager()
	cat, err := mgr.RefreshCatalog(r.Context())
	if err != nil && cat == nil {
		writeError(w, http.StatusBadGateway, err.Error(), "locale_catalog_failed")
		return
	}
	list := mgr.List(r.Context())
	writeJSON(w, http.StatusOK, LocalesListResponse{
		Languages: list,
		CacheDir:  mgr.CacheDir(),
	})
}
