// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeCmdFile creates dir/name with body, failing the test on error.
func writeCmdFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// cmdFileBody builds a markdown command file whose template is identifiable.
func cmdFileBody(desc, tmpl string) string {
	return "---\ndescription: " + desc + "\n---\n" + tmpl + "\n"
}

func TestManagerLoadsAllFourLevels(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	ws := filepath.Join(root, "ws")
	dir := filepath.Join(root, "dir")

	writeCmdFile(t, filepath.Join(global, "commands"), "g.md", cmdFileBody("g", "from-global"))
	writeCmdFile(t, filepath.Join(ws, "commands"), "w.md", cmdFileBody("w", "from-workspace"))
	writeCmdFile(t, dir, "d.md", cmdFileBody("d", "from-directory"))

	m := NewManager(ManagerConfig{
		LeleDir:   global,
		Workspace: ws,
		Dir:       dir,
		Commands: map[string]CommandDef{
			"C": {Description: "c", Template: "from-config"},
		},
	})

	want := map[string]Source{
		"g": SourceGlobal,
		"w": SourceWorkspace,
		"d": SourceDirectory,
		"c": SourceConfig,
	}
	if m.Registry().Len() != len(want) {
		t.Fatalf("registry size = %d, want %d (%v)", m.Registry().Len(), len(want), registeredNames(m))
	}
	for name, src := range want {
		cmd, ok := m.Registry().Get(name)
		if !ok {
			t.Fatalf("command %q not loaded", name)
		}
		if cmd.Source != src {
			t.Errorf("command %q source = %q, want %q", name, cmd.Source, src)
		}
		if cmd.Name != name {
			t.Errorf("command %q stored with name %q", name, cmd.Name)
		}
	}
	// config-defined commands carry no path; file ones do.
	if c, _ := m.Registry().Get("c"); c.Path != "" {
		t.Errorf("config command path = %q, want empty", c.Path)
	}
	if d, _ := m.Registry().Get("d"); !strings.HasSuffix(d.Path, "d.md") {
		t.Errorf("directory command path = %q, want .../d.md", d.Path)
	}
}

func TestManagerPrecedenceDirectoryBeatsAll(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	ws := filepath.Join(root, "ws")
	dir := filepath.Join(root, "dir")

	// Same name at every level; template marks the winner.
	writeCmdFile(t, filepath.Join(global, "commands"), "dup.md", cmdFileBody("g", "GLOBAL"))
	writeCmdFile(t, filepath.Join(ws, "commands"), "dup.md", cmdFileBody("w", "WORKSPACE"))
	writeCmdFile(t, dir, "dup.md", cmdFileBody("d", "DIRECTORY"))

	m := NewManager(ManagerConfig{
		LeleDir:   global,
		Workspace: ws,
		Dir:       dir,
		Commands:  map[string]CommandDef{"dup": {Description: "c", Template: "CONFIG"}},
	})

	cmd, ok := m.Registry().Get("dup")
	if !ok {
		t.Fatal("dup command missing")
	}
	if cmd.Template != "DIRECTORY" || cmd.Source != SourceDirectory {
		t.Fatalf("directory must win, got template=%q source=%q", cmd.Template, cmd.Source)
	}

	// Drop the directory level: workspace wins.
	m2 := NewManager(ManagerConfig{LeleDir: global, Workspace: ws, Commands: m.cfg.Commands})
	if c2, _ := m2.Registry().Get("dup"); c2.Template != "WORKSPACE" || c2.Source != SourceWorkspace {
		t.Fatalf("workspace must win without dir, got %q/%q", c2.Template, c2.Source)
	}

	// Only global + config: global wins.
	m3 := NewManager(ManagerConfig{LeleDir: global, Commands: m.cfg.Commands})
	if c3, _ := m3.Registry().Get("dup"); c3.Template != "GLOBAL" || c3.Source != SourceGlobal {
		t.Fatalf("global must win over config, got %q/%q", c3.Template, c3.Source)
	}

	// Config only.
	m4 := NewManager(ManagerConfig{Commands: m.cfg.Commands})
	if c4, _ := m4.Registry().Get("dup"); c4.Template != "CONFIG" || c4.Source != SourceConfig {
		t.Fatalf("config fallback wrong, got %q/%q", c4.Template, c4.Source)
	}
}

