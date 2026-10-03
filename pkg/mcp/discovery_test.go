package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// discoveryFixture wires three layer roots inside a fresh temp dir.
type discoveryFixture struct {
	paths Paths
	// precomputed paths of each layer file, built with filepath.Join like
	// the production code does (but independently of it).
	globalPath  string
	agentPath   string
	projectPath string
}

func newDiscoveryFixture(t *testing.T) discoveryFixture {
	t.Helper()
	base := t.TempDir()
	paths := Paths{
		LeleDir:        filepath.Join(base, "lele-home"),
		AgentWorkspace: filepath.Join(base, "workspace-coder"),
		Cwd:            filepath.Join(base, "project"),
	}
	return discoveryFixture{
		paths:       paths,
		globalPath:  filepath.Join(paths.LeleDir, "mcp.json"),
		agentPath:   filepath.Join(paths.AgentWorkspace, "mcp.json"),
		projectPath: filepath.Join(paths.Cwd, ".lele", "mcp.json"),
	}
}

// writeLayer writes content at the path of the given layer name.
func (f discoveryFixture) writeLayer(t *testing.T, layer, content string) {
	t.Helper()
	switch layer {
	case LayerGlobal:
		writeFileAt(t, f.globalPath, content)
	case LayerAgent:
		writeFileAt(t, f.agentPath, content)
	case LayerProject:
		writeFileAt(t, f.projectPath, content)
	default:
		t.Fatalf("unknown layer %q", layer)
	}
}

// serverWant is the expected outcome for one server name.
type serverWant struct {
	command     string
	url         string
	description string
	layer       string
}

func TestDiscoverPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		files        map[string]string // layer → content (absent = no file)
		wantServers  map[string]serverWant
		wantDisabled map[string]string
		wantErr      bool
	}{
		{
			name: "only global layer",
			files: map[string]string{
				LayerGlobal: `{"mcpServers": {"g": {"command": "cg", "description": "from global"}}}`,
			},
			wantServers: map[string]serverWant{
				"g": {command: "cg", description: "from global", layer: LayerGlobal},
			},
		},
		{
			name: "only agent layer",
			files: map[string]string{
				LayerAgent: `{"mcpServers": {"a": {"command": "ca"}}}`,
			},
			wantServers: map[string]serverWant{
				"a": {command: "ca", layer: LayerAgent},
			},
		},
		{
			name: "only project layer",
			files: map[string]string{
				LayerProject: `{"mcpServers": {"p": {"url": "https://p.example/mcp"}}}`,
			},
			wantServers: map[string]serverWant{
				"p": {url: "https://p.example/mcp", layer: LayerProject},
			},
		},
		{
			name: "agent replaces the whole global entry, not just its fields",
			files: map[string]string{
				LayerGlobal: `{"mcpServers": {
					"dup": {"command": "c-global", "description": "desc-global"},
					"g-only": {"command": "cg"}}}`,
				LayerAgent: `{"mcpServers": {"dup": {"command": "c-agent"}}}`,
			},
			wantServers: map[string]serverWant{
				"dup":    {command: "c-agent", description: "", layer: LayerAgent},
				"g-only": {command: "cg", layer: LayerGlobal},
			},
		},
		{
			name: "project wins over agent and global",
			files: map[string]string{
				LayerGlobal:  `{"mcpServers": {"dup": {"command": "c-global", "description": "d"}}}`,
				LayerAgent:   `{"mcpServers": {"dup": {"command": "c-agent", "description": "d"}}}`,
				LayerProject: `{"mcpServers": {"dup": {"command": "c-project", "description": "d"}}}`,
			},
			wantServers: map[string]serverWant{
				"dup": {command: "c-project", description: "d", layer: LayerProject},
			},
		},
		{
			name: "project entry fully replaces lower config even across shapes",
			files: map[string]string{
				LayerGlobal:  `{"mcpServers": {"dup": {"command": "c-global", "description": "desc-global"}}}`,
				LayerProject: `{"mcpServers": {"dup": {"url": "https://only.example/mcp"}}}`,
			},
			wantServers: map[string]serverWant{
				// command from the global entry must NOT leak through.
				"dup": {command: "", url: "https://only.example/mcp", layer: LayerProject},
			},
		},
		{
			name: "disabled in the higher layer wins over active lower",
			files: map[string]string{
				LayerGlobal:  `{"mcpServers": {"dup": {"command": "cg"}}}`,
				LayerProject: `{"mcpServers": {"dup": {"command": "cp", "disabled": true}}}`,
			},
			wantServers:  map[string]serverWant{},
			wantDisabled: map[string]string{"dup": LayerProject},
		},
		{
			name: "active in the higher layer overrides disabled lower",
			files: map[string]string{
				LayerGlobal: `{"mcpServers": {"dup": {"command": "cg", "disabled": true}}}`,
				LayerAgent:  `{"mcpServers": {"dup": {"command": "ca"}}}`,
			},
			wantServers: map[string]serverWant{
				"dup": {command: "ca", layer: LayerAgent},
			},
		},
		{
			name: "all three layers coexist with independent names",
			files: map[string]string{
				LayerGlobal:  `{"mcpServers": {"g": {"command": "cg"}}}`,
				LayerAgent:   `{"mcpServers": {"a": {"command": "ca"}}}`,
				LayerProject: `{"mcpServers": {"p": {"command": "cp"}}}`,
			},
			wantServers: map[string]serverWant{
				"g": {command: "cg", layer: LayerGlobal},
				"a": {command: "ca", layer: LayerAgent},
				"p": {command: "cp", layer: LayerProject},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newDiscoveryFixture(t)
			for layer, content := range tt.files {
				fx.writeLayer(t, layer, content)
			}

			got, err := Discover(fx.paths)
			if tt.wantErr != (err != nil) {
				t.Fatalf("Discover() error = %v, wantErr = %v", err, tt.wantErr)
			}
			if len(got.Servers) != len(tt.wantServers) {
				t.Fatalf("Servers = %v, want exactly %v", serverNames(got.Servers), wantNames(tt.wantServers))
			}
			for name, want := range tt.wantServers {
				srv, ok := got.Servers[name]
				if !ok {
					t.Errorf("server %q missing from Servers (got %v)", name, serverNames(got.Servers))
					continue
				}
				if srv.Command != want.command {
					t.Errorf("server %q Command = %q, want %q", name, srv.Command, want.command)
				}
				if srv.URL != want.url {
					t.Errorf("server %q URL = %q, want %q", name, srv.URL, want.url)
				}
				if srv.Description != want.description {
					t.Errorf("server %q Description = %q, want %q", name, srv.Description, want.description)
				}
				if layer := got.Layers[name]; layer != want.layer {
					t.Errorf("server %q layer = %q, want %q", name, layer, want.layer)
				}
			}
			if len(got.Disabled) != len(tt.wantDisabled) {
				t.Errorf("Disabled = %v, want %v", got.Disabled, tt.wantDisabled)
			}
			for name, wantLayer := range tt.wantDisabled {
				if layer := got.Disabled[name]; layer != wantLayer {
					t.Errorf("Disabled[%q] = %q, want %q", name, layer, wantLayer)
				}
			}
		})
	}
}

