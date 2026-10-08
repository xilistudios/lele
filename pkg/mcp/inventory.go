package mcp

import (
	"os"
)

// The inventory is the listing payload of the MCP management UI: one merged
// view per server NAME plus the per-layer provenance behind it. Two rules
// shape this file:
//
//  1. The merge verdict is DELEGATED to Discover — Winner, Effective and
//     Warnings for every name Discover reports are copied from its result,
//     so the precedence rule keeps exactly one implementation and this file
//     can never drift from the live manager.
//  2. Per-layer enumeration runs on raw bytes (parseRawDoc), never on
//     ParseFile, so ${VAR} references stay literal and no expanded secret
//     can reach a response.
//
// The only verdicts ReadInventory derives on its own are "invalid" and the
// disabled-stub corner of "disabled": Discover drops names whose winning
// entry fails validation entirely (see finishDiscovery), and a UI must not
// be blind to them — a name that is present in some layer's file yet absent
// from Discover's Servers and Disabled sets has a winning entry Discover
// refused (invalid, or the documented {"disabled":true} stub, which is
// invalid for lack of a definition but whose intent is "off").

// Effective-state values reported by Inventory.Effective. Exactly these
// three strings may appear.
const (
	stateEnabled  = "enabled"
	stateDisabled = "disabled"
	stateInvalid  = "invalid"
)

// Kind values reported by ServerSummary.Kind.
const (
	kindStdio   = "stdio"
	kindRemote  = "remote"
	kindUnknown = "unknown"
)

// ServerSummary is the display-safe description of one server entry. It is
// built from the RAW file bytes (no ${VAR} expansion) and exposes env and
// header key NAMES only — never their values, which can hold secrets.
type ServerSummary struct {
	Name        string
	Kind        string // "stdio" | "remote" | "unknown"
	Command     string // raw bytes: ${VAR} stays literal
	URL         string // raw bytes: ${VAR} stays literal
	Type        string // "http" (default) | "sse" | "" for stdio entries
	Description string
	Invalid     string // validation message; empty when the entry is valid
	Args        int
	Disabled    bool
	EnvKeys     []string // KEYS only, sorted; nil when the entry has none
	HeaderKeys  []string // KEYS only, sorted; nil when the entry has none
}

// EntryState is one layer's copy of one server name inside Inventory.ByName.
//
// Present reports that this layer's file actually holds a copy of the name:
// ByName only emits rows for layers that hold one, so Present is true for
// every emitted row — it is kept explicit so consumers can assert the shape
// without relying on the row's existence alone. Defines distinguishes a real
// definition (the copy carries a command or a url) from a pure disable stub
// ({"disabled":true} with neither), the documented pattern a writer must
// handle specially.
type EntryState struct {
	Name     string
	Layer    string // LayerGlobal | LayerAgent | LayerProject (file owner)
	Path     string // resolved path of the layer file
	Present  bool
	Defines  bool // copy has a command or url (false for a disable stub)
	Disabled bool // the copy's own "disabled" flag
	Invalid  string
	Summary  ServerSummary
}

// LayerInfo describes one mcp.json layer. When two roots resolve to the same
// file (aliased roots, e.g. LeleDir=$HOME/.lele with Cwd=$HOME), the file
// appears ONCE: the highest layer owns the row — the provenance Discover
// reports — and AliasedWith names the other layers sharing it. A layer whose
// root is empty has no file: Path is "" and Exists is false.
type LayerInfo struct {
	Layer       string
	Path        string
	Exists      bool
	AliasedWith []string // other layers resolving to the same file, low → high
}

// Inventory is the per-layer MCP inventory of one Paths set. It is safe to
// json.Marshal: Warnings holds plain errors (encoded as empty objects) and
// nothing in the struct can carry an expanded secret.
type Inventory struct {
	Paths     Paths
	Layers    []LayerInfo             // low → high, one row per distinct file (aliases folded)
	ByName    map[string][]EntryState // name → copies low → high, incl. shadowed ones
	Winner    map[string]string       // name → layer owning the winning entry
	Effective map[string]string       // name → "enabled" | "disabled" | "invalid"
	Shadowed  map[string][]string     // name → layers below the winner whose copy is inert
	Warnings  []error                 // Discover's warnings, unmangled
}

// LayerFile resolves the mcp.json of one layer, mirroring joinRoot: an empty
// root (layer disabled) and an unknown layer name both yield "" so callers
// can never build a process-relative path from them.
func LayerFile(paths Paths, layer string) string {
	switch layer {
	case LayerGlobal:
		return joinRoot(paths.LeleDir, "mcp.json")
	case LayerAgent:
		return joinRoot(paths.AgentWorkspace, "mcp.json")
	case LayerProject:
		return joinRoot(paths.Cwd, ".lele", "mcp.json")
	default:
		return ""
	}
}

// Layers returns the layer names ordered low → high (the merge precedence).
func Layers() []string {
	return []string{LayerGlobal, LayerAgent, LayerProject}
}

