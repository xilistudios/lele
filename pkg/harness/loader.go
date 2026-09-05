// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package harness

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// frontmatterRe matches an optional leading YAML-ish block delimited by lines
// of exactly "---". (?s) lets "." span newlines; the block is non-greedy so the
// first closing "---" wins. If a file does not start with "---" it has no
// frontmatter and the whole file is the template.
var frontmatterRe = regexp.MustCompile("(?s)\\A---\\r?\\n(.*?)(?:\\r?\\n)---(\\r?\\n|\\z)")

// markdownExts are the file extensions LoadDir considers, lowercased.
var markdownExts = map[string]bool{".md": true, ".markdown": true}

// LoadMarkdownFile parses a single command markdown file. The command name is
// derived from the file stem (lowercased); the body after optional frontmatter
// becomes the template. Invalid files return an error; callers such as LoadDir
// decide whether to skip or fail.
func LoadMarkdownFile(path string, source Source) (*Command, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("harness: read %s: %w", path, err)
	}
	content := string(raw)

	var def CommandDef
	if m := frontmatterRe.FindStringSubmatch(content); m != nil {
		def, err = parseFrontmatter(m[1], path)
		if err != nil {
			return nil, err
		}
		content = content[len(m[0]):]
	}

	name := strings.ToLower(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if err := validateName(name, path); err != nil {
		return nil, err
	}
	def.Template = strings.TrimSpace(content)
	return def.ToCommand(name, source, path), nil
}

// LoadDir loads every *.md / *.markdown file directly inside dir (non
// recursive), sorted by filename for deterministic precedence behaviour.
// A missing directory is not an error: it returns (nil, nil). Files that fail
// to parse are skipped with a slog warning.
func LoadDir(dir string, source Source) ([]*Command, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("harness: read dir %s: %w", dir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !markdownExts[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		files = append(files, e.Name())
	}
	// os.ReadDir already sorts by filename, but sort explicitly so the
	// guarantee survives future refactors.
	slices.Sort(files)

	cmds := make([]*Command, 0, len(files))
	for _, name := range files {
		path := filepath.Join(dir, name)
		// Re-check the regular-file bit after join (symlinks, races).
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		cmd, err := LoadMarkdownFile(path, source)
		if err != nil {
			slog.Warn("harness: skipping invalid command file", "path", path, "error", err)
			continue
		}
		cmds = append(cmds, cmd)
	}
	return cmds, nil
}

// parseFrontmatter reads simple "key: value" lines. Values are trimmed and
// stripped of one layer of surrounding quotes; blank lines and #-comments are
// ignored; unknown keys are ignored. An invalid allow_shell is a hard error so
// a typo cannot silently disable shell expansion.
func parseFrontmatter(block, path string) (CommandDef, error) {
	var def CommandDef
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = unquote(strings.TrimSpace(value))
		switch key {
		case "description":
			def.Description = value
		case "agent":
			def.Agent = value
		case "model":
			def.Model = value
		case "allow_shell":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return def, fmt.Errorf("harness: invalid allow_shell %q in %s: %w", value, path, err)
			}
			def.AllowShell = b
		default:
			// Unknown keys are ignored on purpose: forward compatibility.
		}
	}
	return def, nil
}

// unquote strips one matching pair of surrounding single or double quotes.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// validateName rejects names that could escape the registry namespace or the
// workspace when interpolated into paths.
func validateName(name, path string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return fmt.Errorf("harness: invalid command name %q from %s", name, path)
	case strings.ContainsAny(name, "/\\"):
		return fmt.Errorf("harness: command name %q from %s contains a path separator", name, path)
	}
	return nil
}
