package channels

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// maxFolderLen caps the length of a folder path accepted from the client so a
// PATCH body cannot stuff an absurd string into the session record (the path is
// later echoed into the system prompt).
const maxFolderLen = 4096

// SessionFolderResponse is the payload of GET/PATCH
// /api/v1/chat/sessions/{sessionKey}/folder. Folder carries the effective
// absolute path stored for the session ("" when none is selected).
type SessionFolderResponse struct {
	SessionKey string `json:"session_key"`
	Folder     string `json:"folder"`
}

// SessionFolderUpdateRequest is the PATCH body. An empty folder clears the
// session's selection.
type SessionFolderUpdateRequest struct {
	Folder string `json:"folder"`
}

// handleSessionFolder reads or sets the folder the user selected for a session.
//
// GET   /api/v1/chat/sessions/{sessionKey}/folder -> {"session_key","folder"}
// PATCH /api/v1/chat/sessions/{sessionKey}/folder <- {"folder": "<path>"}
//
// Setting a folder is validated before it is stored, because the path (and a
// first-level listing of it) is injected into the session's system prompt:
//   - empty folder clears the selection;
//   - "~" is expanded and the path is made absolute;
//   - symlinks are resolved; an unresolvable path is a 404;
//   - the target must exist (404) and be a directory (400);
//   - BOTH the literal and the resolved path must satisfy
//     isAllowedWorkspacePath, otherwise 403 — this blocks pointing the agent at
//     system trees and blocks escaping an allowed tree through a symlink.
//
// The response always carries the effective resolved path.
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
		// Clearing needs no validation: "" is always a safe value.
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

// resolveSessionFolder normalises and validates a user-supplied folder path.
// It returns the effective absolute (symlink-resolved) path, or a non-zero
// HTTP status plus error code/message describing why the path was rejected.
func resolveSessionFolder(folder string) (resolved string, status int, code, message string) {
	abs, err := filepath.Abs(expandHomePath(folder))
	if err != nil {
		return "", http.StatusBadRequest, "folder_bad_path", "invalid path: " + err.Error()
	}
	abs = filepath.Clean(abs)

	// Gate the literal path first so traversal ("../") never reaches the OS.
	if !isAllowedWorkspacePath(abs) {
		return "", http.StatusForbidden, "folder_forbidden", "access denied"
	}

	// Then gate the real path: a symlink (or ".." through one) must not land
	// outside the allowed trees.
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
