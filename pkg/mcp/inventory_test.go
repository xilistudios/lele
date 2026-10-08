package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeInventoryLayers writes the fixture contents, resolving each layer
// through LayerFile so a fixture can never write to an empty-root layer.
// For aliased fixtures (HOME case) only the global key may be used for the
// shared file: global and project resolve to the same path.
func writeInventoryLayers(t *testing.T, paths Paths, files map[string]string) {
	t.Helper()
	for _, layer := range Layers() {
		content, ok := files[layer]
		if !ok {
			continue
		}
		path := LayerFile(paths, layer)
		if path == "" {
			t.Fatalf("fixture layer %q resolves to an empty path", layer)
		}
		writeFileAt(t, path, content)
	}
}

// assertInventoryAgrees is the drift guard: ReadInventory's Effective must
// agree with Discover name by name (Servers → enabled, Layers provenance,
// Disabled → disabled). The only names the inventory may add are the ones
// Discover silently drops — invalid winners — and each addition must be
// corroborated by Discover's own warning text and by the winning copy.
func assertInventoryAgrees(t *testing.T, paths Paths) Inventory {
	t.Helper()
	disc, derr := Discover(paths)
	inv := ReadInventory(paths)

	want := make(map[string]string)
	for n := range disc.Servers {
		want[n] = stateEnabled
	}
	for n := range disc.Disabled {
		want[n] = stateDisabled
	}

	// Every verdict Discover reports must surface identically.
	for n, e := range want {
		got, ok := inv.Effective[n]
		if !ok || got != e {
			t.Errorf("Effective[%q] = %q (present=%v), Discover says %q", n, got, ok, e)
			continue
		}
		wantLayer := disc.Layers[n]
		if e == stateDisabled {
			wantLayer = disc.Disabled[n]
		}
		if inv.Winner[n] != wantLayer {
			t.Errorf("Winner[%q] = %q, Discover says %q", n, inv.Winner[n], wantLayer)
		}
		rows := inv.ByName[n]
		if len(rows) == 0 {
			t.Errorf("name %q has a Discover verdict but no per-layer copy", n)
			continue
		}
		if last := rows[len(rows)-1]; last.Layer != inv.Winner[n] {
			t.Errorf("ByName[%q] last row layer = %q, Winner = %q (row order must end at the winner)", n, last.Layer, inv.Winner[n])
		}
	}

	derrText := ""
	if derr != nil {
		derrText = derr.Error()
	}
	warnsAbout := func(n string) bool {
		return strings.Contains(derrText, fmt.Sprintf("mcp server %q", n))
	}

	// The inventory may only add names Discover dropped, and only as
	// "invalid" (a dropped invalid winner) or "disabled" (a dropped
	// disabled stub) — never as "enabled".
	for n, e := range inv.Effective {
		rows := inv.ByName[n]
		var last EntryState
		if len(rows) > 0 {
			last = rows[len(rows)-1]
		}
		switch e {
		case stateEnabled:
			if want[n] != stateEnabled {
				t.Errorf("Effective[%q] = enabled, but Discover does not report it active (%v)", n, want)
			}
		case stateDisabled:
			if want[n] == stateEnabled || want[n] == stateDisabled {
				continue // delegated verdict, checked above
			}
			// A dropped name reported as disabled must be a disabled stub
			// that failed validation (otherwise Discover would report it).
			if !last.Disabled || last.Invalid == "" || !warnsAbout(n) {
				t.Errorf("Effective[%q] = disabled for a name Discover dropped; last row = {disabled:%v invalid:%q}, warning mentions it: %v",
					n, last.Disabled, last.Invalid, warnsAbout(n))
			}
		case stateInvalid:
			if _, known := want[n]; known {
				t.Errorf("Effective[%q] = invalid, but Discover reports it as %q", n, want[n])
			}
			if last.Invalid == "" {
				t.Errorf("Effective[%q] = invalid, but the winning row carries no validation message", n)
			}
			if !warnsAbout(n) {
				t.Errorf("Effective[%q] = invalid, but Discover has no warning naming it (dropped winners are always warned): %v", n, derrText)
			}
		default:
			t.Errorf("Effective[%q] = %q, want one of enabled|disabled|invalid", n, e)
		}
	}

	// Aliased or not, one file must never be listed twice for one name.
	for n, rows := range inv.ByName {
		seen := make(map[string]bool, len(rows))
		for _, r := range rows {
			if seen[r.Path] {
				t.Errorf("ByName[%q] lists file %s twice", n, r.Path)
			}
			seen[r.Path] = true
		}
	}

	// Warnings are Discover's, unmangled.
	joined := errors.Join(inv.Warnings...)
	switch {
	case (derr == nil) != (joined == nil):
		t.Errorf("Warnings presence mismatch: inventory=%v Discover=%v", joined, derr)
	case derr != nil && joined.Error() != derr.Error():
		t.Errorf("Warnings differ:\n inventory: %v\n Discover:  %v", joined, derr)
	}

	return inv
}

