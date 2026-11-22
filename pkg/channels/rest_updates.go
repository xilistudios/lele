package channels

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/xilistudios/lele/pkg/config"
	"github.com/xilistudios/lele/pkg/update"
)

func (n *NativeChannel) SetUpdateService(us *update.Updater) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.updateService = us
}

func (n *NativeChannel) GetUpdateService() *update.Updater {
	return n.getUpdateService()
}

func (n *NativeChannel) handleSystemVersion(w http.ResponseWriter, r *http.Request) {
	us := n.getUpdateService()
	binPath, _ := update.CurrentBinaryPath()

	resp := map[string]interface{}{
		"version":    currentBuildVersion(),
		"git_commit": currentBuildCommit(),
		"build_time": currentBuildTime(),
		"go_version": runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"binary":     binPath,
		"supervisor": "none",
		"dev_build":  update.IsDevBuild(currentBuildVersion()),
		"has_backup": false,
	}
	if us != nil {
		resp["supervisor"] = string(us.Restarter.DetectSupervisor())
		if latest, err := us.Installer.LatestBackup(); err == nil && latest != "" {
			resp["has_backup"] = true
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (n *NativeChannel) handleUpdatesCheck(w http.ResponseWriter, r *http.Request) {
	us := n.getUpdateService()
	if us == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available", "update_unavailable")
		return
	}
	if !n.updatesEnabled() {
		writeError(w, http.StatusForbidden, "self-update is disabled in config", "updates_disabled")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	info, err := us.Check(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "update_check_failed")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (n *NativeChannel) handleUpdatesApply(w http.ResponseWriter, r *http.Request) {
	us := n.getUpdateService()
	if us == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available", "update_unavailable")
		return
	}
	if !n.updatesEnabled() {
		writeError(w, http.StatusForbidden, "self-update is disabled in config", "updates_disabled")
		return
	}
	if us.Busy() {
		writeError(w, http.StatusConflict, "an update is already in progress", "update_in_progress")
		return
	}
	if err := update.CheckEnvironment(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "unsupported_environment")
		return
	}

	var body struct {
		Version string `json:"version"`
		Restart *bool  `json:"restart"`
	}
	body.Restart = boolPtr(true)
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body)
	}

	restart := body.Restart == nil || *body.Restart
	fromVersion := us.CurrentVersion

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		newVer, err := us.Apply(ctx, update.Options{
			Version: body.Version,
			Restart: restart,
			Progress: func(s update.State) {
				n.broadcastAll("update.progress", s)
			},
		})

		if err != nil {
			n.broadcastAll("update.failed", map[string]string{"error": err.Error()})
			return
		}
		n.broadcastAll("update.completed", map[string]interface{}{
			"from":       fromVersion,
			"to":         newVer,
			"restarting": restart,
		})
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	flushAck(w)
}

func (n *NativeChannel) handleUpdatesStatus(w http.ResponseWriter, r *http.Request) {
	us := n.getUpdateService()
	if us == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available", "update_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, us.State())
}

func (n *NativeChannel) handleUpdatesRollback(w http.ResponseWriter, r *http.Request) {
	us := n.getUpdateService()
	if us == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available", "update_unavailable")
		return
	}
	if !n.updatesEnabled() {
		writeError(w, http.StatusForbidden, "self-update is disabled in config", "updates_disabled")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()

	path, err := us.Rollback(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "rollback_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"restored": path})
}

func (n *NativeChannel) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	us := n.getUpdateService()
	if us == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available", "update_unavailable")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	flushAck(w)

	go func() {
		time.Sleep(500 * time.Millisecond)
		method, err := us.Restarter.Restart()
		if err != nil {
			n.broadcastAll("update.failed", map[string]string{"error": "restart failed: " + err.Error()})
			return
		}
		n.broadcastAll("update.progress", update.State{Phase: update.PhaseRestarting, Error: method})
	}()
}

func flushAck(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func (n *NativeChannel) getUpdateService() *update.Updater {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.updateService
}

func (n *NativeChannel) updatesEnabled() bool {
	cfg, err := config.LoadConfig(n.configPath)
	if err != nil {
		return true
	}
	return cfg.Updates.Enabled
}

func boolPtr(b bool) *bool { return &b }

var (
	systemVersion   = "dev"
	systemGitCommit = ""
	systemBuildTime = ""
)

func SetBuildInfo(version, commit, buildTime string) {
	systemVersion = version
	systemGitCommit = commit
	systemBuildTime = buildTime
}

func currentBuildVersion() string { return systemVersion }
func currentBuildCommit() string  { return systemGitCommit }
func currentBuildTime() string    { return systemBuildTime }