func TestManagerEnsureFresh(t *testing.T) {
	root := t.TempDir()
	m := NewManager(ManagerConfig{Dir: root})
	if m.Registry().Len() != 0 {
		t.Fatalf("fresh manager should be empty, got %v", registeredNames(m))
	}

	// A brand-new file must not appear while the TTL has not passed.
	writeCmdFile(t, root, "late.md", cmdFileBody("late", "LATE"))
	m.EnsureFresh(time.Hour)
	if _, ok := m.Registry().Get("late"); ok {
		t.Fatal("command appeared before TTL expired")
	}

	// With an expired TTL it is picked up.
	time.Sleep(2 * time.Millisecond)
	m.EnsureFresh(time.Microsecond)
	if _, ok := m.Registry().Get("late"); !ok {
		t.Fatal("command not picked up after TTL expired")
	}

	// ttl <= 0 always reloads.
	writeCmdFile(t, root, "now.md", cmdFileBody("now", "NOW"))
	m.EnsureFresh(0)
	if _, ok := m.Registry().Get("now"); !ok {
		t.Fatal("ttl<=0 must force a reload")
	}

	// Removal is observed on the next refresh too.
	if err := os.Remove(filepath.Join(root, "now.md")); err != nil {
		t.Fatal(err)
	}
	m.EnsureFresh(0)
	if _, ok := m.Registry().Get("now"); ok {
		t.Fatal("deleted command still present after reload")
	}
}

func TestManagerRegistryIsStableAcrossReloads(t *testing.T) {
	root := t.TempDir()
	m := NewManager(ManagerConfig{Dir: root})
	before := m.Registry()
	writeCmdFile(t, root, "a.md", cmdFileBody("a", "A"))
	if err := m.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after := m.Registry(); after != before {
		t.Fatal("Registry() pointer changed across reload; consumers would go stale")
	}
	if _, ok := m.Registry().Get("a"); !ok {
		t.Fatal("reload did not apply new content")
	}
}

func TestManagerMissingDirsAreFine(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	m := NewManager(ManagerConfig{
		LeleDir:   missing,
		Workspace: missing,
		Dir:       missing,
		Commands:  map[string]CommandDef{"ok": {Template: "T"}},
	})
	if err := m.Reload(); err != nil {
		t.Fatalf("missing dirs must not produce an error, got %v", err)
	}
	if m.Registry().Len() != 1 {
		t.Fatalf("expected only the config command, got %v", registeredNames(m))
	}
	m.EnsureFresh(0) // must not panic or error out visibly
	if _, ok := m.Registry().Get("ok"); !ok {
		t.Fatal("config command lost")
	}
}

func TestManagerSkipsEmptyTemplates(t *testing.T) {
	m := NewManager(ManagerConfig{Commands: map[string]CommandDef{
		"good":   {Template: "body"},
		"blank":  {Template: ""},
		"spaces": {Template: "   "},
		"":       {Template: "no name"},
		"  ":     {Template: "blank name"},
	}})
	if m.Registry().Len() != 1 {
		t.Fatalf("expected 1 command, got %v", registeredNames(m))
	}
	if _, ok := m.Registry().Get("good"); !ok {
		t.Fatal("valid command dropped")
	}
	if _, ok := m.Registry().Get("blank"); ok {
		t.Fatal("empty-template command was registered")
	}
	if _, ok := m.Registry().Get("spaces"); ok {
		t.Fatal("whitespace-template command was registered")
	}
}

