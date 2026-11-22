package channels

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fsTestRoot(t *testing.T) string {
	t.Helper()

	base, err := os.UserHomeDir()
	if err != nil {
		cwd, cerr := os.Getwd()
		if cerr != nil {
			t.Fatalf("no allowed base directory (home: %v, cwd: %v)", err, cerr)
		}
		base = cwd
	}

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	root := filepath.Join(base, fmt.Sprintf("lele-fs-list-test-%d-%s", os.Getpid(), name))
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("RemoveAll(%s) error = %v", root, err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", root, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	return root
}

func fsListGet(t *testing.T, ts *nativeTestServer, path string) (int, []byte) {
	t.Helper()

	u := ts.server.URL + "/api/v1/fs/list"
	if path != "" {
		u += "?path=" + url.QueryEscape(path)
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+ts.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do() error = %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	return resp.StatusCode, body
}

func fsListOK(t *testing.T, ts *nativeTestServer, path string) FsListResponse {
	t.Helper()

	status, body := fsListGet(t, ts, path)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", status, body)
	}
	var payload FsListResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode FsListResponse: %v (body=%s)", err, body)
	}
	return payload
}

func fsListErr(t *testing.T, ts *nativeTestServer, path string, wantStatus int) string {
	t.Helper()

	status, body := fsListGet(t, ts, path)
	if status != wantStatus {
		t.Fatalf("status = %d, want %d (body=%s)", status, wantStatus, body)
	}
	var payload APIError
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode APIError: %v (body=%s)", err, body)
	}
	return payload.Code
}

func resolve(t *testing.T, path string) string {
	t.Helper()

	got, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", path, err)
	}
	return filepath.Clean(got)
}

func TestFsList_EmptyPathListsHome(t *testing.T) {
	ts := newNativeTestServer(t)
	root := fsTestRoot(t)

	payload := fsListOK(t, ts, "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	if want := resolve(t, home); payload.Path != want {
		t.Errorf("path = %q, want %q", payload.Path, want)
	}
	if payload.Home != home {
		t.Errorf("home = %q, want %q", payload.Home, home)
	}

	wantRootPath := filepath.Join(resolve(t, home), filepath.Base(root))
	found := false
	for _, e := range payload.Entries {
		if strings.HasPrefix(e.Name, ".") {
			t.Errorf("hidden entry leaked: %q", e.Name)
		}
		if !e.IsDir {
			t.Errorf("non-directory entry leaked: %q", e.Name)
		}
		if e.Name == filepath.Base(root) {
			found = true
			if e.Path != wantRootPath {
				t.Errorf("entry path = %q, want %q", e.Path, wantRootPath)
			}
		}
	}
	if !found {
		t.Errorf("test root %q not present in home listing", filepath.Base(root))
	}
}

