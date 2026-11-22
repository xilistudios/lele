package mcp

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
)

type Paths struct {
	LeleDir        string
	AgentWorkspace string
	Cwd            string
}

type DiscoveryResult struct {
	Servers  map[string]ServerConfig
	Layers   map[string]string
	Disabled map[string]string
	Names    []string
}

type layerFile struct {
	name string
	path string
}

type mergedEntry struct {
	cfg   ServerConfig
	layer string
	err   error
}

const (
	LayerGlobal  = "global"
	LayerAgent   = "agent"
	LayerProject = "project"
)

func Discover(paths Paths) (DiscoveryResult, error) {
	result := DiscoveryResult{
		Servers:  make(map[string]ServerConfig),
		Layers:   make(map[string]string),
		Disabled: make(map[string]string),
	}
	best := make(map[string]mergedEntry)
	var warnings []error

	for _, lf := range layerFiles(paths) {
		f, err := ParseFile(lf.path)
		if err != nil {
			warnings = append(warnings, fmt.Errorf("%s layer: %w", lf.name, err))
			continue
		}
		if f == nil {
			continue
		}
		warnings = append(warnings, entryWarnings(lf.path, f)...)
		for name, srv := range f.MCPServers {
			best[name] = mergedEntry{cfg: srv, layer: lf.name, err: f.EntryErrors[name]}
		}
	}

	return finishDiscovery(result, best, warnings)
}

func entryWarnings(path string, f *File) []error {
	if len(f.EntryErrors) == 0 {
		return nil
	}
	names := make([]string, 0, len(f.EntryErrors))
	for name := range f.EntryErrors {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]error, 0, len(names))
	for _, name := range names {
		out = append(out, fmt.Errorf("mcp config %s: %w", path, f.EntryErrors[name]))
	}
	return out
}

func finishDiscovery(result DiscoveryResult, best map[string]mergedEntry, warnings []error) (DiscoveryResult, error) {
	names := make([]string, 0, len(best))
	for name := range best {
		names = append(names, name)
	}
	slices.Sort(names)

	active := make([]string, 0, len(names))
	for _, name := range names {
		e := best[name]
		switch {
		case e.err != nil:
		case e.cfg.Disabled:
			result.Disabled[name] = e.layer
		default:
			result.Servers[name] = e.cfg
			result.Layers[name] = e.layer
			active = append(active, name)
		}
	}
	result.Names = active
	return result, errors.Join(warnings...)
}

func layerFiles(paths Paths) []layerFile {
	candidates := []layerFile{
		{name: LayerGlobal, path: joinRoot(paths.LeleDir, "mcp.json")},
		{name: LayerAgent, path: joinRoot(paths.AgentWorkspace, "mcp.json")},
		{name: LayerProject, path: joinRoot(paths.Cwd, ".lele", "mcp.json")},
	}
	seen := map[string]int{}
	files := make([]layerFile, 0, len(candidates))
	for _, lf := range candidates {
		if lf.path == "" {
			continue
		}
		if i, dup := seen[lf.path]; dup {
			files[i] = lf
			continue
		}
		seen[lf.path] = len(files)
		files = append(files, lf)
	}
	return files
}

func joinRoot(root string, elems ...string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(append([]string{root}, elems...)...)
}
