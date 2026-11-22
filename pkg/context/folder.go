package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

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
