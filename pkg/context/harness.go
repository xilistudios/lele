package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxHarnessDirEntries = 100
const maxAgentsFileBytes = 16384

// truncateUTF8 returns s truncated to at most max bytes, cutting at a valid
// UTF-8 rune boundary so the result is always valid UTF-8.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	// Walk backwards to find a valid start byte (not a continuation byte).
	idx := max
	for idx > 0 && (s[idx]&0xC0) == 0x80 {
		idx--
	}
	return s[:idx]
}

// BuildHarnessContext builds the context for the harness module,
// including the current working directory, a list of its first-level elements,
// and the contents of AGENTS.md if it exists.
func BuildHarnessContext() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current working directory: %w", err)
	}

	entries, err := os.ReadDir(cwd)
	if err != nil {
		return "", fmt.Errorf("failed to read current working directory: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("## Harness Module\n")
	sb.WriteString(fmt.Sprintf("Current Directory: `%s`\n\n", cwd))

	sb.WriteString("### Directory Listing (First-Level)\n")
	if len(entries) == 0 {
		sb.WriteString("No files or directories found.\n")
	} else {
		limit := len(entries)
		if limit > maxHarnessDirEntries {
			limit = maxHarnessDirEntries
		}
		for i := 0; i < limit; i++ {
			name := entries[i].Name()
			if entries[i].IsDir() {
				sb.WriteString(fmt.Sprintf("- %s/\n", name))
			} else {
				sb.WriteString(fmt.Sprintf("- %s\n", name))
			}
		}
		if len(entries) > maxHarnessDirEntries {
			sb.WriteString(fmt.Sprintf("- ... and %d more\n", len(entries)-maxHarnessDirEntries))
		}
	}
	sb.WriteString("\n")

	// Check AGENTS.md
	agentsPath := filepath.Join(cwd, "AGENTS.md")
	if data, err := os.ReadFile(agentsPath); err == nil {
		sb.WriteString("### AGENTS.md\n\n")
		truncated := truncateUTF8(string(data), maxAgentsFileBytes)
		sb.WriteString(truncated)
		if len(data) > maxAgentsFileBytes {
			sb.WriteString("\n\n... [AGENTS.md truncated]\n")
		} else {
			sb.WriteString("\n")
		}
	} else {
		// Fallback to lowercase
		agentsPath = filepath.Join(cwd, "agents.md")
		if data, err := os.ReadFile(agentsPath); err == nil {
			sb.WriteString("### AGENTS.md\n\n")
			truncated := truncateUTF8(string(data), maxAgentsFileBytes)
			sb.WriteString(truncated)
			if len(data) > maxAgentsFileBytes {
				sb.WriteString("\n\n... [AGENTS.md truncated]\n")
			} else {
				sb.WriteString("\n")
			}
		}
	}

	return sb.String(), nil
}