// ReadInventory lists every mcp.json layer with its per-name provenance.
//
// The merge verdict (Winner, Effective for enabled/disabled names, Warnings)
// is delegated to Discover; the new part is the per-layer enumeration over
// raw bytes. Names present in some file but unknown to Discover's verdicts
// get the derived verdict described in the comment above (invalid winner, or
// disabled stub → "disabled"). Missing files, empty roots and aliased roots
// behave exactly as in Discover because the layer list itself is Discover's
// own layerFiles.
func ReadInventory(paths Paths) Inventory {
	inv := Inventory{
		Paths:     paths,
		Layers:    layerInfos(paths),
		ByName:    make(map[string][]EntryState),
		Winner:    make(map[string]string),
		Effective: make(map[string]string),
		Shadowed:  make(map[string][]string),
	}

	// The merge verdict comes from Discover — one implementation of the rule.
	result, err := Discover(paths)
	inv.Warnings = splitWarnings(err)
	for name := range result.Servers {
		inv.Winner[name] = result.Layers[name]
		inv.Effective[name] = stateEnabled
	}
	for name, layer := range result.Disabled {
		inv.Winner[name] = layer
		inv.Effective[name] = stateDisabled
	}

	// Per-layer enumeration: the same folded file list Discover reads, in
	// the same order, so ByName rows are low → high and an aliased file is
	// listed exactly once (under its highest layer, like Discover's
	// provenance).
	for _, lf := range layerFiles(paths) {
		inv.readLayer(lf)
	}

	// A name present in some file but absent from Discover's verdicts has a
	// winning entry Discover refused: either invalid or a disabled stub
	// ({"disabled":true} with no definition — invalid too, but its whole
	// point is switching the server off). Discover reports neither, so the
	// verdict is derived from the winning copy: rows are low → high, the
	// last row is the winner.
	for name, rows := range inv.ByName {
		if _, known := inv.Effective[name]; known {
			continue
		}
		last := rows[len(rows)-1]
		inv.Winner[name] = last.Layer
		if last.Disabled {
			inv.Effective[name] = stateDisabled
		} else {
			inv.Effective[name] = stateInvalid
		}
	}

	// Every copy below the winner is inert (whole-entry replace). A single
	// copy has nothing shadowed.
	for name, rows := range inv.ByName {
		if len(rows) < 2 {
			continue
		}
		inert := make([]string, 0, len(rows)-1)
		for _, row := range rows[:len(rows)-1] {
			inert = append(inert, row.Layer)
		}
		inv.Shadowed[name] = inert
	}

	return inv
}

// readLayer appends this file's copies to ByName. A file that is missing,
// unreadable or fails parseRawDoc yields no rows: ParseFile would have
// reached the same verdict, Discover already warned, and enumerating it
// anyway would invent provenance Discover does not have.
func (inv *Inventory) readLayer(lf layerFile) {
	data, err := os.ReadFile(lf.path)
	if err != nil {
		// Layers are optional: a missing file simply holds no copies. Any
		// other failure (permission, directory) was reported by Discover,
		// which reads the same path — skipping keeps both views consistent.
		return
	}
	doc, err := parseRawDoc(data)
	if err != nil {
		return // same verdict as ParseFile; Discover already warned
	}
	if doc.servers == nil {
		return
	}
	for i, m := range doc.servers.members {
		if doc.servers.index[m.key] != i {
			continue // duplicate member name: the last one wins (map semantics)
		}
		summary, ok := doc.Summary(m.key)
		if !ok {
			continue
		}
		inv.ByName[m.key] = append(inv.ByName[m.key], EntryState{
			Name:     m.key,
			Layer:    lf.name,
			Path:     lf.path,
			Present:  true, // rows exist only for layers holding a copy
			Defines:  summary.Command != "" || summary.URL != "",
			Disabled: summary.Disabled,
			Invalid:  summary.Invalid,
			Summary:  summary,
		})
	}
}

// layerInfos builds the per-layer view: one row per layer in low → high
// order, with roots that resolve to the same file folded into a single row.
// The row keeps the position of the lowest aliasing layer (the slot
// layerFiles keeps) but is OWNED by the highest one, so the provenance
// always matches Discover's — in the classic HOME case (LeleDir=$HOME/.lele,
// Cwd=$HOME) the shared file reports Layer=project with AliasedWith=[global].
func layerInfos(paths Paths) []LayerInfo {
	infos := make([]LayerInfo, 0, len(Layers()))
	owner := make(map[string]int, len(Layers())) // path → index in infos
	for _, layer := range Layers() {
		path := LayerFile(paths, layer)
		if path == "" {
			// Empty root: no file, so it can never alias another layer.
			infos = append(infos, LayerInfo{Layer: layer})
			continue
		}
		if i, dup := owner[path]; dup {
			// Aliased roots: fold into the existing row; the higher layer
			// retakes ownership (files[i] = lf in layerFiles) and the
			// previous owner is named in AliasedWith.
			prev := infos[i].Layer
			infos[i].Layer = layer
			infos[i].AliasedWith = append(infos[i].AliasedWith, prev)
			continue
		}
		owner[path] = len(infos)
		infos = append(infos, LayerInfo{Layer: layer, Path: path, Exists: fileExists(path)})
	}
	return infos
}

// fileExists reports whether path is present on disk.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// splitWarnings recovers the individual errors from Discover's errors.Join
// return value so Inventory.Warnings carries them as a slice, not a joined
// blob. It is the only transformation applied to Discover's warnings.
func splitWarnings(err error) []error {
	if err == nil {
		return nil
	}
	if u, ok := err.(interface{ Unwrap() []error }); ok {
		if errs := u.Unwrap(); len(errs) > 0 {
			return errs
		}
	}
	return []error{err}
}
