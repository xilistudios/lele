package channels

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const maxFolderLen = 4096

type SessionFolderResponse struct {
	SessionKey string `json:"session_key"`
	Folder     string `json:"folder"`
}

type SessionFolderUpdateRequest struct {
	Folder string `json:"folder"`
}

func (n *NativeChannel) handleSessionFolder(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.PathValue("sessionKey")
	clientID := getClientID(r)

	if !n.validateSessionOwnership(clientID, sessionKey) {
		writeError(w, http.StatusForbidden, "access denied to this session", "session_forbidden")
		return
	}

	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, SessionFolderResponse{
			SessionKey: sessionKey,
			Folder:     n.agentLoop.GetSessionFolder(sessionKey),
		})
		return
	}

	var req SessionFolderUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", "body_invalid")
		return
	}

	folder := strings.TrimSpace(req.Folder)
	if folder == "" {
		writeJSON(w, http.StatusOK, SessionFolderResponse{
			SessionKey: sessionKey,
			Folder:     n.agentLoop.SetSessionFolder(sessionKey, ""),
		})
		return
	}

	if len(folder) > maxFolderLen {
		writeError(w, http.StatusBadRequest, "folder path too long", "folder_too_long")
		return
	}

	resolved, status, code, message := resolveSessionFolder(folder)
	if status != 0 {
		writeError(w, status, message, code)
		return
	}

	writeJSON(w, http.StatusOK, SessionFolderResponse{
		SessionKey: sessionKey,
		Folder:     n.agentLoop.SetSessionFolder(sessionKey, resolved),
	})
}

func resolveSessionFolder(folder string) (resolved string, status int, code, message string) {
	abs, err := filepath.Abs(expandHomePath(folder))
	if err != nil {
		return "", http.StatusBadRequest, "folder_bad_path", "invalid path: " + err.Error()
	}
	abs = filepath.Clean(abs)

	if !isAllowedWorkspacePath(abs) {
		return "", http.StatusForbidden, "folder_forbidden", "access denied"
	}

	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", http.StatusNotFound, "folder_not_found", "folder not found"
	}
	real = filepath.Clean(real)
	if !isAllowedWorkspacePath(real) {
		return "", http.StatusForbidden, "folder_forbidden", "access denied"
	}

	info, err := os.Stat(real)
	if err != nil {
		return "", http.StatusNotFound, "folder_not_found", "folder not found"
	}
	if !info.IsDir() {
		return "", http.StatusBadRequest, "folder_not_dir", "path is not a directory"
	}

	return real, 0, "", ""
}