// TestReadInventoryAgreesWithDiscover is the equivalence matrix required by
// the plan: missing layers, empty roots, aliased roots (HOME case), an
// invalid entry shadowing a valid lower one, a disabled winner, a stub
// winner and broken JSON in one layer — Effective must agree with Discover
// name by name in every one.
func TestReadInventoryAgreesWithDiscover(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T) (Paths, map[string]string)
	}{
		{
			name: "all layers missing",
			build: func(t *testing.T) (Paths, map[string]string) {
				return newDiscoveryFixture(t).paths, nil
			},
		},
		{
			name: "only global present",
			build: func(t *testing.T) (Paths, map[string]string) {
				return newDiscoveryFixture(t).paths, map[string]string{
					LayerGlobal: `{"mcpServers":{"g":{"command":"cg"}}}`,
				}
			},
		},
		{
			name: "empty agent root",
			build: func(t *testing.T) (Paths, map[string]string) {
				base := t.TempDir()
				paths := Paths{
					LeleDir: filepath.Join(base, "lele-home"),
					Cwd:     filepath.Join(base, "project"),
					// AgentWorkspace deliberately empty: the layer is off.
				}
				return paths, map[string]string{
					LayerGlobal:  `{"mcpServers":{"g":{"command":"cg"},"dup":{"command":"cg2"}}}`,
					LayerProject: `{"mcpServers":{"dup":{"command":"cp"}}}`,
				}
			},
		},
		{
			name: "aliased roots HOME case",
			build: func(t *testing.T) (Paths, map[string]string) {
				home := t.TempDir()
				paths := Paths{
					LeleDir:        filepath.Join(home, ".lele"),
					AgentWorkspace: filepath.Join(home, "ws"),
					Cwd:            home,
				}
				return paths, map[string]string{
					// global and project resolve to <home>/.lele/mcp.json;
					// only the global key may be written for the shared file.
					LayerGlobal: `{"mcpServers":{"shared":{"command":"cs"},"dup":{"command":"cg"}}}`,
					LayerAgent:  `{"mcpServers":{"dup":{"command":"ca"}}}`,
				}
			},
		},
		{
			name: "invalid project entry shadows valid global",
			build: func(t *testing.T) (Paths, map[string]string) {
				return newDiscoveryFixture(t).paths, map[string]string{
					LayerGlobal:  `{"mcpServers":{"s":{"command":"cg"},"keep":{"command":"ck"}}}`,
					LayerProject: `{"mcpServers":{"s":{"url":"https://x/mcp","type":"ftp"}}}`,
				}
			},
		},
		{
			name: "disabled winner over valid global",
			build: func(t *testing.T) (Paths, map[string]string) {
				return newDiscoveryFixture(t).paths, map[string]string{
					LayerGlobal:  `{"mcpServers":{"s":{"command":"cg"}}}`,
					LayerProject: `{"mcpServers":{"s":{"command":"cp","disabled":true}}}`,
				}
			},
		},
		{
			name: "stub winner over valid global",
			build: func(t *testing.T) (Paths, map[string]string) {
				return newDiscoveryFixture(t).paths, map[string]string{
					LayerGlobal: `{"mcpServers":{"s":{"command":"cg"}}}`,
					LayerAgent:  `{"mcpServers":{"s":{"disabled":true}}}`,
				}
			},
		},
		{
			name: "broken JSON agent layer",
			build: func(t *testing.T) (Paths, map[string]string) {
				return newDiscoveryFixture(t).paths, map[string]string{
					LayerGlobal:  `{"mcpServers":{"g":{"command":"cg"}}}`,
					LayerAgent:   `{"mcpServers": {"a": `,
					LayerProject: `{"mcpServers":{"p":{"command":"cp"}}}`,
				}
			},
		},
		{
			name: "broken JSON project layer",
			build: func(t *testing.T) (Paths, map[string]string) {
				return newDiscoveryFixture(t).paths, map[string]string{
					LayerGlobal:  `{"mcpServers":{"g":{"command":"cg"}}}`,
					LayerAgent:   `{"mcpServers":{"a":{"command":"ca"}}}`,
					LayerProject: "not json at all",
				}
			},
		},
		{
			name: "disabled lower overridden by valid higher",
			build: func(t *testing.T) (Paths, map[string]string) {
				return newDiscoveryFixture(t).paths, map[string]string{
					LayerGlobal: `{"mcpServers":{"s":{"command":"cg","disabled":true}}}`,
					LayerAgent:  `{"mcpServers":{"s":{"command":"ca"}}}`,
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths, files := tt.build(t)
			writeInventoryLayers(t, paths, files)
			assertInventoryAgrees(t, paths)
		})
	}
}

