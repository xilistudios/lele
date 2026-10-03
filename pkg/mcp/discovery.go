package mcp

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
)

// Paths holds the roots of the three mcp.json layers. All values are plain
// strings injected by the caller (pkg/agent): this package never imports
// pkg/config and never calls os.Getwd. An empty root disables that layer —
// filepath.Join("", …) would otherwise silently produce a path relative to
// the process working directory.
type Paths struct {
	LeleDir        string // global layer:   <LeleDir>/mcp.json
	AgentWorkspace string // agent layer:    <AgentWorkspace>/mcp.json
	Cwd            string // project layer:  <Cwd>/.lele/mcp.json
}

// DiscoveryResult is the merged view of every mcp.json layer.
type DiscoveryResult struct {
	// Servers holds every active server (valid and not disabled) with the
	// configuration of the highest layer that defines it.
	Servers map[string]ServerConfig
	// Layers maps each name in Servers to its provenance layer
	// (LayerGlobal | LayerAgent | LayerProject).
	Layers map[string]string
	// Disabled maps disabled servers (name → winning layer) that were
	// excluded from Servers, so callers can still count/report them.
	Disabled map[string]string
	// Names lists the keys of Servers sorted, for deterministic output.
	Names []string
}

// layerFile pairs a layer name with the path of its mcp.json.
type layerFile struct {
	name string
	path string
}

// mergedEntry is the highest-layer definition of one server name.
type mergedEntry struct {
	cfg   ServerConfig
	layer string
	err   error // *ServerError of the winning entry when it is invalid
}

const (
	// LayerGlobal is the lowest layer: <LeleDir>/mcp.json.
	LayerGlobal = "global"
	// LayerAgent is the middle layer: <AgentWorkspace>/mcp.json.
	LayerAgent = "agent"
	// LayerProject is the highest layer: <Cwd>/.lele/mcp.json.
	LayerProject = "project"
)

// Discover reads every optional mcp.json layer and merges them.
//
// Precedence (low → high): global < agent < project. A name defined in
// several layers takes its whole configuration from the highest layer that
// mentions it — fields are replaced, never merged. If that winning entry is
// itself invalid, it still shadows lower layers: the name stays out of
// Servers and the failure is already reported.
//
// Missing layers are skipped silently. A layer that fails to parse is
// reported in the returned error but never aborts the merge: a broken higher
// layer cannot drop valid lower layers. The error is a warning for the
// caller to log, not a fatal condition — the result is always usable.
//
// Disabled servers are excluded from Servers and recorded in Disabled with
// the layer that decided their fate. Names are sorted for deterministic
// prompt output.
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
			continue // layer file absent
		}
		warnings = append(warnings, entryWarnings(lf.path, f)...)
		for name, srv := range f.MCPServers {
			best[name] = mergedEntry{cfg: srv, layer: lf.name, err: f.EntryErrors[name]}
		}
	}

	return finishDiscovery(result, best, warnings)
}

// entryWarnings renders the per-entry validation failures of one layer as a
// deterministic (sorted) slice of wrapped errors.
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

// finishDiscovery folds the merged entries into result and joins warnings.
// Invalid winning entries are skipped (already reported by entryWarnings),
// disabled ones are recorded, the rest become the active set.
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
			// invalid entry: excluded from the active set; the warning was
			// already collected when its layer was parsed.
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

// layerFiles resolves the mcp.json of each layer ordered low → high. Layers
// whose root was not provided (empty string) are skipped.
func layerFiles(paths Paths) []layerFile {
	candidates := []layerFile{
		{name: LayerGlobal, path: joinRoot(paths.LeleDir, "mcp.json")},
		{name: LayerAgent, path: joinRoot(paths.AgentWorkspace, "mcp.json")},
		{name: LayerProject, path: joinRoot(paths.Cwd, ".lele", "mcp.json")},
	}
	// Roots can alias onto the same file: <LeleDir>/mcp.json IS the project
	// layer when the process runs from LeleDir's parent (typically $HOME,
	// where LeleDir defaults to $HOME/.lele). Read each file exactly once
	// and let the HIGHEST layer own the provenance, matching the merge
	// order — otherwise a parse problem would be reported once per aliasing
	// layer and the provenance would name a layer that did not win.
	seen := map[string]int{}
	files := make([]layerFile, 0, len(candidates))
	for _, lf := range candidates {
		if lf.path == "" {
			continue
		}
		if i, dup := seen[lf.path]; dup {
			files[i] = lf // the higher layer re-takes the shared file
			continue
		}
		seen[lf.path] = len(files)
		files = append(files, lf)
	}
	return files
}

// joinRoot joins root with elems, returning "" when the root is empty so an
// unavailable root never degenerates into a process-relative path.
func joinRoot(root string, elems ...string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(append([]string{root}, elems...)...)
}
