package channels

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/xilistudios/lele/pkg/locales"
	"github.com/xilistudios/lele/pkg/logger"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

type LocalesListResponse struct {
	Languages []locales.Status `json:"languages"`
	Current   string           `json:"current"`
	CacheDir  string           `json:"cache_dir"`
}

type LocalePackResponse struct {
	Code   string          `json:"code"`
	Web    json.RawMessage `json:"web"`
	Source string          `json:"source"`
}

type LocaleInstallResponse struct {
	Code      string `json:"code"`
	Installed bool   `json:"installed"`
}

func (n *NativeChannel) getLocalesManager() *locales.Manager {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.localesMgr == nil {
		n.localesMgr = locales.NewManagerWithCacheDir(locales.JoinCacheDir(n.leleDir))
	}
	return n.localesMgr
}

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

func (n *NativeChannel) handleLocaleGet(w http.ResponseWriter, r *http.Request) {
	code := locales.NormalizeCode(r.PathValue("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "language code required", "code_required")
		return
	}

	mgr := n.getLocalesManager()

	if code == "es" || code == "en" || code == "pt" {
		writeJSON(w, http.StatusOK, LocalePackResponse{
			Code:   code,
			Web:    json.RawMessage(`{}`),
			Source: "builtin",
		})
		return
	}

	if !mgr.IsInstalled(code) {
		if err := mgr.Install(r.Context(), code); err != nil {
			writeError(w, http.StatusNotFound, "language pack not installed: "+err.Error(), "locale_not_installed")
			return
		}
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