func TestDiscoverAllLayersMissing(t *testing.T) {
	fx := newDiscoveryFixture(t)
	got, err := Discover(fx.paths)
	if err != nil {
		t.Fatalf("Discover() with no files: error = %v, want nil", err)
	}
	if len(got.Servers) != 0 || len(got.Layers) != 0 || len(got.Disabled) != 0 || len(got.Names) != 0 {
		t.Errorf("result = %#v, want fully empty", got)
	}
}

func TestDiscoverMalformedLayerKeepsOthers(t *testing.T) {
	validA := `{"mcpServers": {"from-global": {"command": "cg"}}}`
	validB := `{"mcpServers": {"from-project": {"command": "cp"}}}`
	tests := []struct {
		name          string
		brokenLayer   string
		brokenContent string
		keepFirst     string // server expected from the lower valid layer
		keepLast      string // server expected from the higher valid layer
		errContains   string
	}{
		{
			name:          "malformed global layer",
			brokenLayer:   LayerGlobal,
			brokenContent: `{"mcpServers": {`,
			keepFirst:     "",
			keepLast:      "from-project",
			errContains:   LayerGlobal,
		},
		{
			name:          "malformed agent layer",
			brokenLayer:   LayerAgent,
			brokenContent: "not json at all",
			keepFirst:     "from-global",
			keepLast:      "from-project",
			errContains:   LayerAgent,
		},
		{
			name:          "malformed project layer",
			brokenLayer:   LayerProject,
			brokenContent: `["wrong", "type"]`,
			keepFirst:     "from-global",
			keepLast:      "",
			errContains:   LayerProject,
		},
		{
			name:          "invalid entry inside a layer",
			brokenLayer:   LayerAgent,
			brokenContent: `{"mcpServers": {"bad": {"url": "https://e.com", "type": "ftp"}, "sib": {"command": "cs"}}}`,
			keepFirst:     "from-global",
			keepLast:      "from-project",
			errContains:   `unknown type "ftp"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newDiscoveryFixture(t)
			fx.writeLayer(t, LayerGlobal, validA)
			fx.writeLayer(t, LayerProject, validB)
			// written last so a broken project layer overwrites its own fixture
			fx.writeLayer(t, tt.brokenLayer, tt.brokenContent)

			got, err := Discover(fx.paths)
			if err == nil {
				t.Fatal("Discover() error = nil, want a warning for the broken layer")
			}
			if !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("error %q does not contain %q", err, tt.errContains)
			}
			for _, name := range []string{tt.keepFirst, tt.keepLast} {
				if name == "" {
					continue
				}
				if _, ok := got.Servers[name]; !ok {
					t.Errorf("server %q lost by the broken layer (kept %v)", name, serverNames(got.Servers))
				}
			}
			// a broken agent layer must not hide valid siblings of its own file
			if tt.brokenLayer == LayerAgent && strings.Contains(tt.brokenContent, `"sib"`) {
				if _, ok := got.Servers["sib"]; !ok {
					t.Errorf("valid sibling lost: kept %v", serverNames(got.Servers))
				}
			}
		})
	}
}

func TestDiscoverSortedNames(t *testing.T) {
	fx := newDiscoveryFixture(t)
	fx.writeLayer(t, LayerGlobal, `{"mcpServers": {"zeta": {"command": "c"}, "mid": {"command": "c"}}}`)
	fx.writeLayer(t, LayerProject, `{"mcpServers": {"alpha": {"command": "c"}, "beta": {"command": "c"}}}`)

	got, err := Discover(fx.paths)
	if err != nil {
		t.Fatalf("Discover() error: %v", err)
	}
	want := []string{"alpha", "beta", "mid", "zeta"}
	if !reflect.DeepEqual(got.Names, want) {
		t.Errorf("Names = %v, want sorted %v", got.Names, want)
	}
}

func TestLayerFilesPathsUseFilepathJoin(t *testing.T) {
	paths := Paths{
		LeleDir:        "/home/u/.lele",
		AgentWorkspace: "/home/u/.lele/workspace-coder",
		Cwd:            "/src/proj",
	}
	got := layerFiles(paths)
	want := []layerFile{
		{name: LayerGlobal, path: filepath.Join("/home/u/.lele", "mcp.json")},
		{name: LayerAgent, path: filepath.Join("/home/u/.lele/workspace-coder", "mcp.json")},
		{name: LayerProject, path: filepath.Join("/src/proj", ".lele", "mcp.json")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layerFiles() = %#v, want %#v", got, want)
	}
}

// TestLayerFilesAliasedRootsReadOnce pins the <LeleDir>/mcp.json aliasing:
// running the process from the parent of LeleDir (the classic `cd ~ &&
// lele agent`) makes the global and the project layer resolve to the SAME
// file. It must be read once, and the highest layer must own the provenance
// so a bad file is neither double-reported nor blamed on a layer that did
// not win the merge.
func TestLayerFilesAliasedRootsReadOnce(t *testing.T) {
	home := t.TempDir()
	paths := Paths{LeleDir: filepath.Join(home, ".lele"), Cwd: home}
	got := layerFiles(paths)
	want := []layerFile{{name: LayerProject, path: filepath.Join(home, ".lele", "mcp.json")}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layerFiles() = %#v, want %#v", got, want)
	}

	// End to end: one malformed file, one warning, provenance of the winner.
	if err := os.MkdirAll(filepath.Join(home, ".lele"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), '{')
	if err := os.WriteFile(filepath.Join(home, ".lele", "mcp.json"), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Discover(paths)
	if err == nil {
		t.Fatal("Discover() must report the malformed shared file")
	}
	if n := strings.Count(err.Error(), ".lele/mcp.json"); n != 1 {
		t.Errorf("the shared file was reported %d times, want exactly 1 (double read regression): %v", n, err)
	}
	if !strings.Contains(err.Error(), LayerProject) {
		t.Errorf("provenance should name the winning layer %q, got: %v", LayerProject, err)
	}
}

func TestLayerFilesSkipsEmptyRoots(t *testing.T) {
	tests := []struct {
		name  string
		paths Paths
		want  int
	}{
		{"all roots empty", Paths{}, 0},
		{"only lele dir", Paths{LeleDir: "/h"}, 1},
		{"only agent workspace", Paths{AgentWorkspace: "/w"}, 1},
		{"only cwd", Paths{Cwd: "/p"}, 1},
		{"all roots set", Paths{LeleDir: "/h", AgentWorkspace: "/w", Cwd: "/p"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(layerFiles(tt.paths)); got != tt.want {
				t.Errorf("layerFiles() returned %d layers, want %d", got, tt.want)
			}
		})
	}
}

// serverNames is a sorted-ish helper for failure messages.
func serverNames(m map[string]ServerConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func wantNames(m map[string]serverWant) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
