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

var frontmatterRe = regexp.MustCompile("(?s)\\A---\\r?\\n(.*?)(?:\\r?\\n)---(\\r?\\n|\\z)")

var markdownExts = map[string]bool{".md": true, ".markdown": true}

func LoadMarkdownFile(path string, source Source) (*Command, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("harness: read %s: %w", path, err)
	}
	cmd, err := ParseCommandMarkdown(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), path, raw)
	if err != nil {
		return nil, err
	}
	cmd.Source = source
	return cmd, nil
}

func ParseCommandMarkdown(name, path string, raw []byte) (*Command, error) {
	content := string(raw)

	var def CommandDef
	var err error
	if m := frontmatterRe.FindStringSubmatch(content); m != nil {
		def, err = parseFrontmatter(m[1], path)
		if err != nil {
			return nil, err
		}
		content = content[len(m[0]):]
	}

	name = strings.ToLower(name)
	if err := validateName(name, path); err != nil {
		return nil, err
	}
	def.Template = strings.TrimSpace(content)
	return def.ToCommand(name, "", path), nil
}

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
	slices.Sort(files)

	cmds := make([]*Command, 0, len(files))
	for _, name := range files {
		path := filepath.Join(dir, name)
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
		case "allow_absolute_files":
			b, err := strconv.ParseBool(value)
			if err != nil {
				return def, fmt.Errorf("harness: invalid allow_absolute_files %q in %s: %w", value, path, err)
			}
			def.AllowAbsoluteFiles = &b
		default:
		}
	}
	return def, nil
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func validateName(name, path string) error {
	switch {
	case name == "" || name == "." || name == "..":
		return fmt.Errorf("harness: invalid command name %q from %s", name, path)
	case strings.ContainsAny(name, "/\\"):
		return fmt.Errorf("harness: command name %q from %s contains a path separator", name, path)
	}
	return nil
}