func TestManagerAllowShell(t *testing.T) {
	m := NewManager(ManagerConfig{AllowShellDefault: false})
	off := &Command{Name: "off"}
	on := &Command{Name: "on", AllowShell: true}
	if m.AllowShell(off) {
		t.Error("no flag and no default should deny shell")
	}
	if !m.AllowShell(on) {
		t.Error("command flag should allow shell")
	}
	if m.AllowShell(nil) {
		t.Error("nil command with default false should deny shell")
	}

	// Harness-wide default turns everything on.
	m2 := NewManager(ManagerConfig{AllowShellDefault: true})
	if !m2.AllowShell(off) {
		t.Error("AllowShellDefault must apply to commands without the flag")
	}

	// Explicit override wins over both the flag and the default, and names are
	// normalized on both sides.
	m3 := NewManager(ManagerConfig{AllowShellDefault: true})
	m3.SetAllowShell("ON", false)
	if m3.AllowShell(on) {
		t.Error("override to false must win over AllowShellDefault")
	}
	// Overriding "on" must not leak into other commands.
	if !m3.AllowShell(off) {
		t.Error("default must still apply to non-overridden commands")
	}
	m3.SetAllowShell("off", true)
	if !m3.AllowShell(off) {
		t.Error("override to true must win")
	}
	m3.SetAllowShell("off", false) // pin false beats the default
	if m3.AllowShell(off) {
		t.Error("pin to false must win over AllowShellDefault")
	}
	m3.ClearAllowShell("off") // dropping the pin restores the default
	if !m3.AllowShell(off) {
		t.Error("clearing the pin must fall back to AllowShellDefault")
	}
}

func TestManagerLowercasesConfigNames(t *testing.T) {
	m := NewManager(ManagerConfig{Commands: map[string]CommandDef{"MyCmd": {Template: "T"}}})
	if _, ok := m.Registry().Get("mycmd"); !ok {
		t.Fatalf("config key must be lowercased, have %v", registeredNames(m))
	}
	if c, _ := m.Registry().Get("MYCMD"); c.Name != "mycmd" {
		t.Errorf("stored name = %q, want mycmd", c.Name)
	}
}

// names returns the registered command names for failure messages.
func registeredNames(m *Manager) []string {
	var out []string
	for _, c := range m.Registry().All() {
		out = append(out, string(c.Source)+":"+c.Name)
	}
	return out
}

func TestManagerAllowAbsoluteFiles(t *testing.T) {
	yes, no := true, false

	// nil tri-state inherits the default, in both directions.
	mOff := NewManager(ManagerConfig{AllowAbsoluteFilesDefault: false})
	if mOff.AllowAbsoluteFiles(&Command{Name: "x"}) {
		t.Error("nil flag with default false must deny")
	}
	if mOff.AllowAbsoluteFiles(nil) {
		t.Error("nil command with default false must deny")
	}
	mOn := NewManager(ManagerConfig{AllowAbsoluteFilesDefault: true})
	if !mOn.AllowAbsoluteFiles(&Command{Name: "x"}) {
		t.Error("nil flag with default true must allow")
	}

	// Explicit tri-state overrides the default in BOTH directions (this is
	// the deliberate difference from AllowShell's OR merge).
	if !mOff.AllowAbsoluteFiles(&Command{Name: "y", AllowAbsoluteFiles: &yes}) {
		t.Error("explicit true must beat default false")
	}
	if mOn.AllowAbsoluteFiles(&Command{Name: "z", AllowAbsoluteFiles: &no}) {
		t.Error("explicit false must beat default true")
	}

	// Runtime pin wins over tri-state and default; clearing restores them.
	mOn.SetAllowAbsoluteFiles("Z", true)
	if !mOn.AllowAbsoluteFiles(&Command{Name: "z", AllowAbsoluteFiles: &no}) {
		t.Error("pin true must beat explicit false")
	}
	mOn.SetAllowAbsoluteFiles("z", false)
	if mOn.AllowAbsoluteFiles(&Command{Name: "z", AllowAbsoluteFiles: &yes}) {
		t.Error("pin false must beat explicit true")
	}
	mOn.ClearAllowAbsoluteFiles("z")
	if mOn.AllowAbsoluteFiles(&Command{Name: "z", AllowAbsoluteFiles: &no}) {
		t.Error("after clear, explicit false must win again")
	}
	if !mOn.AllowAbsoluteFiles(&Command{Name: "other"}) {
		t.Error("pins must not leak into other commands")
	}
}

// levelNames returns "source:name" pairs for one level of a Levels() map,
// used in assertions and failure messages.
func levelNames(levels map[Source][]*Command, src Source) []string {
	var out []string
	for _, c := range levels[src] {
		out = append(out, c.Name)
	}
	return out
}

