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

// SetUpdateService sets the self-update service for API access.
func (n *NativeChannel) SetUpdateService(us *update.Updater) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.updateService = us
}

// GetUpdateService returns the self-update service, or nil if none was set.
// The gateway uses it to attach its graceful-shutdown callback to the service's
// Restarter, which is the only way to reach the Restarter the API actually
// calls: it is owned by the Updater, not by the gateway.
func (n *NativeChannel) GetUpdateService() *update.Updater {
	return n.getUpdateService()
}

// handleSystemVersion returns build and runtime information.
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

// handleUpdatesCheck queries GitHub for the latest release.
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

// handleUpdatesApply starts the update pipeline asynchronously.
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
	// The pipeline may end in a self-exec restart that exits this process; make
	// sure the ack is on the wire before the goroutine can get there.
	flushAck(w)
}

// handleUpdatesStatus returns the current pipeline state.
func (n *NativeChannel) handleUpdatesStatus(w http.ResponseWriter, r *http.Request) {
	us := n.getUpdateService()
	if us == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available", "update_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, us.State())
}

// handleUpdatesRollback restores the previous binary.
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

// handleSystemRestart restarts the service via the detected supervisor.
func (n *NativeChannel) handleSystemRestart(w http.ResponseWriter, r *http.Request) {
	us := n.getUpdateService()
	if us == nil {
		writeError(w, http.StatusServiceUnavailable, "update service not available", "update_unavailable")
		return
	}

	// Acknowledge first, then restart shortly after so the response lands.
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	// Flush the ack explicitly: on the self-exec path Restart() terminates this
	// process (os.Exit), so a buffered 202 body that has not reached the socket
	// would be lost and the client would see a dropped connection instead of
	// the acknowledgement it is waiting for.
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

// flushAck pushes any buffered response body out to the client. ResponseWriter
// only guarantees flushing when it implements http.Flusher (it does for the
// net/http one), so the type assertion is the documented pattern.
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

// updatesEnabled checks the runtime config for updates.enabled.
func (n *NativeChannel) updatesEnabled() bool {
	cfg, err := config.LoadConfig(n.configPath)
	if err != nil {
		return true // default enabled if config unreadable
	}
	return cfg.Updates.Enabled
}

func boolPtr(b bool) *bool { return &b }

// Build info is injected at link time in package main; expose via vars here.
var (
	systemVersion   = "dev"
	systemGitCommit = ""
	systemBuildTime = ""
)

// SetBuildInfo is called by the gateway to expose build metadata.
func SetBuildInfo(version, commit, buildTime string) {
	systemVersion = version
	systemGitCommit = commit
	systemBuildTime = buildTime
}

func currentBuildVersion() string { return systemVersion }
func currentBuildCommit() string  { return systemGitCommit }
func currentBuildTime() string    { return systemBuildTime }
