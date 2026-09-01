package channels

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// fsListMaxEntries caps the number of directories returned per listing so a
// folder picker cannot be pointed at a huge tree and balloon the response.
const fsListMaxEntries = 500

// FsListEntry is a single navigable directory in a listing.
type FsListEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

// FsListResponse is the payload of GET /api/v1/fs/list. It carries enough
// context for the WebUI to render a folder picker: the current directory, its
// children, where it can go back to and which roots it may jump to.
type FsListResponse struct {
	Path      string        `json:"path"`
	Parent    string        `json:"parent"`
	Entries   []FsListEntry `json:"entries"`
	Home      string        `json:"home"`
	Roots     []string      `json:"roots"`
	Truncated bool          `json:"truncated"`
}

// fsBrowseSandboxRoots mirrors the sandbox trees isAllowedWorkspacePath trusts
// via prefix matching ("/tmp/", "/var/folders/"). Listing them explicitly lets
// the bare root itself be browsed, which the prefix check would reject.
var fsBrowseSandboxRoots = []string{"/tmp", "/var/folders"}

// isListableFsPath reports whether an absolute path may be browsed.
//
// It delegates to isAllowedWorkspacePath (user home, sandbox dirs, cwd) and
// only adds the sandbox roots themselves plus their macOS "/private" spelling:
// on Darwin /tmp and /var/folders are symlinks into /private/..., so the
// EvalSymlinks-resolved form of an allowed path would otherwise be rejected.
// This never widens the reachable set beyond trees isAllowedWorkspacePath
// already trusts.
func isListableFsPath(absPath string) bool {
	if isAllowedWorkspacePath(absPath) {
		return true
	}
	for _, candidate := range pathAliases(filepath.Clean(absPath)) {
		for _, root := range fsBrowseSandboxRoots {
			if candidate == root || strings.HasPrefix(candidate, root+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}

// pathAliases returns the equivalent spellings of a cleaned absolute path,
// collapsing the macOS "/private" prefix that symlink resolution introduces.
func pathAliases(cleanPath string) []string {
	if rest, ok := strings.CutPrefix(cleanPath, "/private/"); ok {
		return []string{cleanPath, string(filepath.Separator) + rest}
	}
	return []string{cleanPath}
}

// handleFsList lists server-side directories for the WebUI folder picker.
// GET /api/v1/fs/list?path=<absolute-path>  ("?" or empty path => user home)
//
// Security: this endpoint reads the host filesystem, so every request is
// gated by isAllowedWorkspacePath on BOTH the requested path and the
// symlink-resolved path, which blocks escaping an allowed tree via symlink.
func (n *NativeChannel) handleFsList(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimSpace(getQueryParam(r, "path"))

	home, homeErr := os.UserHomeDir()
	if requested == "" {
		if homeErr != nil {
			writeError(w, http.StatusInternalServerError, "user home directory unavailable", "fs_home_unavailable")
			return
		}
		requested = home
	}

	abs, err := filepath.Abs(expandHomePath(requested))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path: "+err.Error(), "fs_bad_path")
		return
	}
	abs = filepath.Clean(abs)

	// Gate the literal path first so traversal ("../") never reaches the OS.
	if !isListableFsPath(abs) {
		writeError(w, http.StatusForbidden, "access denied", "fs_forbidden")
		return
	}

	// Then gate the real path: a symlink (or ".." through one) must not land
	// outside the allowed trees.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, "path not found", "fs_not_found")
		return
	}
	resolved = filepath.Clean(resolved)
	if !isListableFsPath(resolved) {
		writeError(w, http.StatusForbidden, "access denied", "fs_forbidden")
		return
	}

	info, err := os.Stat(resolved)
	if err != nil {
		writeError(w, http.StatusNotFound, "path not found", "fs_not_found")
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory", "fs_not_dir")
		return
	}

	dirEntries, err := os.ReadDir(resolved)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read directory: "+err.Error(), "fs_read_failed")
		return
	}

	// os.ReadDir returns entries in lexical order, so the listing is stable.
	entries := make([]FsListEntry, 0, fsListMaxEntries)
	truncated := false
	for _, de := range dirEntries {
		name := de.Name()
		// The picker only offers directories, and hidden ones are noise.
		if !de.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if len(entries) >= fsListMaxEntries {
			truncated = true
			break
		}
		entries = append(entries, FsListEntry{
			Name:  name,
			Path:  filepath.Join(resolved, name),
			IsDir: true,
		})
	}

	parent := ""
	if up := filepath.Dir(resolved); up != resolved && isListableFsPath(up) {
		parent = up
	}

	writeJSON(w, http.StatusOK, FsListResponse{
		Path:      resolved,
		Parent:    parent,
		Entries:   entries,
		Home:      homeOrEmpty(homeErr, home),
		Roots:     fsListRoots(home, homeErr),
		Truncated: truncated,
	})
}

func homeOrEmpty(err error, home string) string {
	if err != nil {
		return ""
	}
	return home
}

// fsListRoots returns the navigable seed directories (home, /tmp, cwd),
// keeping only the ones that exist, are directories and are allowed.
func fsListRoots(home string, homeErr error) []string {
	cwd, _ := os.Getwd()
	candidates := make([]string, 0, 3)
	if homeErr == nil {
		candidates = append(candidates, home)
	}
	candidates = append(candidates, "/tmp", cwd)

	roots := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		if c == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(c))
		if err != nil {
			continue // does not exist
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		if !isListableFsPath(resolved) {
			continue
		}
		if _, dup := seen[resolved]; dup {
			continue
		}
		seen[resolved] = struct{}{}
		roots = append(roots, resolved)
	}
	return roots
}
