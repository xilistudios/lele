package channels

import (
	"path/filepath"
	"strings"
)

func canonicalContainmentRoot(dir string) string {
	if dir == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil || abs == "" {
		return ""
	}
	return canonicalizeExistingPrefix(abs)
}

func resolvedForContainment(path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil || abs == "" {
		return ""
	}
	return canonicalizeExistingPrefix(abs)
}

func canonicalizeExistingPrefix(abs string) string {
	rest := ""
	cur := abs
	for {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

func isContainedIn(path, dir string) bool {
	root := canonicalContainmentRoot(dir)
	if root == "" {
		return false
	}
	p := resolvedForContainment(path)
	if p == "" {
		return false
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	sep := string(filepath.Separator)
	if rel == ".." || strings.HasPrefix(rel, ".."+sep) || filepath.IsAbs(rel) {
		return false
	}
	return true
}
