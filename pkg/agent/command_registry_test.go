// Lele - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Lele contributors

package agent

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/xilistudios/lele/pkg/agent/commands"
	"github.com/xilistudios/lele/pkg/bus"
	"github.com/xilistudios/lele/pkg/config"
)

// testCommandHandler builds a minimal AgentLoop + command handler good enough to
// dispatch slash commands, mirroring the setup used by command_handler_test.go.
func testCommandHandler(t *testing.T) *commandHandlerImpl {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv("LELE_CONFIG_DIR", tmpDir)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         tmpDir,
				Model:             "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
	}
	return newCommandHandler(NewAgentLoop(cfg, bus.NewMessageBus()))
}

// TestWebUICommands_ReturnsClearAndCompactSorted asserts the registry exposes
// exactly the commands the WebUI palette should show, in stable order.
func TestWebUICommands_ReturnsClearAndCompactSorted(t *testing.T) {
	got := WebUICommands()

	if len(got) != 3 {
		t.Fatalf("WebUICommands() returned %d entries, want 3: %+v", len(got), got)
	}

	want := []CommandInfo{
		{Name: "/clear", Description: "Clear the conversation history for this session.", Usage: "/clear"},
		{Name: "/compact", Description: "Summarize and compact the conversation history (needs 5+ messages).", Usage: "/compact"},
		{Name: "/goal", Description: "Set a persistent goal the agent works toward autonomously, or check/pause/resume/clear it (/goal status|pause|resume|clear).", Usage: "/goal <text>"},
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("WebUICommands()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}

	// Defensive: sorted by name must hold regardless of declaration order.
	for i := 1; i < len(got); i++ {
		if got[i-1].Name >= got[i].Name {
			t.Errorf("commands not sorted by name: %q before %q", got[i-1].Name, got[i].Name)
		}
	}
}

// TestWebUICommands_ReturnsCopy asserts the internal registry is not exposed for
// mutation: overwriting the returned slice (or its elements) must not leak into
// subsequent calls.
func TestWebUICommands_ReturnsCopy(t *testing.T) {
	first := WebUICommands()

	// Clobber the elements and shrink the slice via append-into-backing-array.
	for i := range first {
		first[i] = CommandInfo{Name: "/tampered", Description: "tampered", Usage: "tampered"}
	}
	_ = append(first, CommandInfo{Name: "/injected", Description: "injected", Usage: "/injected"})

	second := WebUICommands()
	if len(second) != 3 {
		t.Fatalf("registry length changed after mutating a copy: %d", len(second))
	}
	if second[0].Name != "/clear" || second[1].Name != "/compact" || second[2].Name != "/goal" {
		t.Errorf("registry mutated through a returned copy: %+v", second)
	}
	for _, c := range second {
		if c.Description == "" || c.Usage == "" {
			t.Errorf("command %q mutated through a returned copy: %+v", c.Name, c)
		}
	}
}

// TestWebUICommands_MatchDispatchedCommands guards the one real risk of keeping
// the registry separate from the handleCommand switch: drift. Every command the
// registry advertises must actually be dispatched by the backend, and no
// session-scoped, argument-free command may be missing from the registry.
func TestWebUICommands_MatchDispatchedCommands(t *testing.T) {
	ch := testCommandHandler(t)

	// Every advertised command must be handled by the dispatcher.
	for _, c := range WebUICommands() {
		if _, handled := ch.handleCommand(context.Background(), bus.InboundMessage{
			Channel:  "test",
			SenderID: "user1",
			ChatID:   "chat1",
			Content:  c.Name,
		}); !handled {
			t.Errorf("registry advertises %q but handleCommand does not dispatch it", c.Name)
		}
	}

	// The commands the WebUI must expose today; if handleCommand grows a new
	// session-scoped command, decide explicitly whether to register it here (and
	// in the registry).
	for _, name := range []string{"/clear", "/compact", "/goal"} {
		found := false
		for _, c := range WebUICommands() {
			if c.Name == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("handleCommand dispatches %q but the registry does not advertise it", name)
		}
	}
}

// TestWebUICommands_FieldsNonEmpty keeps the palette honest: a command with an
// empty description or usage renders as a broken row in the UI.
func TestWebUICommands_FieldsNonEmpty(t *testing.T) {
	for _, c := range WebUICommands() {
		if c.Name == "" || c.Description == "" || c.Usage == "" {
			t.Errorf("command has empty field: %+v", c)
		}
	}
}

// TestCommandRegistry_ReexportMatchesSource guards the alias in
// command_registry.go: agent.WebUICommands must keep returning exactly what the
// registry package (the single source of truth pkg/channels reads) returns, so
// the two entry points can never disagree.
func TestCommandRegistry_ReexportMatchesSource(t *testing.T) {
	viaAgent := WebUICommands()
	viaSource := commands.WebUICommands()

	if len(viaAgent) != len(viaSource) {
		t.Fatalf("agent.WebUICommands() has %d entries, commands.WebUICommands() has %d",
			len(viaAgent), len(viaSource))
	}
	for i := range viaSource {
		if viaAgent[i] != viaSource[i] {
			t.Errorf("entry %d differs: agent=%+v source=%+v", i, viaAgent[i], viaSource[i])
		}
	}
}

// TestDispatcherReserved_MatchesSource is the guard the hand-maintained reserved
// list needs: it reads the real dispatch sources with go/parser and compares
// them against commands.DispatcherReserved(). Drift in either direction fails —
// a new built-in that nobody reserved (its custom-command file would be dead)
// and a reserved name that nothing dispatches anymore (it would block a free
// name forever).
func TestDispatcherReserved_MatchesSource(t *testing.T) {
	dispatched := switchCasesOn(t, "command_handler.go", "handleCommand", "cmd")
	if len(dispatched) == 0 {
		t.Fatal("found no dispatched commands: the parser or the file moved")
	}
	intercepted := switchCasesOn(t, filepath.Join("..", "channels", "telegram_messages.go"), "", "cmd")
	if len(intercepted) == 0 {
		t.Fatal("found no intercepted commands: the parser or the file moved")
	}

	reserved := map[string]bool{}
	for _, name := range commands.DispatcherReserved() {
		reserved[name] = true
	}

	for _, cmd := range dispatched {
		name := strings.TrimPrefix(cmd, "/")
		if !reserved[name] {
			t.Errorf("handleCommand dispatches %q but DispatcherReserved() does not reserve it", cmd)
		}
	}
	for _, name := range commands.DispatcherReserved() {
		if slices.Contains(dispatched, "/"+name) {
			continue
		}
		if !slices.Contains(intercepted, name) && !slices.Contains(intercepted, "/"+name) {
			t.Errorf("DispatcherReserved() reserves %q but neither handleCommand dispatches it nor Telegram intercepts it", name)
		}
	}
}

// TestDispatcherReserved_Shape pins the format the API relies on: lowercase, no
// leading slash, no duplicates, sorted.
func TestDispatcherReserved_Shape(t *testing.T) {
	list := commands.DispatcherReserved()
	if len(list) == 0 {
		t.Fatal("DispatcherReserved() is empty")
	}
	seen := map[string]bool{}
	for _, name := range list {
		if name == "" || strings.HasPrefix(name, "/") {
			t.Errorf("reserved name %q must be bare and non-empty", name)
		}
		if name != strings.ToLower(name) {
			t.Errorf("reserved name %q must be lowercase", name)
		}
		if seen[name] {
			t.Errorf("reserved name %q listed twice", name)
		}
		seen[name] = true
	}
	if !slices.IsSorted(list) {
		t.Errorf("DispatcherReserved() must be sorted, got %v", list)
	}
	// The copy must be fresh: a caller mutating the result may not corrupt the
	// registry for the next one.
	list[0] = "tampered"
	if got := commands.DispatcherReserved(); got[0] == "tampered" {
		t.Error("DispatcherReserved() leaked its backing slice")
	}
}

// switchCasesOn parses a Go source file and returns the string literals of every
// `switch <varName>` case clause, optionally restricted to one function (empty
// funcName = whole file). It is a source-level check on purpose: the dispatcher
// is a switch statement with no reflective way to enumerate it at runtime.
func switchCasesOn(t *testing.T, file, funcName, varName string) []string {
	t.Helper()

	// The test runs with the package dir as cwd, so the callers pass paths
	// relative to pkg/agent.
	path := file
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var out []string
	collect := func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok {
			return true
		}
		if ident, ok := sw.Tag.(*ast.Ident); !ok || ident.Name != varName {
			return true // a switch on something else (subcommands, modes, ...)
		}
		for _, clause := range sw.Body.List {
			cs, ok := clause.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range cs.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				out = append(out, v)
			}
		}
		return true
	}

	if funcName == "" {
		ast.Inspect(parsed, collect)
		return out
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, collect)
	}
	return out
}
