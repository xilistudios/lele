package mcp

import (
	"os"
)

const (
	stateEnabled  = "enabled"
	stateDisabled = "disabled"
	stateInvalid  = "invalid"
)

const (
	kindStdio   = "stdio"
	kindRemote  = "remote"
	kindUnknown = "unknown"
)

type ServerSummary struct {
	Name        string
	Kind        string
	Command     string
	URL         string
	Type        string
	Description string
	Invalid     string
	Args        int
	Disabled    bool
	EnvKeys     []string
	HeaderKeys  []string

	AuthType        string
	AuthScopes      int
	AuthClientIDSet bool
}

type EntryState struct {
	Name     string
	Layer    string
	Path     string
	Present  bool
	Defines  bool
	Disabled bool
	Invalid  string
	Summary  ServerSummary
}

type LayerInfo struct {
	Layer       string
	Path        string
	Exists      bool
	AliasedWith []string
}

type Inventory struct {
	Paths     Paths
	Layers    []LayerInfo
	ByName    map[string][]EntryState
	Winner    map[string]string
	Effective map[string]string
	Shadowed  map[string][]string
	Warnings  []error
}

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

func Layers() []string {
	return []string{LayerGlobal, LayerAgent, LayerProject}
}

func ReadInventory(paths Paths) Inventory {
	inv := Inventory{
		Paths:     paths,
		Layers:    layerInfos(paths),
		ByName:    make(map[string][]EntryState),
		Winner:    make(map[string]string),
		Effective: make(map[string]string),
		Shadowed:  make(map[string][]string),
	}

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

	for _, lf := range layerFiles(paths) {
		inv.readLayer(lf)
	}

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

func (inv *Inventory) readLayer(lf layerFile) {
	data, err := os.ReadFile(lf.path)
	if err != nil {
		return
	}
	doc, err := parseRawDoc(data)
	if err != nil {
		return
	}
	if doc.servers == nil {
		return
	}
	for i, m := range doc.servers.members {
		if doc.servers.index[m.key] != i {
			continue
		}
		summary, ok := doc.Summary(m.key)
		if !ok {
			continue
		}
		inv.ByName[m.key] = append(inv.ByName[m.key], EntryState{
			Name:     m.key,
			Layer:    lf.name,
			Path:     lf.path,
			Present:  true,
			Defines:  summary.Command != "" || summary.URL != "",
			Disabled: summary.Disabled,
			Invalid:  summary.Invalid,
			Summary:  summary,
		})
	}
}

func layerInfos(paths Paths) []LayerInfo {
	infos := make([]LayerInfo, 0, len(Layers()))
	owner := make(map[string]int, len(Layers()))
	for _, layer := range Layers() {
		path := LayerFile(paths, layer)
		if path == "" {
			infos = append(infos, LayerInfo{Layer: layer})
			continue
		}
		if i, dup := owner[path]; dup {
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

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

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