// TestReadInventoryCanarySecret is the no-secret-leak test: a layer whose
// env and header values reference ${CANARY}. The summary must expose key
// names only, and the expanded VALUE must appear nowhere in the inventory —
// not in a dump, not in the JSON encoding, not in Paths, not in Warnings.
func TestReadInventoryCanarySecret(t *testing.T) {
	const canary = "CANARY-5f3a-exp42-value"
	t.Setenv("CANARY", canary)

	fx := newDiscoveryFixture(t)
	fx.writeLayer(t, LayerGlobal, `{"mcpServers":{"leaky":{"command":"cmd ${CANARY}",`+
		`"env":{"TOKEN":"${CANARY}"},"headers":{"Authorization":"Bearer ${CANARY}"}}}}`)
	// A broken layer guarantees Warnings is exercised by the canary check.
	fx.writeLayer(t, LayerProject, `{"mcpServers": {`)

	inv := ReadInventory(fx.paths)

	rows := inv.ByName["leaky"]
	if len(rows) != 1 {
		t.Fatalf("ByName[\"leaky\"] has %d rows, want 1", len(rows))
	}
	s := rows[0].Summary
	if want := []string{"TOKEN"}; !reflect.DeepEqual(s.EnvKeys, want) {
		t.Errorf("EnvKeys = %v, want %v (key names only)", s.EnvKeys, want)
	}
	if want := []string{"Authorization"}; !reflect.DeepEqual(s.HeaderKeys, want) {
		t.Errorf("HeaderKeys = %v, want %v (key names only)", s.HeaderKeys, want)
	}
	if s.Command != "cmd ${CANARY}" {
		t.Errorf("Command = %q, want the literal placeholder (no expansion)", s.Command)
	}
	if inv.Effective["leaky"] != stateEnabled {
		t.Errorf("Effective[\"leaky\"] = %q, want enabled (the broken project layer must not shadow)", inv.Effective["leaky"])
	}
	if len(inv.Warnings) == 0 {
		t.Fatal("fixture must produce a warning so the Warnings canary check is meaningful")
	}

	// Requirement: the canary's VALUE appears nowhere.
	// (json.Marshal must also succeed: Inventory is marshal-safe, errors and all.)
	marshaled, err := json.Marshal(inv)
	if err != nil {
		t.Fatalf("json.Marshal(Inventory) error = %v (must be marshal-safe)", err)
	}
	dumps := map[string]string{
		"fmt.Sprintf(\"%+v\", inv)": fmt.Sprintf("%+v", inv),
		"json.Marshal(inv)":         string(marshaled),
		"inv.Paths":                 fmt.Sprintf("%+v", inv.Paths),
		"inv.Warnings":              fmt.Sprintf("%+v", inv.Warnings),
	}
	for label, dump := range dumps {
		if strings.Contains(dump, canary) {
			t.Errorf("expanded canary value leaked into %s: %s", label, dump)
		}
	}
	// The literal placeholder in the dump proves the value was never expanded.
	if !strings.Contains(dumps["fmt.Sprintf(\"%+v\", inv)"], "${CANARY}") {
		t.Error("expected the literal ${CANARY} in the inventory dump (raw, unexpanded)")
	}
}

