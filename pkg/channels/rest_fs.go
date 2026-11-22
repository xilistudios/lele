package channels

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const fsListMaxEntries = 500

type FsListEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
}

type FsListResponse struct {
	Path      string        `json:"path"`
	Parent    string        `json:"parent"`
	Entries   []FsListEntry `json:"entries"`
	Home      string        `json:"home"`
	Roots     []string      `json:"roots"`
	Truncated bool          `json:"truncated"`
}

var fsBrowseSandboxRoots = []string{"/tmp", "/var/folders"}

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

func pathAliases(cleanPath string) []string {
	if rest, ok := strings.CutPrefix(cleanPath, "/private/"); ok {
		return []string{cleanPath, string(filepath.Separator) + rest}
	}
	return []string{cleanPath}
}

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

	if !isListableFsPath(abs) {
		writeError(w, http.StatusForbidden, "access denied", "fs_forbidden")
		return
	}

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

	entries := make([]FsListEntry, 0, fsListMaxEntries)
	truncated := false
	for _, de := range dirEntries {
		name := de.Name()
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
			continue
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
