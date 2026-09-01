package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BuildFolderContext renders the "per-session folder context" section of the
// system prompt: the absolute path the user selected for the session plus a
// first-level listing of its contents. It is the WebUI analogue of
// BuildHarnessContext (which describes the process cwd for TUI/native
// sessions), but scoped to an arbitrary directory chosen per session.
//
// Security contract: only directory *names* are ever emitted — file contents
// are never read — and the listing is capped at maxHarnessDirEntries so a
// huge directory cannot balloon the prompt.
//
// Returns "" when dir is empty or is not a readable directory, so callers can
// append the section unconditionally and let absence mean "no folder selected".
func BuildFolderContext(dir string) string {
	if dir == "" {
		return ""
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}

	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Selected Folder\n\n")
	sb.WriteString(fmt.Sprintf("Folder: `%s`\n\n", abs))

	entries, err := os.ReadDir(abs)
	if err != nil {
		sb.WriteString("(unable to list contents)")
		return sb.String()
	}

	// Hidden entries (dot-prefixed) are noise for the model and may leak
	// credentials/config the user did not intend to share.
	visible := make([]os.DirEntry, 0, len(entries))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		visible = append(visible, e)
	}

	sb.WriteString("### Directory Listing (First-Level)\n")
	if len(visible) == 0 {
		sb.WriteString("No files or directories found.\n")
		return sb.String()
	}

	limit := len(visible)
	if limit > maxHarnessDirEntries {
		limit = maxHarnessDirEntries
	}
	for i := 0; i < limit; i++ {
		name := visible[i].Name()
		if visible[i].IsDir() {
			sb.WriteString(fmt.Sprintf("- %s/\n", name))
		} else {
			sb.WriteString(fmt.Sprintf("- %s\n", name))
		}
	}
	if len(visible) > maxHarnessDirEntries {
		sb.WriteString(fmt.Sprintf("- ... and %d more\n", len(visible)-maxHarnessDirEntries))
	}

	return sb.String()
}
