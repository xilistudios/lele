package channels

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/xilistudios/lele/pkg/providers"
)

func folderPatch(t *testing.T, ts *nativeTestServer, sessionKey, folder string) (int, SessionFolderResponse, string) {
	t.Helper()

	body, err := json.Marshal(SessionFolderUpdateRequest{Folder: folder})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	u := ts.server.URL + "/api/v1/chat/sessions/" + url.PathEscape(sessionKey) + "/folder"
	req, err := http.NewRequest(http.MethodPatch, u, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		var payload SessionFolderResponse
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode payload: %v (body=%s)", err, raw)
		}
		return resp.StatusCode, payload, ""
	}
	var apiErr APIError
	if err := json.Unmarshal(raw, &apiErr); err != nil {
		t.Fatalf("decode APIError: %v (body=%s)", err, raw)
	}
	return resp.StatusCode, SessionFolderResponse{}, apiErr.Code
}

func folderGet(t *testing.T, ts *nativeTestServer, sessionKey string) (int, SessionFolderResponse, string) {
	t.Helper()

	u := ts.server.URL + "/api/v1/chat/sessions/" + url.PathEscape(sessionKey) + "/folder"
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK {
		var payload SessionFolderResponse
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode payload: %v (body=%s)", err, raw)
		}
		return resp.StatusCode, payload, ""
	}
	var apiErr APIError
	if err := json.Unmarshal(raw, &apiErr); err != nil {
		t.Fatalf("decode APIError: %v (body=%s)", err, raw)
	}
	return resp.StatusCode, SessionFolderResponse{}, apiErr.Code
}

func TestSessionFolder_SetGetClear(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	dir := fsTestRoot(t)
	if err := os.Mkdir(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}

	status, payload, code := folderPatch(t, ts, sessionKey, dir)
	if status != http.StatusOK {
		t.Fatalf("PATCH status = %d (code=%s), want 200", status, code)
	}
	if payload.SessionKey != sessionKey {
		t.Errorf("session_key = %q, want %q", payload.SessionKey, sessionKey)
	}
	want := resolve(t, dir)
	if payload.Folder != want {
		t.Errorf("folder = %q, want %q", payload.Folder, want)
	}

	status, got, code := folderGet(t, ts, sessionKey)
	if status != http.StatusOK {
		t.Fatalf("GET status = %d (code=%s), want 200", status, code)
	}
	if got.Folder != want {
		t.Errorf("GET folder = %q, want %q", got.Folder, want)
	}

	status, cleared, code := folderPatch(t, ts, sessionKey, "")
	if status != http.StatusOK {
		t.Fatalf("PATCH \"\" status = %d (code=%s), want 200", status, code)
	}
	if cleared.Folder != "" {
		t.Errorf("after clear folder = %q, want \"\"", cleared.Folder)
	}
	if _, got, _ := folderGet(t, ts, sessionKey); got.Folder != "" {
		t.Errorf("GET after clear = %q, want \"\"", got.Folder)
	}
}

func TestSessionFolder_AcceptsTrailingSlashAndHome(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	dir := fsTestRoot(t)
	if _, payload, code := folderPatch(t, ts, sessionKey, dir+string(filepath.Separator)); code != "" {
		t.Fatalf("PATCH with trailing slash failed: code=%s", code)
	} else if got := resolve(t, dir); payload.Folder != got {
		t.Errorf("trailing slash folder = %q, want %q", payload.Folder, got)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	if _, payload, code := folderPatch(t, ts, sessionKey, "~"); code != "" {
		t.Fatalf("PATCH ~ failed: code=%s", code)
	} else if payload.Folder != resolve(t, home) {
		t.Errorf("PATCH ~ folder = %q, want %q", payload.Folder, resolve(t, home))
	}
}

func TestSessionFolder_RejectsFile(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	file := filepath.Join(fsTestRoot(t), "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	status, _, code := folderPatch(t, ts, sessionKey, file)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}
	if code != "folder_not_dir" {
		t.Errorf("code = %q, want folder_not_dir", code)
	}
}

func TestSessionFolder_RejectsMissing(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	missing := filepath.Join(fsTestRoot(t), "nope")
	status, _, code := folderPatch(t, ts, sessionKey, missing)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", status, http.StatusNotFound)
	}
	if code != "folder_not_found" {
		t.Errorf("code = %q, want folder_not_found", code)
	}
}

func TestSessionFolder_ForbiddenOutsideAllowed(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	status, _, code := folderPatch(t, ts, sessionKey, "/etc")
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", status, http.StatusForbidden)
	}
	if code != "folder_forbidden" {
		t.Errorf("code = %q, want folder_forbidden", code)
	}

	link := filepath.Join(fsTestRoot(t), "escape")
	if err := os.Symlink("/etc", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	status, _, code = folderPatch(t, ts, sessionKey, link)
	if status != http.StatusForbidden {
		t.Fatalf("symlink escape status = %d, want %d", status, http.StatusForbidden)
	}
	if code != "folder_forbidden" {
		t.Errorf("symlink escape code = %q, want folder_forbidden", code)
	}
}

func TestSessionFolder_PathTraversalIsRejected(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	dir := fsTestRoot(t)
	traversal := filepath.Join(dir, "..", "..", "..", "etc")
	status, _, code := folderPatch(t, ts, sessionKey, traversal)
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want %d (code=%s)", status, http.StatusForbidden, code)
	}
	if code != "folder_forbidden" {
		t.Errorf("code = %q, want folder_forbidden", code)
	}
}

func TestSessionFolder_RequiresOwnership(t *testing.T) {
	ts := newNativeTestServer(t)

	u := ts.server.URL + "/api/v1/chat/sessions/" + url.PathEscape("native:stranger") + "/folder"
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestSessionFolder_RoutesRegistered(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	u := ts.server.URL + "/api/v1/chat/sessions/" + url.PathEscape(sessionKey) + "/folder"

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		t.Fatal("GET route not registered")
	}

	req, err = http.NewRequest(http.MethodDelete, u, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("DELETE status = %d, want 405 (route pattern missing?)", resp.StatusCode)
	}
}

func TestSessionFolder_AppearsInSessionPayloads(t *testing.T) {
	ts := newNativeTestServer(t)
	sessionKey := "native:" + ts.clientID

	dir := resolve(t, fsTestRoot(t))
	if _, _, code := folderPatch(t, ts, sessionKey, dir); code != "" {
		t.Fatalf("PATCH failed: code=%s", code)
	}

	req, err := http.NewRequest(http.MethodGet, ts.server.URL+"/api/v1/chat/sessions/"+url.PathEscape(sessionKey), nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	var single map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&single); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if single["folder"] != dir {
		t.Errorf("session GET folder = %v, want %q", single["folder"], dir)
	}

	ts.channel.auth.TrackSessionKey(ts.clientID, sessionKey)
	ts.loop.histories[sessionKey] = append(ts.loop.histories[sessionKey], providers.Message{Role: "user", Content: "hi"})
	for _, path := range []string{"/api/v1/chat/sessions", "/api/v1/chat/sessions/meta"} {
		req, err := http.NewRequest(http.MethodGet, ts.server.URL+path, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+ts.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		var payload ChatSessionsResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			resp.Body.Close()
			t.Fatalf("decode %s: %v", path, err)
		}
		resp.Body.Close()

		found := false
		for _, s := range payload.Sessions {
			if s.Key == sessionKey {
				found = true
				if s.Folder != dir {
					t.Errorf("%s: folder = %q, want %q", path, s.Folder, dir)
				}
			}
		}
		if !found {
			t.Errorf("%s: session %q not listed", path, sessionKey)
		}
	}
}