// TestReadInventoryInvalidWinner pins the case Discover cannot express: an
// invalid higher-layer entry shadowing a valid lower one.
func TestReadInventoryInvalidWinner(t *testing.T) {
	fx := newDiscoveryFixture(t)
	fx.writeLayer(t, LayerGlobal, `{"mcpServers":{"s":{"command":"cg"}}}`)
	fx.writeLayer(t, LayerProject, `{"mcpServers":{"s":{"url":"https://x/mcp","type":"ftp"}}}`)

	// Context: Discover drops the name entirely (no Servers, no Disabled).
	if disc, _ := Discover(fx.paths); len(disc.Servers) != 0 || len(disc.Disabled) != 0 {
		t.Fatalf("fixture premise broken: Discover reported %v / %v", disc.Servers, disc.Disabled)
	}

	inv := assertInventoryAgrees(t, fx.paths)

	if got := inv.Effective["s"]; got != stateInvalid {
		t.Errorf("Effective[\"s\"] = %q, want %q", got, stateInvalid)
	}
	if got := inv.Winner["s"]; got != LayerProject {
		t.Errorf("Winner[\"s\"] = %q, want the shadowing layer %q", got, LayerProject)
	}
	rows := inv.ByName["s"]
	if len(rows) != 2 {
		t.Fatalf("ByName[\"s\"] has %d rows, want 2 (global copy + invalid project copy)", len(rows))
	}
	if rows[0].Layer != LayerGlobal || rows[0].Invalid != "" {
		t.Errorf("lower row = %+v, want a valid global copy", rows[0])
	}
	if rows[1].Layer != LayerProject {
		t.Errorf("upper row layer = %q, want %q", rows[1].Layer, LayerProject)
	}
	if !strings.Contains(rows[1].Invalid, `unknown type "ftp"`) {
		t.Errorf("upper row Invalid = %q, want the validate() message", rows[1].Invalid)
	}
	if !rows[1].Defines {
		t.Error("upper row Defines = false, want true (the copy carries a url)")
	}
	if want := []string{LayerGlobal}; !reflect.DeepEqual(inv.Shadowed["s"], want) {
		t.Errorf("Shadowed[\"s\"] = %v, want %v", inv.Shadowed["s"], want)
	}
}