// T-B2: Levels() reports every discovery level WITHOUT merging, so a name that
// exists in two levels shows up in BOTH, while Reload()/Registry().All() keep
// the last-write-wins precedence (workspace beats global).
func TestManagerLevelsShowsShadowedCommands(t *testing.T) {
	root := t.TempDir()
	global := filepath.Join(root, "global")
	ws := filepath.Join(root, "ws")
	dir := filepath.Join(root, "dir")

	// "dup" lives in global AND workspace (and config, lowest precedence);
	// "solo" only in global. Template marks the level so the winner is
	// identifiable.
	writeCmdFile(t, filepath.Join(global, "commands"), "dup.md", cmdFileBody("g", "GLOBAL"))
	writeCmdFile(t, filepath.Join(global, "commands"), "solo.md", cmdFileBody("g", "GLOBAL-SOLO"))
	writeCmdFile(t, filepath.Join(ws, "commands"), "dup.md", cmdFileBody("w", "WORKSPACE"))
	writeCmdFile(t, dir, "dup.md", cmdFileBody("d", "DIRECTORY"))

	m := NewManager(ManagerConfig{
		LeleDir:   global,
		Workspace: ws,
		Dir:       dir,
		Commands:  map[string]CommandDef{"dup": {Description: "c", Template: "CONFIG"}},
	})

	levels, err := m.Levels()
	if err != nil {
		t.Fatalf("Levels: %v", err)
	}

	// Every level must be present as a key, even when empty.
	for _, src := range levelOrder {
		if _, ok := levels[src]; !ok {
			t.Fatalf("Levels() missing key %q", src)
		}
	}

	// dup appears in ALL FOUR levels — nothing is merged away. Each level's
	// file carries its own level name as the template, so the entry can be
	// attributed to the level that produced it (config uses "CONFIG" too).
	for _, src := range levelOrder {
		found := false
		for _, c := range levels[src] {
			if c.Name != "dup" {
				continue
			}
			found = true
			if c.Template != strings.ToUpper(string(src)) {
				t.Errorf("level %q: dup template = %q, want %q", src, c.Template, strings.ToUpper(string(src)))
			}
			if c.Source != src {
				t.Errorf("level %q: dup source = %q, want %q", src, c.Source, src)
			}
		}
		if !found {
			t.Errorf("level %q missing shadowed command dup, have %v", src, levelNames(levels, src))
		}
	}
	// solo only exists at global level.
	if got := levelNames(levels, SourceGlobal); len(got) != 2 {
		t.Errorf("global level = %v, want [dup solo]", got)
	}
	if got := levelNames(levels, SourceWorkspace); len(got) != 1 || got[0] != "dup" {
		t.Errorf("workspace level = %v, want [dup]", got)
	}

	// Precedence is untouched: the merged registry keeps last-write-wins
	// (directory > workspace > global > config).
	cmd, ok := m.Registry().Get("dup")
	if !ok {
		t.Fatal("dup missing from registry")
	}
	if cmd.Source != SourceDirectory || cmd.Template != "DIRECTORY" {
		t.Errorf("merged dup = %q/%q, want directory/DIRECTORY", cmd.Source, cmd.Template)
	}
	// Registry().All() agrees with the registry view.
	all := m.Registry().All()
	if len(all) != 2 { // dup + solo
		t.Fatalf("registry has %d commands (%v), want 2", len(all), registeredNames(m))
	}

	// Drop the directory level: workspace must win, and Reload() must reflect
	// the change with the same last-write-wins rule.
	m2 := NewManager(ManagerConfig{
		LeleDir:   global,
		Workspace: ws,
		Commands:  map[string]CommandDef{"dup": {Description: "c", Template: "CONFIG"}},
	})
	if err := m2.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	c2, _ := m2.Registry().Get("dup")
	if c2.Source != SourceWorkspace || c2.Template != "WORKSPACE" {
		t.Fatalf("without dir level, dup = %q/%q, want workspace/WORKSPACE", c2.Source, c2.Template)
	}
	l2, err := m2.Levels()
	if err != nil {
		t.Fatalf("Levels after reload: %v", err)
	}
	if len(l2[SourceDirectory]) != 0 {
		t.Errorf("disabled dir level should be empty, got %v", levelNames(l2, SourceDirectory))
	}

	// Levels() must not mutate the registry or the load timestamp: a stale
	// snapshot on disk is still what Reload applied.
	before := m.Registry().Len()
	if _, err := m.Levels(); err != nil {
		t.Fatal(err)
	}
	if m.Registry().Len() != before {
		t.Errorf("Levels() changed the registry size")
	}
}