func TestFsList_SubdirReturnsOnlyDirsAndCorrectParent(t *testing.T) {
	ts := newNativeTestServer(t)
	root := fsTestRoot(t)

	sub := filepath.Join(root, "sub")
	for _, d := range []string{"alpha", "beta", ".hidden-dir"} {
		if err := os.MkdirAll(filepath.Join(sub, d), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
	}
	for _, f := range []string{"notes.txt", ".hidden-file"} {
		if err := os.WriteFile(filepath.Join(sub, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}

	payload := fsListOK(t, ts, sub)

	wantNames := []string{"alpha", "beta"}
	if len(payload.Entries) != len(wantNames) {
		t.Fatalf("entries = %+v, want exactly %v", payload.Entries, wantNames)
	}
	for i, want := range wantNames {
		got := payload.Entries[i]
		if got.Name != want {
			t.Errorf("entries[%d].name = %q, want %q", i, got.Name, want)
		}
		if !got.IsDir {
			t.Errorf("entries[%d].is_dir = false, want true", i)
		}
		if wantPath := filepath.Join(sub, want); got.Path != wantPath {
			t.Errorf("entries[%d].path = %q, want %q", i, got.Path, wantPath)
		}
	}
	if payload.Path != resolve(t, sub) {
		t.Errorf("path = %q, want %q", payload.Path, resolve(t, sub))
	}
	if payload.Parent != resolve(t, root) {
		t.Errorf("parent = %q, want %q", payload.Parent, resolve(t, root))
	}
	if payload.Truncated {
		t.Error("truncated = true, want false")
	}
}

func TestFsList_TildeExpansion(t *testing.T) {
	ts := newNativeTestServer(t)
	root := fsTestRoot(t)
	target := filepath.Join(root, "tilde")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	rel, err := filepath.Rel(home, target)
	if err != nil {
		t.Fatalf("Rel() error = %v", err)
	}

	payload := fsListOK(t, ts, "~/"+filepath.ToSlash(rel))
	if strings.HasPrefix(payload.Path, "~") {
		t.Errorf("path = %q, want tilde expanded", payload.Path)
	}
	if want := resolve(t, target); payload.Path != want {
		t.Errorf("path = %q, want %q", payload.Path, want)
	}
}

func TestFsList_RejectsPathOutsideAllowedTrees(t *testing.T) {
	if _, err := os.Stat("/etc"); err != nil {
		t.Skip("/etc not available on this host")
	}

	ts := newNativeTestServer(t)

	if code := fsListErr(t, ts, "/etc", http.StatusForbidden); code != "fs_forbidden" {
		t.Errorf("code = %q, want fs_forbidden", code)
	}
}

func TestFsList_MissingPathReturns404(t *testing.T) {
	ts := newNativeTestServer(t)
	root := fsTestRoot(t)

	path := filepath.Join(root, "does-not-exist")
	if code := fsListErr(t, ts, path, http.StatusNotFound); code != "fs_not_found" {
		t.Errorf("code = %q, want fs_not_found", code)
	}
}

func TestFsList_FileReturns400(t *testing.T) {
	ts := newNativeTestServer(t)
	root := fsTestRoot(t)

	file := filepath.Join(root, "afile.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if code := fsListErr(t, ts, file, http.StatusBadRequest); code != "fs_not_dir" {
		t.Errorf("code = %q, want fs_not_dir", code)
	}
}

func TestFsList_SymlinkEscapeIsForbidden(t *testing.T) {
	if _, err := os.Stat("/etc"); err != nil {
		t.Skip("/etc not available on this host")
	}

	ts := newNativeTestServer(t)
	root := fsTestRoot(t)

	link := filepath.Join(root, "escape")
	if err := os.Symlink("/etc", link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if code := fsListErr(t, ts, link, http.StatusForbidden); code != "fs_forbidden" {
		t.Errorf("code = %q, want fs_forbidden", code)
	}
}

func TestFsList_ParentHiddenWhenOutsideAllowedTrees(t *testing.T) {
	if _, err := os.Stat("/tmp"); err != nil {
		t.Skip("/tmp not available on this host")
	}

	ts := newNativeTestServer(t)

	payload := fsListOK(t, ts, "/tmp")
	if payload.Parent != "" {
		t.Errorf("parent = %q, want empty (no browsable above an allowed root)", payload.Parent)
	}
}

func TestFsList_TruncationCap(t *testing.T) {
	if testing.Short() {
		t.Skip("creating many directories is slow")
	}
	ts := newNativeTestServer(t)
	root := fsTestRoot(t)

	for i := 0; i <= fsListMaxEntries; i++ {
		if err := os.MkdirAll(filepath.Join(root, fmt.Sprintf("cap-%04d", i)), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
	}

	payload := fsListOK(t, ts, root)
	if len(payload.Entries) != fsListMaxEntries {
		t.Fatalf("entries = %d, want cap %d", len(payload.Entries), fsListMaxEntries)
	}
	if !payload.Truncated {
		t.Error("truncated = false, want true")
	}
}

func TestFsList_RootsAreAllowedAndExisting(t *testing.T) {
	ts := newNativeTestServer(t)

	payload := fsListOK(t, ts, "")
	if len(payload.Roots) == 0 {
		t.Fatal("roots is empty, want at least the user home")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir() error = %v", err)
	}
	wantHome := resolve(t, home)

	found := false
	for _, r := range payload.Roots {
		if !isListableFsPath(r) {
			t.Errorf("root %q is not in an allowed tree", r)
		}
		info, serr := os.Stat(r)
		if serr != nil || !info.IsDir() {
			t.Errorf("root %q is not an existing directory", r)
		}
		if r == wantHome {
			found = true
		}
	}
	if !found {
		t.Errorf("roots %v do not include home %q", payload.Roots, wantHome)
	}
}

func TestFsList_RequiresAuth(t *testing.T) {
	ts := newNativeTestServer(t)

	resp, err := http.Get(ts.server.URL + "/api/v1/fs/list")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d (route must be wrapped in withAuth)", resp.StatusCode, http.StatusUnauthorized)
	}
}