// TestReadInventoryDisabledStub pins the disable-stub pattern: the winning
// copy is {"disabled":true} with no definition, so Discover drops it as
// invalid, but the user-facing verdict is "disabled" (the server is off
// because of this copy) — this is the state Q6's stub-create reports.
func TestReadInventoryDisabledStub(t *testing.T) {
	fx := newDiscoveryFixture(t)
	fx.writeLayer(t, LayerGlobal, `{"mcpServers":{"s":{"command":"cg"}}}`)
	fx.writeLayer(t, LayerAgent, `{"mcpServers":{"s":{"disabled":true}}}`)

	inv := assertInventoryAgrees(t, fx.paths)

	if got := inv.Effective["s"]; got != stateDisabled {
		t.Errorf("Effective[\"s\"] = %q, want %q (stub winner switches the server off)", got, stateDisabled)
	}
	if got := inv.Winner["s"]; got != LayerAgent {
		t.Errorf("Winner[\"s\"] = %q, want %q", got, LayerAgent)
	}
	rows := inv.ByName["s"]
	if len(rows) != 2 {
		t.Fatalf("ByName[\"s\"] has %d rows, want 2", len(rows))
	}
	stub := rows[1]
	if !stub.Present {
		t.Error("stub row Present = false, want true (the agent layer does hold a copy)")
	}
	if stub.Layer != LayerAgent || stub.Disabled != true {
		t.Errorf("stub row = {layer:%q disabled:%v}, want the disabled agent copy", stub.Layer, stub.Disabled)
	}
	if stub.Defines {
		t.Error("stub row Defines = true, want false (a pure disable stub has neither command nor url)")
	}
	if !strings.Contains(stub.Invalid, "needs command") {
		t.Errorf("stub row Invalid = %q, want the validate() verdict", stub.Invalid)
	}
	if rows[0].Layer != LayerGlobal || !rows[0].Defines {
		t.Errorf("global row = %+v, want a defining copy", rows[0])
	}
	if want := []string{LayerGlobal}; !reflect.DeepEqual(inv.Shadowed["s"], want) {
		t.Errorf("Shadowed[\"s\"] = %v, want %v (the valid lower copy is inert)", inv.Shadowed["s"], want)
	}
}

// TestReadInventoryAliasedRoots pins requirement 5: roots that resolve to
// the same file are folded — one LayerInfo for the shared file with
// AliasedWith naming the other layer, and ByName never lists the same
// file's entry twice.
func TestReadInventoryAliasedRoots(t *testing.T) {
	t.Run("HOME case: global and project share one file", func(t *testing.T) {
		home := t.TempDir()
		paths := Paths{
			LeleDir:        filepath.Join(home, ".lele"),
			AgentWorkspace: filepath.Join(home, "ws"),
			Cwd:            home,
		}
		shared := filepath.Join(home, ".lele", "mcp.json")
		writeFileAt(t, shared, `{"mcpServers":{"s":{"command":"cs"}}}`)

		inv := assertInventoryAgrees(t, paths)

		// Exactly one LayerInfo for the shared file, owned by the highest
		// layer (Discover's provenance), naming the other one.
		var sharedRows []LayerInfo
		for _, li := range inv.Layers {
			if li.Path == shared {
				sharedRows = append(sharedRows, li)
			}
		}
		if len(sharedRows) != 1 {
			t.Fatalf("found %d LayerInfo rows for the shared file, want exactly 1: %+v", len(sharedRows), inv.Layers)
		}
		row := sharedRows[0]
		if row.Layer != LayerProject {
			t.Errorf("shared file owned by layer %q, want %q (highest layer wins)", row.Layer, LayerProject)
		}
		if want := []string{LayerGlobal}; !reflect.DeepEqual(row.AliasedWith, want) {
			t.Errorf("AliasedWith = %v, want %v", row.AliasedWith, want)
		}
		if !row.Exists {
			t.Error("shared file Exists = false, want true")
		}

		// The name's entry appears once, under the owning layer.
		rows := inv.ByName["s"]
		if len(rows) != 1 {
			t.Fatalf("ByName[\"s\"] has %d rows, want 1 (same file never twice)", len(rows))
		}
		if rows[0].Path != shared || rows[0].Layer != LayerProject {
			t.Errorf("row = {layer:%q path:%q}, want the shared file under project", rows[0].Layer, rows[0].Path)
		}
		if inv.Winner["s"] != LayerProject || inv.Effective["s"] != stateEnabled {
			t.Errorf("verdict = {%q,%q}, want {project,enabled}", inv.Winner["s"], inv.Effective["s"])
		}
	})

	t.Run("global and agent share one file", func(t *testing.T) {
		base := t.TempDir()
		shared := filepath.Join(base, "mcp.json")
		paths := Paths{LeleDir: base, AgentWorkspace: base, Cwd: filepath.Join(base, "proj")}
		writeFileAt(t, shared, `{"mcpServers":{"s":{"command":"cs"}}}`)

		inv := assertInventoryAgrees(t, paths)
		for _, li := range inv.Layers {
			if li.Path != shared {
				continue
			}
			if li.Layer != LayerAgent {
				t.Errorf("shared file owned by %q, want %q", li.Layer, LayerAgent)
			}
			if want := []string{LayerGlobal}; !reflect.DeepEqual(li.AliasedWith, want) {
				t.Errorf("AliasedWith = %v, want %v", li.AliasedWith, want)
			}
		}
		if rows := inv.ByName["s"]; len(rows) != 1 || rows[0].Layer != LayerAgent {
			t.Errorf("ByName[\"s\"] = %+v, want a single row owned by agent", rows)
		}
	})
}