// T-B3: disabled levels (empty path) map to an empty slice and never produce
// an error, and Levels() must not create directories as a side effect.
func TestManagerLevelsDisabledLevels(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	writeCmdFile(t, filepath.Join(ws, "commands"), "only.md", cmdFileBody("o", "ONLY"))

	m := NewManager(ManagerConfig{
		LeleDir:   "",  // global disabled
		Workspace: ws,  // workspace enabled
		Dir:       "",  // directory disabled
		Commands:  nil, // config level has no entries at all
	})

	levels, err := m.Levels()
	if err != nil {
		t.Fatalf("disabled levels must not error, got %v", err)
	}
	for _, src := range []Source{SourceConfig, SourceGlobal, SourceDirectory} {
		lv, ok := levels[src]
		if !ok {
			t.Fatalf("Levels() missing disabled key %q", src)
		}
		if lv == nil {
			t.Errorf("disabled level %q = nil, want empty slice", src)
		}
		if len(lv) != 0 {
			t.Errorf("disabled level %q = %v, want empty", src, levelNames(levels, src))
		}
	}
	if got := levelNames(levels, SourceWorkspace); len(got) != 1 || got[0] != "only" {
		t.Errorf("workspace level = %v, want [only]", got)
	}

	// A missing (not disabled) directory is also not an error.
	m2 := NewManager(ManagerConfig{LeleDir: filepath.Join(root, "does-not-exist")})
	levels2, err := m2.Levels()
	if err != nil {
		t.Fatalf("missing dir must not error, got %v", err)
	}
	if len(levels2[SourceGlobal]) != 0 {
		t.Errorf("missing global dir produced commands: %v", levelNames(levels2, SourceGlobal))
	}

	// Levels() is read-only: it must not create the commands dir.
	fresh := filepath.Join(root, "fresh-ws")
	m3 := NewManager(ManagerConfig{Workspace: fresh})
	if _, err := m3.Levels(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(fresh, "commands")); !os.IsNotExist(err) {
		t.Errorf("Levels() must not create directories; stat err = %v", err)
	}
}

// reloadLocked and Levels must agree on per-level errors: a level whose dir is
// unreadable reports the error while the levels that loaded are still
// returned. Uses a file where a directory is expected to force a read failure.
func TestManagerLevelsReportsPerLevelErrors(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	writeCmdFile(t, filepath.Join(ws, "commands"), "good.md", cmdFileBody("g", "GOOD"))

	// Global level: point LeleDir at a location whose "commands" entry is a
	// regular file, so os.ReadDir(<lele>/commands) fails.
	broken := filepath.Join(root, "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "commands"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(ManagerConfig{LeleDir: broken, Workspace: ws})

	levels, err := m.Levels()
	if err == nil {
		t.Fatal("Levels() must report the unreadable global level")
	}
	if !strings.Contains(err.Error(), "commands") {
		t.Errorf("error should mention the failing dir: %v", err)
	}
	// The level that loaded is still included.
	if got := levelNames(levels, SourceWorkspace); len(got) != 1 || got[0] != "good" {
		t.Errorf("workspace level = %v, want [good] despite global failure", got)
	}
	// The failed level is empty, not partially populated.
	if len(levels[SourceGlobal]) != 0 {
		t.Errorf("failed level should be empty, got %v", levelNames(levels, SourceGlobal))
	}

	// reloadLocked keeps the same behaviour: Reload surfaces the joined error
	// and the good level is still registered.
	if err := m.Reload(); err == nil {
		t.Fatal("Reload() must report the failing level")
	}
	if _, ok := m.Registry().Get("good"); !ok {
		t.Error("good workspace command lost when the global level failed")
	}
}