// TestLayerFileEdges pins requirement 6: unknown layer names and empty
// roots resolve to "" (mirroring joinRoot), so callers can never build a
// process-relative path.
func TestLayerFileEdges(t *testing.T) {
	full := Paths{LeleDir: "/h", AgentWorkspace: "/w", Cwd: "/p"}
	tests := []struct {
		name  string
		paths Paths
		layer string
		want  string
	}{
		{"unknown layer name", full, "bogus", ""},
		{"empty layer name", full, "", ""},
		{"global", full, LayerGlobal, filepath.Join("/h", "mcp.json")},
		{"agent", full, LayerAgent, filepath.Join("/w", "mcp.json")},
		{"project", full, LayerProject, filepath.Join("/p", ".lele", "mcp.json")},
		{"empty LeleDir", Paths{Cwd: "/p"}, LayerGlobal, ""},
		{"empty agent root", Paths{LeleDir: "/h", Cwd: "/p"}, LayerAgent, ""},
		{"empty cwd", Paths{LeleDir: "/h"}, LayerProject, ""},
		{"all roots empty", Paths{}, LayerGlobal, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LayerFile(tt.paths, tt.layer); got != tt.want {
				t.Errorf("LayerFile(%+v, %q) = %q, want %q", tt.paths, tt.layer, got, tt.want)
			}
		})
	}
}

// TestLayersOrder pins the low → high layer order and that callers cannot
// mutate the package-level slice.
func TestLayersOrder(t *testing.T) {
	if want := []string{LayerGlobal, LayerAgent, LayerProject}; !reflect.DeepEqual(Layers(), want) {
		t.Fatalf("Layers() = %v, want %v", Layers(), want)
	}
	got := Layers()
	got[0] = "mutated"
	if Layers()[0] != LayerGlobal {
		t.Error("Layers() must return a fresh slice; the caller mutated the package-level order")
	}
}

// TestLayerInfoExistence checks the per-layer file view for the plain
// (non-aliased) case: rows low → high with Exists tracking the disk.
func TestLayerInfoExistence(t *testing.T) {
	fx := newDiscoveryFixture(t)

	inv := ReadInventory(fx.paths)
	if len(inv.Layers) != 3 {
		t.Fatalf("Layers has %d rows, want 3", len(inv.Layers))
	}
	order := []string{LayerGlobal, LayerAgent, LayerProject}
	paths := []string{fx.globalPath, fx.agentPath, fx.projectPath}
	for i, li := range inv.Layers {
		if li.Layer != order[i] || li.Path != paths[i] || li.Exists {
			t.Errorf("Layers[%d] = %+v, want {%q %q exists:false}", i, li, order[i], paths[i])
		}
		if len(li.AliasedWith) != 0 {
			t.Errorf("Layers[%d].AliasedWith = %v, want nil (distinct roots)", i, li.AliasedWith)
		}
	}

	fx.writeLayer(t, LayerGlobal, `{"mcpServers":{"g":{"command":"cg"}}}`)
	fx.writeLayer(t, LayerProject, `{"mcpServers":{"p":{"command":"cp"}}}`)
	inv = ReadInventory(fx.paths)
	if !inv.Layers[0].Exists || inv.Layers[1].Exists || !inv.Layers[2].Exists {
		t.Errorf("Exists flags = [%v %v %v], want [true false true]",
			inv.Layers[0].Exists, inv.Layers[1].Exists, inv.Layers[2].Exists)
	}
}
